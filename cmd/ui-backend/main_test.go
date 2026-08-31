package main

import (
	"os"
	"path/filepath"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		bindAddress string
		kubeconfig  string
		staticDir   string
		namespace   string
		wantErr     bool
	}{
		{
			name:        "defaults",
			bindAddress: ":8080",
			staticDir:   defaultStaticDir(),
		},
		{
			name:        "overrides",
			args:        []string{"-bind-address", "127.0.0.1:9090", "-kubeconfig", "/tmp/kubeconfig", "-static-dir", "/ui", "-namespace", "mikrotik-operator-system"},
			bindAddress: "127.0.0.1:9090",
			kubeconfig:  "/tmp/kubeconfig",
			staticDir:   "/ui",
			namespace:   "mikrotik-operator-system",
		},
		{
			name:    "unknown flag",
			args:    []string{"-nope"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseFlags(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.bindAddress != tt.bindAddress {
				t.Fatalf("bindAddress: got %q want %q", cfg.bindAddress, tt.bindAddress)
			}
			if cfg.kubeconfig != tt.kubeconfig {
				t.Fatalf("kubeconfig: got %q want %q", cfg.kubeconfig, tt.kubeconfig)
			}
			if cfg.staticDir != tt.staticDir {
				t.Fatalf("staticDir: got %q want %q", cfg.staticDir, tt.staticDir)
			}
			if cfg.namespace != tt.namespace {
				t.Fatalf("namespace: got %q want %q", cfg.namespace, tt.namespace)
			}
		})
	}
}

func TestDefaultStaticDirPrefersLocalThenContainer(t *testing.T) {
	got := defaultStaticDir()
	if got != localStaticDir && got != containerStaticDir {
		t.Fatalf("unexpected default static dir %q", got)
	}
}

func TestLoadRESTConfigMissingFile(t *testing.T) {
	t.Parallel()
	_, err := loadRESTConfig(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing kubeconfig")
	}
}

func TestLoadRESTConfigEmptyUsesInCluster(t *testing.T) {
	t.Parallel()
	_, err := loadRESTConfig("")
	if err == nil {
		t.Fatal("expected in-cluster config to fail outside a cluster")
	}
}

func TestNewSchemeIncludesOperatorTypes(t *testing.T) {
	t.Parallel()
	scheme := newScheme()
	if !scheme.Recognizes(api.GroupVersion.WithKind("MikroTikRouter")) {
		t.Fatal("scheme missing MikroTikRouter")
	}
}

func TestOperatorNamespace(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "from-env")
	if got := operatorNamespace("from-flag"); got != "from-flag" {
		t.Fatalf("flag: got %q", got)
	}
	if got := operatorNamespace(""); got != "from-env" {
		t.Fatalf("env: got %q", got)
	}
	t.Setenv("POD_NAMESPACE", "")
	if got := operatorNamespace(""); got != "default" {
		t.Fatalf("fallback: got %q", got)
	}
}

func TestParseFlagsDoesNotUseProcessFlagSet(t *testing.T) {
	t.Parallel()
	if len(os.Args) == 0 {
		t.Fatal("missing os.Args")
	}
	if _, err := parseFlags(nil); err != nil {
		t.Fatal(err)
	}
}
