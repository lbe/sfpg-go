//go:build integration || e2e

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// TestConfigModal_AdminTab_Exists verifies the admin tab button is present on
// the config page.
func TestConfigModal_AdminTab_Exists(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	req := httptest.NewRequest(http.MethodGet, ts.URL+"/config", nil)
	req.AddCookie(MakeAuthCookie(t, app))
	rr := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /config returned status %d, want %d", rr.Code, http.StatusOK)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse config HTML: %v", err)
	}

	btn := findElementByID(doc, "tab-admin-btn")
	if btn == nil {
		t.Fatal("#tab-admin-btn not found")
	}
	if text := strings.ToLower(getTextContent(btn)); !strings.Contains(text, "admin") {
		t.Errorf("expected admin tab button text to contain 'admin', got %q", text)
	}
}

// TestConfigModal_AdminTab_PanelExists verifies the admin tab panel is present
// and marked as a tabpanel.
func TestConfigModal_AdminTab_PanelExists(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	req := httptest.NewRequest(http.MethodGet, ts.URL+"/config", nil)
	req.AddCookie(MakeAuthCookie(t, app))
	rr := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /config returned status %d, want %d", rr.Code, http.StatusOK)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse config HTML: %v", err)
	}

	panel := findElementByID(doc, "tab-admin")
	if panel == nil {
		t.Fatal("#tab-admin panel not found")
	}
	if testutil.GetAttr(panel, "role") != "tabpanel" {
		t.Errorf("expected #tab-admin role='tabpanel', got %q", testutil.GetAttr(panel, "role"))
	}
}

// TestConfigModal_AdminTab_FormFieldsExist verifies the admin tab panel
// contains the expected password-change inputs.
func TestConfigModal_AdminTab_FormFieldsExist(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	req := httptest.NewRequest(http.MethodGet, ts.URL+"/config", nil)
	req.AddCookie(MakeAuthCookie(t, app))
	rr := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /config returned status %d, want %d", rr.Code, http.StatusOK)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse config HTML: %v", err)
	}

	panel := findElementByID(doc, "tab-admin")
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
