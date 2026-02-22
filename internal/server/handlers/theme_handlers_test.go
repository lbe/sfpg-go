package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.local/sfpg/internal/server/config"
)

// TestNewThemeHandlers verifies the constructor properly sets all dependencies
func TestNewThemeHandlers(t *testing.T) {
	//nolint:unlambda // lambda required for type compatibility
	getConfig := func() *config.Config {
		return config.DefaultConfig()
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		return data
	}
	renderModal := func(w http.ResponseWriter, data any) error {
		return nil
	}
	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Server Error", http.StatusInternalServerError)
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, renderModal, serverError)

	if handlers == nil {
		t.Fatal("NewThemeHandlers returned nil")
	}
	if handlers.GetConfig == nil {
		t.Error("GetConfig is nil")
	}
	if handlers.AddCommonTemplateData == nil {
		t.Error("AddCommonTemplateData is nil")
	}
	if handlers.RenderThemeModal == nil {
		t.Error("RenderThemeModal is nil")
	}
	if handlers.ServerError == nil {
		t.Error("ServerError is nil")
	}
}

// TestThemeHandlers_GetEffectiveTheme verifies theme resolution priority:
// 1. Cookie (if valid), 2. Server default
func TestThemeHandlers_GetEffectiveTheme(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light", "cupcake"}
	defaultConfig.CurrentTheme = "dark"

	getConfig := func() *config.Config {
		return defaultConfig
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	tests := []struct {
		name          string
		cookieValue   string
		cookieSet     bool
		expectedTheme string
	}{
		{
			name:          "no cookie returns default theme",
			cookieSet:     false,
			expectedTheme: "dark",
		},
		{
			name:          "valid cookie theme returns cookie value",
			cookieValue:   "light",
			cookieSet:     true,
			expectedTheme: "light",
		},
		{
			name:          "invalid cookie theme returns default",
			cookieValue:   "invalid-theme",
			cookieSet:     true,
			expectedTheme: "dark",
		},
		{
			name:          "another valid theme works",
			cookieValue:   "cupcake",
			cookieSet:     true,
			expectedTheme: "cupcake",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.cookieSet {
				req.AddCookie(&http.Cookie{
					Name:  "theme",
					Value: tt.cookieValue,
				})
			}

			got := handlers.GetEffectiveTheme(req)
			if got != tt.expectedTheme {
				t.Errorf("GetEffectiveTheme() = %v, want %v", got, tt.expectedTheme)
			}
		})
	}
}

// TestThemeHandlers_GetEffectiveTheme_NilConfig verifies fallback when config is nil
func TestThemeHandlers_GetEffectiveTheme_NilConfig(t *testing.T) {
	getConfig := func() *config.Config {
		return nil
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	req := httptest.NewRequest("GET", "/", nil)
	got := handlers.GetEffectiveTheme(req)

	if got != "dark" {
		t.Errorf("GetEffectiveTheme() with nil config = %v, want 'dark'", got)
	}
}

// TestThemeHandlers_GetEffectiveTheme_EmptyThemes verifies behavior with empty themes list
func TestThemeHandlers_GetEffectiveTheme_EmptyThemes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Themes = []string{} // Empty themes list
	cfg.CurrentTheme = "light"

	getConfig := func() *config.Config {
		return cfg
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	req := httptest.NewRequest("GET", "/", nil)
	got := handlers.GetEffectiveTheme(req)

	// Should return currentTheme even if themes list is empty
	if got != "light" {
		t.Errorf("GetEffectiveTheme() with empty themes = %v, want 'light'", got)
	}
}

// TestThemeHandlers_ThemePostHandler verifies POST /theme endpoint
func TestThemeHandlers_ThemePostHandler(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light", "cupcake"}

	getConfig := func() *config.Config {
		return defaultConfig
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	tests := []struct {
		name           string
		formData       url.Values
		expectedStatus int
		expectedCookie bool
		cookieValue    string
	}{
		{
			name:           "valid theme sets cookie",
			formData:       url.Values{"theme": []string{"light"}},
			expectedStatus: http.StatusOK,
			expectedCookie: true,
			cookieValue:    "light",
		},
		{
			name:           "another valid theme sets cookie",
			formData:       url.Values{"theme": []string{"cupcake"}},
			expectedStatus: http.StatusOK,
			expectedCookie: true,
			cookieValue:    "cupcake",
		},
		{
			name:           "invalid theme returns error",
			formData:       url.Values{"theme": []string{"invalid-theme"}},
			expectedStatus: http.StatusBadRequest,
			expectedCookie: false,
		},
		{
			name:           "empty theme returns error",
			formData:       url.Values{"theme": []string{""}},
			expectedStatus: http.StatusBadRequest,
			expectedCookie: false,
		},
		{
			name:           "missing theme returns error",
			formData:       url.Values{},
			expectedStatus: http.StatusBadRequest,
			expectedCookie: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formData := tt.formData.Encode()
			req := httptest.NewRequest("POST", "/theme", strings.NewReader(formData))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()

			handlers.ThemePostHandler(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("ThemePostHandler() status = %v, want %v", rec.Code, tt.expectedStatus)
			}

			cookies := rec.Result().Cookies()
			if tt.expectedCookie {
				found := false
				for _, cookie := range cookies {
					if cookie.Name == "theme" && cookie.Value == tt.cookieValue {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected cookie 'theme' with value '%s' not found", tt.cookieValue)
				}
			} else {
				for _, cookie := range cookies {
					if cookie.Name == "theme" {
						t.Error("Unexpected theme cookie set on error response")
					}
				}
			}
		})
	}
}

// TestThemeHandlers_ThemePostHandler_CookieAttributes verifies cookie is set with correct attributes
func TestThemeHandlers_ThemePostHandler_CookieAttributes(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light"}

	getConfig := func() *config.Config {
		return defaultConfig
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	formData := url.Values{"theme": []string{"light"}}.Encode()
	req := httptest.NewRequest("POST", "/theme", strings.NewReader(formData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handlers.ThemePostHandler(rec, req)

	cookies := rec.Result().Cookies()
	var themeCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "theme" {
			themeCookie = c
			break
		}
	}

	if themeCookie == nil {
		t.Fatal("Theme cookie not found")
	}

	if themeCookie.Name != "theme" {
		t.Errorf("Cookie name = %v, want 'theme'", themeCookie.Name)
	}
	if themeCookie.Value != "light" {
		t.Errorf("Cookie value = %v, want 'light'", themeCookie.Value)
	}
	if themeCookie.Path != "/" {
		t.Errorf("Cookie path = %v, want '/'", themeCookie.Path)
	}
	if themeCookie.MaxAge != 365*24*60*60 {
		t.Errorf("Cookie MaxAge = %v, want %v", themeCookie.MaxAge, 365*24*60*60)
	}
	if themeCookie.HttpOnly != false {
		t.Error("Cookie HttpOnly should be false for _hyperscript access")
	}
	if themeCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("Cookie SameSite = %v, want Lax", themeCookie.SameSite)
	}

	// Content-Type should be text/html for HTMX
	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %v, want 'text/html; charset=utf-8'", contentType)
	}
}

// TestThemeHandlers_ThemePostHandler_NilConfig verifies 500 when config is nil
func TestThemeHandlers_ThemePostHandler_NilConfig(t *testing.T) {
	getConfig := func() *config.Config {
		return nil
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	formData := url.Values{"theme": []string{"light"}}.Encode()
	req := httptest.NewRequest("POST", "/theme", strings.NewReader(formData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handlers.ThemePostHandler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

// TestThemeHandlers_ThemePostHandler_InvalidForm verifies 400 on malformed form
func TestThemeHandlers_ThemePostHandler_InvalidForm(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	getConfig := func() *config.Config {
		return defaultConfig
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	// Send invalid form data (wrong content type without proper encoding)
	req := httptest.NewRequest("POST", "/theme", strings.NewReader("not-valid-form-data"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handlers.ThemePostHandler(rec, req)

	// Should handle gracefully (ParseForm returns nil for simple invalid data)
	// The handler will just get empty theme value
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d for invalid form, got %d", http.StatusBadRequest, rec.Code)
	}
}

// TestThemeHandlers_ThemeModalHandler_NilConfig verifies 500 when config is nil
func TestThemeHandlers_ThemeModalHandler_NilConfig(t *testing.T) {
	getConfig := func() *config.Config {
		return nil
	}

	handlers := NewThemeHandlers(getConfig, nil, nil, nil)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

// TestThemeHandlers_ThemeModalHandler_NoRenderer verifies 500 when renderer is nil
func TestThemeHandlers_ThemeModalHandler_NoRenderer(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	getConfig := func() *config.Config {
		return defaultConfig
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		return data
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, nil, nil)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

// TestThemeHandlers_ThemeModalHandler_RendererError verifies serverError is called on render error
func TestThemeHandlers_ThemeModalHandler_RendererError(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light"}
	getConfig := func() *config.Config {
		return defaultConfig
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		return data
	}

	renderError := errors.New("render failed")
	renderModal := func(w http.ResponseWriter, data any) error {
		return renderError
	}

	serverErrorCalled := false
	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		serverErrorCalled = true
		if err != renderError {
			t.Errorf("ServerError called with wrong error: %v", err)
		}
		http.Error(w, "Server Error", http.StatusInternalServerError)
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, renderModal, serverError)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	if !serverErrorCalled {
		t.Error("ServerError was not called when renderer returned error")
	}
}

// TestThemeHandlers_ThemeModalHandler_WithCookie verifies modal uses cookie theme when valid
func TestThemeHandlers_ThemeModalHandler_WithCookie(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light", "cupcake"}
	defaultConfig.CurrentTheme = "dark"

	getConfig := func() *config.Config {
		return defaultConfig
	}

	addCommonDataCalled := false
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		addCommonDataCalled = true
		data["IsAuthenticated"] = false
		data["CSRFToken"] = "test-token"
		return data
	}

	renderCalled := false
	var renderData map[string]any
	renderModal := func(w http.ResponseWriter, data any) error {
		renderCalled = true
		renderData = data.(map[string]any)
		return nil
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, renderModal, nil)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	req.AddCookie(&http.Cookie{
		Name:  "theme",
		Value: "cupcake",
	})
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if !addCommonDataCalled {
		t.Error("AddCommonTemplateData was not called")
	}

	if !renderCalled {
		t.Fatal("Render function was not called")
	}

	// Check Content-Type
	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %v, want 'text/html; charset=utf-8'", contentType)
	}

	// Verify data passed to renderer
	if renderData["CurrentTheme"] != "cupcake" {
		t.Errorf("CurrentTheme = %v, want 'cupcake'", renderData["CurrentTheme"])
	}

	themes, ok := renderData["Themes"].([]string)
	if !ok {
		t.Fatal("Themes not in render data or wrong type")
	}
	if len(themes) != 3 {
		t.Errorf("len(Themes) = %v, want 3", len(themes))
	}
}

// TestThemeHandlers_ThemeModalHandler_InvalidCookie verifies fallback to default on invalid cookie
func TestThemeHandlers_ThemeModalHandler_InvalidCookie(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light"}
	defaultConfig.CurrentTheme = "dark"

	getConfig := func() *config.Config {
		return defaultConfig
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		return data
	}

	var renderData map[string]any
	renderModal := func(w http.ResponseWriter, data any) error {
		renderData = data.(map[string]any)
		return nil
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, renderModal, nil)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	req.AddCookie(&http.Cookie{
		Name:  "theme",
		Value: "invalid-theme",
	})
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	if renderData["CurrentTheme"] != "dark" {
		t.Errorf("CurrentTheme = %v, want 'dark' (default)", renderData["CurrentTheme"])
	}
}

// TestThemeHandlers_ThemeModalHandler_NoCookie uses default theme when no cookie
func TestThemeHandlers_ThemeModalHandler_NoCookie(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light"}
	defaultConfig.CurrentTheme = "light"

	getConfig := func() *config.Config {
		return defaultConfig
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		return data
	}

	var renderData map[string]any
	renderModal := func(w http.ResponseWriter, data any) error {
		renderData = data.(map[string]any)
		return nil
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, renderModal, nil)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	if renderData["CurrentTheme"] != "light" {
		t.Errorf("CurrentTheme = %v, want 'light' (default)", renderData["CurrentTheme"])
	}
}

// TestThemeHandlers_ThemeModalHandler_AddCommonDataIntegration verifies AddCommonTemplateData is called with correct base data
func TestThemeHandlers_ThemeModalHandler_AddCommonDataIntegration(t *testing.T) {
	defaultConfig := config.DefaultConfig()
	defaultConfig.Themes = []string{"dark", "light"}
	defaultConfig.CurrentTheme = "dark"

	getConfig := func() *config.Config {
		return defaultConfig
	}

	var receivedData map[string]any
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		receivedData = data
		data["IsAuthenticated"] = true
		data["CSRFToken"] = "csrf-token-123"
		return data
	}

	renderModal := func(w http.ResponseWriter, data any) error {
		return nil
	}

	handlers := NewThemeHandlers(getConfig, addCommonData, renderModal, nil)

	req := httptest.NewRequest("GET", "/theme/modal", nil)
	rec := httptest.NewRecorder()

	handlers.ThemeModalHandler(rec, req)

	// Verify base data was passed to AddCommonTemplateData
	if receivedData == nil {
		t.Fatal("AddCommonTemplateData not called with data")
	}
	if receivedData["Themes"] == nil {
		t.Error("Themes not in base data")
	}
	if receivedData["CurrentTheme"] != "dark" {
		t.Errorf("CurrentTheme in base data = %v, want 'dark'", receivedData["CurrentTheme"])
	}
}

// TestThemeCookieConstants verifies cookie constant values
func TestThemeCookieConstants(t *testing.T) {
	if ThemeCookieName != "theme" {
		t.Errorf("ThemeCookieName = %v, want 'theme'", ThemeCookieName)
	}
	if ThemeCookieMaxAge != 365*24*60*60 {
		t.Errorf("ThemeCookieMaxAge = %v, want %v", ThemeCookieMaxAge, 365*24*60*60)
	}
}
