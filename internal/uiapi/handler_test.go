package uiapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHealthzAndReadyz(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("healthz body %q", rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestOverviewCounts(t *testing.T) {
	t.Parallel()
	owned := true
	h := newTestHandler(t,
		readyRouter("app", "edge"),
		notReadyRouter("app", "core"),
		&api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc",
				Namespace: "app",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       "web",
					Controller: &owned,
				}},
			},
			Spec: api.MikroTikDNSRecordSpec{Name: "svc.example.com", Address: "10.0.0.8"},
		},
	)

	rec := doRequest(t, h, http.MethodGet, "/api/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := decodeMap(t, rec)
	kinds, ok := body["kinds"].([]any)
	if !ok {
		t.Fatalf("kinds missing: %s", rec.Body.String())
	}
	if len(kinds) != len(kindOrder) {
		t.Fatalf("got %d kinds, want %d", len(kinds), len(kindOrder))
	}

	want := map[string]kindCount{
		kindRouters:       {Kind: kindRouters, Count: 2, NotReady: 1},
		kindDNSRecords:    {Kind: kindDNSRecords, Count: 1, NotReady: 1},
		kindRoutes:        {Kind: kindRoutes},
		kindPortForwards:  {Kind: kindPortForwards},
		kindFirewallRules: {Kind: kindFirewallRules},
	}
	for i, raw := range kinds {
		item := asMap(t, raw)
		kind := stringField(t, item, "kind")
		got := kindCount{
			Kind:     kind,
			Count:    intFromJSON(t, item["count"]),
			NotReady: intFromJSON(t, item["notReady"]),
		}
		expected, ok := want[kind]
		if !ok {
			t.Fatalf("unexpected kind %q", kind)
		}
		if got != expected {
			t.Fatalf("index %d: got %#v want %#v", i, got, expected)
		}
		if kind != kindOrder[i] {
			t.Fatalf("kind order: index %d got %q want %q", i, kind, kindOrder[i])
		}
	}
}

func TestResourceCRUD(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, readyRouter("app", "edge"))

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		check      func(t *testing.T, rec *httptest.ResponseRecorder)
	}{
		{
			name:       "list all namespaces",
			method:     http.MethodGet,
			path:       "/api/resources/mikrotikrouters",
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				items := listItems(t, rec)
				if len(items) != 1 {
					t.Fatalf("got %d items", len(items))
				}
				if items[0]["kind"] != "MikroTikRouter" {
					t.Fatalf("kind %v", items[0]["kind"])
				}
				if _, ok := items[0]["managedBy"]; ok {
					t.Fatal("standalone resource should omit managedBy")
				}
			},
		},
		{
			name:       "list namespace filter",
			method:     http.MethodGet,
			path:       "/api/resources/mikrotikrouters?namespace=app",
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if len(listItems(t, rec)) != 1 {
					t.Fatalf("expected 1 item: %s", rec.Body.String())
				}
			},
		},
		{
			name:       "list other namespace empty",
			method:     http.MethodGet,
			path:       "/api/resources/mikrotikrouters?namespace=other",
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				items := listItems(t, rec)
				if len(items) != 0 {
					t.Fatalf("got %d items, want empty list", len(items))
				}
			},
		},
		{
			name:       "get one",
			method:     http.MethodGet,
			path:       "/api/resources/mikrotikrouters/app/edge",
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				body := decodeMap(t, rec)
				meta := asMap(t, body["metadata"])
				if meta["name"] != "edge" || meta["namespace"] != "app" {
					t.Fatalf("metadata %#v", meta)
				}
			},
		},
		{
			name:       "get missing",
			method:     http.MethodGet,
			path:       "/api/resources/mikrotikrouters/app/missing",
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "create json",
			method: http.MethodPost,
			path:   "/api/resources/mikrotikrouters/app",
			body: `{
				"apiVersion": "mikrotik.operator.io/v1alpha1",
				"kind": "MikroTikRouter",
				"metadata": {"name": "core"},
				"spec": {"address": "192.0.2.20", "credentialsSecret": {"name": "creds"}}
			}`,
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				body := decodeMap(t, rec)
				if body["kind"] != "MikroTikRouter" {
					t.Fatalf("kind %v", body["kind"])
				}
				meta := asMap(t, body["metadata"])
				if meta["namespace"] != "app" {
					t.Fatalf("namespace %v", meta["namespace"])
				}
			},
		},
		{
			name:   "create yaml",
			method: http.MethodPost,
			path:   "/api/resources/mikrotikroutes/app",
			body: `apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikRoute
metadata:
  name: default
spec:
  destination: 0.0.0.0/0
  gateway: 192.0.2.1
`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "create invalid json",
			method:     http.MethodPost,
			path:       "/api/resources/mikrotikrouters/app",
			body:       "{not-json",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/api/resources/mikrotikrouters/app/edge",
			body: `{
				"metadata": {"name": "edge"},
				"spec": {"address": "192.0.2.99", "credentialsSecret": {"name": "creds"}}
			}`,
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				spec := asMap(t, decodeMap(t, rec)["spec"])
				if spec["address"] != "192.0.2.99" {
					t.Fatalf("address %v", spec["address"])
				}
			},
		},
		{
			name:       "delete",
			method:     http.MethodDelete,
			path:       "/api/resources/mikrotikrouters/app/edge",
			wantStatus: http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, h, tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d want %d body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.check != nil {
				tt.check(t, rec)
			}
		})
	}

	rec := doRequest(t, h, http.MethodGet, "/api/resources/mikrotikrouters/app/edge", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted resource still present: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOwnedPutAndDeleteConflict(t *testing.T) {
	t.Parallel()
	owned := true
	notOwned := false
	dns := &api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Service",
				Name:       "frontend",
				Controller: &owned,
			}},
		},
		Spec: api.MikroTikDNSRecordSpec{Name: "web.example.com", Address: "10.0.0.8"},
	}
	standalone := &api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "manual",
			Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "note",
				Controller: &notOwned,
			}},
		},
		Spec: api.MikroTikDNSRecordSpec{Name: "manual.example.com", Address: "10.0.0.9"},
	}
	h := newTestHandler(t, dns, standalone)

	get := doRequest(t, h, http.MethodGet, "/api/resources/mikrotikdnsrecords/app/web", "")
	if get.Code != http.StatusOK {
		t.Fatalf("get owned: %d %s", get.Code, get.Body.String())
	}
	managedBy := asMap(t, decodeMap(t, get)["managedBy"])
	if managedBy["kind"] != "Service" || managedBy["name"] != "frontend" || managedBy["namespace"] != "app" {
		t.Fatalf("managedBy %#v", managedBy)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantOwner  bool
	}{
		{
			name:       "put owned",
			method:     http.MethodPut,
			path:       "/api/resources/mikrotikdnsrecords/app/web",
			body:       `{"metadata":{"name":"web"},"spec":{"name":"changed.example.com","address":"10.0.0.8"}}`,
			wantStatus: http.StatusConflict,
			wantOwner:  true,
		},
		{
			name:       "delete owned",
			method:     http.MethodDelete,
			path:       "/api/resources/mikrotikdnsrecords/app/web",
			wantStatus: http.StatusConflict,
			wantOwner:  true,
		},
		{
			name:       "put non-controller owner allowed",
			method:     http.MethodPut,
			path:       "/api/resources/mikrotikdnsrecords/app/manual",
			body:       `{"metadata":{"name":"manual"},"spec":{"name":"manual.example.com","address":"10.0.0.10"}}`,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, h, tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d want %d body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !tt.wantOwner {
				return
			}
			body := decodeMap(t, rec)
			if !strings.Contains(stringField(t, body, "error"), "Service/frontend") {
				t.Fatalf("error %v", body["error"])
			}
			owner := asMap(t, body["managedBy"])
			if owner["name"] != "frontend" {
				t.Fatalf("managedBy %#v", owner)
			}
		})
	}
}

func TestUnknownKindRejected(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list pods", method: http.MethodGet, path: "/api/resources/pods"},
		{name: "get secret kind", method: http.MethodGet, path: "/api/resources/secrets/app/creds"},
		{name: "create unknown", method: http.MethodPost, path: "/api/resources/widgets/app", body: `{"metadata":{"name":"x"}}`},
		{name: "put unknown", method: http.MethodPut, path: "/api/resources/widgets/app/x", body: `{"metadata":{"name":"x"}}`},
		{name: "delete unknown", method: http.MethodDelete, path: "/api/resources/widgets/app/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := doRequest(t, h, tt.method, tt.path, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d want 400 body %s", rec.Code, rec.Body.String())
			}
			body := decodeMap(t, rec)
			if body["error"] != "unknown resource kind" {
				t.Fatalf("error %v", body["error"])
			}
		})
	}
}

func TestSecretsListStripsData(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "router-creds", Namespace: "app"},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"password": []byte("super-secret")},
			StringData: map[string]string{"token": "also-secret"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other"},
			Data:       map[string][]byte{"password": []byte("other-secret")},
		},
	)

	rec := doRequest(t, h, http.MethodGet, "/api/secrets/app", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	for _, leak := range []string{"super-secret", "also-secret", "other-secret", `"data"`, `"stringData"`} {
		if strings.Contains(raw, leak) {
			t.Fatalf("secret payload leaked %q in %s", leak, raw)
		}
	}
	items := listNameItems(t, rec)
	if len(items) != 1 || items[0] != "router-creds" {
		t.Fatalf("items %#v", items)
	}
}

func TestNamespacesList(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
	)
	rec := doRequest(t, h, http.MethodGet, "/api/namespaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	items := listNameItems(t, rec)
	got := map[string]bool{}
	for _, name := range items {
		got[name] = true
	}
	if !got["app"] || !got["other"] {
		t.Fatalf("namespaces %#v", items)
	}
}

func TestConfigReturnsOperatorNamespace(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if stringField(t, decodeMap(t, rec), "namespace") != "default" {
		t.Fatalf("default namespace %s", rec.Body.String())
	}

	scheme := testScheme(t)
	custom := New(Options{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace: "mikrotik-operator-system",
	})
	rec = doRequest(t, custom, http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if stringField(t, decodeMap(t, rec), "namespace") != "mikrotik-operator-system" {
		t.Fatalf("configured namespace %s", rec.Body.String())
	}
}

func TestUnknownAPIPathNotFound(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/api/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("spa-index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	scheme := testScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := New(Options{
		Client:    kube,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaticDir: dir,
	})

	index := doRequest(t, h, http.MethodGet, "/routers/app/edge", "")
	if index.Code != http.StatusOK || index.Body.String() != "spa-index" {
		t.Fatalf("spa fallback: %d %q", index.Code, index.Body.String())
	}
	asset := doRequest(t, h, http.MethodGet, "/app.js", "")
	if asset.Code != http.StatusOK || asset.Body.String() != "console.log(1)" {
		t.Fatalf("static asset: %d %q", asset.Code, asset.Body.String())
	}
	apiRec := doRequest(t, h, http.MethodGet, "/api/overview", "")
	if apiRec.Code != http.StatusOK {
		t.Fatalf("api shadowed by spa: %d %s", apiRec.Code, apiRec.Body.String())
	}
}

func TestIsReady(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{name: "empty"},
		{name: "ready true", conditions: []metav1.Condition{{Type: readyConditionType, Status: metav1.ConditionTrue}}, want: true},
		{name: "ready false", conditions: []metav1.Condition{{Type: readyConditionType, Status: metav1.ConditionFalse}}},
		{name: "other type", conditions: []metav1.Condition{{Type: "Applied", Status: metav1.ConditionTrue}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isReady(tt.conditions); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func newTestHandler(t *testing.T, objs ...client.Object) http.Handler {
	t.Helper()
	scheme := testScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return New(Options{
		Client: builder.Build(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func readyRouter(namespace, name string) *api.MikroTikRouter {
	return &api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.0.2.10",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
		Status: api.MikroTikRouterStatus{
			Connected: true,
			Conditions: []metav1.Condition{{
				Type:   readyConditionType,
				Status: metav1.ConditionTrue,
			}},
		},
	}
}

func notReadyRouter(namespace, name string) *api.MikroTikRouter {
	router := readyRouter(namespace, name)
	router.Status.Connected = false
	router.Status.Conditions = []metav1.Condition{{
		Type:   readyConditionType,
		Status: metav1.ConditionFalse,
	}}
	return router
}

func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	return out
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want map, got %T (%v)", v, v)
	}
	return m
}

func stringField(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("%s want string, got %T (%v)", key, m[key], m[key])
	}
	return v
}

func sliceField(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s want array, got %T (%v)", key, m[key], m[key])
	}
	return v
}

func listItems(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	raw, ok := decodeMap(t, rec)["items"].([]any)
	if !ok {
		t.Fatalf("items missing: %s", rec.Body.String())
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		items = append(items, asMap(t, item))
	}
	return items
}

func listNameItems(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	raw, ok := decodeMap(t, rec)["items"].([]any)
	if !ok {
		t.Fatalf("items missing: %s", rec.Body.String())
	}
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		m := asMap(t, item)
		names = append(names, stringField(t, m, "name"))
	}
	return names
}

func intFromJSON(t *testing.T, v any) int {
	t.Helper()
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("want number, got %T (%v)", v, v)
	}
	return int(n)
}

func TestWriteKubeErrorUsesStatusCode(t *testing.T) {
	t.Parallel()
	h := &handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	h.writeKubeError(rec, apierrors.NewNotFound(api.GroupVersion.WithResource("mikrotikrouters").GroupResource(), "edge"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}
