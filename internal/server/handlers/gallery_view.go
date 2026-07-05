package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// GalleryByID handles GET /gallery/{id}, returning the gallery view HTML with ETag caching.
// ConditionalMiddleware generates 304 Not Modified on ETag match.
func (h *GalleryHandlers) GalleryByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	folderID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.deps.ServerError(w, r, fmt.Errorf("invalid folder id: %s", idStr))
		return
	}

	gd, err := h.fetchGalleryData(folderID)
	if h.handleDBError(w, r, err) {
		return
	}

	etagVersion := h.deps.GetETagVersion()
	// Get theme from cookie for ETag - theme changes must invalidate cache
	theme := "dark" // default
	if cookie, err := r.Cookie("theme"); err == nil && cookie.Value != "" {
		theme = cookie.Value
	}
	etag := fmt.Sprintf("\"%s-%d-%s\"", etagVersion, folderID, theme)
	h.setCacheHeaders(w, etag)
	w.Header().Set("HX-Push-URL", fmt.Sprintf("/gallery/%d?v=%s", folderID, etagVersion))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Vary on HTMX headers so the browser does not serve a cached partial for a full page request (e.g. breadcrumb).
	// Vary on Cookie because theme affects the rendered page (data-theme attribute on body).
	w.Header().Add("Vary", "HX-Request")
	w.Header().Add("Vary", "HX-Target")
	w.Header().Add("Vary", "Cookie")

	hxRequest := r.Header.Get("HX-Request") == "true"
	hxTarget := r.Header.Get("HX-Target")
	// Return partial only for folder tile (hx-get, target #gallery-content). Boosted links (breadcrumbs) target body and send HX-Boosted.
	isHTMX := hxRequest && hxTarget == "gallery-content"
	// Partial responses must not be cached or stored in bfcache (gallery is public, so no auth middleware).
	if isHTMX {
		w.Header().Set("Cache-Control", "no-store")
	}
	slog.Debug("galleryByIDHandler", "folderID", folderID, "isHTMX", isHTMX, "hxTarget", hxTarget, "url", r.URL.Path)

	data := map[string]any{
		"Breadcrumbs": gd.Breadcrumbs,
		"GalleryName": gd.GalleryName,
		"ImageCount":  gd.ImageCount,
		"IsImageView": gd.IsImageView,
		"Thumbs":      gd.Thumbs,
	}
	data = h.deps.AddCommonTemplateData(w, r, data, isHTMX)
	if err := ui.RenderPage(w, "gallery", data, isHTMX); err != nil {
		h.deps.ServerError(w, r, err)
		return
	}

	// Fire-and-forget: schedule background cache preload for folder contents.
	// Skip when this request is from our own internal preload to avoid cascading preloads.
	if h.PreloadService != nil && r.Header.Get(cachepreload.InternalPreloadHeader) != "true" {
		sessionID := getSessionIDForPreload(r)
		acceptEncoding := r.Header.Get("Accept-Encoding")
		go h.PreloadService.ScheduleFolderPreload(r.Context(), folderID, sessionID, acceptEncoding)
	}
}
