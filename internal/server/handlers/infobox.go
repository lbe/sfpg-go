package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// InfoBoxFolder handles GET /info/folder/{id}, returning folder metadata HTML with Last-Modified caching.
// ConditionalMiddleware generates 304 Not Modified on Last-Modified match.
func (h *GalleryHandlers) InfoBoxFolder(w http.ResponseWriter, r *http.Request) {
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
	if h.handleDBError(w, r, err) {
		return
	}

	updatedAt, ok := folder.UpdatedAt.(int64)
	if !ok {
		slog.Warn("folder.UpdatedAt is not int64, using epoch", "folder_id", folderID, "type", fmt.Sprintf("%T", folder.UpdatedAt))
		updatedAt = 0
	}
	w.Header().Set("Last-Modified", time.Unix(updatedAt, 0).UTC().Format(http.TimeFormat))
	h.setCacheHeaders(w, "")
	// Do NOT set HX-Push-URL for info box: loading info into #box_info (e.g. from lightbox) must not change the URL,
	// so that back/j after closing lightbox goes to the previous folder (desired behavior at 0d6377c).

	counts, err := qh.GetFolderInfoCountsByID(h.Ctx, folderID)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	data := struct {
		Folder         gallerydb.Folder
		FormattedMtime string
		DirCount       int
		ImageCount     int
		FileCount      int
	}{
		Folder:         folder,
		FormattedMtime: time.Unix(updatedAt, 0).Format(time.ANSIC),
		DirCount:       int(counts.DirCount),
		ImageCount:     int(counts.ImageCount),
		FileCount:      int(counts.FileCount),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.RenderTemplate(w, "infobox-folder.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
	}
}

// InfoBoxImage handles GET /info/image/{id}, returning image metadata HTML with Last-Modified caching.
// ConditionalMiddleware generates 304 Not Modified on Last-Modified match.
func (h *GalleryHandlers) InfoBoxImage(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fileID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.ServerError(w, r, fmt.Errorf("invalid file id: %s", idStr))
		return
	}

	qh, cpcRo, put, err := h.getQueries()
	if err != nil {
		h.ServerError(w, r, err)
		return
	}
	defer put()

	file, err := qh.GetFileViewByID(h.Ctx, fileID)
	if h.handleDBError(w, r, err) {
		return
	}

	updatedAt, ok := file.UpdatedAt.(int64)
	if !ok {
		slog.Warn("file.UpdatedAt is not int64, using epoch", "file_id", fileID, "type", fmt.Sprintf("%T", file.UpdatedAt))
		updatedAt = 0
	}
	w.Header().Set("Last-Modified", time.Unix(updatedAt, 0).UTC().Format(http.TimeFormat))
	h.setCacheHeaders(w, "")
	// Do NOT set HX-Push-URL for info box: the lightbox loads /info/image/{id} into #box_info on open;
	// pushing that URL would change the address bar and make back/j return to lightbox after close (bug).

	imageIndex := -1
	imageCount := 0
	idx, err := qh.GetFileFolderIndexByID(h.Ctx, fileID)
	switch {
	case err == nil:
		imageIndex = int(idx.ImageIndex)
		imageCount = int(idx.ImageCount)
	case errors.Is(err, sql.ErrNoRows):
		// orphan file (folder_id IS NULL): preserve legacy -1 / 0 and render 200
	default:
		h.ServerError(w, r, err)
		return
	}

	mq := h.galleryOps.GetMetadataQueries(cpcRo)
	exif, err := mq.GetExifByFile(h.Ctx, fileID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.ServerError(w, r, err)
		return
	}
	if exif.Latitude.Valid && exif.Latitude.Float64 == 0.0 && exif.Longitude.Valid && exif.Longitude.Float64 == 0.0 {
		exif.Latitude.Valid = false
		exif.Longitude.Valid = false
	}

	iptc, err := mq.GetIPTCByFile(h.Ctx, fileID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.ServerError(w, r, err)
		return
	}

	data := struct {
		File       gallerydb.FileView
		Exif       gallerydb.ExifMetadatum
		Iptc       gallerydb.IptcMetadatum
		ImageIndex int
		ImageCount int
	}{
		File:       file,
		Exif:       exif,
		Iptc:       iptc,
		ImageIndex: imageIndex,
		ImageCount: imageCount,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.RenderTemplate(w, "infobox-image.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
	}
}
