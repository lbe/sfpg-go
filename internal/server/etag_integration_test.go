//go:build integration

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

func TestIntegration_ETagIncrementWorkflow(t *testing.T) {
	// Create app with fresh database
	app := CreateApp(t, false)
	ctx := context.Background()

	// Step 1: Verify default ETag is set
	cfg, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	originalETag := cfg.ETagVersion
	if originalETag == "" {
		t.Fatal("Default ETag is empty")
	}

	today := time.Now().Format("20060102")
	if !strings.HasPrefix(originalETag, today) {
		t.Logf("Warning: Default ETag %q does not start with today's date %q", originalETag, today)
	}

	// Step 2: Increment via CLI method
	newETag1, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag CLI: %v", err)
	}

	if newETag1 == originalETag {
		t.Error("CLI increment did not change ETag")
	}

	// Step 3: Verify persistence
	cfg, _ = app.configService.Load(ctx)
	if cfg.ETagVersion != newETag1 {
		t.Errorf("Database ETag = %q, want %q", cfg.ETagVersion, newETag1)
	}

	// Step 4: Increment via API
	if err = app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers: %v", err)
	}

	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	addAuthToRequest(t, app.sessionManager, req)

	app.configHandlers.ConfigIncrementETag(w, req)

	if w.Code != 200 {
		t.Fatalf("API increment status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Step 5: Parse response and verify new ETag
	doc, err := testutil.ParseHTML(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}

	etagElem := testutil.FindElementByID(doc, "config-etag-version")
	if etagElem == nil {
		t.Fatal("Response missing #config-etag-version")
	}

	newETag2 := testutil.GetAttr(etagElem, "value")
	if newETag2 == newETag1 {
		t.Error("API increment did not change ETag")
	}

	// Step 6: Verify final persistence
	cfg, _ = app.configService.Load(ctx)
	if cfg.ETagVersion != newETag2 {
		t.Errorf("Final database ETag = %q, want %q", cfg.ETagVersion, newETag2)
	}

	// Step 7: Verify ETag appears in rendered pages
	req2 := httptest.NewRequest("GET", "/config", nil)
	w2 := httptest.NewRecorder()

	// Auth for ConfigGet
	addAuthToRequest(t, app.sessionManager, req2)

	app.configHandlers.ConfigGet(w2, req2)

	pageDoc, err := testutil.ParseHTML(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}
	pageETag := testutil.FindElementByID(pageDoc, "config-etag-version")
	if pageETag == nil {
		t.Fatal("Config page missing #config-etag-version")
	}
	pageValue := testutil.GetAttr(pageETag, "value")
	if pageValue != newETag2 {
		t.Errorf("Config page ETag = %q, want %q", pageValue, newETag2)
	}
}

func TestIntegration_ETagIncrementSameDay(t *testing.T) {
	app := CreateApp(t, false)
	ctx := context.Background()

	// Set ETag to today-01
	today := time.Now().Format("20060102")
	cfg, _ := app.configService.Load(ctx)
	cfg.ETagVersion = today + "-01"
	app.configService.Save(ctx, cfg)

	// Increment
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag: %v", err)
	}

	expected := today + "-02"
	if newETag != expected {
		t.Errorf("Incremented ETag = %q, want %q", newETag, expected)
	}
}

func TestIntegration_ETagIncrementOldDate(t *testing.T) {
	app := CreateApp(t, false)
	ctx := context.Background()

	// Set ETag to old date
	cfg, _ := app.configService.Load(ctx)
	cfg.ETagVersion = "20260101-99"
	app.configService.Save(ctx, cfg)

	// Increment
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag: %v", err)
	}

	today := time.Now().Format("20060102")
	expected := today + "-01"
	if newETag != expected {
		t.Errorf("Incremented ETag = %q, want %q", newETag, expected)
	}
}

func TestIntegration_ConfigModal_FullWorkflow(t *testing.T) {
	app := CreateApp(t, false)
	ctx := context.Background()
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers: %v", err)
	}

	// Set known ETag from yesterday to force today's first version
	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	today := time.Now().Format("20060102")
	initialETag := yesterday + "-99"
	expectedETag := today + "-01"

	cfg, _ := app.configService.Load(ctx)
	cfg.ETagVersion = initialETag
	app.configService.Save(ctx, cfg)

	// Step 1: Open config modal
	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()

	addAuthToRequest(t, app.sessionManager, req)

	app.configHandlers.ConfigGet(w, req)

	if w.Code != 200 {
		t.Fatalf("ConfigGet status = %d", w.Code)
	}

	// Step 2: Verify modal displays current ETag
	doc, err := testutil.ParseHTML(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}
	etagInput := testutil.FindElementByID(doc, "config-etag-version")
	if etagInput == nil {
		t.Fatal("Config modal missing ETag input")
	}

	value := testutil.GetAttr(etagInput, "value")
	if value != initialETag {
		t.Errorf("Config modal ETag = %q, want %q", value, initialETag)
	}

	// Step 3: Click increment button (simulate HTMX request)
	formData := strings.NewReader("csrf_token=valid-token")
	req2 := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()

	addAuthToRequest(t, app.sessionManager, req2)

	app.configHandlers.ConfigIncrementETag(w2, req2)

	// Step 4: Verify response has incremented value
	doc2, err := testutil.ParseHTML(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}
	etagInput2 := testutil.FindElementByID(doc2, "config-etag-version")

	newValue := testutil.GetAttr(etagInput2, "value")
	if newValue != expectedETag {
		t.Errorf("After increment, ETag = %q, want %q", newValue, expectedETag)
	}

	// Step 5: Verify persistence
	cfg, _ = app.configService.Load(ctx)
	if cfg.ETagVersion != expectedETag {
		t.Errorf("Database ETag = %q, want %q", cfg.ETagVersion, expectedETag)
	}
}

func TestIntegration_MultipleIncrements(t *testing.T) {
	app := CreateApp(t, false)
	ctx := context.Background()

	today := time.Now().Format("20060102")

	// Set to today-08
	cfg, _ := app.configService.Load(ctx)
	cfg.ETagVersion = today + "-08"
	app.configService.Save(ctx, cfg)

	// Increment 5 times
	expected := []string{
		today + "-09",
		today + "-10",
		today + "-11",
		today + "-12",
		today + "-13",
	}

	for i, exp := range expected {
		newETag, err := app.IncrementETag()
		if err != nil {
			t.Fatalf("Increment %d failed: %v", i+1, err)
		}
		if newETag != exp {
			t.Errorf("Increment %d: got %q, want %q", i+1, newETag, exp)
		}
	}
}

func TestConfigIncrementETag_RuntimePropagation(t *testing.T) {
	// 1. Initialize the App using CreateApp(t, false)
	app := CreateApp(t, false)

	// Ensure config is loaded and applied so UI/Handlers are initialized
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	app.applyConfig()

	// 2. Verify initial ETag version in both UI (ui.GetCacheVersion()) and ETag middleware (app.GetETagVersion())
	uiVersion := ui.GetCacheVersion()
	handlerVersion := app.GetETagVersion()

	if uiVersion == "" {
		t.Fatal("Initial UI cache version is empty")
	}
	if handlerVersion == "" {
		t.Fatal("Initial handler ETag version is empty")
	}
	if uiVersion != handlerVersion {
		t.Errorf("Initial mismatch: UI version %q != Handler version %q", uiVersion, handlerVersion)
	}

	// 3. Simulate a POST request to /config/increment-etag
	w := httptest.NewRecorder()
	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Set up authentication and CSRF token in the session
	addAuthToRequest(t, app.sessionManager, req)

	app.configHandlers.ConfigIncrementETag(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ConfigIncrementETag failed: expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// 4. Verify that both UI and ETag middleware versions have incremented
	newUiVersion := ui.GetCacheVersion()
	newHandlerVersion := app.GetETagVersion()

	if newUiVersion == uiVersion {
		t.Errorf("UI version did not increment: stayed at %q", uiVersion)
	}
	if newHandlerVersion == handlerVersion {
		t.Errorf("Handler version did not increment: stayed at %q", handlerVersion)
	}
	if newUiVersion != newHandlerVersion {
		t.Errorf("Updated mismatch: UI version %q != Handler version %q", newUiVersion, newHandlerVersion)
	}

	// 5. Verify that a subsequent request for a template (e.g., app.configHandlers.ConfigGet) or a static asset has the new ETag or version parameter.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/config", nil)

	addAuthToRequest(t, app.sessionManager, req2)

	app.configHandlers.ConfigGet(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("ConfigGet failed: expected status 200, got %d", w2.Code)
	}

	pageDoc, err := testutil.ParseHTML(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}
	pageETag := testutil.FindElementByID(pageDoc, "config-etag-version")
	if pageETag == nil {
		t.Fatal("Config page missing #config-etag-version")
	}
	pageValue := testutil.GetAttr(pageETag, "value")
	if pageValue != newUiVersion {
		t.Errorf("Config page ETag = %q, want %q", pageValue, newUiVersion)
	}
}
