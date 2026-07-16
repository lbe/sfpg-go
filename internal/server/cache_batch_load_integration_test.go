//go:build integration

package server

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/internal/writebatcher"
	"golang.org/x/net/html"
)

// --- moved from server_e2e_test.go (cache batch load) ---
func createAppWithBatchLoadForIntegration(t *testing.T) *App {
	t.Helper()
	opt := getopt.Opt{}
	opt.SessionSecret.String = "e2e-batch-load-secret-with-min-32-bytes"
	opt.SessionSecret.IsSet = true
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	app := CreateApp(t, WithGetoptOpt(opt))
	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.Config = config.DefaultConfig()
	}
	app.ConfigManager.Config.EnableHTTPCache = true
	app.ConfigManager.ConfigMu.Unlock()
	app.SubsystemManager.batchLoadManager = cachebatch.NewManager(cachebatch.Config{
		GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
			cpcRo, err := app.dbRoPool.Get()
			if err != nil {
				return nil, nil
			}
			return cpcRo.Queries, func() { app.dbRoPool.Put(cpcRo) }
		},
		GetHandler:         app.getRouter,
		GetETagVersion:     app.GetETagVersion,
		ModuleStateService: app.SubsystemManager.moduleStateService,
	})
	if app.RuntimeManager.metricsCollector != nil {
		app.RuntimeManager.metricsCollector.SetCacheBatchLoad(app.SubsystemManager.batchLoadManager)
	}
	return app
}

func TestIntegration_CacheBatchLoad_HTTP_Unauthorized(t *testing.T) {
	app := createAppWithBatchLoadForIntegration(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/server/cache-batch-load", nil)
	req.Header.Set("Origin", ts.URL)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /server/cache-batch-load without auth: status = %d, want 401 or 403", resp.StatusCode)
	}
}

func TestIntegration_CacheBatchLoad_HTTP_BlockedWhenDiscoveryActive(t *testing.T) {
	app := createAppWithBatchLoadForIntegration(t)
	defer app.Shutdown()

	ctx := context.Background()
	if err := app.SubsystemManager.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(discovery, true): %v", err)
	}
	defer app.SubsystemManager.moduleStateService.SetActive(ctx, "discovery", false)

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	// Extract CSRF token after login
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/server/cache-batch-load", strings.NewReader("csrf_token="+url.QueryEscape(csrfToken)))
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("POST /server/cache-batch-load when discovery active: status = %d, want 409", resp.StatusCode)
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}
	alertEl := testutil.FindElementByClass(doc, "alert-warning")
	if alertEl == nil {
		t.Fatal("expected alert-warning element (blocked toast)")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(alertEl)); got != "Cache batch load blocked: discovery active" {
		t.Errorf("alert text = %q, want %q", got, "Cache batch load blocked: discovery active")
	}
}

func TestIntegration_CacheBatchLoad_HTTP_StartsWhenIdle(t *testing.T) {
	app := createAppWithBatchLoadForIntegration(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	// Extract CSRF token after login
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)

	// POST to trigger cache batch load with CSRF token
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/server/cache-batch-load", strings.NewReader("csrf_token="+url.QueryEscape(csrfToken)))
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /server/cache-batch-load when idle: status = %d, want 200", resp.StatusCode)
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}
	alertEl := testutil.FindElementByClass(doc, "alert-success")
	if alertEl == nil {
		t.Fatal("expected alert-success element (success toast)")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(alertEl)); got != "Cache batch load started" {
		t.Errorf("alert text = %q, want %q", got, "Cache batch load started")
	}
}

func TestIntegration_CacheBatchLoad_CLI_Success(t *testing.T) {
	oldCommandLine := flag.CommandLine
	oldArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = oldCommandLine
		os.Args = oldArgs
	})
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "e2e-cli-success-with-min-32-bytes")
	t.Setenv("SEPG_SESSION_SECURE", "false")

	opt := getopt.Parse()
	app := New(opt, "e2e")
	defer app.Shutdown()

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}

	code := app.RunCacheBatchLoad()
	if code != 0 {
		t.Errorf("RunCacheBatchLoad() = %d, want 0 (success)", code)
	}
}

func TestIntegration_CacheBatchLoad_ErrorWhenManagerNil(t *testing.T) {
	// Initialize enough of App so getCtx() works without panic.
	app := &App{
		RuntimeManager:   NewRuntimeManager(context.Background()),
		SubsystemManager: NewSubsystemManager(nil),
	}

	code := app.RunCacheBatchLoad()
	if code != 1 {
		t.Errorf("RunCacheBatchLoad() with nil manager = %d, want 1 (error)", code)
	}
}

func TestIntegration_CacheBatchLoad_SubmitCacheWrite(t *testing.T) {
	t.Run("successfully submits cache entry when batcher is available", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()

		app := &App{
			InfrastructureService: NewInfrastructureService(),
		}
		app.writeBatcher = wb

		entry := cachelite.GetHTTPCacheEntry()
		entry.Path = "/test/path"
		entry.ETag = sql.NullString{String: "etag123", Valid: true}
		entry.ContentLength = sql.NullInt64{Int64: 100, Valid: true}
		entry.Body = []byte("test body")

		app.submitCacheWrite(entry)

		// Verify entry was submitted
		if wb.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", wb.PendingCount())
		}
	})

	t.Run("handles nil batcher gracefully", func(t *testing.T) {
		app := &App{
			InfrastructureService: NewInfrastructureService(),
		}
		app.writeBatcher = nil

		entry := cachelite.GetHTTPCacheEntry()
		entry.Path = "/test/path"

		// Should not panic
		app.submitCacheWrite(entry)
	})

	t.Run("handles submission error by returning entry to pool", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		wb, err := writebatcher.New[BatchedWrite](ctx, writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()

		app := &App{
			InfrastructureService: NewInfrastructureService(),
		}
		app.writeBatcher = wb

		entry := cachelite.GetHTTPCacheEntry()
		entry.Path = "/test/path"

		// Should not panic even if batcher rejects it
		app.submitCacheWrite(entry)
	})
}

func TestIntegration_CacheBatchLoad_CLI_Blocked(t *testing.T) {
	oldCommandLine := flag.CommandLine
	oldArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = oldCommandLine
		os.Args = oldArgs
	})
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "e2e-cli-blocked-with-min-32-bytes")
	t.Setenv("SEPG_SESSION_SECURE", "false")

	opt := getopt.Parse()
	app := New(opt, "e2e")
	defer app.Shutdown()

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}

	ctx := context.Background()
	if err := app.SubsystemManager.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(discovery, true): %v", err)
	}
	defer app.SubsystemManager.moduleStateService.SetActive(ctx, "discovery", false)

	code := app.RunCacheBatchLoad()
	if code != 2 {
		t.Errorf("RunCacheBatchLoad() when discovery active = %d, want 2 (blocked)", code)
	}
}

type captureSlogHandler struct {
	mu      sync.Mutex
	records []string
	next    slog.Handler
}

func (h *captureSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	msg := r.Message
	// Append "err" attr if present (often contains the actual error text)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "err" && a.Value.Kind() == slog.KindString {
			msg += " " + a.Value.String()
		}
		return true
	})
	h.records = append(h.records, msg)
	h.mu.Unlock()
	if h.next != nil {
		return h.next.Handle(ctx, r)
	}
	return nil
}

func (h *captureSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.next != nil {
		return &captureSlogHandler{records: h.records, next: h.next.WithAttrs(attrs)}
	}
	return h
}

func (h *captureSlogHandler) WithGroup(name string) slog.Handler {
	if h.next != nil {
		return &captureSlogHandler{records: h.records, next: h.next.WithGroup(name)}
	}
	return h
}

func (h *captureSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next != nil {
		return h.next.Enabled(ctx, level)
	}
	return true
}

func (h *captureSlogHandler) hasFlushError() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.records {
		if strings.Contains(s, "failed to flush") {
			return true
		}
		if strings.Contains(s, "connection is already closed") ||
			strings.Contains(s, "sqlite3: interrupted") ||
			strings.Contains(s, "context canceled") {
			// These in cache/writebatcher context indicate shutdown race
			if strings.Contains(s, "flush") || strings.Contains(s, "batch") {
				return true
			}
		}
	}
	return false
}

func TestIntegration_CacheBatchLoad_ShutdownNoFlushErrors(t *testing.T) {
	oldCommandLine := flag.CommandLine
	oldArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = oldCommandLine
		os.Args = oldArgs
	})
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "e2e-shutdown-no-flush-with-min-32")
	t.Setenv("SEPG_SESSION_SECURE", "false")

	orig := slog.Default()
	cap := &captureSlogHandler{next: orig.Handler()}
	slog.SetDefault(slog.New(cap))
	defer slog.SetDefault(orig)

	opt := getopt.Parse()
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	app := New(opt, "e2e")
	defer app.Shutdown()

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}

	// Seed DB with many folders and files so batch load produces cache writes
	// and the WriteBatcher has a real backlog when RunCacheBatchLoad returns.
	ctx := context.Background()
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("dbRwPool.Get: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	rootPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(ctx, "/seed")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID /seed: %v", err)
	}
	rootFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		PathID:    rootPathID,
		Name:      "seed",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder: %v", err)
	}

	// Create ~30 child folders and ~30 files -> ~120 targets (enough for WriteBatcher backlog)
	for i := 0; i < 30; i++ {
		path := "/seed/f" + fmt.Sprintf("%d", i)
		fpID, err := cpcRw.Queries.UpsertFolderPathReturningID(ctx, path)
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID %s: %v", path, err)
		}
		_, err = cpcRw.Queries.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
			ParentID:  sql.NullInt64{Int64: rootFolder.ID, Valid: true},
			PathID:    fpID,
			Name:      fmt.Sprintf("f%d", i),
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder %s: %v", path, err)
		}
	}
	for i := 0; i < 30; i++ {
		path := "/seed/img" + fmt.Sprintf("%02d", i) + ".jpg"
		fpID, err := cpcRw.Queries.UpsertFilePathReturningID(ctx, path)
		if err != nil {
			t.Fatalf("UpsertFilePathReturningID %s: %v", path, err)
		}
		_, err = cpcRw.Queries.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: rootFolder.ID, Valid: true},
			PathID:    fpID,
			Filename:  fmt.Sprintf("img%02d.jpg", i),
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile %s: %v", path, err)
		}
	}

	// Run batch load then shutdown; WriteBatcher must drain before Shutdown cancels context
	_ = app.RunCacheBatchLoad()
	app.Shutdown()

	if cap.hasFlushError() {
		t.Error("slog captured flush/connection errors during Shutdown; WriteBatcher must drain before context cancellation")
	}
}
