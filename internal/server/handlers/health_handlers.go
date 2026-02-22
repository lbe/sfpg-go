package handlers

import "net/http"

// HealthHandlers holds dependencies for health check handlers.
// It has minimal dependencies (~1) compared to the main Handlers struct.
type HealthHandlers struct {
	// GetETagVersion returns the current ETag version for cache-busting URLs.
	// Used by RootRedirect to generate the redirect URL with version parameter.
	GetETagVersion func() string
}

// NewHealthHandlers creates a new HealthHandlers with the given dependencies.
func NewHealthHandlers(getETagVersion func() string) *HealthHandlers {
	return &HealthHandlers{
		GetETagVersion: getETagVersion,
	}
}

// Health handles GET /health, returning a simple 200 OK response for health checks.
// Used by monitoring systems and the restart overlay to detect when the server is up.
func (h *HealthHandlers) Health(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// RootRedirect handles GET /, redirecting the root path to the first gallery.
func (h *HealthHandlers) RootRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "400 Bad Request", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/gallery/1?v="+h.GetETagVersion(), http.StatusFound)
}
