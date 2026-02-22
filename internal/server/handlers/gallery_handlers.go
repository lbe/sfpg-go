// Package handlers provides HTTP request handlers for the web application.
// Handlers are organized by domain (auth, config, gallery, health, etc.)
// and support both full-page and HTMX partial responses.
package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/server/cachepreload"
	"go.local/sfpg/internal/server/files"
	"go.local/sfpg/internal/server/interfaces"
	"go.local/sfpg/internal/server/ui"
)

// Breadcrumb represents a navigation breadcrumb.
// Breadcrumb represents a navigation breadcrumb for gallery hierarchy navigation.
type Breadcrumb struct {
	Name string // Display name for this level
	Path string // URL path to this level
}

// DirectoryInfo holds display info for a gallery item (folder or image).
type DirectoryInfo struct {
	ID        int64
	DispName  string
	Index     int
	IsImage   bool
	Path      string
	ThumbPath string
}

// GalleryData holds all data needed to render the gallery view.
type GalleryData struct {
	Breadcrumbs []Breadcrumb
	GalleryName string
	ImageCount  int
	IsImageView bool
	Thumbs      []DirectoryInfo
}

// ImageData holds data for the single-image view.
type ImageData struct {
	Breadcrumbs  []Breadcrumb
	ImageID      int64
	ImagePath    string
	IsImageView  bool
	CacheVersion int64
	ImageCount   int
}

// LightboxData holds data for the lightbox view.
type LightboxData struct {
	Breadcrumbs     []Breadcrumb
	CurrentImageID  int64
	CurrentIndex    int
	FirstIndex      int
	GalleryName     string
	HasNext         bool
	HasPrev         bool
	ImageCount      int
	LastIndex       int
	NextIndex       int
	PreloadPrevPath string
	PreloadNextPath string
	PrevIndex       int
}

// fixupDirectoryName cleans and formats a directory name for display.
func fixupDirectoryName(name string) string {
	const maxLen = 23
	name = filepath.Base(name)
	if len(name) <= maxLen {
		return "📁︎ " + name
	}
	if maxLen <= 3 {
		return "📁︎ " + name[:maxLen]
	}
	return "📁︎ " + name[:maxLen-3] + "..."
}

// fixupFileName cleans and formats a file name for display.
func fixupFileName(name string) string {
	const maxLen = 23
	name = filepath.Base(name)
	ext := filepath.Ext(name)
	base := name[:len(name)-len(ext)]
	if len(base) > maxLen {
		base = base[:maxLen-3] + "..."
	}
	return base + ext
}

// getSessionIDForPreload extracts a session identifier for cache preloading.
// Uses the session cookie if available, otherwise falls back to RemoteAddr.
func getSessionIDForPreload(r *http.Request) string {
	if c, err := r.Cookie("session-name"); err == nil && c.Value != "" {
		return c.Value
	}
	return r.RemoteAddr
}

// GalleryHandlers holds dependencies for gallery, image, thumbnail, and lightbox handlers.
// It has ~10 dependencies compared to ~35 in the main Handlers struct.
type GalleryHandlers struct {
	// Infrastructure
	DBRoPool     dbconnpool.ConnectionPool
	Ctx          context.Context
	ImagesDir    string
	GetImagesDir func() string

	// Query providers
	GetHandlerQueries  func(*dbconnpool.CpConn) interfaces.HandlerQueries
	GetMetadataQueries func(*dbconnpool.CpConn) MetadataQueries

	// ETag version for cache busting
	GetETagVersion func() string

	// Template and error helpers
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any
	ServerError           func(http.ResponseWriter, *http.Request, error)

	// Optional: cache preload service
	PreloadService cachepreload.PreloadService
}

// NewGalleryHandlers creates a new GalleryHandlers with the given dependencies.
func NewGalleryHandlers(
	dbRoPool dbconnpool.ConnectionPool,
	ctx context.Context,
	imagesDir string,
	getImagesDir func() string,
	getHandlerQueries func(*dbconnpool.CpConn) interfaces.HandlerQueries,
	getMetadataQueries func(*dbconnpool.CpConn) MetadataQueries,
	getETagVersion func() string,
	addCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any,
	serverError func(http.ResponseWriter, *http.Request, error),
) *GalleryHandlers {
	return &GalleryHandlers{
		DBRoPool:              dbRoPool,
		Ctx:                   ctx,
		ImagesDir:             imagesDir,
		GetImagesDir:          getImagesDir,
		GetHandlerQueries:     getHandlerQueries,
		GetMetadataQueries:    getMetadataQueries,
		GetETagVersion:        getETagVersion,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// getQueries returns a HandlerQueries and a cleanup function.
func (h *GalleryHandlers) getQueries() (interfaces.HandlerQueries, *dbconnpool.CpConn, func(), error) {
	cpc, err := h.DBRoPool.Get()
	if err != nil {
		return nil, nil, nil, err
	}
	qh := h.GetHandlerQueries(cpc)
	return qh, cpc, func() { h.DBRoPool.Put(cpc) }, nil
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

	subFolders, err := qh.GetFoldersViewsByParentIDOrderByName(h.Ctx, sql.NullInt64{Int64: folderID, Valid: true})
	if err != nil {
		return GalleryData{}, err
	}

	etagVersion := h.GetETagVersion()
	thumbs := make([]DirectoryInfo, 0, 64)
	for _, sf := range subFolders {
		thumbs = append(thumbs, DirectoryInfo{
			ID:        sf.ID,
			Path:      fmt.Sprintf("/gallery/%d", sf.ID),
			ThumbPath: fmt.Sprintf("/thumbnail/folder/%d?v=%s", sf.ID, etagVersion),
			DispName:  fixupDirectoryName(sf.Name),
			IsImage:   false,
		})
	}

	images, err := qh.GetFileViewsByFolderIDOrderByFileName(h.Ctx, sql.NullInt64{Int64: folderID, Valid: true})
	if err != nil {
		return GalleryData{}, err
	}

	for i, img := range images {
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
		ImageCount:  len(images),
		IsImageView: false,
		Thumbs:      thumbs,
	}, nil
}

// setCacheHeaders sets standard cache headers for gallery responses.
func (h *GalleryHandlers) setCacheHeaders(w http.ResponseWriter, etag string) {
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.Header().Set("Cache-Control", "public, max-age=2592000") // 30 days
	// Only set Last-Modified to now if it hasn't been set yet.
	if w.Header().Get("Last-Modified") == "" {
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	}
}

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
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	etagVersion := h.GetETagVersion()
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
	data = h.AddCommonTemplateData(w, r, data)
	if err := ui.RenderPage(w, "gallery", data, isHTMX); err != nil {
		h.ServerError(w, r, err)
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
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	etagVersion := h.GetETagVersion()
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
	data = h.AddCommonTemplateData(w, r, data)
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
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	imagesDir := h.ImagesDir
	if h.GetImagesDir != nil {
		imagesDir = h.GetImagesDir()
	}
	path := filepath.Join(imagesDir, file.Path)
	absPath, err := filepath.Abs(path)
	if err != nil {
		http.Error(w, "Invalid file path", http.StatusBadRequest)
		return
	}

	absImagesDir, err := filepath.Abs(imagesDir)
	if err != nil {
		slog.Error("failed to get absolute images directory", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if !strings.HasSuffix(absImagesDir, string(filepath.Separator)) {
		absImagesDir += string(filepath.Separator)
	}
	if !strings.HasPrefix(absPath+string(filepath.Separator), absImagesDir) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	http.ServeFile(w, r, absPath)
}

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
		if err == sql.ErrNoRows {
			h.NoThumbnail(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	thumb, err := qh.GetThumbnailBlobDataByID(h.Ctx, thumbnailMeta.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			h.NoThumbnail(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	etag := fmt.Sprintf("\"%s-%d\"", h.GetETagVersion(), fileID)
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
		if err == sql.ErrNoRows {
			h.NoThumbnail(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	if !folder.TileID.Valid {
		h.NoThumbnail(w, r)
		return
	}

	thumbnailMeta, err := qh.GetThumbnailsByFileID(h.Ctx, folder.TileID.Int64)
	if err != nil {
		if err == sql.ErrNoRows {
			h.NoThumbnail(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	thumb, err := qh.GetThumbnailBlobDataByID(h.Ctx, thumbnailMeta.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			h.NoThumbnail(w, r)
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
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	etagVersion := h.GetETagVersion()
	etag := fmt.Sprintf("\"%s-%d\"", etagVersion, fileID)
	h.setCacheHeaders(w, etag)
	// Do NOT set HX-Push-URL for lightbox: opening the lightbox should not create a history
	// entry, so that after closing, back/j goes to the previous folder (desired behavior at 0d6377c).

	images, err := qh.GetFileViewsByFolderIDOrderByFileName(h.Ctx, file.FolderID)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	imageCount := len(images)
	if imageCount == 0 {
		http.NotFound(w, r)
		return
	}

	currentIndex := -1
	for i, img := range images {
		if img.ID == fileID {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		h.ServerError(w, r, fmt.Errorf("could not find file in folder view"))
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
		GalleryName:    folder.Name,
		CurrentImageID: fileID,
		CurrentIndex:   currentIndex,
		ImageCount:     imageCount,
		FirstIndex:     int(images[0].ID),
		LastIndex:      int(images[imageCount-1].ID),
		Breadcrumbs:    breadcrumbs,
	}

	if imageCount > 1 {
		data.HasPrev = true
		if currentIndex > 0 {
			data.PrevIndex = int(images[currentIndex-1].ID)
		} else {
			data.PrevIndex = int(images[imageCount-1].ID)
		}
		data.PreloadPrevPath = fmt.Sprintf("/raw-image/%d", data.PrevIndex)
	}
	if imageCount > 1 {
		data.HasNext = true
		if currentIndex < imageCount-1 {
			data.NextIndex = int(images[currentIndex+1].ID)
		} else {
			data.NextIndex = int(images[0].ID)
		}
		data.PreloadNextPath = fmt.Sprintf("/raw-image/%d", data.NextIndex)
	}

	if err := ui.RenderTemplate(w, "lightbox-content.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
	}
}

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
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	updatedAt, ok := folder.UpdatedAt.(int64)
	if !ok {
		updatedAt = time.Now().Unix()
	}
	w.Header().Set("Last-Modified", time.Unix(updatedAt, 0).UTC().Format(http.TimeFormat))
	h.setCacheHeaders(w, "")
	// Do NOT set HX-Push-URL for info box: loading info into #box_info (e.g. from lightbox) must not change the URL,
	// so that back/j after closing lightbox goes to the previous folder (desired behavior at 0d6377c).

	subFolders, err := qh.GetFoldersViewsByParentIDOrderByName(h.Ctx, sql.NullInt64{Int64: folderID, Valid: true})
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	fileViews, err := qh.GetFileViewsByFolderIDOrderByFileName(h.Ctx, sql.NullInt64{Int64: folderID, Valid: true})
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	var imageCount, fileCount int
	for _, f := range fileViews {
		if files.IsImageFile(f.Path) {
			imageCount++
		} else {
			fileCount++
		}
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
		DirCount:       len(subFolders),
		ImageCount:     imageCount,
		FileCount:      fileCount,
	}

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

	qh, cpc, put, err := h.getQueries()
	if err != nil {
		h.ServerError(w, r, err)
		return
	}
	defer put()

	file, err := qh.GetFileViewByID(h.Ctx, fileID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		h.ServerError(w, r, err)
		return
	}

	updatedAt, ok := file.UpdatedAt.(int64)
	if !ok {
		updatedAt = time.Now().Unix()
	}
	w.Header().Set("Last-Modified", time.Unix(updatedAt, 0).UTC().Format(http.TimeFormat))
	h.setCacheHeaders(w, "")
	// Do NOT set HX-Push-URL for info box: the lightbox loads /info/image/{id} into #box_info on open;
	// pushing that URL would change the address bar and make back/j return to lightbox after close (bug).

	imagesInFolder, err := qh.GetFileViewsByFolderIDOrderByFileName(h.Ctx, file.FolderID)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	imageIndex := -1
	for i, img := range imagesInFolder {
		if img.ID == fileID {
			imageIndex = i + 1
			break
		}
	}

	mq := h.GetMetadataQueries(cpc)
	exif, err := mq.GetExifByFile(h.Ctx, fileID)
	if err != nil && err != sql.ErrNoRows {
		h.ServerError(w, r, err)
		return
	}
	if exif.Latitude.Valid && exif.Latitude.Float64 == 0.0 && exif.Longitude.Valid && exif.Longitude.Float64 == 0.0 {
		exif.Latitude.Valid = false
		exif.Longitude.Valid = false
	}

	iptc, err := mq.GetIPTCByFile(h.Ctx, fileID)
	if err != nil && err != sql.ErrNoRows {
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
		ImageCount: len(imagesInFolder),
	}

	if err := ui.RenderTemplate(w, "infobox-image.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
	}
}

// NoThumbnail serves a placeholder SVG when a thumbnail is not available.
func (h *GalleryHandlers) NoThumbnail(w http.ResponseWriter, r *http.Request) {
	_ = r
	thumb := []byte(`<svg viewBox="-12 -6 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
	//  <g id="SVGRepo_bgCarrier" stroke-width="0"></g>
	//  <g id="SVGRepo_tracerCarrier" stroke-linecap="round" stroke-linejoin="round"></g>
	//  <g id="SVGRepo_iconCarrier"> 
	//  	  <path d="M12.4615 4V9C12.4615 9.55228 12.9093 10 13.4615 10H18M12.4615 4L18 10M12.4615 4H8.5M18 10V15M15 20H7C6.44772 20 6 19.5523 6 19V10" stroke="#333333" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></path> 
	//  	  <line x1="3.4137" y1="3.03821" x2="20.0382" y2="20.5863" stroke="#333333" stroke-width="2" stroke-linecap="round"></line> 
	//  </g>
	//</svg>`)
	w.Header().Set("Content-Type", "image/svg+xml")
	if _, err := w.Write(thumb); err != nil {
		slog.Error("failed to write no-thumbnail response", "err", err)
	}
}
