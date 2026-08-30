package uiapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestValidKubeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "simple", in: "app", want: true},
		{name: "dns subdomain", in: "web.frontend.svc", want: true},
		{name: "kube-system", in: "kube-system", want: true},
		{name: "empty", in: ""},
		{name: "uppercase", in: "App"},
		{name: "underscore", in: "web_app"},
		{name: "space", in: "web app"},
		{name: "slash", in: "app/web"},
		{name: "dotdot", in: ".."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := validKubeName(tt.in); got != tt.want {
				t.Fatalf("validKubeName(%q)=%v want %v (dns=%v)", tt.in, got, tt.want, validation.IsDNS1123Subdomain(tt.in))
			}
		})
	}
}

func FuzzValidKubeName(f *testing.F) {
	f.Add("app")
	f.Add("kube-system")
	f.Add("web.frontend")
	f.Add("Not_Valid")
	f.Add("")
	f.Add("a")
	f.Fuzz(func(t *testing.T, name string) {
		got := validKubeName(name)
		want := len(validation.IsDNS1123Subdomain(name)) == 0
		if got != want {
			t.Fatalf("validKubeName(%q)=%v want %v", name, got, want)
		}
	})
}

func TestPathInside(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "ui")
	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "index", target: filepath.Join(root, "index.html"), want: true},
		{name: "nested", target: filepath.Join(root, "assets", "app.js"), want: true},
		{name: "root itself", target: root, want: true},
		{name: "parent", target: filepath.Join(root, "..", "etc", "passwd")},
		{name: "absolute escape", target: filepath.Join(string(filepath.Separator), "etc", "passwd")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pathInside(root, tt.target); got != tt.want {
				t.Fatalf("pathInside(%q, %q)=%v want %v", root, tt.target, got, tt.want)
			}
		})
	}
}

func TestWriteErrorJSON(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "unknown resource kind")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	body := decodeMap(t, rec)
	if body["error"] != "unknown resource kind" {
		t.Fatalf("body %#v", body)
	}
}

func TestWriteKubeErrorNonStatus(t *testing.T) {
	t.Parallel()
	h := &handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	h.writeKubeError(rec, errors.New("dial tcp: timeout"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if decodeMap(t, rec)["error"] != "internal error" {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestWriteKubeErrorLowStatusCode(t *testing.T) {
	t.Parallel()
	h := &handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	statusErr := apierrors.NewAlreadyExists(schema.GroupResource{Group: "mikrotik.operator.io", Resource: "mikrotikrouters"}, "edge")
	h.writeKubeError(rec, statusErr)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestWriteOwnedConflictBody(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeOwnedConflict(rec, &managedBy{APIVersion: "v1", Kind: "Service", Namespace: "app", Name: "web"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	body := decodeMap(t, rec)
	if !strings.Contains(stringField(t, body, "error"), "Service/web") {
		t.Fatalf("error %v", body["error"])
	}
	owner := asMap(t, body["managedBy"])
	if owner["kind"] != "Service" || owner["name"] != "web" || owner["namespace"] != "app" {
		t.Fatalf("managedBy %#v", owner)
	}
}

func TestReadBodyEmptyAndTooLarge(t *testing.T) {
	t.Parallel()

	empty := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	data, err := readBody(empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("got %d bytes", len(data))
	}

	large := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", maxBodyBytes+1)))
	_, err = readBody(large)
	if err == nil {
		t.Fatal("expected max-bytes error")
	}
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("got %T %v, want MaxBytesError", err, err)
	}
}
