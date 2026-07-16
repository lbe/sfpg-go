package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lbe/sfpg-go/internal/server/pathutil"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// ImageByID returns image view HTML with ETag caching. ConditionalMiddleware generates 304 Not Modified on ETag match.
func (h *GalleryHandlers) ImageByID(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("HX-Push-URL", fmt.Sprintf("/image/%d?v=%s", fileID, etagVersion))

	breadcrumbs, err := h.generateBreadcrumbsByID(file.FolderID.Int64)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	data := map[string]any{
		"ImageID":      fileID,
		"ImagePath":    file.Path,
		"Breadcrumbs":  breadcrumbs,
		"IsImageView":  true,
		"CacheVersion": time.Now().Unix(),
		"ImageCount":   1,
	}
	data = h.AddCommonTemplateData(w, r, data, false)
	if err := ui.RenderPage(w, "image", data, false); err != nil {
		h.ServerError(w, r, err)
	}
}

// RawImageByID streams large image files. Uses http.ServeFile which natively supports If-Modified-Since. ConditionalMiddleware not applied to avoid memory overhead.
func (h *GalleryHandlers) RawImageByID(w http.ResponseWriter, r *http.Request) {
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

	imagesDir := h.galleryOps.ImagesDir()

	absPath, err := pathutil.SafeImagePath(imagesDir, file.Path)
	if err != nil {
		switch {
		case errors.Is(err, pathutil.ErrPathTraversal):
			http.Error(w, "Forbidden", http.StatusForbidden)
		case errors.Is(err, pathutil.ErrInvalidImagesDir):
			slog.Error("failed to get absolute images directory", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		default:
			http.Error(w, "Invalid file path", http.StatusBadRequest)
		}
		return
	}

	http.ServeFile(w, r, absPath)
}
