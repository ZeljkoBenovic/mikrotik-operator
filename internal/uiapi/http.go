package uiapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

const maxBodyBytes = 1 << 20

type errorBody struct {
	Error string `json:"error"`
}

type listResponse struct {
	Items []map[string]any `json:"items"`
}

type namesResponse struct {
	Items []nameItem `json:"items"`
}

type nameItem struct {
	Name string `json:"name"`
}

type overviewResponse struct {
	Kinds []kindCount `json:"kinds"`
}

type kindCount struct {
	Kind     string `json:"kind"`
	Count    int    `json:"count"`
	NotReady int    `json:"notReady"`
}

type ownedConflictBody struct {
	Error     string    `json:"error"`
	ManagedBy managedBy `json:"managedBy"`
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (h *handler) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")

		started := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		h.log.InfoContext(
			r.Context(),
			"request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Duration("duration", time.Since(started)),
		)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}

func writeOwnedConflict(w http.ResponseWriter, owner *managedBy) {
	writeJSON(w, http.StatusConflict, ownedConflictBody{
		Error:     ownedConflictMessage(owner),
		ManagedBy: *owner,
	})
}

func (h *handler) writeKubeError(w http.ResponseWriter, err error) {
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		code := int(statusErr.Status().Code)
		if code < 400 {
			code = http.StatusInternalServerError
		}
		writeError(w, code, statusErr.Status().Message)
		return
	}
	h.log.Error("kubernetes request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	limited := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func validKubeName(name string) bool {
	return len(validation.IsDNS1123Subdomain(name)) == 0
}
