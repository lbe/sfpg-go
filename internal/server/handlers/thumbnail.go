package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

// ThumbnailByID returns cached thumbnail with ETag support. ConditionalMiddleware generates 304 on ETag match.
func (h *GalleryHandlers) ThumbnailByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fileID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.ServerError(w, r, fmt.Errorf("invalid file id for thumbnail: %s", idStr))
		return
	}

	qh, _, put, err := h.getQueries()
	if err != nil {
		h.ServerError(w, r, err)
		return
	}
	defer put()

	thumbnailMeta, err := qh.GetThumbnailsByFileID(h.Ctx, fileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.NoThumbnail(w)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	thumb, err := qh.GetThumbnailBlobDataByID(h.Ctx, thumbnailMeta.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.NoThumbnail(w)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	etag := fmt.Sprintf("\"%s-%d\"", h.galleryOps.GetETagVersion(), fileID)
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "image/jpeg")
	if _, err := w.Write(thumb); err != nil {
		slog.Error("failed to write thumbnail response", "err", err)
	}
}

// FolderThumbnailByID streams folder thumbnails from disk. ConditionalMiddleware not applied to avoid buffering large binary data.
func (h *GalleryHandlers) FolderThumbnailByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	folderID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.ServerError(w, r, fmt.Errorf("invalid folder id: %s", idStr))
		return
	}

	qh, _, put, err := h.getQueries()
	if err != nil {
		h.ServerError(w, r, err)
		return
	}
	defer put()

	folder, err := qh.GetFolderByID(h.Ctx, folderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.NoThumbnail(w)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	if !folder.TileID.Valid {
		h.NoThumbnail(w)
		return
	}

	thumbnailMeta, err := qh.GetThumbnailsByFileID(h.Ctx, folder.TileID.Int64)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.NoThumbnail(w)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	thumb, err := qh.GetThumbnailBlobDataByID(h.Ctx, thumbnailMeta.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.NoThumbnail(w)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	if _, err := w.Write(thumb); err != nil {
		slog.Error("failed to write folder thumbnail response", "err", err)
	}
}

// NoThumbnail serves a placeholder SVG when a thumbnail is not available.
func (h *GalleryHandlers) NoThumbnail(w http.ResponseWriter) {
	thumb := []byte(`<svg viewBox="-12 -6 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
	<path d="M12.4615 4V9C12.4615 9.55228 12.9093 10 13.4615 10H18M12.4615 4L18 10M12.4615 4H8.5M18 10V15M15 20H7C6.44772 20 6 19.5523 6 19V10" stroke="#333333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></path>
	<line x1="3.4137" y1="3.03821" x2="20.0382" y2="20.5863" stroke="#333333" stroke-width="2" stroke-linecap="round"></line>
</svg>`)
	w.Header().Set("Content-Type", "image/svg+xml")
	if _, err := w.Write(thumb); err != nil {
		slog.Error("failed to write no-thumbnail response", "err", err)
	}
}
