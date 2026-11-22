package server

import (
	"slices"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

func TestCacheableRoutes_GalleryAndInfoRoutes(t *testing.T) {
	want := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
	if got := cacheableRoutes(); !slices.Equal(got, want) {
		t.Errorf("cacheableRoutes() = %v, want %v", got, want)
	}
}

func TestPreloadRoutes_DefaultWhenCacheOff(t *testing.T) {
	infra := NewInfrastructureService()
	want := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
	if got := infra.preloadRoutes(); !slices.Equal(got, want) {
		t.Errorf("preloadRoutes() with cache off = %v, want %v", got, want)
	}
}

func TestPreloadRoutes_CacheConfiguredRoutesWhenOn(t *testing.T) {
	infra := NewInfrastructureService()
	routes := []string{"/only/"}
	infra.cacheMW = cachelite.NewHTTPCacheMiddleware(nil,
		cachelite.CacheConfig{CacheableRoutes: routes},
		nil,
		func(*cachelite.HTTPCacheEntry) {})
	if got := infra.preloadRoutes(); !slices.Equal(got, routes) {
		t.Errorf("preloadRoutes() with cache on = %v, want %v", got, routes)
	}
}
