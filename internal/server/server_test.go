package server

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/handlers"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// --- merged from server_test.go ---
func TestServerError(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	testErr := fmt.Errorf("test error")
	app.ServerError(rr, req, testErr)

	if rr.Code != 500 {
		t.Errorf("Expected status 500, got %d", rr.Code)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML response: %v", err)
	}

	body := testutil.FindElementByTag(doc, "body")
	if body == nil {
		t.Fatal("Expected body element in HTML response")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(body)); got != "Internal Server Error" {
		t.Errorf("Expected 'Internal Server Error' message in HTML response, got %q", got)
	}
}

func TestSetRootDir_WithExplicitPath(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	testDir := t.TempDir()
	app.setRootDir(&testDir)

	if app.rootDir != testDir {
		t.Errorf("Expected rootDir to be %q, got %q", testDir, app.rootDir)
	}
}

func TestSetRootDir_WithNilPath(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	app.testSeams.Executable = func() (string, error) {
		return "/some/test/path/binary", nil
	}

	app.setRootDir(nil)

	if app.rootDir == "" {
		t.Error("Expected rootDir to be set")
	}
}

func TestSetRootDir_Multiple(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	testDir1 := t.TempDir()
	testDir2 := t.TempDir()

	app.setRootDir(&testDir1)
	if app.rootDir != testDir1 {
		t.Errorf("Expected rootDir to be %q, got %q", testDir1, app.rootDir)
	}

	app.setRootDir(&testDir2)
	if app.rootDir != testDir2 {
		t.Errorf("Expected rootDir to be %q", testDir2)
	}
}

func TestSetConfigDefaultsLegacy_Coverage(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	if app == nil {
		t.Fatal("Expected app to be created")
	}
}

func TestAddCommonTemplateData_PartialIncludesGalleryStats(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")
	app.ensureSession()

	data := make(map[string]any)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, data, true)

	if _, ok := result["IsAuthenticated"]; !ok {
		t.Error("Expected IsAuthenticated in template data")
	}
	// GalleryStats must be present when partial=true (dashboard polls need it)
	if gs, ok := result["GalleryStats"].(*GalleryStats); !ok || gs == nil {
		t.Error("Expected non-nil GalleryStats when partial=true")
	}
}

func TestAddCommonTemplateData_FullPageIncludesGalleryStats(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")
	app.ensureSession()

	data := make(map[string]any)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, data, false)
	if gs, ok := result["GalleryStats"].(*GalleryStats); !ok || gs == nil {
		t.Error("Expected non-nil GalleryStats when partial=false")
	}
}

func TestAddCommonTemplateData_AboutModalGetsN_A(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")
	app.ensureSession()

	gs := app.RuntimeManager.GalleryStats()
	gs.markRunning(1)
	defer gs.markRunning(-1)

	data := make(map[string]any)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	result := app.AddCommonTemplateData(rr, req, data, false)
	stats, ok := result["GalleryStats"].(*GalleryStats)
	if !ok || stats == nil {
		t.Fatal("expected GalleryStats in result")
	}
	if got := stats.Folders(); got != "N/A" {
		t.Errorf("Folders() = %q, want N/A (running>0 and count=0)", got)
	}
	if got := stats.Images(); got != "N/A" {
		t.Errorf("Images() = %q, want N/A (running>0 and count=0)", got)
	}
}

func TestGalleryStats_DisplayN_A(t *testing.T) {
	gs := &GalleryStats{}
	gs.markRunning(1)
	if got := gs.Folders(); got != "N/A" {
		t.Errorf("Folders() = %q, want N/A", got)
	}
	if got := gs.Images(); got != "N/A" {
		t.Errorf("Images() = %q, want N/A", got)
	}
}

func TestGalleryStats_DisplayZero(t *testing.T) {
	gs := &GalleryStats{}
	// running=0 by default; counters are zero
	if got := gs.Folders(); got != "0" {
		t.Errorf("Folders() = %q, want 0", got)
	}
	if got := gs.Images(); got != "0" {
		t.Errorf("Images() = %q, want 0", got)
	}
}

func TestGalleryStats_DisplayFormatted(t *testing.T) {
	gs := &GalleryStats{}
	gs.setFolders(1234567)
	gs.setFileStats(98765, 0, 0, 0)
	if got := gs.Folders(); got != "1,234,567" {
		t.Errorf("Folders() = %q, want 1,234,567", got)
	}
	if got := gs.Images(); got != "98,765" {
		t.Errorf("Images() = %q, want 98,765", got)
	}
}

func TestGalleryStats_RunningCounter(t *testing.T) {
	gs := &GalleryStats{}
	gs.markRunning(1)
	gs.markRunning(1)
	if got := gs.running.Load(); got != 2 {
		t.Errorf("running = %d, want 2", got)
	}
	gs.markRunning(-1)
	gs.markRunning(-1)
	if got := gs.running.Load(); got != 0 {
		t.Errorf("running = %d, want 0", got)
	}
}

func TestGalleryStats_addFile(t *testing.T) {
	gs := &GalleryStats{}
	gs.addFile(1024)
	if got := gs.Images(); got != "1" {
		t.Errorf("Images() = %q, want 1", got)
	}
	if got := gs.ImagesSize(); got != 1024 {
		t.Errorf("ImagesSize() = %d, want 1024", got)
	}
	if got := gs.FirstDiscovery(); got == "" {
		t.Error("FirstDiscovery() should be non-empty after addFile")
	}
	if got := gs.LastDiscovery(); got == "" {
		t.Error("LastDiscovery() should be non-empty after addFile")
	}
	// firstDisc uses CAS — second addFile should NOT overwrite it.
	first := gs.FirstDiscovery()
	gs.addFile(2048)
	if got := gs.FirstDiscovery(); got != first {
		t.Errorf("FirstDiscovery() = %q, want %q (should not change after CAS)", got, first)
	}
	if got := gs.Images(); got != "2" {
		t.Errorf("Images() = %q, want 2 after second addFile", got)
	}
	if got := gs.ImagesSize(); got != 3072 {
		t.Errorf("ImagesSize() = %d, want 3072", got)
	}
}

func TestGalleryStats_FirstLastDiscovery(t *testing.T) {
	gs := &GalleryStats{}
	if got := gs.FirstDiscovery(); got != "" {
		t.Errorf("FirstDiscovery() = %q, want empty", got)
	}
	if got := gs.LastDiscovery(); got != "" {
		t.Errorf("LastDiscovery() = %q, want empty", got)
	}
	gs.addFile(100)
	first := gs.FirstDiscovery()
	last := gs.LastDiscovery()
	if first == "" || last == "" {
		t.Fatal("FirstDiscovery and LastDiscovery should be set after addFile")
	}
	if first == last {
		// Both set at roughly the same time; this is expected for first file.
		t.Log("FirstDiscovery and LastDiscovery match (expected for single file)")
	}
}

func TestGalleryStats_ImagesSize_NAWhileRunning(t *testing.T) {
	gs := &GalleryStats{}
	gs.markRunning(1)
	gs.setFolders(5)
	if got := gs.Folders(); got != "5" {
		t.Errorf("Folders() = %q, want 5", got)
	}
	if got := gs.Images(); got != "N/A" {
		t.Errorf("Images() = %q, want N/A while running", got)
	}
	if got := gs.ImagesSize(); got != -1 {
		t.Errorf("ImagesSize() = %d, want -1 (N/A) while running", got)
	}
	gs.markRunning(-1)
	if got := gs.Images(); got != "0" {
		t.Errorf("Images() = %q, want 0 after running", got)
	}
	if got := gs.ImagesSize(); got != 0 {
		t.Errorf("ImagesSize() = %d, want 0 after running", got)
	}
}

func TestReloadLoggingFromConfig(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	tempDir := t.TempDir()
	app.setRootDir(&tempDir)

	// setupBootstrapLogging so logger exists
	app.setupBootstrapLogging()
	if app.logger == nil {
		t.Fatal("Expected logger after setupBootstrapLogging")
	}

	// reloadLoggingFromConfig with nil config (no config loaded) should not panic
	_ = app.reloadLoggingFromConfig()
}

func TestGetSessionOptions_FallbackWithoutSessionManager(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	// Ensure config stays nil so GetSessionOptions uses defaults.
	if app.ConfigManager.Config != nil {
		t.Fatal("expected ConfigManager.Config to be nil for this test")
	}

	opts := session.GetSessionOptions(app.getSessionOptionsConfig())
	if opts == nil {
		t.Fatal("expected non-nil session options")
	}

	want := session.GetSessionOptions(nil)
	if opts.MaxAge != want.MaxAge {
		t.Errorf("MaxAge = %d, want %d", opts.MaxAge, want.MaxAge)
	}
	if opts.HttpOnly != want.HttpOnly {
		t.Errorf("HttpOnly = %v, want %v", opts.HttpOnly, want.HttpOnly)
	}
	if opts.Secure != want.Secure {
		t.Errorf("Secure = %v, want %v", opts.Secure, want.Secure)
	}
	if opts.SameSite != want.SameSite {
		t.Errorf("SameSite = %v, want %v", opts.SameSite, want.SameSite)
	}
}

func TestEnsureSession(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	// Before ensureSession
	if app.SessionAuthFacade.store != nil {
		t.Fatal("expected store to be nil before ensureSession")
	}
	if app.SessionAuthFacade.sessionManager != nil {
		t.Fatal("expected sessionManager to be nil before ensureSession")
	}

	app.ensureSession()

	if app.SessionAuthFacade.store == nil {
		t.Error("Expected store to be initialized after ensureSession")
	}
	if app.SessionAuthFacade.sessionManager == nil {
		t.Error("Expected sessionManager to be initialized after ensureSession")
	}
}

func TestGetSessionOptions_AfterEnsureSession(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	app.ensureSession()

	opts := app.SessionAuthFacade.sessionManager.GetOptions()
	if opts == nil {
		t.Fatal("Expected non-nil session options after ensureSession")
	}
	if opts.MaxAge <= 0 {
		t.Error("Expected positive MaxAge")
	}
}

func TestGetSessionOptionsConfig_NilConfig(t *testing.T) {
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}, "x.y.z")

	cfg := app.getSessionOptionsConfig()
	if cfg != nil {
		t.Error("Expected nil session options config when app.ConfigManager.Config is nil")
	}
}

func TestInitForUnlock_Coverage(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	testDir := t.TempDir()
	app.setRootDir(&testDir)

	err := app.InitForUnlock()
	if err != nil {
		t.Fatalf("InitForUnlock failed: %v", err)
	}

	if app.dbPaths.Main == "" {
		t.Error("Expected dbPaths.Main to be set")
	}
	if app.dbRwPool == nil {
		t.Error("Expected dbRwPool to be set")
	}
	if app.dbRoPool == nil {
		t.Error("Expected dbRoPool to be set")
	}
}

func TestInitForUnlock_Multiple(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	testDir := t.TempDir()
	app.setRootDir(&testDir)

	// Initialize multiple times
	err1 := app.InitForUnlock()
	if err1 != nil {
		t.Fatalf("First InitForUnlock failed: %v", err1)
	}

	// Multiple initializations should work
	app2 := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app2.Shutdown()
	app2.setRootDir(&testDir)

	err2 := app2.InitForUnlock()
	if err2 != nil {
		t.Fatalf("Second InitForUnlock failed: %v", err2)
	}
}

func TestApp_AuthWrappers(t *testing.T) {
	app := newAppForUnlock(t)
	ctx := context.Background()

	if err := app.UpdateUsername(ctx, "admin"); err != nil {
		t.Fatalf("UpdateUsername: %v", err)
	}
	if err := app.UpdatePassword(ctx, "secret-hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	user, err := app.GetUser(ctx, "admin")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Username != "admin" || user.Password != "secret-hash" {
		t.Errorf("GetUser returned unexpected user: %+v", user)
	}

	locked, err := app.CheckAccountLockout(ctx, "admin")
	if err != nil {
		t.Fatalf("CheckAccountLockout: %v", err)
	}
	if locked {
		t.Error("expected account to be unlocked initially")
	}

	if err := app.RecordFailedLoginAttempt(ctx, "admin"); err != nil {
		t.Fatalf("RecordFailedLoginAttempt: %v", err)
	}
	if err := app.ClearLoginAttempts(ctx, "admin"); err != nil {
		t.Fatalf("ClearLoginAttempts: %v", err)
	}
	if err := app.unlockAccountFromTask(ctx, "admin"); err != nil {
		t.Fatalf("unlockAccountFromTask: %v", err)
	}
}

func TestApp_SetPreloadEnabled(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.SetPreloadEnabled(false)
	app.SetPreloadEnabled(true)
}

func TestApp_RestartRequired(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.SetRestartRequired(true)
	if !app.RestartRequired() {
		t.Error("expected RestartRequired to be true")
	}
}

func TestApp_ResetStats(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.ResetStats()
}

// --- merged from bench_test.go ---
func BenchmarkRemoveImagesDirPrefix(b *testing.B) {
	normalizedImagesDir := "Images"
	path := "Images/gallery/subfolder/photo.jpg"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = removeImagesDirPrefix(normalizedImagesDir, path)
	}
}

func BenchmarkRemoveImagesDirPrefix_WithFilepathJoin(b *testing.B) {
	normalizedImagesDir := "Images"

	b.Run("PreNormalized", func(b *testing.B) {
		path := "Images/gallery/subfolder/photo.jpg"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = removeImagesDirPrefix(normalizedImagesDir, path)
		}
	})

	b.Run("WithJoin", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			path := filepath.Join("Images", "gallery", "subfolder", "photo.jpg")
			_, _ = removeImagesDirPrefix(normalizedImagesDir, path)
		}
	})
}

func BenchmarkFileOpen(b *testing.B) {
	// Create a temporary JPEG file for testing
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "test.jpg")

	// Create a simple 1x1 JPEG
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(testFile)
	if err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		b.Fatalf("Failed to encode JPEG: %v", err)
	}
	f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ff, err := os.Open(testFile)
		if err != nil {
			b.Fatalf("Failed to open file: %v", err)
		}
		// Read first 512 bytes (simulating MIME detection)
		buf := make([]byte, 512)
		_, _ = ff.Read(buf)
		ff.Close()
	}
}

func BenchmarkIsImageFile(b *testing.B) {
	paths := []string{
		"/path/to/image.jpg",
		"/path/to/image.png",
		"/path/to/image.gif",
		"/path/to/image.webp",
		"/path/to/document.pdf",
		"/path/to/video.mp4",
		"/path/to/archive.zip",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range paths {
			_ = files.IsImageFile(path)
		}
	}
}

func BenchmarkPathOperations(b *testing.B) {
	b.Run("filepath.Join", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = filepath.Join("Images", "gallery", "photo.jpg")
		}
	})

	b.Run("filepath.ToSlash", func(b *testing.B) {
		path := filepath.Join("Images", "gallery", "photo.jpg")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = filepath.ToSlash(path)
		}
	})

	b.Run("CachedNormalization", func(b *testing.B) {
		// Simulates the optimization where we cache filepath.ToSlash(imagesDir)
		normalizedBase := filepath.ToSlash("Images")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Just string concatenation after pre-normalization
			_ = normalizedBase + "/gallery/photo.jpg"
		}
	})
}

// --- merged from server_handlers_test.go ---
type mockServerDeps struct {
	Cfg       *config.Config
	ETag      string
	BatchLoad interfaces.StartCacheBatchLoadResult
	BatchErr  error
}

func (m *mockServerDeps) CheckAccountLockout(ctx context.Context, username string) (bool, error) {
	return false, nil
}

func (m *mockServerDeps) GetUser(ctx context.Context, username string) (*session.User, error) {
	return nil, nil
}

func (m *mockServerDeps) RecordFailedLoginAttempt(ctx context.Context, username string) error {
	return nil
}

func (m *mockServerDeps) ClearLoginAttempts(ctx context.Context, username string) error {
	return nil
}

func (m *mockServerDeps) UpdateUsername(ctx context.Context, username string) error {
	return nil
}

func (m *mockServerDeps) UpdatePassword(ctx context.Context, passwordHash string) error {
	return nil
}

func (m *mockServerDeps) UpdateConfigWithPrecedence(cfg *config.Config, changedFields []string) {
	m.Cfg = cfg
}

func (m *mockServerDeps) ApplyConfig() {}

func (m *mockServerDeps) InvalidateHTTPCache() {}

func (m *mockServerDeps) SetPreloadEnabled(enabled bool) {}

func (m *mockServerDeps) SetRestartRequired(b bool) {}

func (m *mockServerDeps) TriggerRestart() {}

func (m *mockServerDeps) GetHandlerQueries(cpc *dbconnpool.CpConn) interfaces.HandlerQueries {
	return nil
}

func (m *mockServerDeps) GetMetadataQueries(cpc *dbconnpool.CpConn) interfaces.MetadataQueries {
	return nil
}

func (m *mockServerDeps) GetConfigQueries(cpc *dbconnpool.CpConn) config.ConfigQueries {
	return nil
}

func (m *mockServerDeps) GetETagVersion() string { return m.ETag }

func (m *mockServerDeps) ImagesDir() string { return "" }

func (m *mockServerDeps) Shutdown() {}

func (m *mockServerDeps) TriggerDiscovery() {}

func (m *mockServerDeps) ResetStats() {}

func (m *mockServerDeps) StartCacheBatchLoad() (interfaces.StartCacheBatchLoadResult, error) {
	return m.BatchLoad, m.BatchErr
}

func (m *mockServerDeps) GetConfig() *config.Config { return m.Cfg }

func (m *mockServerDeps) AddCommonTemplateData(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any {
	if data == nil {
		data = make(map[string]any)
	}
	data["IsAuthenticated"] = true
	return data
}

func (m *mockServerDeps) ServerError(w http.ResponseWriter, r *http.Request, err error) {
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

type fakeSessionManager struct{}

func (f *fakeSessionManager) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return true
}

func setupTestApp(t *testing.T) *App {
	t.Helper()

	app := New(getopt.Opt{}, "test")
	// Replace the SessionAuthFacade created by New (which uses an empty session
	// secret) with one that has a real secret so session cookies can be signed.
	app.SessionAuthFacade = NewSessionAuthFacade("test-session-secret-with-min-32-bytes")
	app.ensureSession()

	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	deps := &mockServerDeps{
		Cfg: config.DefaultConfig(),
	}
	app.HandlerManager.serverHandlers = handlers.NewServerHandlers(&fakeSessionManager{}, deps, deps.AddCommonTemplateData, deps.ServerError)

	return app
}

func authenticatedRequest(t *testing.T, app *App, method, path string) *http.Request {
	t.Helper()

	// Establish an authenticated session and capture the cookie.
	loginReq := httptest.NewRequest(http.MethodGet, "/", nil)
	loginRR := httptest.NewRecorder()
	if err := app.SessionAuthFacade.sessionManager.SetAuthenticated(loginRR, loginReq, true); err != nil {
		t.Fatalf("SetAuthenticated failed: %v", err)
	}

	req := httptest.NewRequest(method, path, nil)
	for _, c := range loginRR.Result().Cookies() {
		req.AddCookie(c)
	}

	return req
}

func TestServerHandlers_Initialized(t *testing.T) {
	app := setupTestApp(t)

	if app.HandlerManager.serverHandlers == nil {
		t.Error("serverHandlers not initialized")
	}
}

func TestServerShutdownRoute(t *testing.T) {
	app := setupTestApp(t)

	// Unauthenticated: route exists if it is not a 404.
	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", nil)
	rr := httptest.NewRecorder()
	app.getRouter().ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Errorf("shutdown route returned 404 - not registered")
	}

	// Authenticated: should render the shutdown page.
	req = authenticatedRequest(t, app, http.MethodPost, "/server/shutdown")
	rr = httptest.NewRecorder()
	app.getRouter().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d for authenticated shutdown, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %s", contentType)
	}
}

func TestServerDiscoveryRoute(t *testing.T) {
	app := setupTestApp(t)

	// Unauthenticated: route exists if it is not a 404.
	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()
	app.getRouter().ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Errorf("discovery route returned 404 - not registered")
	}

	// Authenticated: should render the discovery started notification.
	req = authenticatedRequest(t, app, http.MethodPost, "/server/discovery")
	rr = httptest.NewRecorder()
	app.getRouter().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d for authenticated discovery, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %s", contentType)
	}
}

var _ interfaces.ServerDeps = (*mockServerDeps)(nil)

var _ handlers.SessionManager = (*fakeSessionManager)(nil)
