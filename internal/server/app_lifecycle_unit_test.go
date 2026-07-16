package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// TestApp_EnsureCSRFToken_DelegatesToSessionAuthFacade verifies that
// App.EnsureCSRFToken delegates to the embedded SessionAuthFacade.
func TestApp_EnsureCSRFToken_DelegatesToSessionAuthFacade(t *testing.T) {
	secret := "test-secret-with-at-least-32-bytes-long"
	s := NewSessionAuthFacade(secret)
	s.store = sessions.NewCookieStore([]byte(secret))
	s.sessionManager = session.NewManager(s.store, func() *session.OptionsConfig { return nil })

	app := &App{SessionAuthFacade: s}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := app.EnsureCSRFToken(w, r)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	// Calling the facade directly should return the same persisted token.
	if facadeToken := app.SessionAuthFacade.EnsureCSRFToken(w, r); facadeToken != token {
		t.Errorf("App.EnsureCSRFToken() = %q, facade returned %q", token, facadeToken)
	}

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Error("expected session cookie to be set")
	}
}

// TestApp_TriggerRestart_DelegatesToRuntimeManager verifies that
// App.TriggerRestart sets the restart requested flag on the RuntimeManager.
func TestApp_TriggerRestart_DelegatesToRuntimeManager(t *testing.T) {
	app := &App{
		RuntimeManager: NewRuntimeManager(context.Background()),
	}

	if app.IsRestartRequested() {
		t.Fatal("IsRestartRequested should be false initially")
	}

	app.TriggerRestart()

	if !app.IsRestartRequested() {
		t.Error("IsRestartRequested should be true after TriggerRestart")
	}
}

// TestApp_ExecRestart_DelegatesToRuntimeManager verifies that
// App.ExecRestart delegates to RuntimeManager.ExecRestart using its test seams.
func TestApp_ExecRestart_DelegatesToRuntimeManager(t *testing.T) {
	app := &App{
		RuntimeManager: NewRuntimeManager(context.Background()),
	}

	var execCalled bool
	var gotPath string
	app.RuntimeManager.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	app.RuntimeManager.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		gotPath = path
		return nil
	}
	app.RuntimeManager.testSeams.Exit = func(code int) {}

	app.ExecRestart()

	if !execCalled {
		t.Fatal("ExecCommand was not invoked")
	}
	if gotPath != "/test/exe" {
		t.Errorf("ExecCommand path = %q, want %q", gotPath, "/test/exe")
	}
}
