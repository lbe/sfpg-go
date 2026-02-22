package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/web"
)

func TestServerShutdownTemplate(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Ensure templates are initialized
	if serverShutdownTemplate == nil {
		t.Fatal("serverShutdownTemplate not initialized")
	}

	data := map[string]any{
		"CSRFToken": "test-token",
	}

	var buf bytes.Buffer
	err := serverShutdownTemplate.ExecuteTemplate(&buf, "layout", data)
	if err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	body := buf.String()

	// Check for expected content
	if !strings.Contains(body, "Shutting Down") {
		t.Error("template should contain 'Shutting Down' heading")
	}
	if !strings.Contains(body, "server is shutting down") {
		t.Error("template should contain shutdown message")
	}
	if !strings.Contains(body, "close this window") {
		t.Error("template should tell user they can close the window")
	}
	// Check for DaisyUI classes
	if !strings.Contains(body, "hero") {
		t.Error("template should use DaisyUI hero class")
	}
	if !strings.Contains(body, "alert") {
		t.Error("template should use DaisyUI alert class")
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

	var buf bytes.Buffer
	err := discoveryStartedTemplate.Execute(&buf, data)
	if err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	body := buf.String()

	// Check for expected content
	if !strings.Contains(body, "File discovery started") {
		t.Error("template should contain the message")
	}
	// Check for toast structure
	if !strings.Contains(body, "toast") {
		t.Error("template should use DaisyUI toast class")
	}
	if !strings.Contains(body, "alert-success") {
		t.Error("template should use DaisyUI alert-success class")
	}
	// Check for auto-hide Hyperscript
	if !strings.Contains(body, "wait") || !strings.Contains(body, "remove me") {
		t.Error("template should include auto-hide Hyperscript")
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
	data := map[string]any{
		"CSRFToken": "test-token",
	}

	var buf bytes.Buffer
	err := shutdownModalTemplate.Execute(&buf, data)
	if err != nil {
		t.Fatalf("template execution failed: %v", err)
	}

	body := buf.String()

	// Check for modal structure
	if !strings.Contains(body, "shutdown_modal") {
		t.Error("template should contain shutdown_modal id")
	}
	if !strings.Contains(body, "modal-toggle") {
		t.Error("template should use DaisyUI modal-toggle class")
	}
	if !strings.Contains(body, "modal-box") {
		t.Error("template should use DaisyUI modal-box class")
	}
	if !strings.Contains(body, "Confirm Shutdown") {
		t.Error("template should contain 'Confirm Shutdown' heading")
	}
	if !strings.Contains(body, "/server/shutdown") {
		t.Error("template should POST to /server/shutdown")
	}
}
