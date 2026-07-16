package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// hasClass reports whether n has the given token in its class attribute.
func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(testutil.GetAttr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// normalizeSpace collapses whitespace in s to a single space and trims it.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func TestConfigHandlers_ConfigGet_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandlers_ConfigGet_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigGet_UsernameFallback(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		getConfigValueFun: func(ctx context.Context, key string) (string, error) {
			return "", errors.New("username lookup failed")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	input := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && testutil.GetAttr(n, "name") == "admin_username"
	})
	if input == nil {
		t.Fatal("admin_username input not found")
	}
	if value := testutil.GetAttr(input, "value"); value != "admin" {
		t.Errorf("admin_username value = %q, want %q", value, "admin")
	}
}

func TestConfigHandlers_ConfigGet_WithCategory(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config?category=security", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	// The config-modal template does not currently emit the category as a visible
	// marker; this test exercises the category branch in ConfigGet and confirms
	// the modal still renders successfully.
	if testutil.FindElementByID(doc, "config-modal-container") == nil {
		t.Error("expected config modal container to render")
	}
}

func TestConfigHandlers_ConfigGet_ThemesSorted(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Create config with unsorted themes
	cfg := config.DefaultConfig()
	cfg.Themes = []string{"dark", "light", "cupcake", "wireframe", "valentine"}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Parse response and verify themes are sorted
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find the themes select element
	themesSelect := testutil.FindElementByID(doc, "")
	if themesSelect == nil {
		// The themes are rendered in a select element with name="themes"
		// We need to check the order of options in the rendered HTML
		bodyStr := w.Body.String()
		// Look for themes in order they should appear (light, dark first, then alphabetical)
		expectedOrder := []string{"light", "dark", "cupcake", "valentine", "wireframe"}

		// Find positions of each theme in the HTML
		positions := make(map[string]int)
		for _, theme := range expectedOrder {
			pos := strings.Index(bodyStr, fmt.Sprintf("value=\"%s\"", theme))
			if pos == -1 {
				t.Errorf("theme %q not found in response", theme)
				continue
			}
			positions[theme] = pos
		}

		// Verify order - each theme should appear before the next alphabetically
		for i := 0; i < len(expectedOrder)-1; i++ {
			curr := expectedOrder[i]
			next := expectedOrder[i+1]
			if positions[curr] > positions[next] {
				t.Errorf("themes not sorted alphabetically: %q (pos %d) should come before %q (pos %d)",
					curr, positions[curr], next, positions[next])
			}
		}
	}
}

func TestConfigHandlers_ConfigGet_FormDisplay_ListenerPortInput(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Verify listener_port input exists in the rendered config form
	input := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" &&
			testutil.GetAttr(n, "name") == "listener_port"
	})
	if input == nil {
		t.Error("config form should contain listener_port input field")
	}
}

func TestConfigHandlers_ConfigGet_DisplaysETagVersion(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Set known ETag version via mock
	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			cfg := config.DefaultConfig()
			cfg.ETagVersion = "20260129-42"
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find ETag version input by ID
	etagInput := testutil.FindElementByID(doc, "config-etag-version")
	if etagInput == nil {
		t.Fatal("element #config-etag-version not found in config page")
	}

	// Verify it displays the current value
	if value := testutil.GetAttr(etagInput, "value"); value != "20260129-42" {
		t.Errorf("ETag input value = %q, want %q", value, "20260129-42")
	}

	// Verify the input is read-only
	var foundReadonly bool
	for _, a := range etagInput.Attr {
		if a.Key == "readonly" {
			foundReadonly = true
			break
		}
	}
	if !foundReadonly {
		t.Error("ETag input should be readonly")
	}
}

func TestConfigHandlers_ConfigGet_EnableCachePreloadCheckbox(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			cfg := config.DefaultConfig()
			cfg.EnableCachePreload = true
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find checkbox by name and type
	checkbox := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" &&
			testutil.GetAttr(n, "name") == "enable_cache_preload" &&
			testutil.GetAttr(n, "type") == "checkbox"
	})
	if checkbox == nil {
		t.Fatal("enable_cache_preload checkbox not found")
	}

	// When EnableCachePreload is true, checkbox should be checked (boolean attribute)
	var foundChecked bool
	for _, a := range checkbox.Attr {
		if a.Key == "checked" {
			foundChecked = true
			break
		}
	}
	if !foundChecked {
		t.Error("enable_cache_preload checkbox should be checked when Config.EnableCachePreload is true")
	}

	// Verify label text
	labelSpan := testutil.FindElement(checkbox.Parent, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "span" && testutil.GetAttr(n, "class") == "label-text font-semibold"
	})
	if labelSpan == nil {
		t.Fatal("enable_cache_preload label text span not found")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(labelSpan)); got != "Enable Cache Preload" {
		t.Errorf("enable_cache_preload label text = %q, want %q", got, "Enable Cache Preload")
	}
}

func TestConfigHandlers_ConfigGet_HasIncrementETagButton(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find button with HTMX post to /config/increment-etag
	button := testutil.FindElement(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "button" {
			return false
		}
		hxPost := testutil.GetAttr(n, "hx-post")
		return hxPost == "/config/increment-etag"
	})

	if button == nil {
		t.Fatal("increment ETag button with hx-post='/config/increment-etag' not found")
	}

	// Verify button text
	text := strings.TrimSpace(testutil.GetTextContent(button))
	if text != "Increment ETag Version" {
		t.Errorf("button text = %q, want %q", text, "Increment ETag Version")
	}

	// Verify HTMX attributes
	hxTarget := testutil.GetAttr(button, "hx-target")
	if hxTarget != "#config-etag-display" {
		t.Errorf("button hx-target = %q, want %q", hxTarget, "#config-etag-display")
	}

	hxInclude := testutil.GetAttr(button, "hx-include")
	if hxInclude != "[name='csrf_token']" {
		t.Errorf("button hx-include = %q, want %q", hxInclude, "[name='csrf_token']")
	}
}

func TestConfigHandlers_ConfigGet_AdminTabUsernamePrepopulated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		getConfigValueFun: func(ctx context.Context, key string) (string, error) {
			return "admin", nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find admin_username input field
	input := testutil.FindElement(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "input" {
			return false
		}
		name := testutil.GetAttr(n, "name")
		return name == "admin_username"
	})
	if input == nil {
		t.Fatal("admin_username input field not found")
	}

	// Check that value attribute is prepopulated
	value := testutil.GetAttr(input, "value")
	if value != "admin" {
		t.Errorf("expected admin_username field to be prepopulated with 'admin', got '%s'", value)
	}
}

func TestConfigHandlers_ConfigGet_NoInlineJavaScript(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	inlineScripts := testutil.FindAllElements(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "script" {
			return false
		}
		for _, attr := range n.Attr {
			if attr.Key == "type" && attr.Val == "text/javascript" {
				return true
			}
		}
		return false
	})

	for _, script := range inlineScripts {
		if testutil.GetAttr(script, "id") != "config-password-validator" {
			t.Fatalf("Found unexpected inline script block in config modal: id=%q", testutil.GetAttr(script, "id"))
		}
	}
}

func TestConfigHandlers_ConfigGet_TabPanelWiring(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Assertion (a): Seven tab buttons exist with correct attributes
	tablist := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" && testutil.GetAttr(n, "role") == "tablist"
	})
	if tablist == nil {
		t.Fatal("div[role='tablist'] not found in config modal")
	}

	tabButtons := testutil.FindAllElements(tablist, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "button" && testutil.GetAttr(n, "role") == "tab"
	})
	if len(tabButtons) != 7 {
		t.Fatalf("expected 7 button[role='tab'] inside tablist, got %d", len(tabButtons))
	}

	for i, btn := range tabButtons {
		id := testutil.GetAttr(btn, "id")
		if id == "" {
			t.Errorf("tab button %d has no id attribute", i)
		}

		dataTab := testutil.GetAttr(btn, "data-tab")
		if dataTab == "" {
			t.Errorf("tab button %q has no data-tab attribute", id)
		}

		ariaControls := testutil.GetAttr(btn, "aria-controls")
		if ariaControls == "" {
			t.Errorf("tab button %q has no aria-controls attribute", id)
		}
		if ariaControls != dataTab {
			t.Errorf("tab button %q: aria-controls=%q does not match data-tab=%q", id, ariaControls, dataTab)
		}

		ariaSelected := testutil.GetAttr(btn, "aria-selected")
		if i == 0 && ariaSelected != "true" {
			t.Errorf("first tab button %q: expected aria-selected='true', got %q", id, ariaSelected)
		} else if i > 0 && ariaSelected != "false" {
			t.Errorf("tab button %q (index %d): expected aria-selected='false', got %q", id, i, ariaSelected)
		}

		if i == 0 && !hasClass(btn, "tab-active") {
			t.Errorf("first tab button %q: expected class to contain 'tab-active', got %q", id, testutil.GetAttr(btn, "class"))
		} else if i > 0 && hasClass(btn, "tab-active") {
			t.Errorf("tab button %q (index %d): expected no 'tab-active' in class, got %q", id, i, testutil.GetAttr(btn, "class"))
		}

		// Check hyperscript _ attribute with TabSwitcher behavior
		hyperscript := testutil.GetAttr(btn, "_")
		if hyperscript != "install TabSwitcher" {
			t.Errorf("tab button %q: expected _ attribute to be 'install TabSwitcher', got %q", id, hyperscript)
		}
	}

	// Assertion (b): Each data-tab maps to an existing panel
	tabPanels := testutil.FindElementByID(doc, "tab-panels")
	if tabPanels == nil {
		t.Fatal("#tab-panels not found in config modal")
	}

	for _, btn := range tabButtons {
		dataTab := testutil.GetAttr(btn, "data-tab")
		panel := testutil.FindElementByID(doc, dataTab)
		if panel == nil {
			t.Errorf("panel #%s referenced by data-tab not found", dataTab)
			continue
		}

		if testutil.GetAttr(panel, "role") != "tabpanel" {
			t.Errorf("panel #%s: expected role='tabpanel', got %q", dataTab, testutil.GetAttr(panel, "role"))
		}

		if !hasClass(panel, "tab-panel") {
			t.Errorf("panel #%s: expected class to contain 'tab-panel', got %q", dataTab, testutil.GetAttr(panel, "class"))
		}

		if !testutil.IsDescendant(tabPanels, panel) {
			t.Errorf("panel #%s is not a descendant of #tab-panels", dataTab)
		}

		// Check aria-labelledby matches button id
		btnID := testutil.GetAttr(btn, "id")
		ariaLabelledBy := testutil.GetAttr(panel, "aria-labelledby")
		if ariaLabelledBy == "" {
			t.Errorf("panel #%s: expected aria-labelledby attribute", dataTab)
		} else if ariaLabelledBy != btnID {
			t.Errorf("panel #%s: aria-labelledby=%q does not match button id=%q", dataTab, ariaLabelledBy, btnID)
		}
	}

	// Assertion (c): Initial visibility is correct
	for i, btn := range tabButtons {
		dataTab := testutil.GetAttr(btn, "data-tab")
		panel := testutil.FindElementByID(doc, dataTab)
		if panel == nil {
			continue
		}

		if i == 0 {
			if hasClass(panel, "hidden") {
				t.Errorf("first tab panel #%s: expected NOT to have 'hidden' class, got %q", dataTab, testutil.GetAttr(panel, "class"))
			}
		} else {
			if !hasClass(panel, "hidden") {
				t.Errorf("tab panel #%s (index %d): expected 'hidden' class, got %q", dataTab, i, testutil.GetAttr(panel, "class"))
			}
		}
	}
}

func TestConfigHandlers_ConfigGet_HelpTextDisplayed(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.HelpText = map[string]string{
		"listener_port": "The port number the server listens on (1-65535)",
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	helpText := "The port number the server listens on (1-65535)"
	found := false
	for _, span := range testutil.FindAllElements(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "span" && hasClass(n, "label-text-alt")
	}) {
		if normalizeSpace(testutil.GetTextContent(span)) == helpText {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Help text %q not found in HTML response", helpText)
	}
}

func TestConfigHandlers_ConfigGet_TooltipsWork(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.HelpText = map[string]string{
		"listener_address": "IP address or hostname to bind to (e.g., 0.0.0.0 for all interfaces)",
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	tipText := "IP address or hostname to bind to (e.g., 0.0.0.0 for all interfaces)"
	found := false
	for _, el := range testutil.FindAllElements(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && testutil.GetAttr(n, "data-tip") == tipText
	}) {
		if hasClass(el, "tooltip") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Tooltip with data-tip %q not found", tipText)
	}
}

func TestConfigHandlers_ConfigGet_ExamplesShown(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.ExampleValues = map[string]string{
		"cache_max_time": "30m, 2h, 7d, 720h",
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	expectedText := "Examples: 30m, 2h, 7d, 720h"
	found := false
	for _, span := range testutil.FindAllElements(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "span" && hasClass(n, "label-text-alt")
	}) {
		if normalizeSpace(testutil.GetTextContent(span)) == expectedText {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Example text %q not found in HTML response", expectedText)
	}
}

func TestConfigHandlers_ConfigGet_PasswordVisibilityToggles(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find Admin ID tab panel
	adminTabPanel := testutil.FindElementByID(doc, "tab-admin")
	if adminTabPanel == nil {
		t.Fatal("Admin ID tab panel not found - cannot test password toggles")
	}

	// Look for password visibility toggle buttons in the admin panel
	// The template has buttons with ids: toggle_admin_current_password,
	// toggle_admin_new_password, toggle_admin_confirm_password
	requiredToggles := []string{
		"admin_current_password",
		"admin_new_password",
		"admin_confirm_password",
	}

	foundCount := 0
	for _, field := range requiredToggles {
		toggleID := "toggle_" + field
		if toggleBtn := testutil.FindElementByID(adminTabPanel, toggleID); toggleBtn == nil {
			t.Errorf("Password toggle button #%s not found in Admin tab", toggleID)
		} else {
			foundCount++
		}
	}
	if foundCount == 0 {
		t.Log("Password visibility toggles not found in Admin tab")
	}
}

// TestConfigHandlers_ConfigGet_AdminTabButtonText verifies the admin tab button
// is present and contains "admin" text. Ported from WP-20's
// TestConfigModal_AdminTab_Exists (deleted config_modal_admin_tab_test.go).
func TestConfigHandlers_ConfigGet_AdminTabButtonText(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	btn := testutil.FindElementByID(doc, "tab-admin-btn")
	if btn == nil {
		t.Fatal("#tab-admin-btn not found")
	}
	if text := strings.TrimSpace(testutil.GetTextContent(btn)); text != "Admin ID" {
		t.Errorf("admin tab button text = %q, want %q", text, "Admin ID")
	}
}

// TestConfigHandlers_ConfigGet_AdminTabPanelExists verifies the admin tab panel
// is present and marked as a tabpanel. Ported from WP-20's
// TestConfigModal_AdminTab_PanelExists (deleted config_modal_admin_tab_test.go).
func TestConfigHandlers_ConfigGet_AdminTabPanelExists(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	panel := testutil.FindElementByID(doc, "tab-admin")
	if panel == nil {
		t.Fatal("#tab-admin panel not found")
	}
	if testutil.GetAttr(panel, "role") != "tabpanel" {
		t.Errorf("expected #tab-admin role='tabpanel', got %q", testutil.GetAttr(panel, "role"))
	}
}

// TestConfigHandlers_ConfigGet_AdminTabFormFieldsExist verifies the admin tab
// panel contains the expected password-change inputs. Ported from WP-20's
// TestConfigModal_AdminTab_FormFieldsExist (deleted config_modal_admin_tab_test.go).
func TestConfigHandlers_ConfigGet_AdminTabFormFieldsExist(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	panel := testutil.FindElementByID(doc, "tab-admin")
	if panel == nil {
		t.Fatal("#tab-admin panel not found")
	}

	required := map[string]bool{
		"admin_username":         false,
		"admin_current_password": false,
		"admin_new_password":     false,
		"admin_confirm_password": false,
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name := testutil.GetAttr(n, "name")
			if _, ok := required[name]; ok {
				required[name] = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(panel)

	for name, found := range required {
		if !found {
			t.Errorf("required input %q not found in #tab-admin", name)
		}
	}
}

// TestConfigHandlers_ConfigGet_LoginSecurityFields verifies the session tab
// renders the login security inputs with values from the loaded config.
func TestConfigHandlers_ConfigGet_LoginSecurityFields(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			cfg := config.DefaultConfig()
			cfg.LoginRateLimitPerIP = 7
			cfg.LockoutThreshold = 4
			cfg.LockoutDuration = 1800
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	panel := testutil.FindElementByID(doc, "tab-session")
	if panel == nil {
		t.Fatal("#tab-session panel not found")
	}

	values := make(map[string]string)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name := testutil.GetAttr(n, "name")
			if name != "" {
				values[name] = testutil.GetAttr(n, "value")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(panel)

	want := map[string]string{
		"login_rate_limit_per_ip": "7",
		"lockout_threshold":       "4",
		"lockout_duration":        "1800",
	}
	for name, wantValue := range want {
		got, ok := values[name]
		if !ok {
			t.Errorf("input %q not found in #tab-session", name)
			continue
		}
		if got != wantValue {
			t.Errorf("input %q value = %q, want %q", name, got, wantValue)
		}
	}
}
