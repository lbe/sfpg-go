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
		h.ServerError(w, r, fmt.Errorf("invalid folder id: %s", idStr))
		return
	}

	gd, err := h.fetchGalleryData(folderID)
	if h.handleDBError(w, r, err) {
		return
	}

	etagVersion := h.galleryOps.GetETagVersion()
	etag := fmt.Sprintf("\"%s-%d\"", etagVersion, folderID)
	h.setCacheHeaders(w, etag)
	w.Header().Set("HX-Push-URL", fmt.Sprintf("/gallery/%d?v=%s", folderID, etagVersion))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ui.SetLayoutPartialVary(w, true)

	partial := ui.IsPartialRequest(r, ui.PartialRuleHXRequestTarget, ui.HXTargetGalleryContent)
	hxTarget := r.Header.Get("HX-Target")
	if partial {
		ui.SetPartialNoStore(w)
	}
	slog.Debug("galleryByIDHandler", "folderID", folderID, "isHTMX", partial, "hxTarget", hxTarget, "url", r.URL.Path)

	data := map[string]any{
		"Breadcrumbs": gd.Breadcrumbs,
		"GalleryName": gd.GalleryName,
		"ImageCount":  gd.ImageCount,
		"IsImageView": gd.IsImageView,
		"Thumbs":      gd.Thumbs,
	}
	data = h.AddCommonTemplateData(w, r, data)
	if err := ui.RenderPage(w, "gallery", data, partial); err != nil {
		h.ServerError(w, r, err)
		return
	}

	// Fire-and-forget: schedule background cache preload for folder contents.
	// Skip when this request is from our own internal preload to avoid cascading preloads.
	if h.PreloadService != nil && r.Header.Get(cachepreload.InternalPreloadHeader) != "true" {
		sessionID := getSessionIDForPreload(r)
		go h.PreloadService.ScheduleFolderPreload(r.Context(), folderID, sessionID)
	}
}
