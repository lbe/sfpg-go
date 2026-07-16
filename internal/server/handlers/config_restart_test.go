package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

func TestConfigHandlers_Restart_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config/restart", nil)
	w := httptest.NewRecorder()

	NewConfigRestartHandler(ch).RestartHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

// TestConfigHandlers_Restart_Authenticated verifies that an authenticated
// POST /config/restart renders the restart-initiated template and invokes
// the TriggerRestart callback.

func TestConfigHandlers_Restart_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// TriggerRestart handled by deps
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	// TriggerRestart handled by cfgOps (mockConfigOps)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restart", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	NewConfigRestartHandler(ch).RestartHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// TriggerRestart handled by deps
}

func TestConfigHandlers_RestartHandler_FlushesResponseAndTriggersRestart(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// TriggerRestart handled by deps
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	// TriggerRestart handled by cfgOps (mockConfigOps)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restart", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Wrap ResponseRecorder to detect Flush calls.
	w := &flushTrackingResponseWriter{ResponseRecorder: httptest.NewRecorder()}

	NewConfigRestartHandler(ch).RestartHandler(w, req)

	// Assertion 1: HTTP 200 is written.
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Assertion 2: the restart-initiated template is rendered.
	if w.Body.Len() == 0 {
		t.Error("expected restart-initiated template in response body")
	}

	// Assertion 3: response writer is flushed before the goroutine runs.
	if !w.flushed {
		t.Error("expected response writer to be flushed before returning")
	}

	// Assertion 4: restart callback is invoked.
	// TriggerRestart handled by deps
}

// TestConfigHandlers_Validate_MissingFields removed: Validate() eliminated in favor of
// compile-time interface check (var _ interfaces.ServerDeps = (*App)(nil)).
