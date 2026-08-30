package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: crdvalidate <file-or-directory> [...]")
		os.Exit(2)
	}

	count, err := validatePaths(context.Background(), os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "CRD validation failed:\n%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("validated %d CustomResourceDefinition document(s)\n", count)
}

func validatePaths(ctx context.Context, paths []string) (int, error) {
	files, err := collectYAMLFiles(paths)
	if err != nil {
		return 0, err
	}

	count := 0
	problems := []error{}
	for _, path := range files {
		fileCount, fileProblems := validateFile(ctx, path)
		count += fileCount
		problems = append(problems, fileProblems...)
	}
	if count == 0 {
		problems = append(problems, errors.New("no CustomResourceDefinition documents found"))
	}

	return count, errors.Join(problems...)
}

func collectYAMLFiles(paths []string) ([]string, error) {
	files := []string{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect %q: %w", path, err)
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}

		err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !isYAMLFile(candidate) {
				return nil
			}
			files = append(files, candidate)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %q: %w", path, err)
		}
	}

	sort.Strings(files)
	return files, nil
}

func isYAMLFile(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".yaml" || extension == ".yml"
}

func validateFile(ctx context.Context, path string) (int, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, []error{fmt.Errorf("read %q: %w", path, err)}
	}

	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	count := 0
	problems := []error{}
	for document := 1; ; document++ {
		raw, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			problems = append(problems, fmt.Errorf("%s document %d: parse YAML: %w", path, document, readErr))
			break
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}

		var typeMeta metav1.TypeMeta
		if err := yaml.Unmarshal(raw, &typeMeta); err != nil {
			problems = append(problems, fmt.Errorf("%s document %d: parse type metadata: %w", path, document, err))
			continue
		}
		if typeMeta.APIVersion != apiextensionsv1.SchemeGroupVersion.String() || typeMeta.Kind != "CustomResourceDefinition" {
			problems = append(
				problems,
				fmt.Errorf(
					"%s document %d: expected apiextensions.k8s.io/v1 CustomResourceDefinition, got %s %s",
					path,
					document,
					typeMeta.APIVersion,
					typeMeta.Kind,
				),
			)
			continue
		}

		count++
		var external apiextensionsv1.CustomResourceDefinition
		if err := yaml.UnmarshalStrict(raw, &external); err != nil {
			problems = append(problems, fmt.Errorf("%s document %d: decode CRD strictly: %w", path, document, err))
			continue
		}
		problems = append(problems, validateCRD(ctx, path, document, &external)...)
	}

	return count, problems
}

func validateCRD(
	ctx context.Context,
	path string,
	document int,
	external *apiextensionsv1.CustomResourceDefinition,
) []error {
	internal := &apiextensions.CustomResourceDefinition{}
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(
		external,
		internal,
		nil,
	); err != nil {
		return []error{crdError(path, document, external.Name, "convert to Kubernetes internal CRD", err)}
	}
	prepareStoredVersions(internal)

	problems := []error{}
	if validationErrors := crdvalidation.ValidateCustomResourceDefinition(ctx, internal); len(validationErrors) > 0 {
		problems = append(
			problems,
			crdError(path, document, external.Name, "Kubernetes CRD validation", validationErrors.ToAggregate()),
		)
	}
	problems = append(problems, validateStructuralSchemas(path, document, external.Name, internal)...)
	return problems
}

func prepareStoredVersions(crd *apiextensions.CustomResourceDefinition) {
	for _, version := range crd.Spec.Versions {
		if version.Storage {
			crd.Status.StoredVersions = []string{version.Name}
			return
		}
	}
}

func validateStructuralSchemas(
	path string,
	document int,
	name string,
	crd *apiextensions.CustomResourceDefinition,
) []error {
	problems := []error{}
	for index := range crd.Spec.Versions {
		version := &crd.Spec.Versions[index]
		if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			continue
		}

		structural, err := structuralschema.NewStructural(version.Schema.OpenAPIV3Schema)
		if err != nil {
			problems = append(
				problems,
				crdError(path, document, name, "build structural schema for version "+version.Name, err),
			)
			continue
		}
		schemaPath := field.NewPath("spec", "versions").Index(index).Child("schema", "openAPIV3Schema")
		if validationErrors := structuralschema.ValidateStructural(schemaPath, structural); len(validationErrors) > 0 {
			problems = append(
				problems,
				crdError(
					path,
					document,
					name,
					"structural schema validation for version "+version.Name,
					validationErrors.ToAggregate(),
				),
			)
		}
	}
	return problems
}

func crdError(path string, document int, name, operation string, err error) error {
	return fmt.Errorf("%s document %d CRD %q: %s: %w", path, document, name, operation, err)
}
