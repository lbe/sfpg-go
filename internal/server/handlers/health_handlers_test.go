package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth_Success(t *testing.T) {
	h := NewHealthHandlers(func() string { return "v1" })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	h.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Status != "ok" {
		t.Errorf("expected status ok, got %q", payload.Status)
	}
}

func TestRootRedirect_Success(t *testing.T) {
	h := NewHealthHandlers(func() string { return "abc123" })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h.RootRedirect(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	expected := "/gallery/1?v=abc123"
	if location != expected {
		t.Errorf("expected Location %s, got %s", expected, location)
	}
}

func TestRootRedirect_NonRootPath(t *testing.T) {
	h := NewHealthHandlers(func() string { return "v1" })

	req := httptest.NewRequest(http.MethodGet, "/not-root", nil)
	w := httptest.NewRecorder()

	h.RootRedirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestRootRedirect_WithQueryParams(t *testing.T) {
	h := NewHealthHandlers(func() string { return "test-etag" })

	req := httptest.NewRequest(http.MethodGet, "/?foo=bar", nil)
	w := httptest.NewRecorder()

	h.RootRedirect(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	expected := "/gallery/1?v=test-etag"
	if location != expected {
		t.Errorf("expected Location %s, got %s", expected, location)
	}
}
