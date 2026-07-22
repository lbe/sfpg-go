//go:build e2eweb

package web_testsuite

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// =========================================================================
// Section 8: Config Modal Save Smoke Tests
//
// These tests exercise the configuration modal's save flow end-to-end:
// log in, fetch the config form, change a harmless field (site_name),
// POST it back, and verify the change persists.
// =========================================================================

// TestConfigModal_SaveSiteName verifies the full config modal save flow.
// It parses the live config form, changes site_name, submits it, then
// re-fetches the form to confirm the new value was persisted.
func TestConfigModal_SaveSiteName(t *testing.T) {
	client := newClient()
	login(t, client)

	// Fetch the current config form and capture all field values so we can
	// resubmit them without accidentally toggling checkboxes to false.
	originalValues, err := parseConfigForm(t, client)
	if err != nil {
		t.Fatalf("failed to parse config form: %v", err)
	}

	originalName := originalValues.Get("site_name")
	testName := fmt.Sprintf("WP48Smoke_%d", time.Now().UnixNano())

	// Build the submission: start from the original values, then override
	// site_name. Drop credential/import fields that should not be part of
	// this save.
	submission := cloneValues(originalValues)
	submission.Set("site_name", testName)
	for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
		submission.Del(key)
	}

	// POST the config form (simulates clicking Save Settings in the modal).
	resp := doRequest(t, client, http.MethodPost, "/config", submission, false)
	defer resp.Body.Close()

	status := "PASS"
	note := "OK"
	if resp.StatusCode != http.StatusOK {
		status = "FAIL"
		body, _ := io.ReadAll(resp.Body)
		note = fmt.Sprintf("expected 200, got %d: %s", resp.StatusCode, string(body))
		reportResult(t, 70, "/config", "POST", "Yes", http.StatusOK, resp.StatusCode, status, note)
		t.Fatalf("#70 POST /config: %s", note)
	}

	// Verify HX-Trigger: config-saved header is present.
	if resp.Header.Get("HX-Trigger") != "config-saved" {
		note = fmt.Sprintf("expected HX-Trigger: config-saved, got %q", resp.Header.Get("HX-Trigger"))
		reportResult(t, 70, "/config", "POST", "Yes", http.StatusOK, resp.StatusCode, "FAIL", note)
		t.Fatalf("#70 POST /config: %s", note)
	}

	reportResult(t, 70, "/config", "POST", "Yes", http.StatusOK, resp.StatusCode, status, note)

	// Re-fetch the config form and verify the new site_name persisted.
	t.Run("#71-config-persisted", func(t *testing.T) {
		values, err := parseConfigForm(t, client)
		if err != nil {
			t.Fatalf("failed to re-parse config form: %v", err)
		}
		savedName := values.Get("site_name")

		status := "PASS"
		note := "OK"
		if savedName != testName {
			status = "FAIL"
			note = fmt.Sprintf("expected site_name %q, got %q", testName, savedName)
		}

		reportResult(t, 71, "/config", "GET", "Yes", http.StatusOK, http.StatusOK, status, note)
		if status == "FAIL" {
			t.Fatalf("#71 GET /config: %s", note)
		}
	})

	// Restore the original site_name so the snapshot/restore cleanup is
	// left with a clean state.
	t.Run("#72-config-restore-site-name", func(t *testing.T) {
		restoreValues, err := parseConfigForm(t, client)
		if err != nil {
			t.Fatalf("failed to parse config form for restore: %v", err)
		}
		restoreValues = cloneValues(restoreValues)
		restoreValues.Set("site_name", originalName)
		for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
			restoreValues.Del(key)
		}

		resp := doRequest(t, client, http.MethodPost, "/config", restoreValues, false)
		defer resp.Body.Close()

		status := "PASS"
		note := "OK"
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			status = "FAIL"
			note = fmt.Sprintf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		reportResult(t, 72, "/config", "POST", "Yes", http.StatusOK, resp.StatusCode, status, note)
		if status == "FAIL" {
			t.Fatalf("#72 POST /config restore: %s", note)
		}
	})
}

// TestConfigModal_Unauthenticated_SaveFails verifies that POST /config
// without an authenticated session is rejected.
func TestConfigModal_Unauthenticated_SaveFails(t *testing.T) {
	client := newClient()
	resp := doRequest(t, client, http.MethodPost, "/config", url.Values{
		"site_name": {"ShouldNotPersist"},
	}, false)
	defer resp.Body.Close()

	status := "PASS"
	note := "OK"
	if resp.StatusCode != http.StatusUnauthorized {
		status = "FAIL"
		note = fmt.Sprintf("expected 401, got %d", resp.StatusCode)
	}

	reportResult(t, 73, "/config", "POST", "No", http.StatusUnauthorized, resp.StatusCode, status, note)
	if status == "FAIL" {
		t.Fatalf("#73 POST /config unauth: %s", note)
	}
}

// parseConfigForm fetches GET /config, parses the form, and returns all
// current field values. It understands text inputs, checkboxes, single-selects,
// and multi-selects.
func parseConfigForm(t *testing.T, client *http.Client) (url.Values, error) {
	t.Helper()
	return parseConfigFormRaw(client)
}

// parseConfigFormRaw is the TestMain-safe variant of parseConfigForm: same
// logic, but takes no *testing.T so package-level setup helpers can use it.
func parseConfigFormRaw(client *http.Client) (url.Values, error) {
	resp, err := client.Get(serverURL + "/config")
	if err != nil {
		return nil, fmt.Errorf("GET /config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET /config expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	form := FindElementByID(doc, "config-form")
	if form == nil {
		return nil, fmt.Errorf("#config-form not found")
	}

	values := url.Values{}

	for _, input := range FindAllElements(form, func(n *html.Node) bool {
		return n.Type == html.ElementNode && (n.Data == "input" || n.Data == "select" || n.Data == "textarea")
	}) {
		name := GetAttr(input, "name")
		if name == "" {
			continue
		}

		// Hidden token input removed; COP handles request forgery at the router level

		switch input.Data {
		case "input":
			inputType := GetAttr(input, "type")
			switch inputType {
			case "checkbox":
				if hasAttr(input, "checked") {
					values.Set(name, "on")
				} else {
					values.Set(name, "false")
				}
			case "file", "submit", "button", "image":
				// Not part of the save payload.
			default:
				values.Set(name, GetAttr(input, "value"))
			}
		case "select":
			if hasAttr(input, "multiple") {
				for _, opt := range FindAllElements(input, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "option" && hasAttr(n, "selected")
				}) {
					values.Add(name, GetAttr(opt, "value"))
				}
			} else {
				for c := input.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "option" && hasAttr(c, "selected") {
						values.Set(name, GetAttr(c, "value"))
						break
					}
				}
				if values.Get(name) == "" {
					// Fall back to the first option if none is explicitly selected.
					for c := input.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && c.Data == "option" {
							values.Set(name, GetAttr(c, "value"))
							break
						}
					}
				}
			}
		case "textarea":
			values.Set(name, GetTextContent(input))
		}
	}

	return values, nil
}

// cloneValues returns a deep copy of url.Values.
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

// hasAttr reports whether the node has the given attribute (regardless of value).
func hasAttr(n *html.Node, key string) bool {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return true
		}
	}
	return false
}

// TestConfigModal_SaveLoginSecurityFields verifies the full config modal save
// flow for the three login security fields: change, save, re-fetch, restore.
func TestConfigModal_SaveLoginSecurityFields(t *testing.T) {
	client := newClient()
	login(t, client)

	originalValues, err := parseConfigForm(t, client)
	if err != nil {
		t.Fatalf("failed to parse config form: %v", err)
	}

	origIPLimit := originalValues.Get("login_rate_limit_per_ip")
	origThreshold := originalValues.Get("lockout_threshold")
	origDuration := originalValues.Get("lockout_duration")

	// Build the submission from the full original form so checkboxes are not
	// accidentally toggled; override only the three login security fields.
	submission := cloneValues(originalValues)
	submission.Set("login_rate_limit_per_ip", "9")
	submission.Set("lockout_threshold", "4")
	submission.Set("lockout_duration", "1500")
	for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
		submission.Del(key)
	}
	resp := doRequest(t, client, http.MethodPost, "/config", submission, false)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /config: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if resp.Header.Get("HX-Trigger") != "config-saved" {
		t.Fatalf("expected HX-Trigger: config-saved, got %q", resp.Header.Get("HX-Trigger"))
	}

	// Re-fetch the config form and verify the new values persisted.
	t.Run("login-security-persisted", func(t *testing.T) {
		values, err := parseConfigForm(t, client)
		if err != nil {
			t.Fatalf("failed to re-parse config form: %v", err)
		}
		checks := map[string]string{
			"login_rate_limit_per_ip": "9",
			"lockout_threshold":       "4",
			"lockout_duration":        "1500",
		}
		for key, want := range checks {
			if got := values.Get(key); got != want {
				t.Errorf("after save: %s = %q, want %q", key, got, want)
			}
		}
	})

	// Restore the original values (mirrors #72-config-restore-site-name).
	t.Run("login-security-restore", func(t *testing.T) {
		restoreValues, err := parseConfigForm(t, client)
		if err != nil {
			t.Fatalf("failed to parse config form for restore: %v", err)
		}
		restoreValues = cloneValues(restoreValues)
		restoreValues.Set("login_rate_limit_per_ip", origIPLimit)
		restoreValues.Set("lockout_threshold", origThreshold)
		restoreValues.Set("lockout_duration", origDuration)
		for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
			restoreValues.Del(key)
		}

		resp := doRequest(t, client, http.MethodPost, "/config", restoreValues, false)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("restore POST /config: expected 200, got %d: %s", resp.StatusCode, string(body))
		}
	})
}
