//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// TestAdminCredentials_E2E_UpdateFlow tests the complete end-to-end flow for updating admin credentials:
// 1. Load config modal
// 2. Navigate to Admin ID tab
// 3. Fill in admin credential fields
// 4. Submit form
// 5. Verify credentials were updated in database
func TestAdminCredentials_E2E_UpdateFlow(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()
	app.setDB()
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to load config: %v", loadErr)
	}

	// Get current username and password hash for verification
	originalUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get original username: %v", err)
	}

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	originalPasswordHash, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "password")
	app.dbRoPool.Put(cpcRo)
	if err != nil {
		t.Fatalf("Failed to get original password hash: %v", err)
	}

	// Set up authenticated session with CSRF token (following pattern from admin_credentials_test.go)
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_username=newadminuser&admin_current_password=admin&admin_new_password=NewSecureP@ss1&admin_confirm_password=NewSecureP@ss1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	csrfToken := app.ensureCsrfToken(w, req)
	session.Values["csrf_token"] = csrfToken
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Copy cookies and add CSRF token to form
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	form := url.Values{}
	form.Add("admin_username", "newadminuser")
	form.Add("admin_current_password", "admin")
	form.Add("admin_new_password", "NewSecureP@ss1")
	form.Add("admin_confirm_password", "NewSecureP@ss1")
	form.Add("csrf_token", csrfToken)
	req = httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

	w2 := httptest.NewRecorder()

	// Call handler
	app.configHandlers.ConfigPost(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w2.Code)
	}

	// Step 5: Verify credentials were updated in database
	newUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get new username: %v", err)
	}
	if newUsername != "newadminuser" {
		t.Errorf("Expected username 'newadminuser', got %q", newUsername)
	}

	// Verify password was updated
	cpcRo2, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	newPasswordHash, err := cpcRo2.Queries.GetConfigValueByKey(app.ctx, "password")
	app.dbRoPool.Put(cpcRo2)
	if err != nil {
		t.Fatalf("Failed to get new password hash: %v", err)
	}

	// Verify new password works
	err = bcrypt.CompareHashAndPassword([]byte(newPasswordHash), []byte("NewSecureP@ss1"))
	if err != nil {
		t.Errorf("New password verification failed: %v", err)
	}

	// Verify old password no longer works
	err = bcrypt.CompareHashAndPassword([]byte(newPasswordHash), []byte("admin"))
	if err == nil {
		t.Error("Old password should no longer work")
	}

	// Verify password hash changed
	if newPasswordHash == originalPasswordHash {
		t.Error("Password hash should have changed")
	}

	// Verify username changed
	if newUsername == originalUsername {
		t.Error("Username should have changed")
	}

	// Verify success message in response
	doc2, err := testutil.ParseHTML(w2.Body)
	if err != nil {
		t.Fatalf("Failed to parse response HTML: %v", err)
	}

	successMsg := testutil.FindElementByID(doc2, "config-success-message")
	if successMsg == nil {
		// Check if error message exists instead
		errorMsg := testutil.FindElementByID(doc2, "config-error-message")
		if errorMsg != nil {
			text := testutil.GetTextContent(errorMsg)
			t.Errorf("Expected success message, got error: %s", text)
		} else {
			t.Error("Expected success message element with id='config-success-message'")
		}
	}
}

// TestAdminCredentials_E2E_ValidationErrors tests that validation errors are properly displayed
// in the UI when invalid credentials are submitted.
func TestAdminCredentials_E2E_ValidationErrors(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()
	app.setDB()
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to load config: %v", loadErr)
	}

	// Set up authenticated session with CSRF token
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_username=ab&admin_current_password=admin"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	csrfToken := app.ensureCsrfToken(w, req)
	session.Values["csrf_token"] = csrfToken
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Copy cookies and add CSRF token to form
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	form := url.Values{}
	form.Add("admin_username", "abcdefg") // Too short (7 chars)
	form.Add("admin_current_password", "admin")
	form.Add("csrf_token", csrfToken)
	req = httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

	w2 := httptest.NewRecorder()

	// Call handler
	app.configHandlers.ConfigPost(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200 (HTMX error response), got %d", w2.Code)
	}

	// Parse response and verify error message
	doc2, err := html.Parse(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("Failed to parse response HTML: %v", err)
	}

	errorMsg := testutil.FindElementByID(doc2, "config-error-message")
	if errorMsg == nil {
		t.Error("Expected error message element with id='config-error-message'")
	} else {
		text := testutil.GetTextContent(errorMsg)
		if !strings.Contains(text, "username must be at least 8 characters") {
			t.Errorf("Expected validation error about username length, got: %s", text)
		}
	}

	// Verify username was NOT updated
	currentUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get username: %v", err)
	}
	if currentUsername == "ab" {
		t.Error("Username should not have been updated with invalid value")
	}
}

// TestAdminCredentials_E2E_WrongCurrentPassword tests that the system rejects updates
// when the current password is incorrect.
func TestAdminCredentials_E2E_WrongCurrentPassword(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()
	app.setDB()
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to load config: %v", loadErr)
	}

	// Get original username
	originalUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get original username: %v", err)
	}

	// Set up authenticated session with CSRF token
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_username=newadmin&admin_current_password=wrongpassword&admin_new_password=NewPass123!&admin_confirm_password=NewPass123!"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	csrfToken := app.ensureCsrfToken(w, req)
	session.Values["csrf_token"] = csrfToken
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Copy cookies and add CSRF token to form
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	form := url.Values{}
	form.Add("admin_username", "newadmin")
	form.Add("admin_current_password", "wrongpassword")
	form.Add("admin_new_password", "NewPass123!")
	form.Add("admin_confirm_password", "NewPass123!")
	form.Add("csrf_token", csrfToken)
	req = httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

	w2 := httptest.NewRecorder()

	// Call handler
	app.configHandlers.ConfigPost(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200 (HTMX error response), got %d", w2.Code)
	}

	// Parse response and verify error message
	doc2, err := testutil.ParseHTML(w2.Body)
	if err != nil {
		t.Fatalf("Failed to parse response HTML: %v", err)
	}

	errorMsg := testutil.FindElementByID(doc2, "config-error-message")
	if errorMsg == nil {
		t.Error("Expected error message element with id='config-error-message'")
	} else {
		text := testutil.GetTextContent(errorMsg)
		if !strings.Contains(text, "Current password is incorrect") {
			t.Errorf("Expected error about incorrect password, got: %s", text)
		}
	}

	// Verify username was NOT updated
	currentUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get username: %v", err)
	}
	if currentUsername != originalUsername {
		t.Errorf("Username should not have changed, expected %q, got %q", originalUsername, currentUsername)
	}
}
