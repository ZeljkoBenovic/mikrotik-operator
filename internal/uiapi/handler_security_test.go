package uiapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/healthz", "")
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	}
	for key, value := range want {
		if got := rec.Header().Get(key); got != value {
			t.Fatalf("%s=%q want %q", key, got, value)
		}
	}
}

func TestReadyzWithoutClient(t *testing.T) {
	t.Parallel()
	h := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	rec := doRequest(t, h, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d want 503 body %s", rec.Code, rec.Body.String())
	}
	if decodeMap(t, rec)["error"] != "kubernetes client is not ready" {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestReadyzWhenListFails(t *testing.T) {
	t.Parallel()
	scheme := testScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
			return fmt.Errorf("apiserver unavailable")
		},
	}).Build()
	h := New(Options{
		Client: kube,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	rec := doRequest(t, h, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d want 503 body %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidPathValuesRejected(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list namespace uppercase", method: http.MethodGet, path: "/api/resources/mikrotikrouters?namespace=App"},
		{name: "list namespace underscore", method: http.MethodGet, path: "/api/resources/mikrotikrouters?namespace=web_app"},
		{name: "secrets invalid namespace", method: http.MethodGet, path: "/api/secrets/App"},
		{name: "services invalid namespace", method: http.MethodGet, path: "/api/services/App"},
		{name: "pods invalid namespace", method: http.MethodGet, path: "/api/pods/web_app"},
		{name: "get invalid namespace", method: http.MethodGet, path: "/api/resources/mikrotikrouters/App/edge"},
		{name: "get invalid name", method: http.MethodGet, path: "/api/resources/mikrotikrouters/app/Edge_1"},
		{name: "create invalid namespace", method: http.MethodPost, path: "/api/resources/mikrotikrouters/App", body: `{"metadata":{"name":"edge"},"spec":{"address":"192.0.2.10","credentialsSecret":{"name":"creds"}}}`},
		{name: "put invalid name", method: http.MethodPut, path: "/api/resources/mikrotikrouters/app/Edge_1", body: `{"metadata":{"name":"Edge_1"}}`},
		{name: "delete invalid namespace", method: http.MethodDelete, path: "/api/resources/mikrotikrouters/App/edge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := doRequest(t, h, tt.method, tt.path, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d want 400 body %s", rec.Code, rec.Body.String())
			}
			errMsg := stringField(t, decodeMap(t, rec), "error")
			if !strings.Contains(errMsg, "invalid") {
				t.Fatalf("error %q", errMsg)
			}
		})
	}
}

func TestCreateBodyValidation(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{name: "empty body", body: "", wantStatus: http.StatusBadRequest, wantErr: "request body is required"},
		{name: "missing name", body: `{"spec":{"address":"192.0.2.10","credentialsSecret":{"name":"creds"}}}`, wantStatus: http.StatusBadRequest, wantErr: "metadata.name is required"},
		{name: "invalid yaml", body: ": : :", wantStatus: http.StatusBadRequest, wantErr: "invalid JSON or YAML"},
		{name: "too large", body: `{"metadata":{"name":"edge"},"spec":{"address":"` + strings.Repeat("a", maxBodyBytes) + `"}}`, wantStatus: http.StatusRequestEntityTooLarge, wantErr: "request body too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := doRequest(t, h, http.MethodPost, "/api/resources/mikrotikrouters/app", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d want %d body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if decodeMap(t, rec)["error"] != tt.wantErr {
				t.Fatalf("error %v want %q", decodeMap(t, rec)["error"], tt.wantErr)
			}
		})
	}
}

func TestSecretsNeverReturnDataOnErrorPaths(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"},
		Data:       map[string][]byte{"password": []byte("leaked-value")},
	})
	rec := doRequest(t, h, http.MethodGet, "/api/secrets/missing_ns", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "leaked-value") {
		t.Fatalf("secret leaked on error path: %s", rec.Body.String())
	}
}

func TestSPASecurityAndFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("spa-index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("asset-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "..", "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	scheme := testScheme(t)
	h := New(Options{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		StaticDir: dir,
	})

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
		forbid     string
	}{
		{name: "root", method: http.MethodGet, path: "/", wantStatus: http.StatusOK, wantBody: "spa-index"},
		{name: "client route", method: http.MethodGet, path: "/firewall-rules/app/drop", wantStatus: http.StatusOK, wantBody: "spa-index"},
		{name: "asset", method: http.MethodGet, path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "asset-ok"},
		{name: "directory fallback", method: http.MethodGet, path: "/assets", wantStatus: http.StatusOK, wantBody: "spa-index"},
		{name: "head index", method: http.MethodHead, path: "/", wantStatus: http.StatusOK},
		{name: "post rejected", method: http.MethodPost, path: "/", wantStatus: http.StatusMethodNotAllowed},
		{name: "traversal", method: http.MethodGet, path: "/../secret.txt", forbid: "outside-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := doRequest(t, h, tt.method, tt.path, "")
			if tt.wantStatus != 0 && rec.Code != tt.wantStatus {
				t.Fatalf("status %d want %d body %q", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody && tt.method != http.MethodHead {
				t.Fatalf("body %q want %q", rec.Body.String(), tt.wantBody)
			}
			if tt.forbid != "" && strings.Contains(rec.Body.String(), tt.forbid) {
				t.Fatalf("leaked %q", tt.forbid)
			}
		})
	}
}

func TestSPAAbsentWhenStaticDirEmpty(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d want 404", rec.Code)
	}
}

func FuzzUnknownKindRejected(f *testing.F) {
	f.Add("pods")
	f.Add("mikrotikrouters")
	f.Add("MIKROTIKROUTERS")
	f.Add("secrets")
	f.Add("x")
	f.Fuzz(func(t *testing.T, kind string) {
		if kind == "" || strings.ContainsAny(kind, "/?#%") {
			t.Skip()
		}
		h := newTestHandler(t)
		rec := doRequest(t, h, http.MethodGet, "/api/resources/"+kind, "")
		_, ok := lookupKind(kind)
		if ok {
			if rec.Code != http.StatusOK {
				t.Fatalf("known kind %q status %d body %s", kind, rec.Code, rec.Body.String())
			}
			return
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unknown kind %q status %d body %s", kind, rec.Code, rec.Body.String())
		}
	})
}
