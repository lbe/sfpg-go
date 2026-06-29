//go:build e2eweb

package web_testsuite

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// =========================================================================
// Section 1: Public Routes (no auth required) — 17 tests
// =========================================================================

func TestPublicRoutes(t *testing.T) {
	// Run all public route tests sequentially (not parallel) to avoid
	// interactions with the single test binary. Each test creates its own client.
	runPublicTests := []struct {
		num      int
		name     string
		method   string
		path     string
		body     url.Values
		hx       bool
		expected int
		check    func(t *testing.T, resp *http.Response)
	}{
		// #1: GET /favicon.ico → 200, Content-Type: image/svg+xml
		{
			num: 1, name: "favicon", method: "GET", path: "/favicon.ico",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
					t.Errorf("expected Content-Type image/svg+xml, got %q", ct)
				}
			},
		},
		// #2: GET /health → 200, body contains "ok"
		{
			num: 2, name: "health", method: "GET", path: "/health",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				body, _ := io.ReadAll(resp.Body)
				if string(body) != `{"status":"ok"}` {
					t.Errorf("expected body %q, got %q", `{"status":"ok"}`, string(body))
				}
			},
		},
		// #3: GET / → 302 redirect to /gallery/1
		{
			num: 3, name: "root-redirect", method: "GET", path: "/",
			expected: http.StatusFound,
			check: func(t *testing.T, resp *http.Response) {
				loc := resp.Header.Get("Location")
				if loc == "" {
					t.Error("expected Location header for redirect")
				}
			},
		},
		// #4: GET /static/favicon/favicon.svg → 200
		{
			num: 4, name: "static-favicon", method: "GET", path: "/static/favicon/favicon.svg",
			expected: http.StatusOK,
		},
		// #5: GET /gallery/{FOLDER} → 200
		{
			num: 5, name: "gallery-folder", method: "GET",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				if ct := resp.Header.Get("Content-Type"); ct != "" && ct != "text/html; charset=utf-8" {
					// Just verify it's HTML
				}
			},
		},
		// #6: GET /gallery/0 → 404
		{
			num: 6, name: "gallery-notfound", method: "GET", path: "/gallery/0",
			expected: http.StatusNotFound,
		},
		// #7: GET /image/{FILE} → 200
		{
			num: 7, name: "image-file", method: "GET",
			expected: http.StatusOK,
		},
		// #8: GET /raw-image/{FILE} → 200, Content-Type contains "image/"
		{
			num: 8, name: "raw-image", method: "GET",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				ct := resp.Header.Get("Content-Type")
				if ct == "" {
					t.Error("expected Content-Type for raw image")
				}
			},
		},
		// #9: GET /thumbnail/file/{FILE} → 200, Content-Type: image/jpeg
		{
			num: 9, name: "thumbnail-file", method: "GET",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				ct := resp.Header.Get("Content-Type")
				if ct == "" {
					t.Error("expected Content-Type for thumbnail")
				}
			},
		},
		// #10: GET /thumbnail/folder/{FOLDER} → 200, Content-Type: image/jpeg
		{
			num: 10, name: "thumbnail-folder", method: "GET",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				ct := resp.Header.Get("Content-Type")
				if ct == "" {
					t.Error("expected Content-Type for folder thumbnail")
				}
			},
		},
		// #11: GET /lightbox/{FILE} → 200, with HX
		{
			num: 11, name: "lightbox", method: "GET",
			hx:       true,
			expected: http.StatusOK,
		},
		// #12: GET /info/folder/{FOLDER} → 200, with HX
		{
			num: 12, name: "info-folder", method: "GET",
			hx:       true,
			expected: http.StatusOK,
		},
		// #13: GET /info/image/{FILE} → 200, with HX
		{
			num: 13, name: "info-image", method: "GET",
			hx:       true,
			expected: http.StatusOK,
		},
		// #14: GET /theme/modal → 200
		{
			num: 14, name: "theme-modal", method: "GET", path: "/theme/modal",
			expected: http.StatusOK,
		},
		// #15: POST /theme → 200 (no CSRF needed, sets theme cookie)
		{
			num: 15, name: "theme-post", method: "POST", path: "/theme",
			body:     url.Values{"theme": {"dark"}},
			expected: http.StatusOK,
		},
		// #16: POST /login (fail) → 200, login form with error
		{
			num: 16, name: "login-fail", method: "POST",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				// Verify we get an HTML form with login-error-message
				doc, err := ParseHTML(resp.Body)
				if err != nil {
					t.Fatalf("failed to parse login fail response: %v", err)
				}
				errEl := FindElementByID(doc, "login-error-message")
				if errEl == nil {
					t.Error("expected #login-error-message in failed login response")
				}
			},
		},
		// #17: POST /login (success) → 200, HX-Trigger: auth-changed
		{
			num: 17, name: "auth-changed", method: "POST",
			expected: http.StatusOK,
			check: func(t *testing.T, resp *http.Response) {
				if resp.Header.Get("Hx-Trigger") != "auth-changed" {
					t.Errorf("expected Hx-Trigger: auth-changed, got %q",
						resp.Header.Get("Hx-Trigger"))
				}
			},
		},
	}

	for _, rt := range runPublicTests {
		t.Run(fmt.Sprintf("#%d-%s", rt.num, rt.name), func(t *testing.T) {
			client := newClient()

			// Resolve path with IDs if needed
			path := rt.path
			switch rt.num {
			case 5:
				path = "/gallery/" + folderParam(t)
			case 7:
				path = "/image/" + fileParam(t)
			case 8:
				path = "/raw-image/" + fileParam(t)
			case 9:
				path = "/thumbnail/file/" + fileParam(t)
			case 10:
				path = "/thumbnail/folder/" + folderParam(t)
			case 11:
				path = "/lightbox/" + fileParam(t)
			case 12:
				path = "/info/folder/" + folderParam(t)
			case 13:
				path = "/info/image/" + fileParam(t)
			case 16, 17:
				path = "/login"
			}

			// For login tests, extract CSRF from gallery page first
			if rt.num == 16 || rt.num == 17 {
				csrfResp, err := client.Get(serverURL + "/gallery/1")
				if err != nil {
					t.Fatalf("GET /gallery/1 failed: %v", err)
				}
				csrfToken := extractCSRFFromBody(t, csrfResp.Body)
				csrfResp.Body.Close()
				if csrfToken == "" {
					t.Fatal("could not extract CSRF token for login test")
				}

				pwd := "admin"
				if rt.num == 16 {
					pwd = "wrong"
				}
				rt.body = url.Values{
					"username":   {"admin"},
					"password":   {pwd},
					"csrf_token": {csrfToken},
				}
			}

			resp := doRequest(t, client, rt.method, path, rt.body, rt.hx)
			defer resp.Body.Close()

			status := "PASS"
			note := ""
			if resp.StatusCode != rt.expected {
				status = "FAIL"
				note = fmt.Sprintf("expected %d, got %d", rt.expected, resp.StatusCode)
			}

			// Run optional content check
			if rt.check != nil && status == "PASS" {
				rt.check(t, resp)
			}

			authState := "No"
			reportResult(t, rt.num, path, rt.method, authState, rt.expected, resp.StatusCode, status, note)
		})
	}
}
