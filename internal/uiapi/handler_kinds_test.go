package uiapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCRUDAllKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		plural  string
		kind    string
		name    string
		create  string
		update  string
		address func(spec map[string]any) string
		want    string
		updated string
	}{
		{
			plural: kindRouters,
			kind:   "MikroTikRouter",
			name:   "edge",
			create: `{
				"apiVersion":"mikrotik.operator.io/v1alpha1",
				"kind":"MikroTikRouter",
				"metadata":{"name":"edge"},
				"spec":{"address":"192.0.2.10","credentialsSecret":{"name":"creds"}}
			}`,
			update: `{
				"metadata":{"name":"edge"},
				"spec":{"address":"192.0.2.99","credentialsSecret":{"name":"creds"}}
			}`,
			address: func(spec map[string]any) string { return fmt.Sprint(spec["address"]) },
			want:    "192.0.2.10",
			updated: "192.0.2.99",
		},
		{
			plural: kindDNSRecords,
			kind:   "MikroTikDNSRecord",
			name:   "www",
			create: `{
				"metadata":{"name":"www"},
				"spec":{"name":"www.example.com","address":"10.0.0.8","routerRef":"edge"}
			}`,
			update: `{
				"metadata":{"name":"www"},
				"spec":{"name":"www.example.com","address":"10.0.0.9","routerRef":"edge"}
			}`,
			address: func(spec map[string]any) string { return fmt.Sprint(spec["address"]) },
			want:    "10.0.0.8",
			updated: "10.0.0.9",
		},
		{
			plural: kindRoutes,
			kind:   "MikroTikRoute",
			name:   "default",
			create: `{
				"metadata":{"name":"default"},
				"spec":{"destination":"0.0.0.0/0","gateway":"192.0.2.1"}
			}`,
			update: `{
				"metadata":{"name":"default"},
				"spec":{"destination":"0.0.0.0/0","gateway":"192.0.2.254"}
			}`,
			address: func(spec map[string]any) string { return fmt.Sprint(spec["gateway"]) },
			want:    "192.0.2.1",
			updated: "192.0.2.254",
		},
		{
			plural: kindPortForwards,
			kind:   "MikroTikPortForward",
			name:   "https",
			create: `{
				"metadata":{"name":"https"},
				"spec":{"routerRef":"edge","protocol":"tcp","externalPort":8443,"targetPort":443,"targetAddress":"10.0.0.8"}
			}`,
			update: `{
				"metadata":{"name":"https"},
				"spec":{"routerRef":"edge","protocol":"tcp","externalPort":8443,"targetPort":443,"targetAddress":"10.0.0.9"}
			}`,
			address: func(spec map[string]any) string { return fmt.Sprint(spec["targetAddress"]) },
			want:    "10.0.0.8",
			updated: "10.0.0.9",
		},
		{
			plural: kindFirewallRules,
			kind:   "MikroTikFirewallRule",
			name:   "allow-web",
			create: `{
				"metadata":{"name":"allow-web"},
				"spec":{"chain":"forward","action":"accept","protocol":"tcp","destinationPort":"80"}
			}`,
			update: `{
				"metadata":{"name":"allow-web"},
				"spec":{"chain":"forward","action":"drop","protocol":"tcp","destinationPort":"80"}
			}`,
			address: func(spec map[string]any) string { return fmt.Sprint(spec["action"]) },
			want:    "accept",
			updated: "drop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.plural, func(t *testing.T) {
			t.Parallel()
			h := newTestHandler(t)

			created := doRequest(t, h, http.MethodPost, "/api/resources/"+tt.plural+"/app", tt.create)
			if created.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", created.Code, created.Body.String())
			}
			body := decodeMap(t, created)
			if body["kind"] != tt.kind {
				t.Fatalf("kind %v want %s", body["kind"], tt.kind)
			}
			meta := asMap(t, body["metadata"])
			if meta["name"] != tt.name || meta["namespace"] != "app" {
				t.Fatalf("metadata %#v", meta)
			}
			if tt.address(asMap(t, body["spec"])) != tt.want {
				t.Fatalf("spec after create %#v", body["spec"])
			}

			got := doRequest(t, h, http.MethodGet, "/api/resources/"+tt.plural+"/app/"+tt.name, "")
			if got.Code != http.StatusOK {
				t.Fatalf("get: %d %s", got.Code, got.Body.String())
			}

			listed := doRequest(t, h, http.MethodGet, "/api/resources/"+tt.plural+"?namespace=app", "")
			if got := listItems(t, listed); len(got) != 1 {
				t.Fatalf("list: %d items", len(got))
			}

			updated := doRequest(t, h, http.MethodPut, "/api/resources/"+tt.plural+"/app/"+tt.name, tt.update)
			if updated.Code != http.StatusOK {
				t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
			}
			if tt.address(asMap(t, decodeMap(t, updated)["spec"])) != tt.updated {
				t.Fatalf("spec after update %s", updated.Body.String())
			}

			deleted := doRequest(t, h, http.MethodDelete, "/api/resources/"+tt.plural+"/app/"+tt.name, "")
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
			}
			missing := doRequest(t, h, http.MethodGet, "/api/resources/"+tt.plural+"/app/"+tt.name, "")
			if missing.Code != http.StatusNotFound {
				t.Fatalf("get after delete: %d %s", missing.Code, missing.Body.String())
			}
		})
	}
}

func TestCreateForcesNamespaceFromPath(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	rec := doRequest(t, h, http.MethodPost, "/api/resources/mikrotikdnsrecords/app", `{
		"metadata":{"name":"www","namespace":"other"},
		"spec":{"name":"www.example.com","address":"10.0.0.8"}
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	meta := asMap(t, decodeMap(t, rec)["metadata"])
	if meta["namespace"] != "app" {
		t.Fatalf("namespace %v, want path namespace app", meta["namespace"])
	}
}

func TestUpdateCopiesResourceVersion(t *testing.T) {
	t.Parallel()
	router := readyRouter("app", "edge")
	router.ResourceVersion = "7"
	h := newTestHandler(t, router)

	rec := doRequest(t, h, http.MethodPut, "/api/resources/mikrotikrouters/app/edge", `{
		"metadata":{"name":"edge"},
		"spec":{"address":"192.0.2.11","credentialsSecret":{"name":"creds"}}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestDuplicateCreateConflict(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, readyRouter("app", "edge"))
	rec := doRequest(t, h, http.MethodPost, "/api/resources/mikrotikrouters/app", `{
		"metadata":{"name":"edge"},
		"spec":{"address":"192.0.2.10","credentialsSecret":{"name":"creds"}}
	}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d want 409 body %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateAndDeleteMissing(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)

	put := doRequest(t, h, http.MethodPut, "/api/resources/mikrotikroutes/app/missing", `{
		"metadata":{"name":"missing"},
		"spec":{"destination":"10.0.0.0/8","gateway":"192.0.2.1"}
	}`)
	if put.Code != http.StatusNotFound {
		t.Fatalf("put missing: %d %s", put.Code, put.Body.String())
	}
	del := doRequest(t, h, http.MethodDelete, "/api/resources/mikrotikroutes/app/missing", "")
	if del.Code != http.StatusNotFound {
		t.Fatalf("delete missing: %d %s", del.Code, del.Body.String())
	}
}

func TestOverviewReadyCountsAllKinds(t *testing.T) {
	t.Parallel()
	objs := []client.Object{
		readyRouter("app", "edge"),
		notReadyRouter("app", "core"),
		readyDNS("app", "www"),
		notReadyRoute("app", "default"),
		readyPortForward("app", "https"),
		notReadyFirewall("app", "drop-all"),
	}
	h := newTestHandler(t, objs...)
	rec := doRequest(t, h, http.MethodGet, "/api/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	want := map[string]kindCount{
		kindRouters:       {Kind: kindRouters, Count: 2, NotReady: 1},
		kindDNSRecords:    {Kind: kindDNSRecords, Count: 1, NotReady: 0},
		kindRoutes:        {Kind: kindRoutes, Count: 1, NotReady: 1},
		kindPortForwards:  {Kind: kindPortForwards, Count: 1, NotReady: 0},
		kindFirewallRules: {Kind: kindFirewallRules, Count: 1, NotReady: 1},
	}
	for _, raw := range sliceField(t, decodeMap(t, rec), "kinds") {
		item := asMap(t, raw)
		kind := stringField(t, item, "kind")
		got := kindCount{
			Kind:     kind,
			Count:    intFromJSON(t, item["count"]),
			NotReady: intFromJSON(t, item["notReady"]),
		}
		if got != want[kind] {
			t.Fatalf("%s: got %#v want %#v", kind, got, want[kind])
		}
	}
}

func TestListAnnotatesManagedBy(t *testing.T) {
	t.Parallel()
	owned := true
	h := newTestHandler(t, &api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "public",
				Controller: &owned,
			}},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:     "edge",
			Protocol:      "tcp",
			ExternalPort:  80,
			TargetPort:    80,
			TargetAddress: "10.0.0.8",
		},
	})
	rec := doRequest(t, h, http.MethodGet, "/api/resources/mikrotikportforwards", "")
	items := listItems(t, rec)
	if len(items) != 1 {
		t.Fatalf("items %d", len(items))
	}
	managedBy := asMap(t, items[0]["managedBy"])
	if managedBy["kind"] != "Ingress" || managedBy["name"] != "public" {
		t.Fatalf("managedBy %#v", managedBy)
	}
}

func TestOwnedIngressAndHTTPRouteConflict(t *testing.T) {
	t.Parallel()
	owned := true
	ingressChild := &api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-dns",
			Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "web",
				Controller: &owned,
			}},
		},
		Spec: api.MikroTikDNSRecordSpec{Name: "ingress.example.com", Address: "10.0.0.8"},
	}
	routeChild := &api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw-route",
			Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "gateway.networking.k8s.io/v1",
				Kind:       "HTTPRoute",
				Name:       "web",
				Controller: &owned,
			}},
		},
		Spec: api.MikroTikRouteSpec{Destination: "10.0.0.8/32", Gateway: "192.0.2.1"},
	}
	h := newTestHandler(t, ingressChild, routeChild)

	tests := []struct {
		name   string
		path   string
		owner  string
		method string
		body   string
	}{
		{
			name:   "put ingress owned",
			path:   "/api/resources/mikrotikdnsrecords/app/ingress-dns",
			owner:  "Ingress/web",
			method: http.MethodPut,
			body:   `{"metadata":{"name":"ingress-dns"},"spec":{"name":"changed","address":"10.0.0.8"}}`,
		},
		{
			name:   "delete httproute owned",
			path:   "/api/resources/mikrotikroutes/app/gw-route",
			owner:  "HTTPRoute/web",
			method: http.MethodDelete,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, h, tt.method, tt.path, tt.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status %d %s", rec.Code, rec.Body.String())
			}
			body := decodeMap(t, rec)
			if !strings.Contains(stringField(t, body, "error"), tt.owner) {
				t.Fatalf("error %v want %s", body["error"], tt.owner)
			}
		})
	}
}

func readyDNS(namespace, name string) *api.MikroTikDNSRecord {
	return &api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       api.MikroTikDNSRecordSpec{Name: name + ".example.com", Address: "10.0.0.8"},
		Status: api.MikroTikDNSRecordStatus{
			Applied: true,
			Conditions: []metav1.Condition{{
				Type:   readyConditionType,
				Status: metav1.ConditionTrue,
			}},
		},
	}
}

func notReadyRoute(namespace, name string) *api.MikroTikRoute {
	return &api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       api.MikroTikRouteSpec{Destination: "0.0.0.0/0", Gateway: "192.0.2.1"},
		Status: api.MikroTikRouteStatus{
			Applied: false,
			Conditions: []metav1.Condition{{
				Type:   readyConditionType,
				Status: metav1.ConditionFalse,
			}},
		},
	}
}

func readyPortForward(namespace, name string) *api.MikroTikPortForward {
	return &api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:     "edge",
			Protocol:      "tcp",
			ExternalPort:  443,
			TargetPort:    443,
			TargetAddress: "10.0.0.8",
		},
		Status: api.MikroTikPortForwardStatus{
			Applied: true,
			Conditions: []metav1.Condition{{
				Type:   readyConditionType,
				Status: metav1.ConditionTrue,
			}},
		},
	}
}

func notReadyFirewall(namespace, name string) *api.MikroTikFirewallRule {
	return &api.MikroTikFirewallRule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       api.MikroTikFirewallRuleSpec{Chain: "forward", Action: "drop"},
		Status: api.MikroTikFirewallRuleStatus{
			Applied: false,
			Conditions: []metav1.Condition{{
				Type:   readyConditionType,
				Status: metav1.ConditionFalse,
			}},
		},
	}
}
