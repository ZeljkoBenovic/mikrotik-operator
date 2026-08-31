package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	customresourcevalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

func TestValidatePathsRejectsMalformedYAML(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "malformed.yaml", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: [\n")
	_, err := validatePaths(t.Context(), []string{path})
	if err == nil || !strings.Contains(err.Error(), "parse type metadata") {
		t.Fatalf("validatePaths() error = %v, want malformed YAML error", err)
	}
}

func TestValidatePathsRejectsZeroCRDs(t *testing.T) {
	t.Parallel()

	_, err := validatePaths(t.Context(), []string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no CustomResourceDefinition documents found") {
		t.Fatalf("validatePaths() error = %v, want zero CRD error", err)
	}
}

func TestValidatePathsRejectsNonCRDDocument(t *testing.T) {
	t.Parallel()

	path := writeFixture(t, "configmap.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: unexpected\n")
	_, err := validatePaths(t.Context(), []string{path})
	if err == nil || !strings.Contains(err.Error(), "expected apiextensions.k8s.io/v1 CustomResourceDefinition") {
		t.Fatalf("validatePaths() error = %v, want non-CRD error", err)
	}
}

func TestValidatePathsRejectsNonStructuralSchema(t *testing.T) {
	t.Parallel()

	manifest := validCRD("widgets", "Widget")
	invalid := strings.Replace(
		manifest,
		"        properties:\n          spec:\n            type: object",
		"        anyOf:\n        - properties:\n            spec:\n              type: object",
		1,
	)
	if invalid == manifest {
		t.Fatal("test fixture replacement did not modify the schema")
	}
	path := writeFixture(t, "invalid.yaml", invalid)
	_, err := validatePaths(t.Context(), []string{path})
	if err == nil || !strings.Contains(err.Error(), "structural") {
		t.Fatalf("validatePaths() error = %v, want structural schema error", err)
	}
}

func TestValidatePathsAcceptsMultipleCRDs(t *testing.T) {
	t.Parallel()

	manifest := validCRD("widgets", "Widget") + "---\n" + validCRD("gadgets", "Gadget")
	path := writeFixture(t, "multiple.yaml", manifest)
	count, err := validatePaths(t.Context(), []string{path})
	if err != nil {
		t.Fatalf("validatePaths() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("validatePaths() count = %d, want 2", count)
	}
}

func TestValidatePathsAcceptsRepositoryCRDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "raw", path: filepath.Join("..", "..", "config", "crd", "bases")},
		{name: "chart", path: filepath.Join("..", "..", "charts", "mikrotik-operator", "crds")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			count, err := validatePaths(context.Background(), []string{test.path})
			if err != nil {
				t.Fatalf("validatePaths() error = %v", err)
			}
			if count != 7 {
				t.Fatalf("validatePaths() count = %d, want 7", count)
			}
		})
	}
}

func TestMikroTikRouterSchemaValidation(t *testing.T) {
	t.Parallel()

	openAPIValidator, structural := repositoryCRDSchemaValidators(
		t,
		"mikrotik.operator.io_mikrotikrouters.yaml",
	)
	celValidator := cel.NewValidator(structural, true, celconfig.PerCallLimit)
	if celValidator == nil {
		t.Fatal("MikroTikRouter schema does not contain CEL validation rules")
	}

	tests := []struct {
		name  string
		spec  map[string]any
		valid bool
	}{
		{name: "empty spec", spec: map[string]any{}},
		{name: "legacy address only", spec: map[string]any{"address": "192.0.2.1"}},
		{name: "legacy credentials only", spec: map[string]any{
			"credentialsSecret": map[string]any{"name": "router-credentials"},
		}},
		{name: "legacy empty address", spec: map[string]any{
			"address":           "",
			"credentialsSecret": map[string]any{"name": "router-credentials"},
		}},
		{name: "legacy empty credentials name", spec: map[string]any{
			"address":           "192.0.2.1",
			"credentialsSecret": map[string]any{"name": ""},
		}},
		{name: "valid legacy", valid: true, spec: map[string]any{
			"address":           "192.0.2.1",
			"credentialsSecret": map[string]any{"name": "router-credentials"},
		}},
		{name: "valid legacy with an empty endpoint list", valid: true, spec: map[string]any{
			"address":           "192.0.2.1",
			"credentialsSecret": map[string]any{"name": "router-credentials"},
			"routers":           []any{},
		}},
		{name: "valid endpoint list", valid: true, spec: map[string]any{
			"routers": []any{map[string]any{
				"name":              "primary",
				"address":           "192.0.2.1",
				"credentialsSecret": map[string]any{"name": "router-credentials"},
			}},
		}},
		{name: "empty endpoint list", spec: map[string]any{"routers": []any{}}},
		{name: "endpoint with empty identity fields", spec: map[string]any{
			"routers": []any{map[string]any{
				"name":              "",
				"address":           "",
				"credentialsSecret": map[string]any{"name": ""},
			}},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validationErrors := validateCustomResourceSpec(
				t,
				openAPIValidator,
				structural,
				celValidator,
				"MikroTikRouter",
				test.spec,
			)
			if test.valid && len(validationErrors) > 0 {
				t.Fatalf("valid object rejected: %v", validationErrors.ToAggregate())
			}
			if !test.valid && len(validationErrors) == 0 {
				t.Fatal("invalid object accepted")
			}
		})
	}
}

func TestMikroTikPortForwardSchemaValidation(t *testing.T) {
	t.Parallel()

	openAPIValidator, structural := repositoryCRDSchemaValidators(
		t,
		"mikrotik.operator.io_mikrotikportforwards.yaml",
	)
	celValidator := cel.NewValidator(structural, true, celconfig.PerCallLimit)
	if celValidator == nil {
		t.Fatal("MikroTikPortForward schema does not contain CEL validation rules")
	}

	tests := []struct {
		name    string
		targets map[string]any
		valid   bool
	}{
		{name: "no target", targets: map[string]any{}},
		{name: "blank address", targets: map[string]any{"targetAddress": ""}},
		{name: "blank service name", targets: map[string]any{
			"serviceRef": map[string]any{"name": ""},
		}},
		{name: "blank pod name", targets: map[string]any{
			"podRef": map[string]any{"name": ""},
		}},
		{name: "service and pod", targets: map[string]any{
			"serviceRef": map[string]any{"name": "web"},
			"podRef":     map[string]any{"name": "web-0"},
		}},
		{name: "address and pod", targets: map[string]any{
			"targetAddress": "192.0.2.10",
			"podRef":        map[string]any{"name": "web-0"},
		}},
		{name: "all target fields", targets: map[string]any{
			"targetAddress": "192.0.2.10",
			"serviceRef":    map[string]any{"name": "web"},
			"podRef":        map[string]any{"name": "web-0"},
		}},
		{name: "valid address", valid: true, targets: map[string]any{
			"targetAddress": "192.0.2.10",
		}},
		{name: "valid service", valid: true, targets: map[string]any{
			"serviceRef": map[string]any{"name": "web"},
		}},
		{name: "valid pod", valid: true, targets: map[string]any{
			"podRef": map[string]any{"name": "web-0"},
		}},
		{name: "valid generated address and service", valid: true, targets: map[string]any{
			"targetAddress": "192.0.2.10",
			"serviceRef":    map[string]any{"namespace": "default", "name": "web-nodeport"},
		}},
		{name: "valid destination address", valid: true, targets: map[string]any{
			"targetAddress":      "192.0.2.10",
			"destinationAddress": "198.51.100.10",
		}},
		{name: "blank destination address", targets: map[string]any{
			"targetAddress":      "192.0.2.10",
			"destinationAddress": "",
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validationErrors := validateCustomResourceSpec(
				t,
				openAPIValidator,
				structural,
				celValidator,
				"MikroTikPortForward",
				portForwardSpec(test.targets),
			)
			if test.valid && len(validationErrors) > 0 {
				t.Fatalf("valid object rejected: %v", validationErrors.ToAggregate())
			}
			if !test.valid && len(validationErrors) == 0 {
				t.Fatal("invalid object accepted")
			}
		})
	}
}

func TestMikroTikBackupSchemaValidation(t *testing.T) {
	t.Parallel()

	openAPIValidator, structural := repositoryCRDSchemaValidators(
		t,
		"mikrotik.operator.io_mikrotikbackups.yaml",
	)
	celValidator := cel.NewValidator(structural, true, celconfig.PerCallLimit)
	if celValidator == nil {
		t.Fatal("MikroTikBackup schema does not contain CEL validation rules")
	}

	tests := []struct {
		name  string
		spec  map[string]any
		valid bool
	}{
		{name: "empty spec", spec: map[string]any{}},
		{name: "blank routerRef", spec: map[string]any{"routerRef": ""}},
		{name: "manual snapshot", valid: true, spec: map[string]any{"routerRef": "edge"}},
		{name: "scheduled policy", valid: true, spec: map[string]any{
			"routerRef": "edge",
			"schedule":  "0 2 * * *",
			"retention": int64(7),
		}},
		{name: "remote disabled", valid: true, spec: map[string]any{
			"routerRef": "edge",
			"remote":    map[string]any{"enabled": false},
		}},
		{name: "remote enabled", spec: map[string]any{
			"routerRef": "edge",
			"remote":    map[string]any{"enabled": true},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validationErrors := validateCustomResourceSpec(
				t,
				openAPIValidator,
				structural,
				celValidator,
				"MikroTikBackup",
				test.spec,
			)
			if test.valid && len(validationErrors) > 0 {
				t.Fatalf("valid object rejected: %v", validationErrors.ToAggregate())
			}
			if !test.valid && len(validationErrors) == 0 {
				t.Fatal("invalid object accepted")
			}
		})
	}
}

func TestMikroTikRestoreSchemaValidation(t *testing.T) {
	t.Parallel()

	openAPIValidator, structural := repositoryCRDSchemaValidators(
		t,
		"mikrotik.operator.io_mikrotikrestores.yaml",
	)
	celValidator := cel.NewValidator(structural, true, celconfig.PerCallLimit)
	if celValidator == nil {
		t.Fatal("MikroTikRestore schema does not contain CEL validation rules")
	}

	tests := []struct {
		name  string
		spec  map[string]any
		valid bool
	}{
		{name: "empty spec", spec: map[string]any{}},
		{name: "backup only", spec: map[string]any{
			"backupRef": map[string]any{"name": "once"},
		}},
		{name: "routerRef", valid: true, spec: map[string]any{
			"backupRef": map[string]any{"name": "once"},
			"routerRef": "edge",
		}},
		{name: "inline connection", valid: true, spec: map[string]any{
			"backupRef": map[string]any{"name": "once"},
			"connection": map[string]any{
				"address":           "192.0.2.1",
				"credentialsSecret": map[string]any{"name": "creds"},
			},
		}},
		{name: "both targets", spec: map[string]any{
			"backupRef": map[string]any{"name": "once"},
			"routerRef": "edge",
			"connection": map[string]any{
				"address":           "192.0.2.1",
				"credentialsSecret": map[string]any{"name": "creds"},
			},
		}},
		{name: "connection missing secret", spec: map[string]any{
			"backupRef":  map[string]any{"name": "once"},
			"connection": map[string]any{"address": "192.0.2.1"},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validationErrors := validateCustomResourceSpec(
				t,
				openAPIValidator,
				structural,
				celValidator,
				"MikroTikRestore",
				test.spec,
			)
			if test.valid && len(validationErrors) > 0 {
				t.Fatalf("valid object rejected: %v", validationErrors.ToAggregate())
			}
			if !test.valid && len(validationErrors) == 0 {
				t.Fatal("invalid object accepted")
			}
		})
	}
}

func validateCustomResourceSpec(
	t *testing.T,
	openAPIValidator customresourcevalidation.SchemaValidator,
	structural *structuralschema.Structural,
	celValidator *cel.Validator,
	kind string,
	spec map[string]any,
) field.ErrorList {
	t.Helper()

	object := map[string]any{
		"apiVersion": "mikrotik.operator.io/v1alpha1",
		"kind":       kind,
		"metadata":   map[string]any{"name": "test-object", "namespace": "default"},
		"spec":       spec,
	}
	validationErrors := customresourcevalidation.ValidateCustomResource(
		field.NewPath("root"),
		object,
		openAPIValidator,
	)
	celErrors, _ := celValidator.Validate(
		t.Context(),
		field.NewPath("root"),
		structural,
		object,
		nil,
		celconfig.RuntimeCELCostBudget,
	)
	return append(validationErrors, celErrors...)
}

func portForwardSpec(targets map[string]any) map[string]any {
	spec := map[string]any{
		"routerRef":    "home-router",
		"protocol":     "tcp",
		"externalPort": int64(443),
		"targetPort":   int64(8443),
	}
	for key, value := range targets {
		spec[key] = value
	}
	return spec
}

func repositoryCRDSchemaValidators(
	t *testing.T,
	filename string,
) (customresourcevalidation.SchemaValidator, *structuralschema.Structural) {
	t.Helper()

	path := filepath.Join("..", "..", "config", "crd", "bases", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CRD %q: %v", filename, err)
	}
	var external apiextensionsv1.CustomResourceDefinition
	if err := yaml.UnmarshalStrict(data, &external); err != nil {
		t.Fatalf("decode CRD %q: %v", filename, err)
	}
	internal := &apiextensions.CustomResourceDefinition{}
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(
		&external,
		internal,
		nil,
	); err != nil {
		t.Fatalf("convert CRD %q: %v", filename, err)
	}
	if len(internal.Spec.Versions) != 1 {
		t.Fatalf("CRD %q has unexpected versions: %d", filename, len(internal.Spec.Versions))
	}
	validation := internal.Spec.Validation
	if validation == nil {
		validation = internal.Spec.Versions[0].Schema
	}
	if validation == nil || validation.OpenAPIV3Schema == nil {
		t.Fatalf("CRD %q does not have an OpenAPI schema", filename)
	}
	schema := validation.OpenAPIV3Schema
	openAPIValidator, _, err := customresourcevalidation.NewSchemaValidator(schema)
	if err != nil {
		t.Fatalf("build OpenAPI validator for CRD %q: %v", filename, err)
	}
	structural, err := structuralschema.NewStructural(schema)
	if err != nil {
		t.Fatalf("build structural schema for CRD %q: %v", filename, err)
	}
	return openAPIValidator, structural
}

func writeFixture(t *testing.T, name, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func validCRD(plural, kind string) string {
	return fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %[1]s.example.com
spec:
  group: example.com
  names:
    kind: %[2]s
    listKind: %[2]sList
    plural: %[1]s
    singular: %[1]s
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
`, plural, kind)
}
