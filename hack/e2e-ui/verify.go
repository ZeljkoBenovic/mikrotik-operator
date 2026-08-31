package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "UI E2E verification failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Admin UI E2E verification passed")
}

func run() error {
	base := env("E2E_UI_BASE_URL", "http://127.0.0.1:18080")
	ns := env("E2E_UI_NAMESPACE", "e2e-ui")
	client := &http.Client{Timeout: 15 * time.Second}

	if err := waitPlain(client, base+"/healthz", "ok", 30*time.Second); err != nil {
		return err
	}
	if err := assertPlain(client, http.MethodGet, base+"/readyz", http.StatusOK, "ok"); err != nil {
		return err
	}

	index, err := do(client, http.MethodGet, base+"/", nil)
	if err != nil {
		return err
	}
	if index.status != http.StatusOK {
		return fmt.Errorf("GET / status %d", index.status)
	}
	if !strings.Contains(strings.ToLower(index.body), "<!doctype html") && !strings.Contains(index.body, "<div id=\"root\"") {
		return fmt.Errorf("GET / did not look like the SPA: %s", truncate(index.body, 200))
	}

	fallback, err := do(client, http.MethodGet, base+"/routers/"+ns+"/missing", nil)
	if err != nil {
		return err
	}
	if fallback.status != http.StatusOK {
		return fmt.Errorf("SPA fallback status %d", fallback.status)
	}

	if err := assertJSONStatus(client, http.MethodGet, base+"/api/nope", http.StatusNotFound, nil); err != nil {
		return err
	}
	if err := assertJSONStatus(client, http.MethodGet, base+"/api/resources/pods", http.StatusBadRequest, nil); err != nil {
		return err
	}

	operatorNS := env("E2E_UI_OPERATOR_NAMESPACE", "mikrotik-operator-system")
	cfg, err := getJSON(client, base+"/api/config")
	if err != nil {
		return err
	}
	if cfg["namespace"] != operatorNS {
		return fmt.Errorf("config namespace %v want %s", cfg["namespace"], operatorNS)
	}

	overview, err := getJSON(client, base+"/api/overview")
	if err != nil {
		return err
	}
	kinds, ok := overview["kinds"].([]any)
	if !ok {
		return fmt.Errorf("overview kinds is %T, want array", overview["kinds"])
	}
	if len(kinds) != 7 {
		return fmt.Errorf("overview kinds=%d want 7", len(kinds))
	}

	namespaces, err := getJSON(client, base+"/api/namespaces")
	if err != nil {
		return err
	}
	if !jsonNamesContain(namespaces["items"], ns) {
		return fmt.Errorf("namespaces missing %s: %#v", ns, namespaces["items"])
	}

	secrets, status, raw, err := getRaw(client, base+"/api/secrets/"+ns)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("secrets status %d body %s", status, raw)
	}
	for _, leak := range []string{"super-secret-e2e", `"data"`, `"stringData"`} {
		if strings.Contains(raw, leak) {
			return fmt.Errorf("secret payload leaked %q in %s", leak, raw)
		}
	}
	if !jsonNamesContain(secrets["items"], "router-creds") {
		return fmt.Errorf("secrets missing router-creds: %s", raw)
	}

	resources := []struct {
		kind string
		name string
		body string
	}{
		{
			kind: "mikrotikrouters",
			name: "ui-edge",
			body: `{
				"apiVersion":"mikrotik.operator.io/v1alpha1",
				"kind":"MikroTikRouter",
				"metadata":{"name":"ui-edge"},
				"spec":{"address":"192.0.2.10","credentialsSecret":{"name":"router-creds"}}
			}`,
		},
		{
			kind: "mikrotikdnsrecords",
			name: "ui-dns",
			body: `{
				"metadata":{"name":"ui-dns"},
				"spec":{"name":"ui.e2e.home.arpa","address":"10.99.0.20","routerRef":"ui-edge"}
			}`,
		},
		{
			kind: "mikrotikroutes",
			name: "ui-route",
			body: `{
				"metadata":{"name":"ui-route"},
				"spec":{"destination":"10.99.0.0/24","gateway":"192.0.2.1","routerRef":"ui-edge"}
			}`,
		},
		{
			kind: "mikrotikportforwards",
			name: "ui-fwd",
			body: `{
				"metadata":{"name":"ui-fwd"},
				"spec":{"routerRef":"ui-edge","protocol":"tcp","externalPort":8443,"targetPort":443,"targetAddress":"10.99.0.20"}
			}`,
		},
		{
			kind: "mikrotikfirewallrules",
			name: "ui-fw",
			body: `{
				"metadata":{"name":"ui-fw"},
				"spec":{"routerRef":"ui-edge","chain":"forward","action":"accept","protocol":"tcp","destinationPort":"443"}
			}`,
		},
		{
			kind: "mikrotikbackups",
			name: "ui-backup",
			body: `{
				"metadata":{"name":"ui-backup"},
				"spec":{"routerRef":"ui-edge"}
			}`,
		},
		{
			kind: "mikrotikrestores",
			name: "ui-restore",
			body: `{
				"metadata":{"name":"ui-restore"},
				"spec":{"backupRef":{"name":"ui-backup"},"routerRef":"ui-edge"}
			}`,
		},
	}

	for _, resource := range resources {
		create, err := do(client, http.MethodPost, base+"/api/resources/"+resource.kind+"/"+ns, strings.NewReader(resource.body))
		if err != nil {
			return err
		}
		if create.status != http.StatusCreated {
			return fmt.Errorf("create %s: %d %s", resource.kind, create.status, create.body)
		}
		got, err := getJSON(client, base+"/api/resources/"+resource.kind+"/"+ns+"/"+resource.name)
		if err != nil {
			return err
		}
		meta, err := asMap(got["metadata"])
		if err != nil {
			return fmt.Errorf("get %s metadata: %w", resource.kind, err)
		}
		if meta["name"] != resource.name || meta["namespace"] != ns {
			return fmt.Errorf("get %s metadata %#v", resource.kind, meta)
		}
		if _, ok := got["managedBy"]; ok {
			return fmt.Errorf("standalone %s should omit managedBy", resource.kind)
		}
	}

	owned, err := getJSON(client, base+"/api/resources/mikrotikdnsrecords/"+ns+"/owned-dns")
	if err != nil {
		return err
	}
	managedBy, err := asMap(owned["managedBy"])
	if err != nil {
		return fmt.Errorf("owned managedBy: %w", err)
	}
	if managedBy["kind"] != "Service" || managedBy["name"] != "web" {
		return fmt.Errorf("owned managedBy %#v", managedBy)
	}
	putOwned, err := do(client, http.MethodPut, base+"/api/resources/mikrotikdnsrecords/"+ns+"/owned-dns", strings.NewReader(`{
		"metadata":{"name":"owned-dns"},
		"spec":{"name":"changed.e2e.home.arpa","address":"10.99.0.8"}
	}`))
	if err != nil {
		return err
	}
	if putOwned.status != http.StatusConflict {
		return fmt.Errorf("put owned: %d %s", putOwned.status, putOwned.body)
	}
	if !strings.Contains(putOwned.body, "Service/web") {
		return fmt.Errorf("owned conflict body %s", putOwned.body)
	}
	delOwned, err := do(client, http.MethodDelete, base+"/api/resources/mikrotikdnsrecords/"+ns+"/owned-dns", nil)
	if err != nil {
		return err
	}
	if delOwned.status != http.StatusConflict {
		return fmt.Errorf("delete owned: %d %s", delOwned.status, delOwned.body)
	}

	del, err := do(client, http.MethodDelete, base+"/api/resources/mikrotikfirewallrules/"+ns+"/ui-fw", nil)
	if err != nil {
		return err
	}
	if del.status != http.StatusNoContent {
		return fmt.Errorf("delete standalone: %d %s", del.status, del.body)
	}
	return nil
}

type httpResult struct {
	status int
	body   string
}

func do(client *http.Client, method, url string, body io.Reader) (result httpResult, err error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return httpResult{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close response body: %w", closeErr)
		}
	}()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{status: res.StatusCode, body: string(data)}, err
}

func assertPlain(client *http.Client, method, url string, status int, body string) error {
	got, err := do(client, method, url, nil)
	if err != nil {
		return err
	}
	if got.status != status {
		return fmt.Errorf("%s %s status %d want %d body %s", method, url, got.status, status, got.body)
	}
	if body != "" && strings.TrimSpace(got.body) != body {
		return fmt.Errorf("%s %s body %q want %q", method, url, got.body, body)
	}
	return nil
}

func assertJSONStatus(client *http.Client, method, url string, status int, body io.Reader) error {
	got, err := do(client, method, url, body)
	if err != nil {
		return err
	}
	if got.status != status {
		return fmt.Errorf("%s %s status %d want %d body %s", method, url, got.status, status, got.body)
	}
	return nil
}

func getJSON(client *http.Client, url string) (map[string]any, error) {
	obj, status, raw, err := getRaw(client, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s status %d body %s", url, status, raw)
	}
	return obj, nil
}

func getRaw(client *http.Client, url string) (map[string]any, int, string, error) {
	got, err := do(client, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, "", err
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(got.body), &obj); err != nil {
		return nil, got.status, got.body, fmt.Errorf("decode %s: %w body=%s", url, err, got.body)
	}
	return obj, got.status, got.body, nil
}

func jsonNamesContain(items any, name string) bool {
	list, ok := items.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		switch v := item.(type) {
		case string:
			if v == name {
				return true
			}
		case map[string]any:
			if v["name"] == name {
				return true
			}
		}
	}
	return false
}

func asMap(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object, got %T", v)
	}
	return m, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func waitPlain(client *http.Client, url, body string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		last = assertPlain(client, http.MethodGet, url, http.StatusOK, body)
		if last == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("wait for %s: %w", url, last)
}
