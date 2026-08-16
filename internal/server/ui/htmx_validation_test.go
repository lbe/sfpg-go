package ui

import (
	"strings"
	"testing"
)

// TestValidateHTMXResponseStructure tests the validation helper.
func TestValidateHTMXResponseStructure(t *testing.T) {
	t.Run("valid single root element for outerHTML", func(t *testing.T) {
		body := `<div id="config-success-message"></div>`
		err := ValidateHTMXResponseStructure(body, "outerHTML", "")
		if err != nil {
			t.Errorf("Expected no error for valid single root, got: %v", err)
		}
	})

	t.Run("invalid multiple root elements for outerHTML", func(t *testing.T) {
		body := `<div id="config-success-message"></div><div id="config-error-message"></div>`
		err := ValidateHTMXResponseStructure(body, "outerHTML", "")
		if err == nil {
			t.Error("Expected error for multiple root elements, got nil")
		}
		if !strings.Contains(err.Error(), "one root element") {
			t.Errorf("Expected error about root elements, got: %v", err)
		}
	})

	t.Run("valid OOB swap with ID and main swap element", func(t *testing.T) {
		// OOB swaps are processed separately, main swap needs one root
		body := `<div id="config-success-message" hx-swap-oob="outerHTML"></div><div id="config-success-message"></div>`
		err := ValidateHTMXResponseStructure(body, "outerHTML", "config-success-message")
		if err != nil {
			t.Errorf("Expected no error for valid OOB swap with main element, got: %v", err)
		}
	})

	t.Run("invalid OOB swap without ID", func(t *testing.T) {
		body := `<div hx-swap-oob="outerHTML"></div><div id="config-success-message"></div>`
		err := ValidateHTMXResponseStructure(body, "outerHTML", "")
		if err == nil {
			t.Error("Expected error for OOB swap without ID, got nil")
		}
		if !strings.Contains(err.Error(), "missing required 'id' attribute") {
			t.Errorf("Expected error about missing ID, got: %v", err)
		}
	})
}
