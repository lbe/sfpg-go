package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/lbe/sfpg-go/internal/server/ui"
)

// LightboxByID handles GET /lightbox/{id}, returning the lightbox view HTML with ETag caching.
// ConditionalMiddleware generates 304 Not Modified on ETag match.
func (h *GalleryHandlers) LightboxByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fileID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.ServerError(w, r, fmt.Errorf("invalid file id: %s", idStr))
		return
	}

	qh, _, put, err := h.getQueries()
	if err != nil {
		h.ServerError(w, r, err)
		return
	}
	defer put()

	file, err := qh.GetFileViewByID(h.Ctx, fileID)
	if h.handleDBError(w, r, err) {
		return
	}

	etagVersion := h.galleryOps.GetETagVersion()
	etag := fmt.Sprintf("\"%s-%d\"", etagVersion, fileID)
	h.setCacheHeaders(w, etag)
	// Do NOT set HX-Push-URL for lightbox: opening the lightbox should not create a history
	// entry, so that after closing, back/j goes to the previous folder (desired behavior at 0d6377c).

	nav, err := qh.GetLightboxNavByFileID(h.Ctx, fileID)
	if h.handleDBError(w, r, err) {
		return
	}

	// Defensive only: a successful nav row always has ImageCount >= 1, since ErrNoRows
	// above already covers the empty/orphan case.
	if nav.ImageCount == 0 {
		http.NotFound(w, r)
		return
	}

	folder, err := qh.GetFolderViewByID(h.Ctx, file.FolderID.Int64)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	breadcrumbs, err := h.generateBreadcrumbsByID(file.FolderID.Int64)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	data := LightboxData{
		CurrentImageID: fileID,
		CurrentIndex:   int(nav.CurrentIndex),
		ImageCount:     int(nav.ImageCount),
		FirstIndex:     int(nav.FirstID), // field name is Index but value is file ID (legacy)
		LastIndex:      int(nav.LastID),
		GalleryName:    folder.Name,
		Breadcrumbs:    breadcrumbs,
	}

	if nav.ImageCount > 1 {
		data.HasPrev, data.HasNext = true, true
		if nav.PrevID.Valid {
			data.PrevIndex = int(nav.PrevID.Int64)
		} else {
			data.PrevIndex = int(nav.LastID)
		}
		if nav.NextID.Valid {
			data.NextIndex = int(nav.NextID.Int64)
		} else {
			data.NextIndex = int(nav.FirstID)
		}
		data.PreloadPrevPath = fmt.Sprintf("/raw-image/%d", data.PrevIndex)
		data.PreloadNextPath = fmt.Sprintf("/raw-image/%d", data.NextIndex)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.RenderTemplate(w, "lightbox-content.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
	}
}
