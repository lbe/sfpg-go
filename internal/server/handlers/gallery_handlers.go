// Package handlers provides HTTP request handlers for the web application.
// Handlers are organized by domain (auth, config, gallery, health, etc.)
// and support both full-page and HTMX partial responses.
package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
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

// GalleryHandlers holds dependencies for gallery, image, thumbnail, and lightbox handlers.
// Dependencies are provided via constructor injection (concrete services) and
// the galleryOps field (interfaces.GalleryOps), with template helpers passed as
// function fields — eliminating dependency on the monolithic ServerDeps.
type GalleryHandlers struct {
	DBRoPool              dbconnpool.ConnectionPool
	Ctx                   context.Context
	galleryOps            interfaces.GalleryOps
	AddCommonTemplateData func(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any
	ServerError           func(w http.ResponseWriter, r *http.Request, err error)
	PreloadService        cachepreload.PreloadService
}

// NewGalleryHandlers creates a new GalleryHandlers with the given dependencies.
func NewGalleryHandlers(
	dbRoPool dbconnpool.ConnectionPool,
	ctx context.Context,
	galleryOps interfaces.GalleryOps,
	addCommonTemplateData func(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any,
	serverError func(w http.ResponseWriter, r *http.Request, err error),
) *GalleryHandlers {
	return &GalleryHandlers{
		DBRoPool:              dbRoPool,
		Ctx:                   ctx,
		galleryOps:            galleryOps,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// getQueries returns a HandlerQueries and a cleanup function.
func (h *GalleryHandlers) getQueries() (interfaces.HandlerQueries, *dbconnpool.CpConn, func(), error) {
	cpcRo, err := h.DBRoPool.Get()
	if err != nil {
		return nil, nil, nil, err
	}
	qh := h.galleryOps.GetHandlerQueries(cpcRo)
	return qh, cpcRo, func() { h.DBRoPool.Put(cpcRo) }, nil
}

// handleDBError translates sql.ErrNoRows into 404 Not Found and other errors
// into the configured server-error response. It returns true when an error was
// handled and the caller should stop processing.
func (h *GalleryHandlers) handleDBError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return true
	}
	h.ServerError(w, r, err)
	return true
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
