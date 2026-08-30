package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

const uiComponent = "ui"

var requiredCRDs = []string{
	"mikrotikrouters",
	"mikrotikdnsrecords",
	"mikrotikportforwards",
	"mikrotikroutes",
	"mikrotikfirewallrules",
}

var requiredVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete"}

func main() {
	ui := flag.Bool("ui", true, "whether the rendered chart should include UI resources")
	rbac := flag.Bool("rbac", true, "whether UI ClusterRole/Binding should be present")
	notes := flag.String("notes", "", "optional NOTES.txt contents or file to check for the no-auth warning")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: uichart [-ui=true] [-rbac=true] [-notes path] <rendered.yaml>")
		os.Exit(2)
	}

	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "read rendered manifests: %v\n", err)
		os.Exit(1)
	}
	docs, err := decodeDocs(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse rendered manifests: %v\n", err)
		os.Exit(1)
	}
	if err := validateRendered(docs, *ui, *rbac); err != nil {
		fmt.Fprintf(os.Stderr, "UI chart validation failed:\n%v\n", err)
		os.Exit(1)
	}
	if *notes != "" {
		notesData, err := os.ReadFile(*notes)
		if err != nil {
			notesData = []byte(*notes)
		}
		if err := validateNotes(string(notesData), *ui); err != nil {
			fmt.Fprintf(os.Stderr, "UI NOTES validation failed: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("UI chart validation passed")
}

func decodeDocs(data []byte) ([]unstructured.Unstructured, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	docs := []unstructured.Unstructured{}
	for document := 1; ; document++ {
		raw, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", document, err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var typeMeta metav1.TypeMeta
		if err := yaml.Unmarshal(raw, &typeMeta); err != nil {
			return nil, fmt.Errorf("document %d type meta: %w", document, err)
		}
		if typeMeta.Kind == "" {
			continue
		}
		obj := unstructured.Unstructured{}
		if err := yaml.Unmarshal(raw, &obj.Object); err != nil {
			return nil, fmt.Errorf("document %d: %w", document, err)
		}
		docs = append(docs, obj)
	}
	return docs, nil
}

func validateRendered(docs []unstructured.Unstructured, wantUI, wantRBAC bool) error {
	uiDocs := filterUI(docs)
	if !wantUI {
		if len(uiDocs) > 0 {
			names := make([]string, 0, len(uiDocs))
			for _, doc := range uiDocs {
				names = append(names, doc.GetKind()+"/"+doc.GetName())
			}
			return fmt.Errorf("ui.enabled=false but found UI resources: %s", strings.Join(names, ", "))
		}
		return nil
	}

	got := map[string]unstructured.Unstructured{}
	for _, doc := range uiDocs {
		got[doc.GetKind()] = doc
	}
	required := []string{"Deployment", "Service", "ServiceAccount"}
	if wantRBAC {
		required = append(required, "ClusterRole", "ClusterRoleBinding")
	}
	var problems []error
	for _, kind := range required {
		if _, ok := got[kind]; !ok {
			problems = append(problems, fmt.Errorf("missing UI %s", kind))
		}
	}
	if !wantRBAC {
		if _, ok := got["ClusterRole"]; ok {
			problems = append(problems, errors.New("UI ClusterRole present despite rbac.create=false"))
		}
		if _, ok := got["ClusterRoleBinding"]; ok {
			problems = append(problems, errors.New("UI ClusterRoleBinding present despite rbac.create=false"))
		}
	}
	if dep, ok := got["Deployment"]; ok {
		problems = append(problems, validateUIDeployment(dep)...)
	}
	if svc, ok := got["Service"]; ok {
		problems = append(problems, validateUIService(svc)...)
	}
	if role, ok := got["ClusterRole"]; ok {
		problems = append(problems, validateUIClusterRole(role)...)
	}
	return errors.Join(problems...)
}

func filterUI(docs []unstructured.Unstructured) []unstructured.Unstructured {
	out := []unstructured.Unstructured{}
	for _, doc := range docs {
		if doc.GetLabels()["app.kubernetes.io/component"] == uiComponent {
			out = append(out, doc)
		}
	}
	return out
}

func validateUIDeployment(doc unstructured.Unstructured) []error {
	raw, err := yaml.Marshal(doc.Object)
	if err != nil {
		return []error{fmt.Errorf("marshal deployment: %w", err)}
	}
	var deploy appsv1.Deployment
	if err := yaml.Unmarshal(raw, &deploy); err != nil {
		return []error{fmt.Errorf("decode deployment: %w", err)}
	}
	var problems []error
	if deploy.Spec.Replicas != nil && *deploy.Spec.Replicas < 1 {
		problems = append(problems, errors.New("UI deployment replicas must be at least 1"))
	}
	if len(deploy.Spec.Template.Spec.Containers) != 1 {
		problems = append(problems, fmt.Errorf("UI deployment wants 1 container, got %d", len(deploy.Spec.Template.Spec.Containers)))
		return problems
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	if !containsArg(container.Args, "-static-dir=/ui") {
		problems = append(problems, fmt.Errorf("UI container args %v missing -static-dir=/ui", container.Args))
	}
	if !hasBindAddress(container.Args) {
		problems = append(problems, fmt.Errorf("UI container args %v missing -bind-address", container.Args))
	}
	if container.SecurityContext == nil || container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		problems = append(problems, errors.New("UI container must set readOnlyRootFilesystem"))
	}
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
		problems = append(problems, errors.New("UI container must drop privilege escalation"))
	}
	if container.SecurityContext == nil || container.SecurityContext.Capabilities == nil || !dropsAll(container.SecurityContext.Capabilities.Drop) {
		problems = append(problems, errors.New("UI container must drop all capabilities"))
	}
	if container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet == nil || container.ReadinessProbe.HTTPGet.Path != "/readyz" {
		problems = append(problems, errors.New("UI readiness probe must GET /readyz"))
	}
	if container.LivenessProbe == nil || container.LivenessProbe.HTTPGet == nil || container.LivenessProbe.HTTPGet.Path != "/healthz" {
		problems = append(problems, errors.New("UI liveness probe must GET /healthz"))
	}
	if container.Resources.Requests.Cpu().IsZero() || container.Resources.Requests.Memory().IsZero() {
		problems = append(problems, errors.New("UI container must set CPU and memory requests"))
	}
	if container.Resources.Limits.Cpu().IsZero() || container.Resources.Limits.Memory().IsZero() {
		problems = append(problems, errors.New("UI container must set CPU and memory limits"))
	}
	return problems
}

func validateUIService(doc unstructured.Unstructured) []error {
	raw, err := yaml.Marshal(doc.Object)
	if err != nil {
		return []error{fmt.Errorf("marshal service: %w", err)}
	}
	var svc corev1.Service
	if err := yaml.Unmarshal(raw, &svc); err != nil {
		return []error{fmt.Errorf("decode service: %w", err)}
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP && svc.Spec.Type != "" {
		return []error{fmt.Errorf("UI service type %q, want ClusterIP", svc.Spec.Type)}
	}
	return nil
}

func validateUIClusterRole(doc unstructured.Unstructured) []error {
	raw, err := yaml.Marshal(doc.Object)
	if err != nil {
		return []error{fmt.Errorf("marshal clusterrole: %w", err)}
	}
	var role rbacv1.ClusterRole
	if err := yaml.Unmarshal(raw, &role); err != nil {
		return []error{fmt.Errorf("decode clusterrole: %w", err)}
	}
	var problems []error
	crdRule := findRule(role.Rules, "mikrotik.operator.io")
	if crdRule == nil {
		problems = append(problems, errors.New("UI ClusterRole missing mikrotik.operator.io rule"))
	} else {
		if !sameSet(crdRule.Resources, requiredCRDs) {
			problems = append(problems, fmt.Errorf("UI CRD resources %v want %v", crdRule.Resources, requiredCRDs))
		}
		if !sameSet(crdRule.Verbs, requiredVerbs) {
			problems = append(problems, fmt.Errorf("UI CRD verbs %v want %v", crdRule.Verbs, requiredVerbs))
		}
		for _, resource := range crdRule.Resources {
			if strings.Contains(resource, "/status") {
				problems = append(problems, fmt.Errorf("UI ClusterRole must not write status subresources, found %s", resource))
			}
		}
	}
	secretRule := findCoreRule(role.Rules, "secrets")
	if secretRule == nil {
		problems = append(problems, errors.New("UI ClusterRole missing secrets list"))
	} else if !sameSet(secretRule.Verbs, []string{"list"}) {
		problems = append(problems, fmt.Errorf("secrets verbs %v, want [list] only", secretRule.Verbs))
	}
	nsRule := findCoreRule(role.Rules, "namespaces")
	if nsRule == nil {
		problems = append(problems, errors.New("UI ClusterRole missing namespaces list"))
	} else if !sameSet(nsRule.Verbs, []string{"list"}) {
		problems = append(problems, fmt.Errorf("namespaces verbs %v, want [list] only", nsRule.Verbs))
	}
	forbidden := []string{"pods", "nodes", "leases", "configmaps", "events"}
	for _, name := range forbidden {
		if findAnyResource(role.Rules, name) {
			problems = append(problems, fmt.Errorf("UI ClusterRole must not grant %s", name))
		}
	}
	return problems
}

func validateNotes(notes string, uiEnabled bool) error {
	lower := strings.ToLower(notes)
	if !strings.Contains(lower, "no authentication") {
		return errors.New("NOTES.txt must warn that the UI has no authentication")
	}
	if uiEnabled && !strings.Contains(lower, "port-forward") {
		return errors.New("enabled NOTES.txt must mention port-forward")
	}
	return nil
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasBindAddress(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-bind-address=") {
			return true
		}
	}
	return false
}

func dropsAll(caps []corev1.Capability) bool {
	for _, cap := range caps {
		if cap == "ALL" {
			return true
		}
	}
	return false
}

func findRule(rules []rbacv1.PolicyRule, apiGroup string) *rbacv1.PolicyRule {
	for i := range rules {
		for _, group := range rules[i].APIGroups {
			if group == apiGroup {
				return &rules[i]
			}
		}
	}
	return nil
}

func findCoreRule(rules []rbacv1.PolicyRule, resource string) *rbacv1.PolicyRule {
	for i := range rules {
		core := false
		for _, group := range rules[i].APIGroups {
			if group == "" {
				core = true
				break
			}
		}
		if !core {
			continue
		}
		for _, name := range rules[i].Resources {
			if name == resource {
				return &rules[i]
			}
		}
	}
	return nil
}

func findAnyResource(rules []rbacv1.PolicyRule, resource string) bool {
	for _, rule := range rules {
		for _, name := range rule.Resources {
			if name == resource {
				return true
			}
		}
	}
	return false
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	have := map[string]bool{}
	for _, item := range got {
		have[item] = true
	}
	for _, item := range want {
		if !have[item] {
			return false
		}
	}
	return true
}
