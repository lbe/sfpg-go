package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
	"golang.org/x/net/html"
)

func hasClass(t *testing.T, n *html.Node, class string) bool {
	t.Helper()
	return slices.Contains(strings.Fields(testutil.GetAttr(n, "class")), class)
}

func TestServerShutdownTemplate(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Ensure templates are initialized
	if serverShutdownTemplate == nil {
		t.Fatal("serverShutdownTemplate not initialized")
	}

	var buf strings.Builder
	err := serverShutdownTemplate.ExecuteTemplate(&buf, "layout", nil)
	if err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	doc, err := testutil.ParseHTML(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	page := testutil.FindElementByID(doc, "server-shutdown-page")
	if page == nil {
		t.Fatal("missing #server-shutdown-page")
	}
	if !hasClass(t, page, "hero") {
		t.Error("#server-shutdown-page should have class hero")
	}

	h1 := testutil.FindElementByTag(page, "h1")
	if h1 == nil {
		t.Fatal("missing h1 inside #server-shutdown-page")
	}
	if got := testutil.GetTextContent(h1); got != "Shutting Down" {
		t.Errorf("h1 text = %q, want %q", got, "Shutting Down")
	}

	alert := testutil.FindElementByClass(page, "alert")
	if alert == nil {
		t.Fatal("missing .alert inside #server-shutdown-page")
	}
	if !hasClass(t, alert, "alert-info") {
		t.Error(".alert should have class alert-info")
	}
}

func TestDiscoveryStartedTemplate(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Ensure templates are initialized
	if discoveryStartedTemplate == nil {
		t.Fatal("discoveryStartedTemplate not initialized")
	}

	data := map[string]any{
		"Message": "File discovery started",
	}

	var buf strings.Builder
	err := discoveryStartedTemplate.Execute(&buf, data)
	if err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	doc, err := testutil.ParseHTML(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	toast := testutil.FindElementByID(doc, "discovery-started-toast")
	if toast == nil {
		t.Fatal("missing #discovery-started-toast")
	}
	if !hasClass(t, toast, "toast") {
		t.Error("#discovery-started-toast should have class toast")
	}

	alert := testutil.FindElementByClass(toast, "alert-success")
	if alert == nil {
		t.Fatal("missing .alert-success inside #discovery-started-toast")
	}

	span := testutil.FindElementByTag(alert, "span")
	if span == nil {
		t.Fatal("missing span inside .alert-success")
	}
	if got := testutil.GetTextContent(span); got != "File discovery started" {
		t.Errorf("alert text = %q, want %q", got, "File discovery started")
	}

	if hyperscript := testutil.GetAttr(toast, "_"); hyperscript == "" {
		t.Error("#discovery-started-toast should have a Hyperscript _ attribute")
	}
}

func TestShutdownModalTemplate(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// This template is parsed as part of baseTemplates
	if shutdownModalTemplate == nil {
		t.Fatal("shutdownModalTemplate not initialized")
	}

	// Verify the shutdown modal template can be executed
	var buf strings.Builder
	err := shutdownModalTemplate.Execute(&buf, nil)
	if err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	doc, err := testutil.ParseHTML(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	modalToggle := testutil.FindElementByID(doc, "shutdown_modal")
	if modalToggle == nil {
		t.Fatal("missing #shutdown_modal")
	}
	if !hasClass(t, modalToggle, "modal-toggle") {
		t.Error("#shutdown_modal should have class modal-toggle")
	}

	modalBox := testutil.FindElementByClass(doc, "modal-box")
	if modalBox == nil {
		t.Fatal("missing .modal-box")
	}

	h3 := testutil.FindElementByTag(modalBox, "h3")
	if h3 == nil {
		t.Fatal("missing h3 inside .modal-box")
	}
	if got := testutil.GetTextContent(h3); got != "Confirm Shutdown" {
		t.Errorf("h3 text = %q, want %q", got, "Confirm Shutdown")
	}

	form := testutil.FindElementByID(doc, "shutdown-form")
	if form == nil {
		t.Fatal("missing #shutdown-form")
	}
	if got := testutil.GetAttr(form, "hx-post"); got != "/server/shutdown" {
		t.Errorf("form hx-post = %q, want %q", got, "/server/shutdown")
	}

	button := testutil.FindElementByTag(form, "button")
	if button == nil {
		t.Fatal("missing submit button inside #shutdown-form")
	}
	if got := testutil.GetTextContent(button); got != "Shutdown Server" {
		t.Errorf("submit button text = %q, want %q", got, "Shutdown Server")
	}
}
