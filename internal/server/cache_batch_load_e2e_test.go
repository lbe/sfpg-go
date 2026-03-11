//go:build e2e

// Package server cache batch load e2e tests.
//
// Run with:
//
//	go test -tags e2e ./internal/server -run TestE2E_CacheBatchLoad -v
//
// Or include with full suite:
//
//	go test -tags "integration e2e" ./...
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
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/testutil"
)

// createAppWithBatchLoadForE2E creates an app with batch load manager wired,
// suitable for e2e testing the cache batch load HTTP endpoint.
func createAppWithBatchLoadForE2E(t *testing.T) *App {
	t.Helper()
	opt := getopt.Opt{}
	opt.SessionSecret.String = "e2e-batch-load-secret"
	opt.SessionSecret.IsSet = true
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	app := CreateAppWithOpt(t, false, opt)
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.EnableHTTPCache = true
	app.configMu.Unlock()
	app.batchLoadManager = cachebatch.NewManager(cachebatch.Config{
		GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
			cpc, err := app.dbRoPool.Get()
			if err != nil {
				return nil, nil
			}
			return cpc.Queries, func() { app.dbRoPool.Put(cpc) }
		},
		GetHandler:         app.getRouter,
		GetETagVersion:     app.GetETagVersion,
		ModuleStateService: app.moduleStateService,
	})
	if app.metricsCollector != nil {
		app.metricsCollector.SetCacheBatchLoad(&cacheBatchLoadAdapter{m: app.batchLoadManager})
	}
	return app
}

// TestE2E_CacheBatchLoad_HTTP_Unauthorized verifies POST /server/cache-batch-load
// returns 401 or 403 when not authenticated (403 from CSRF when Origin missing).
func TestE2E_CacheBatchLoad_HTTP_Unauthorized(t *testing.T) {
	app := createAppWithBatchLoadForE2E(t)
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

// TestE2E_CacheBatchLoad_HTTP_BlockedWhenDiscoveryActive verifies POST returns 409
// and "discovery active" message when discovery is running.
func TestE2E_CacheBatchLoad_HTTP_BlockedWhenDiscoveryActive(t *testing.T) {
	app := createAppWithBatchLoadForE2E(t)
	defer app.Shutdown()

	ctx := context.Background()
	if err := app.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(discovery, true): %v", err)
	}
	defer app.moduleStateService.SetActive(ctx, "discovery", false)

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/server/cache-batch-load", nil)
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
	if got := testutil.GetTextContent(alertEl); !strings.Contains(got, "discovery active") {
		t.Errorf("alert text = %q, want to contain 'discovery active'", got)
	}
}

// TestE2E_CacheBatchLoad_HTTP_StartsWhenIdle verifies POST returns 200 and success
// toast when idle.
func TestE2E_CacheBatchLoad_HTTP_StartsWhenIdle(t *testing.T) {
	app := createAppWithBatchLoadForE2E(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/server/cache-batch-load", nil)
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
	if got := testutil.GetTextContent(alertEl); !strings.Contains(got, "Cache batch load started") {
		t.Errorf("alert text = %q, want to contain 'Cache batch load started'", got)
	}
}

// TestE2E_CacheBatchLoad_CLI_Success verifies InitForBatchLoad + RunCacheBatchLoad
// returns exit code 0 when idle (functional equivalent of ./sfpg-go --cache-batch-load).
func TestE2E_CacheBatchLoad_CLI_Success(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "e2e-cli-success")
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

// TestE2E_CacheBatchLoad_CLI_Blocked verifies RunCacheBatchLoad returns exit code 2
// when discovery is active (blocked).
func TestE2E_CacheBatchLoad_CLI_Blocked(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "e2e-cli-blocked")
	t.Setenv("SEPG_SESSION_SECURE", "false")

	opt := getopt.Parse()
	app := New(opt, "e2e")
	defer app.Shutdown()

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}

	ctx := context.Background()
	if err := app.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(discovery, true): %v", err)
	}
	defer app.moduleStateService.SetActive(ctx, "discovery", false)

	code := app.RunCacheBatchLoad()
	if code != 2 {
		t.Errorf("RunCacheBatchLoad() when discovery active = %d, want 2 (blocked)", code)
	}
}

// captureSlogHandler records log messages for later inspection.
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

// TestE2E_CacheBatchLoad_ShutdownNoFlushErrors verifies that Shutdown does not
// trigger "failed to flush unified batch" or similar errors when the WriteBatcher
// has a backlog from a cache batch load. Regression test for WriteBatcher/Shutdown race.
func TestE2E_CacheBatchLoad_ShutdownNoFlushErrors(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "e2e-shutdown-no-flush")
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
