package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const enabledManifest = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mtop-ui
  labels:
    app.kubernetes.io/component: ui
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/component: ui
  template:
    metadata:
      labels:
        app.kubernetes.io/component: ui
    spec:
      containers:
      - name: ui
        image: ghcr.io/zeljkobenovic/mikrotik-operator-ui:v0.1.0
        args:
        - "-bind-address=:8080"
        - "-static-dir=/ui"
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: ["ALL"]
        readinessProbe:
          httpGet:
            path: /readyz
            port: http
        livenessProbe:
          httpGet:
            path: /healthz
            port: http
        resources:
          requests:
            cpu: 25m
            memory: 32Mi
          limits:
            cpu: 200m
            memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: mtop-ui
  labels:
    app.kubernetes.io/component: ui
spec:
  type: ClusterIP
  ports:
  - name: http
    port: 8080
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mtop-ui
  labels:
    app.kubernetes.io/component: ui
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mtop-ui
  labels:
    app.kubernetes.io/component: ui
rules:
- apiGroups: ["mikrotik.operator.io"]
  resources:
  - mikrotikrouters
  - mikrotikdnsrecords
  - mikrotikportforwards
  - mikrotikroutes
  - mikrotikfirewallrules
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [""]
  resources: [secrets]
  verbs: [list]
- apiGroups: [""]
  resources: [namespaces]
  verbs: [list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: mtop-ui
  labels:
    app.kubernetes.io/component: ui
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: mtop-ui
`

func TestValidateRenderedEnabled(t *testing.T) {
	t.Parallel()
	docs, err := decodeDocs([]byte(enabledManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRendered(docs, true, true); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRenderedDisabledRejectsUI(t *testing.T) {
	t.Parallel()
	docs, err := decodeDocs([]byte(enabledManifest))
	if err != nil {
		t.Fatal(err)
	}
	err = validateRendered(docs, false, true)
	if err == nil || !strings.Contains(err.Error(), "ui.enabled=false") {
		t.Fatalf("error %v", err)
	}
}

func TestValidateRenderedDisabledEmpty(t *testing.T) {
	t.Parallel()
	docs, err := decodeDocs([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: other\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRendered(docs, false, true); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRenderedMissingRBAC(t *testing.T) {
	t.Parallel()
	docs, err := decodeDocs([]byte(enabledManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRendered(docs, true, false); err == nil {
		t.Fatal("expected ClusterRole present error")
	}
}

func TestValidateClusterRoleRejectsSecretGet(t *testing.T) {
	t.Parallel()
	manifest := strings.Replace(enabledManifest, "resources: [secrets]\n  verbs: [list]", "resources: [secrets]\n  verbs: [get, list]", 1)
	docs, err := decodeDocs([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	err = validateRendered(docs, true, true)
	if err == nil || !strings.Contains(err.Error(), "secrets verbs") {
		t.Fatalf("error %v", err)
	}
}

func TestValidateClusterRoleRejectsStatusAndPods(t *testing.T) {
	t.Parallel()
	manifest := strings.Replace(
		enabledManifest,
		"- mikrotikfirewallrules\n  verbs: [get, list, watch, create, update, patch, delete]",
		"- mikrotikfirewallrules\n  - mikrotikrouters/status\n  verbs: [get, list, watch, create, update, patch, delete]",
		1,
	)
	docs, err := decodeDocs([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	err = validateRendered(docs, true, true)
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("error %v", err)
	}

	withPods := strings.Replace(enabledManifest, "resources: [namespaces]\n  verbs: [list]", "resources: [namespaces, pods]\n  verbs: [list]", 1)
	docs, err = decodeDocs([]byte(withPods))
	if err != nil {
		t.Fatal(err)
	}
	err = validateRendered(docs, true, true)
	if err == nil || !strings.Contains(err.Error(), "pods") {
		t.Fatalf("error %v", err)
	}
}

func TestValidateDeploymentSecurity(t *testing.T) {
	t.Parallel()
	manifest := strings.Replace(enabledManifest, "readOnlyRootFilesystem: true", "readOnlyRootFilesystem: false", 1)
	docs, err := decodeDocs([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	err = validateRendered(docs, true, true)
	if err == nil || !strings.Contains(err.Error(), "readOnlyRootFilesystem") {
		t.Fatalf("error %v", err)
	}
}

func TestChartNotesTemplateHasWarnings(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	notesPath := filepath.Join(filepath.Dir(file), "..", "..", "charts", "mikrotik-operator", "templates", "NOTES.txt")
	data, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(strings.ToLower(text), "no authentication") {
		t.Fatal("chart NOTES.txt must warn that the UI has no authentication")
	}
	if !strings.Contains(text, ".Values.ui.enabled") {
		t.Fatal("chart NOTES.txt must branch on ui.enabled")
	}
	if !strings.Contains(strings.ToLower(text), "port-forward") {
		t.Fatal("chart NOTES.txt must document port-forward")
	}
}

func TestValidateNotes(t *testing.T) {
	t.Parallel()
	if err := validateNotes("The admin UI has no authentication. port-forward svc/ui", true); err != nil {
		t.Fatal(err)
	}
	if err := validateNotes("UI enabled", true); err == nil {
		t.Fatal("expected missing auth warning")
	}
	if err := validateNotes("The admin UI has no authentication.", true); err == nil {
		t.Fatal("expected missing port-forward")
	}
}
