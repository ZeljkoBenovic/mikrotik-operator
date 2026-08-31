// Package uiapi implements a thin REST API over client-go for the admin UI.
package uiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Options configures the UI HTTP API.
type Options struct {
	Client    client.Client
	Logger    *slog.Logger
	StaticDir string
	Namespace string
}

type handler struct {
	kube      client.Client
	log       *slog.Logger
	staticDir string
	namespace string
}

// New returns the UI HTTP handler (API + optional SPA static files).
func New(opts Options) http.Handler {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	namespace := opts.Namespace
	if namespace == "" {
		namespace = "default"
	}
	h := &handler{
		kube:      opts.Client,
		log:       log,
		staticDir: opts.StaticDir,
		namespace: namespace,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /api/overview", h.overview)
	mux.HandleFunc("GET /api/config", h.config)
	mux.HandleFunc("GET /api/namespaces", h.listNamespaces)
	mux.HandleFunc("GET /api/secrets/{namespace}", h.listSecrets)
	mux.HandleFunc("GET /api/resources/{kind}", h.listResources)
	mux.HandleFunc("GET /api/resources/{kind}/{namespace}/{name}", h.getResource)
	mux.HandleFunc("POST /api/resources/{kind}/{namespace}", h.createResource)
	mux.HandleFunc("PUT /api/resources/{kind}/{namespace}/{name}", h.updateResource)
	mux.HandleFunc("DELETE /api/resources/{kind}/{namespace}/{name}", h.deleteResource)
	mux.HandleFunc("/api/", h.apiNotFound)
	if h.staticDir != "" {
		mux.HandleFunc("/", h.serveSPA)
	}
	return h.withMiddleware(mux)
}

func (h *handler) apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func (h *handler) healthz(w http.ResponseWriter, r *http.Request) {
	h.writePlain(w, r, "ok")
}

func (h *handler) readyz(w http.ResponseWriter, r *http.Request) {
	if h.kube == nil {
		writeError(w, http.StatusServiceUnavailable, "kubernetes client is not ready")
		return
	}
	var list corev1.NamespaceList
	if err := h.kube.List(r.Context(), &list, client.Limit(1)); err != nil {
		h.log.ErrorContext(r.Context(), "readyz kubernetes check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "kubernetes is not ready")
		return
	}
	h.writePlain(w, r, "ok")
}

func (h *handler) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, configResponse{Namespace: h.namespace})
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	counts := make([]kindCount, 0, len(kindOrder))
	for _, plural := range kindOrder {
		spec := kinds[plural]
		list := spec.newList()
		if err := h.kube.List(r.Context(), list); err != nil {
			h.writeKubeError(w, err)
			return
		}
		objects, err := objectsFromList(list)
		if err != nil {
			h.log.ErrorContext(r.Context(), "extract overview list", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		var notReady int
		for _, obj := range objects {
			if !isReady(spec.conditionsOf(obj)) {
				notReady++
			}
		}
		counts = append(counts, kindCount{
			Kind:     plural,
			Count:    len(objects),
			NotReady: notReady,
		})
	}
	writeJSON(w, http.StatusOK, overviewResponse{Kinds: counts})
}

func (h *handler) listNamespaces(w http.ResponseWriter, r *http.Request) {
	var list corev1.NamespaceList
	if err := h.kube.List(r.Context(), &list); err != nil {
		h.writeKubeError(w, err)
		return
	}
	items := make([]nameItem, 0, len(list.Items))
	for _, ns := range list.Items {
		items = append(items, nameItem{Name: ns.Name})
	}
	writeJSON(w, http.StatusOK, namesResponse{Items: items})
}

func (h *handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if !validKubeName(namespace) {
		writeError(w, http.StatusBadRequest, "invalid namespace")
		return
	}
	var list corev1.SecretList
	if err := h.kube.List(r.Context(), &list, client.InNamespace(namespace)); err != nil {
		h.writeKubeError(w, err)
		return
	}
	items := make([]nameItem, 0, len(list.Items))
	for _, secret := range list.Items {
		items = append(items, nameItem{Name: secret.Name})
	}
	writeJSON(w, http.StatusOK, namesResponse{Items: items})
}

func (h *handler) listResources(w http.ResponseWriter, r *http.Request) {
	spec, ok := lookupKind(r.PathValue("kind"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown resource kind")
		return
	}
	listOpts := []client.ListOption{}
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		if !validKubeName(ns) {
			writeError(w, http.StatusBadRequest, "invalid namespace")
			return
		}
		listOpts = append(listOpts, client.InNamespace(ns))
	}
	list := spec.newList()
	if err := h.kube.List(r.Context(), list, listOpts...); err != nil {
		h.writeKubeError(w, err)
		return
	}
	objects, err := objectsFromList(list)
	if err != nil {
		h.log.ErrorContext(r.Context(), "extract resource list", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items := make([]map[string]any, 0, len(objects))
	for _, obj := range objects {
		annotated, err := annotateResource(obj, spec.gvk)
		if err != nil {
			h.log.ErrorContext(r.Context(), "annotate resource", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, annotated)
	}
	writeJSON(w, http.StatusOK, listResponse{Items: items})
}

func (h *handler) getResource(w http.ResponseWriter, r *http.Request) {
	spec, obj, ok := h.lookupObject(w, r)
	if !ok {
		return
	}
	if err := h.kube.Get(r.Context(), client.ObjectKey{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}, obj); err != nil {
		h.writeKubeError(w, err)
		return
	}
	h.writeResource(w, obj, spec.gvk)
}

func (h *handler) createResource(w http.ResponseWriter, r *http.Request) {
	spec, ok := lookupKind(r.PathValue("kind"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown resource kind")
		return
	}
	namespace := r.PathValue("namespace")
	if !validKubeName(namespace) {
		writeError(w, http.StatusBadRequest, "invalid namespace")
		return
	}
	obj, ok := h.decodeObject(w, r, spec)
	if !ok {
		return
	}
	obj.SetNamespace(namespace)
	if err := h.kube.Create(r.Context(), obj); err != nil {
		h.writeKubeError(w, err)
		return
	}
	h.writeResourceStatus(w, http.StatusCreated, obj, spec.gvk)
}

func (h *handler) updateResource(w http.ResponseWriter, r *http.Request) {
	spec, existing, ok := h.lookupObject(w, r)
	if !ok {
		return
	}
	if err := h.kube.Get(r.Context(), client.ObjectKey{
		Namespace: existing.GetNamespace(),
		Name:      existing.GetName(),
	}, existing); err != nil {
		h.writeKubeError(w, err)
		return
	}
	if owner := controllerOwner(existing); owner != nil {
		writeOwnedConflict(w, owner)
		return
	}
	obj, ok := h.decodeObject(w, r, spec)
	if !ok {
		return
	}
	obj.SetNamespace(existing.GetNamespace())
	obj.SetName(existing.GetName())
	if obj.GetResourceVersion() == "" {
		obj.SetResourceVersion(existing.GetResourceVersion())
	}
	if err := h.kube.Update(r.Context(), obj); err != nil {
		h.writeKubeError(w, err)
		return
	}
	h.writeResource(w, obj, spec.gvk)
}

func (h *handler) deleteResource(w http.ResponseWriter, r *http.Request) {
	_, existing, ok := h.lookupObject(w, r)
	if !ok {
		return
	}
	if err := h.kube.Get(r.Context(), client.ObjectKey{
		Namespace: existing.GetNamespace(),
		Name:      existing.GetName(),
	}, existing); err != nil {
		h.writeKubeError(w, err)
		return
	}
	if owner := controllerOwner(existing); owner != nil {
		writeOwnedConflict(w, owner)
		return
	}
	if err := h.kube.Delete(r.Context(), existing); err != nil {
		h.writeKubeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) lookupObject(w http.ResponseWriter, r *http.Request) (kindSpec, client.Object, bool) {
	spec, ok := lookupKind(r.PathValue("kind"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown resource kind")
		return kindSpec{}, nil, false
	}
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")
	if !validKubeName(namespace) {
		writeError(w, http.StatusBadRequest, "invalid namespace")
		return kindSpec{}, nil, false
	}
	if !validKubeName(name) {
		writeError(w, http.StatusBadRequest, "invalid name")
		return kindSpec{}, nil, false
	}
	obj := spec.newObject()
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return spec, obj, true
}

func (h *handler) decodeObject(w http.ResponseWriter, r *http.Request, spec kindSpec) (client.Object, bool) {
	data, err := readBody(r)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "unable to read request body")
		return nil, false
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return nil, false
	}
	obj := spec.newObject()
	if err := yaml.Unmarshal(data, obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON or YAML")
		return nil, false
	}
	if obj.GetName() == "" {
		writeError(w, http.StatusBadRequest, "metadata.name is required")
		return nil, false
	}
	return obj, true
}

func (h *handler) writeResource(w http.ResponseWriter, obj client.Object, gvk schema.GroupVersionKind) {
	h.writeResourceStatus(w, http.StatusOK, obj, gvk)
}

func (h *handler) writeResourceStatus(w http.ResponseWriter, status int, obj client.Object, gvk schema.GroupVersionKind) {
	annotated, err := annotateResource(obj, gvk)
	if err != nil {
		h.log.Error("annotate resource", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, status, annotated)
}

func (h *handler) serveSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.staticDir == "" {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if rel == "." || rel == "" {
		rel = "index.html"
	}
	full := filepath.Join(h.staticDir, filepath.FromSlash(rel))
	if !pathInside(h.staticDir, full) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.ServeFile(w, r, filepath.Join(h.staticDir, "index.html"))
		return
	}
	http.ServeFile(w, r, full)
}

func annotateResource(obj client.Object, gvk schema.GroupVersionKind) (map[string]any, error) {
	obj.GetObjectKind().SetGroupVersionKind(gvk)
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal resource: %w", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode resource: %w", err)
	}
	if owner := controllerOwner(obj); owner != nil {
		out["managedBy"] = owner
	}
	return out, nil
}

func objectsFromList(list client.ObjectList) ([]client.Object, error) {
	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	out := make([]client.Object, 0, len(items))
	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok {
			return nil, fmt.Errorf("list item is not a kubernetes object")
		}
		out = append(out, obj)
	}
	return out, nil
}

func pathInside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
