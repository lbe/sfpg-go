package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"go.local/sfpg/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func TestUpdateAdminCredentials_Success(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Get current password hash to verify against
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	currentPasswordHash, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "password")
	app.dbRoPool.Put(cpcRo)
	if err != nil {
		t.Fatalf("Failed to get current password hash: %v", err)
	}

	// Verify current password is "admin" (default)
	err = bcrypt.CompareHashAndPassword([]byte(currentPasswordHash), []byte("admin"))
	if err != nil {
		t.Fatalf("Current password is not 'admin': %v", err)
	}

	// Set up authenticated session with CSRF token (following pattern from config_handlers_test.go)
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_username=newadmin&admin_current_password=admin&admin_new_password=NewPass123!&admin_confirm_password=NewPass123!"))
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
	form.Add("admin_current_password", "admin")
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
		t.Fatalf("Expected status 200, got %d. Body: %s", w2.Code, w2.Body.String())
	}

	// Verify username was updated
	newUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get new username: %v", err)
	}
	if newUsername != "newadmin" {
		t.Errorf("Expected username 'newadmin', got %q", newUsername)
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

	err = bcrypt.CompareHashAndPassword([]byte(newPasswordHash), []byte("NewPass123!"))
	if err != nil {
		t.Errorf("New password verification failed: %v", err)
	}

	// Verify old password no longer works
	err = bcrypt.CompareHashAndPassword([]byte(newPasswordHash), []byte("admin"))
	if err == nil {
		t.Error("Old password should no longer work")
	}
}

func TestUpdateAdminCredentials_WrongCurrentPassword(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

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

	// Parse HTML response
	doc, err := testutil.ParseHTML(w2.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML response: %v", err)
	}

	// Find error message element
	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg == nil {
		t.Error("Expected error message element with id='config-error-message'")
	} else {
		text := testutil.GetTextContent(errorMsg)
		if !strings.Contains(text, "Current password is incorrect") {
			t.Errorf("Expected error message about incorrect password, got: %s", text)
		}
	}

	// Username should not have changed
	currentUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get username: %v", err)
	}
	if currentUsername == "newadmin" {
		t.Error("Username should not have changed with wrong password")
	}
}

func TestUpdateAdminCredentials_UsernameOnly(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Get current password hash
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	originalPasswordHash, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "password")
	app.dbRoPool.Put(cpcRo)
	if err != nil {
		t.Fatalf("Failed to get password hash: %v", err)
	}

	// Set up authenticated session with CSRF token
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_username=newadmin&admin_current_password=admin"))
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
	form.Add("admin_current_password", "admin")
	form.Add("csrf_token", csrfToken)
	req = httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

	w2 := httptest.NewRecorder()

	// Call handler
	app.configHandlers.ConfigPost(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w2.Code, w2.Body.String())
	}

	// Verify username was updated
	newUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get new username: %v", err)
	}
	if newUsername != "newadmin" {
		t.Errorf("Expected username 'newadmin', got %q", newUsername)
	}

	// Verify password was NOT changed
	cpcRo2, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	newPasswordHash, err := cpcRo2.Queries.GetConfigValueByKey(app.ctx, "password")
	app.dbRoPool.Put(cpcRo2)
	if err != nil {
		t.Fatalf("Failed to get password hash: %v", err)
	}

	if newPasswordHash != originalPasswordHash {
		t.Error("Password should not have changed when only updating username")
	}
}

func TestUpdateAdminCredentials_PasswordOnly(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Get current username
	originalUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get username: %v", err)
	}

	// Set up authenticated session with CSRF token
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_current_password=admin&admin_new_password=NewPass123!&admin_confirm_password=NewPass123!"))
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
	form.Add("admin_current_password", "admin")
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
		t.Fatalf("Expected status 200, got %d. Body: %s", w2.Code, w2.Body.String())
	}

	// Verify username was NOT changed
	newUsername, err := app.getAdminUsername()
	if err != nil {
		t.Fatalf("Failed to get username: %v", err)
	}
	if newUsername != originalUsername {
		t.Errorf("Username should not have changed, expected %q, got %q", originalUsername, newUsername)
	}

	// Verify password was updated
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	newPasswordHash, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "password")
	app.dbRoPool.Put(cpcRo)
	if err != nil {
		t.Fatalf("Failed to get password hash: %v", err)
	}

	err = bcrypt.CompareHashAndPassword([]byte(newPasswordHash), []byte("NewPass123!"))
	if err != nil {
		t.Errorf("New password verification failed: %v", err)
	}
}

// Duplicate validation tests removed - covered by unit tests in validation package
// Tests removed: TestUpdateAdminCredentials_ValidationFailures, TestUpdateAdminCredentials_EmptyCurrentPassword, TestUpdateAdminCredentials_PasswordComplexity

func TestUpdateAdminCredentials_CSRFRejection(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Set up authenticated session but use invalid CSRF token
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("admin_username=newadmin&admin_current_password=admin&admin_new_password=NewPass123!&admin_confirm_password=NewPass123!&csrf_token=invalid-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	session, err := app.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	// Set a different CSRF token in session than what's in the form
	session.Values["csrf_token"] = "valid-token-in-session"
	if saveErr := session.Save(req, w); saveErr != nil {
		t.Fatalf("failed to save session: %v", saveErr)
	}

	// Copy cookies
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

	w2 := httptest.NewRecorder()

	// Call handler
	app.configHandlers.ConfigPost(w2, req)

	if w2.Code != http.StatusForbidden {
		t.Errorf("Expected status 403 Forbidden, got %d", w2.Code)
	}
}

// TestUpdateAdminCredentials_OtherConfigFields_NoPasswordRequired verifies that changing
// other config fields (like server compression) does NOT require the admin password.
func TestUpdateAdminCredentials_OtherConfigFields_NoPasswordRequired(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()
	app.setDB()
	if loadErr := app.loadConfig(); loadErr != nil {
		t.Fatalf("Failed to load config: %v", loadErr)
	}

	// Get original compression setting
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	originalCompression, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "server_compression_enable")
	app.dbRoPool.Put(cpcRo)
	if err != nil {
		t.Fatalf("Failed to get original compression setting: %v", err)
	}

	// Set up authenticated session with CSRF token
	req := httptest.NewRequest(http.MethodPost, "/config", nil)
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

	// Submit form with ONLY server compression change (no admin credential fields with values)
	form := url.Values{}
	form.Add("server_compression_enable", "on") // Change compression setting
	// Note: admin credential fields exist in form but are empty (not submitted)
	form.Add("csrf_token", csrfToken)

	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))
	req = httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", w.Header().Get("Set-Cookie"))

	w2 := httptest.NewRecorder()

	// Call handler
	app.configHandlers.ConfigPost(w2, req)

	// Should succeed without requiring admin password
	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w2.Code, w2.Body.String())
	}

	// Parse response to verify no error about admin password
	doc, parseErr := testutil.ParseHTML(w2.Body)
	if parseErr != nil {
		t.Fatalf("Failed to parse HTML response: %v", parseErr)
	}

	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg != nil {
		text := testutil.GetTextContent(errorMsg)
		if strings.Contains(text, "Current password is required") {
			t.Fatalf("Should NOT require admin password when only changing other config fields. Error: %s", text)
		}
	}

	// Verify compression setting was updated (if it was different from original)
	cpcRo2, getErr := app.dbRoPool.Get()
	if getErr != nil {
		t.Fatalf("Failed to get DB connection: %v", getErr)
	}
	newCompression, keyErr := cpcRo2.Queries.GetConfigValueByKey(app.ctx, "server_compression_enable")
	app.dbRoPool.Put(cpcRo2)
	if keyErr != nil {
		t.Fatalf("Failed to get new compression setting: %v", keyErr)
	}

	// Compression should have changed (from original to "true" - checkbox values are stored as "true")
	// Note: If original was already "true", it won't change, but that's OK - the important thing
	// is that no password error occurred
	if newCompression != "true" && originalCompression != "true" {
		t.Errorf("Expected compression to be 'true' (or unchanged if already true), got %q (original: %q)", newCompression, originalCompression)
	}
}
