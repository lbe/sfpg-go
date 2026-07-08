package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/humanize"
)

// Minimal Thumb struct for testing purposes

// TestAuthMiddleware tests the authMiddleware to ensure it correctly protects
// routes, redirecting unauthenticated requests and allowing authenticated ones.
func TestAddCommonTemplateData_Additional(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	data := make(map[string]any)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, data, false)

	// addCommonTemplateData adds IsAuthenticated and CSRFToken
	if _, ok := result["IsAuthenticated"]; !ok {
		t.Error("Expected IsAuthenticated in template data")
	}
	if _, ok := result["CSRFToken"]; !ok {
		t.Error("Expected CSRFToken in template data")
	}
}

// TestAddCommonTemplateData_PartialSkipsGalleryStats tests that when partial=true,
// addCommonTemplateData does NOT fetch GalleryStats (expensive DB query).
// Partials (HTMX swaps, modals, toasts) don't include the about modal.
func TestAddCommonTemplateData_PartialSkipsGalleryStats(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	data := make(map[string]any)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, data, true)

	// Still adds cheap common data
	if _, ok := result["IsAuthenticated"]; !ok {
		t.Error("Expected IsAuthenticated in template data")
	}
	if _, ok := result["Theme"]; !ok {
		t.Error("Expected Theme in template data")
	}
	// GalleryStats must NOT be present when partial=true (avoids expensive getGalleryStatistics)
	if _, ok := result["GalleryStats"]; ok {
		t.Error("Expected GalleryStats to be absent when partial=true (should skip expensive DB query)")
	}
}

// TestRefreshGalleryStatsCache_AndGetCached tests that refreshGalleryStatsCache populates
// the cache and getGalleryStatsCached returns it when LastStartedAt matches.
func TestRefreshGalleryStatsCache_AndGetCached(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	ctx := context.Background()
	lastStarted := int64(12345)

	stats, err := app.refreshGalleryStatsCache(ctx, lastStarted)
	if err != nil {
		t.Fatalf("refreshGalleryStatsCache: %v", err)
	}
	if stats.Folders == "" && stats.Images == "" {
		t.Error("expected non-empty stats from refreshGalleryStatsCache")
	}

	cached := app.getGalleryStatsCached(lastStarted)
	if cached == nil {
		t.Fatal("expected cached stats when LastStartedAt matches")
	}
	if cached.Folders != stats.Folders || cached.Images != stats.Images {
		t.Error("cached stats should match refresh result")
	}
}

// TestGetGalleryStatsCached_ReturnsNilWhenStale tests that getGalleryStatsCached
// returns nil when LastStartedAt differs from cached.
func TestGetGalleryStatsCached_ReturnsNilWhenStale(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	ctx := context.Background()
	_, err := app.refreshGalleryStatsCache(ctx, 11111)
	if err != nil {
		t.Fatalf("refreshGalleryStatsCache: %v", err)
	}

	if got := app.getGalleryStatsCached(22222); got != nil {
		t.Error("expected nil when LastStartedAt differs")
	}
}

// TestAddCommonTemplateData_FullPageIncludesGalleryStats tests that when partial=false,
// addCommonTemplateData includes GalleryStats for the about modal.
func TestAddCommonTemplateData_FullPageIncludesGalleryStats(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	data := make(map[string]any)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, data, false)

	if _, ok := result["GalleryStats"]; !ok {
		t.Error("Expected GalleryStats when partial=false (full page has about modal)")
	}
}

// TestGetUser_Additional tests user retrieval from database
func TestAddCommonTemplateData_EdgeCases(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	t.Run("nil data map", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		result := app.AddCommonTemplateData(rr, req, nil, false)

		if result == nil {
			t.Error("Expected non-nil result")
		}
		if _, ok := result["IsAuthenticated"]; !ok {
			t.Error("Expected IsAuthenticated in result")
		}
	})
}

// TestBuildHandlers tests handler building
func TestAddCommonTemplateData_AboutModal(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Create test request and response recorder
	req := httptest.NewRequest("GET", "/gallery/1", nil)
	rr := httptest.NewRecorder()

	// Call addCommonTemplateData which is used for pages that include the about-modal
	data := make(map[string]any)
	result := app.AddCommonTemplateData(rr, req, data, false)

	// Verify Version is present and is a string
	if version, ok := result["Version"].(string); !ok {
		t.Errorf("Expected Version to be string, got %T", result["Version"])
	} else if version == "" {
		t.Error("Expected Version to be non-empty")
	} else {
		t.Logf("✓ Version: %s", version)
	}

	// Verify IsAuthenticated is present and is boolean
	if _, ok := result["IsAuthenticated"].(bool); !ok {
		t.Errorf("Expected IsAuthenticated to be bool, got %T", result["IsAuthenticated"])
	} else {
		t.Logf("✓ IsAuthenticated: %v", result["IsAuthenticated"])
	}

	// Verify CSRFToken is present and is string
	if csrfToken, ok := result["CSRFToken"].(string); !ok {
		t.Errorf("Expected CSRFToken to be string, got %T", result["CSRFToken"])
	} else if csrfToken == "" {
		t.Error("Expected CSRFToken to be non-empty")
	} else {
		t.Logf("✓ CSRFToken present (length: %d)", len(csrfToken))
	}

	// Verify Theme is present and is string
	if theme, ok := result["Theme"].(string); !ok {
		t.Errorf("Expected Theme to be string, got %T", result["Theme"])
	} else if theme == "" {
		t.Error("Expected Theme to be non-empty")
	} else {
		t.Logf("✓ Theme: %s", theme)
	}

	// Verify GalleryStats is present
	galleryStats, ok := result["GalleryStats"].(GalleryStats)
	if !ok {
		t.Fatalf("Expected GalleryStats to be GalleryStats struct, got %T", result["GalleryStats"])
	}

	// Verify GalleryStats.Folders is a STRING (not int64) with comma formatting
	if _, ok := interface{}(galleryStats.Folders).(string); !ok {
		t.Errorf("Expected GalleryStats.Folders to be string (formatted with commas), got %T", galleryStats.Folders)
	} else {
		t.Logf("✓ GalleryStats.Folders: %s (type: %T)", galleryStats.Folders, galleryStats.Folders)
	}

	// Verify GalleryStats.Images is a STRING (not int64) with comma formatting
	if _, ok := interface{}(galleryStats.Images).(string); !ok {
		t.Errorf("Expected GalleryStats.Images to be string (formatted with commas), got %T", galleryStats.Images)
	} else {
		t.Logf("✓ GalleryStats.Images: %s (type: %T)", galleryStats.Images, galleryStats.Images)
	}

	// Verify GalleryStats.ImagesSize is int64 (bytes)
	if galleryStats.ImagesSize < 0 {
		t.Errorf("Expected GalleryStats.ImagesSize to be non-negative int64, got %d", galleryStats.ImagesSize)
	} else {
		t.Logf("✓ GalleryStats.ImagesSize: %d bytes (type: %T)", galleryStats.ImagesSize, galleryStats.ImagesSize)
	}

	// Verify GalleryStats.FirstDiscovery is a string if present
	if galleryStats.FirstDiscovery != "" {
		// Should be in format "2006-01-02 15:04:05"
		if _, ok := interface{}(galleryStats.FirstDiscovery).(string); !ok {
			t.Errorf("Expected GalleryStats.FirstDiscovery to be string, got %T", galleryStats.FirstDiscovery)
		} else {
			t.Logf("✓ GalleryStats.FirstDiscovery: %s", galleryStats.FirstDiscovery)
		}
	} else {
		t.Logf("✓ GalleryStats.FirstDiscovery: empty (no discovery data)")
	}

	// Verify GalleryStats.LastDiscovery is a string if present
	if galleryStats.LastDiscovery != "" {
		// Should be in format "2006-01-02 15:04:05"
		if _, ok := interface{}(galleryStats.LastDiscovery).(string); !ok {
			t.Errorf("Expected GalleryStats.LastDiscovery to be string, got %T", galleryStats.LastDiscovery)
		} else {
			t.Logf("✓ GalleryStats.LastDiscovery: %s", galleryStats.LastDiscovery)
		}
	} else {
		t.Logf("✓ GalleryStats.LastDiscovery: empty (no discovery data)")
	}

	// Test that large numbers are properly formatted with commas
	// Create a large number and verify it gets formatted
	testNum := int64(1234567)
	formatted := humanize.Comma(testNum).String()
	expected := "1,234,567"
	if formatted != expected {
		t.Errorf("Expected humanize.Comma(%d) to return %q, got %q", testNum, expected, formatted)
	} else {
		t.Logf("✓ Number formatting verified: %d → %s", testNum, formatted)
	}
}

func TestAddCommonTemplateData_ModuleStateNil_StatsSuccess(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.moduleStateService = nil
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return GalleryStats{Folders: "1", Images: "2", ImagesSize: 3}, nil
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats, ok := result["GalleryStats"].(GalleryStats)
	if !ok {
		t.Fatalf("expected GalleryStats, got %T", result["GalleryStats"])
	}
	if stats.Folders != "1" || stats.Images != "2" || stats.ImagesSize != 3 {
		t.Errorf("stats = %+v, want Folders=1 Images=2 ImagesSize=3", stats)
	}
}

func TestAddCommonTemplateData_ModuleStateNil_StatsError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.moduleStateService = nil
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return GalleryStats{}, errors.New("stats failed")
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats, ok := result["GalleryStats"].(GalleryStats)
	if !ok {
		t.Fatalf("expected GalleryStats, got %T", result["GalleryStats"])
	}
	if stats != (GalleryStats{}) {
		t.Errorf("stats = %+v, want zero GalleryStats", stats)
	}
}

func TestAddCommonTemplateData_ModuleStateActive_CachedHit(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return true, nil
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 12345, true, nil
	}
	app.SetGalleryStatsCache(&GalleryStats{Folders: "5"}, 12345)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats.Folders != "5" {
		t.Errorf("Folders = %q, want %q", stats.Folders, "5")
	}
}

func TestAddCommonTemplateData_ModuleStateActive_CachedMiss(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return true, nil
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 12345, true, nil
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats != (GalleryStats{}) {
		t.Errorf("stats = %+v, want zero GalleryStats", stats)
	}
}

func TestAddCommonTemplateData_ModuleStateInactive_CachedHit(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return false, nil
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 67890, true, nil
	}
	app.SetGalleryStatsCache(&GalleryStats{Images: "9"}, 67890)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats.Images != "9" {
		t.Errorf("Images = %q, want %q", stats.Images, "9")
	}
}

func TestAddCommonTemplateData_ModuleStateInactive_CachedMiss_RefreshSuccess(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return false, nil
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 11111, true, nil
	}
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return GalleryStats{Folders: "3"}, nil
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats.Folders != "3" {
		t.Errorf("Folders = %q, want %q", stats.Folders, "3")
	}

	cached := app.getGalleryStatsCached(11111)
	if cached == nil || cached.Folders != "3" {
		t.Error("expected cache to be seeded with refreshed stats")
	}
}

func TestAddCommonTemplateData_ModuleStateInactive_CachedMiss_RefreshError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return false, nil
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 22222, true, nil
	}
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return GalleryStats{}, errors.New("refresh failed")
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats != (GalleryStats{}) {
		t.Errorf("stats = %+v, want zero GalleryStats", stats)
	}
}

func TestAddCommonTemplateData_ModuleStateIsActiveError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return false, errors.New("isactive failed")
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 0, false, nil
	}
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return GalleryStats{}, nil
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats != (GalleryStats{}) {
		t.Errorf("stats = %+v, want zero GalleryStats", stats)
	}
}

func TestAddCommonTemplateData_ModuleStateLastStartedError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookAddCommonDataIsActive = func(ctx context.Context, name string) (bool, error) {
		return true, nil
	}
	app.testHookAddCommonDataLastStarted = func(ctx context.Context, name string) (int64, bool, error) {
		return 0, false, errors.New("laststarted failed")
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, nil, false)
	stats := result["GalleryStats"].(GalleryStats)
	if stats != (GalleryStats{}) {
		t.Errorf("stats = %+v, want zero GalleryStats", stats)
	}
}
