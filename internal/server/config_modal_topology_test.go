package server

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// TestConfigModal_Topology_ThemesOutsideMainForm verifies that the themes <select> has been
// moved out of #config-form into its own <form> that POSTs to /config/themes.
func TestConfigModal_Topology_ThemesOutsideMainForm(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Prepare data for template rendering
	data := map[string]any{
		"CSRFToken": "test-csrf-token",
		"Username":  "admin",
		"Config": &config.Config{
			Themes:       []string{"light", "dark"},
			CurrentTheme: "dark",
		},
		"ETagVersion":  "test-version",
		"HelpText":     map[string]string{},
		"ExampleValue": map[string]string{},
	}

	// Render template
	var buf bytes.Buffer
	if err := ui.RenderTemplate(&buf, "config-modal.html.tmpl", data); err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	// Parse HTML
	doc, err := html.Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("ParseHTML failed: %v", err)
	}

	// --- Assertion (i): themes select still exists in the document ---
	themesSelect := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "select" && testutil.GetAttr(n, "name") == "themes"
	})
	if themesSelect == nil {
		t.Fatal("select[name='themes'] not found in config modal (was it deleted?)")
	}

	// --- Assertion (ii): themes select is NOT a descendant of #config-form ---
	configForm := testutil.FindElementByID(doc, "config-form")
	if configForm == nil {
		t.Fatal("#config-form not found in config modal")
	}

	// Verify themes select is NOT inside the main config form
	if testutil.IsDescendant(configForm, themesSelect) {
		t.Error("select[name='themes'] should NOT be a descendant of #config-form, but it is currently inside the main form")
	}

	// --- Assertion (iii): themes select IS a descendant of a form whose hx-post is '/config/themes' ---
	themesForm := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "form" && testutil.GetAttr(n, "hx-post") == "/config/themes"
	})
	if themesForm == nil {
		// Try action attribute as fallback
		themesForm = testutil.FindElement(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "form" && testutil.GetAttr(n, "action") == "/config/themes"
		})
	}
	if themesForm == nil {
		t.Fatal("form with hx-post='/config/themes' or action='/config/themes' not found")
	}

	// Verify themes select IS inside the themes form
	if !testutil.IsDescendant(themesForm, themesSelect) {
		t.Error("select[name='themes'] should be a descendant of the form with hx-post='/config/themes'")
	}

	// --- Additional check: themes form contains CSRF token and Save button ---
	csrfInput := testutil.FindElement(themesForm, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" &&
			testutil.GetAttr(n, "name") == "csrf_token" &&
			testutil.GetAttr(n, "type") == "hidden"
	})
	if csrfInput == nil {
		t.Error("themes form should contain a hidden CSRF token input")
	}

	saveBtn := testutil.FindElement(themesForm, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "button" &&
			testutil.GetAttr(n, "type") == "submit" &&
			strings.Contains(strings.ToLower(testutil.GetTextContent(n)), "save")
	})
	if saveBtn == nil {
		t.Error("themes form should contain a Save Themes submit button")
	}
}
