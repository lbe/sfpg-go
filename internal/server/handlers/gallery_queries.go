package handlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// getSessionIDForPreload extracts a session identifier for cache preloading.
// Uses the session cookie if available, otherwise falls back to RemoteAddr.
func getSessionIDForPreload(r *http.Request) string {
	if c, err := r.Cookie(session.SessionName); err == nil && c.Value != "" {
		return c.Value
	}
	return r.RemoteAddr
}

// generateBreadcrumbsByID returns breadcrumbs for a folder ID.
func (h *GalleryHandlers) generateBreadcrumbsByID(folderID int64) ([]Breadcrumb, error) {
	qh, _, put, err := h.getQueries()
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	defer put()

	var breadcrumbs []Breadcrumb
	currentID := sql.NullInt64{Int64: folderID, Valid: true}

	for currentID.Valid {
		folder, err := qh.GetFolderViewByID(h.Ctx, currentID.Int64)
		if err != nil {
			return nil, fmt.Errorf("failed to get folder view for ID %d: %w", currentID.Int64, err)
		}
		breadcrumbs = append([]Breadcrumb{{Name: folder.Name, Path: fmt.Sprintf("/gallery/%d", folder.ID)}}, breadcrumbs...)
		currentID = folder.ParentID
	}

	return append([]Breadcrumb{{Name: "Home", Path: "/"}}, breadcrumbs...), nil
}

// fetchGalleryData fetches all data needed to render a gallery view.
func (h *GalleryHandlers) fetchGalleryData(folderID int64) (GalleryData, error) {
	qh, _, put, err := h.getQueries()
	if err != nil {
		return GalleryData{}, err
	}
	defer put()

	folder, err := qh.GetFolderViewByID(h.Ctx, folderID)
	if err != nil {
		return GalleryData{}, err
	}

	subFolderRows, err := qh.GetGalleryFolderThumbRowsByParentID(h.Ctx, sql.NullInt64{Int64: folderID, Valid: true})
	if err != nil {
		return GalleryData{}, err
	}

	etagVersion := h.galleryOps.GetETagVersion()
	thumbs := make([]DirectoryInfo, 0, 64)
	for _, sf := range subFolderRows {
		thumbs = append(thumbs, DirectoryInfo{
			ID:        sf.ID,
			Path:      fmt.Sprintf("/gallery/%d", sf.ID),
			ThumbPath: fmt.Sprintf("/thumbnail/folder/%d?v=%s", sf.ID, etagVersion),
			DispName:  fixupDirectoryName(sf.Name),
			IsImage:   false,
		})
	}

	fileRows, err := qh.GetGalleryFileThumbRowsByFolderID(h.Ctx, sql.NullInt64{Int64: folderID, Valid: true})
	if err != nil {
		return GalleryData{}, err
	}

	for i, img := range fileRows {
		thumbs = append(thumbs, DirectoryInfo{
			ID:        img.ID,
			Path:      fmt.Sprintf("/image/%d", img.ID),
			ThumbPath: fmt.Sprintf("/thumbnail/file/%d?v=%s", img.ID, etagVersion),
			DispName:  fixupFileName(img.Filename),
			Index:     i,
			IsImage:   true,
		})
	}

	breadcrumbs, err := h.generateBreadcrumbsByID(folderID)
	if err != nil {
		return GalleryData{}, err
	}

	return GalleryData{
		Breadcrumbs: breadcrumbs,
		GalleryName: folder.Name,
		ImageCount:  len(fileRows),
		IsImageView: false,
		Thumbs:      thumbs,
	}, nil
}
