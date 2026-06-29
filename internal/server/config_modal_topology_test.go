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

// TestConfigModal_Topology_TabPanelWiring verifies that the config modal
// has 7 properly wired tab buttons and panels with correct initial visibility.
//
// This is a structural guard against regressions:
//   - Deleting a tab button
//   - Renaming data-tab without renaming the panel
//   - Removing tab-active from the first tab
//   - Moving a panel outside #tab-panels
//   - Misspelling install TabSwitcher in the _ attribute
func TestConfigModal_Topology_TabPanelWiring(t *testing.T) {
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

	// --- Assertion (a): Seven tab buttons exist with correct attributes ---
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

		hyperscript := testutil.GetAttr(btn, "_")
		if !strings.Contains(hyperscript, "install TabSwitcher") {
			t.Errorf("tab button %q: _ attribute does not contain 'install TabSwitcher'", id)
		}

		// Verify tab-active class
		class := testutil.GetAttr(btn, "class")
		if i == 0 && !strings.Contains(class, "tab-active") {
			t.Errorf("first tab button %q: expected class to contain 'tab-active', got %q", id, class)
		} else if i > 0 && strings.Contains(class, "tab-active") {
			t.Errorf("tab button %q (index %d): expected no 'tab-active' in class, got %q", id, i, class)
		}
	}

	// --- Assertion (b): Each data-tab maps to an existing panel ---
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

		expectedLabelledBy := testutil.GetAttr(btn, "id")
		actualLabelledBy := testutil.GetAttr(panel, "aria-labelledby")
		if actualLabelledBy != expectedLabelledBy {
			t.Errorf("panel #%s: expected aria-labelledby=%q, got %q", dataTab, expectedLabelledBy, actualLabelledBy)
		}

		panelClass := testutil.GetAttr(panel, "class")
		if !strings.Contains(panelClass, "tab-panel") {
			t.Errorf("panel #%s: expected class to contain 'tab-panel', got %q", dataTab, panelClass)
		}

		// Panel must be a descendant of #tab-panels
		if !testutil.IsDescendant(tabPanels, panel) {
			t.Errorf("panel #%s is not a descendant of #tab-panels", dataTab)
		}
	}

	// --- Assertion (c): Initial visibility is correct ---
	for i, btn := range tabButtons {
		dataTab := testutil.GetAttr(btn, "data-tab")
		panel := testutil.FindElementByID(doc, dataTab)
		if panel == nil {
			continue
		}

		panelClass := testutil.GetAttr(panel, "class")
		if i == 0 {
			// First tab: panel should NOT be hidden
			if strings.Contains(panelClass, "hidden") {
				t.Errorf("first tab panel #%s: expected NOT to have 'hidden' class, got %q", dataTab, panelClass)
			}
		} else {
			// Other tabs: panels should be hidden
			if !strings.Contains(panelClass, "hidden") {
				t.Errorf("tab panel #%s (index %d): expected 'hidden' class, got %q", dataTab, i, panelClass)
			}
		}
	}
}
