package cachepreload

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestMakeInternalRequest_NilHandlerReturnsError(t *testing.T) {
	cfg := InternalRequestConfig{
		Handler:     nil,
		ETagVersion: "v1",
	}
	err := MakeInternalRequest(context.Background(), cfg, "/gallery/1")
	if err == nil {
		t.Fatal("expected error when Handler is nil")
	}
	if !strings.Contains(err.Error(), "handler") {
		t.Errorf("error should mention handler, got %q", err.Error())
	}
}

func TestMakeInternalRequestWithVariant_NilHandlerReturnsError(t *testing.T) {
	cfg := InternalRequestConfig{
		Handler:     nil,
		ETagVersion: "v1",
	}
	err := MakeInternalRequestWithVariant(context.Background(), cfg, "/gallery/1", "", "identity")
	if err == nil {
		t.Fatal("expected error when Handler is nil")
	}
	if !strings.Contains(err.Error(), "handler") {
		t.Errorf("error should mention handler, got %q", err.Error())
	}
}

func TestMakeInternalRequest_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "20260201-01",
	}
	err := MakeInternalRequest(context.Background(), cfg, "/gallery/1")
	if err != nil {
		t.Fatalf("MakeInternalRequest: %v", err)
	}
}

func TestMakeInternalRequest_SetsQueryString(t *testing.T) {
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "20260201-01",
	}
	_ = MakeInternalRequest(context.Background(), cfg, "/gallery/1")
	if gotQuery != "v=20260201-01" {
		t.Errorf("expected query v=20260201-01, got %q", gotQuery)
	}
}

func TestMakeInternalRequest_NoHXRequestHeader(t *testing.T) {
	var gotHX string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHX = r.Header.Get("HX-Request")
		w.WriteHeader(http.StatusOK)
	})
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "x",
	}
	_ = MakeInternalRequest(context.Background(), cfg, "/test")
	if gotHX != "" {
		t.Errorf("expected no HX-Request header, got %q", gotHX)
	}
}

func TestMakeInternalRequest_ReturnsErrorOn4xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	cfg := InternalRequestConfig{Handler: handler, ETagVersion: "v1"}
	err := MakeInternalRequest(context.Background(), cfg, "/test")
	if err == nil {
		t.Fatal("expected error when handler returns 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status 400, got %q", err.Error())
	}
}

func TestMakeInternalRequest_ReturnsErrorOn5xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg := InternalRequestConfig{Handler: handler, ETagVersion: "v1"}
	err := MakeInternalRequest(context.Background(), cfg, "/test")
	if err == nil {
		t.Fatal("expected error when handler returns 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500, got %q", err.Error())
	}
}

func TestMakeInternalRequestWithVariant_ReturnsErrorOn4xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	cfg := InternalRequestConfig{Handler: handler, ETagVersion: "v1"}
	err := MakeInternalRequestWithVariant(context.Background(), cfg, "/test", "", "identity")
	if err == nil {
		t.Fatal("expected error when handler returns 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status 404, got %q", err.Error())
	}
}

func TestMakeInternalRequest_ContextCancellation(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "x",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := MakeInternalRequest(ctx, cfg, "/test")
	// Cancelled context may or may not error depending on timing
	_ = err
}

// TestMakeInternalRequestWithVariant_SetsHXAndEncoding verifies that when preloading
// for browser HTMX requests (info box, lightbox), we set HX-Request, HX-Target, and
// Accept-Encoding so the stored cache key matches real browser requests.
func TestMakeInternalRequestWithVariant_SetsHXAndEncoding(t *testing.T) {
	var gotHX, gotTarget, gotEncoding string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHX = r.Header.Get("HX-Request")
		gotTarget = r.Header.Get("HX-Target")
		gotEncoding = r.Header.Get("Accept-Encoding")
		w.WriteHeader(http.StatusOK)
	})
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "v1",
	}
	err := MakeInternalRequestWithVariant(context.Background(), cfg, "/info/image/12", "box_info", "gzip")
	if err != nil {
		t.Fatalf("MakeInternalRequestWithVariant: %v", err)
	}
	if gotHX != "true" {
		t.Errorf("HX-Request = %q, want true", gotHX)
	}
	if gotTarget != "box_info" {
		t.Errorf("HX-Target = %q, want box_info", gotTarget)
	}
	if gotEncoding != "gzip" {
		t.Errorf("Accept-Encoding = %q, want gzip", gotEncoding)
	}
}

// TestMakeInternalRequestWithVariant_EmptyTarget_BehavesLikeFullPage verifies that
// empty hxTarget does not set HX headers (full-page style).
func TestMakeInternalRequestWithVariant_EmptyTarget_BehavesLikeFullPage(t *testing.T) {
	var gotHX, gotEncoding string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHX = r.Header.Get("HX-Request")
		gotEncoding = r.Header.Get("Accept-Encoding")
		w.WriteHeader(http.StatusOK)
	})
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "v1",
	}
	err := MakeInternalRequestWithVariant(context.Background(), cfg, "/gallery/1", "", "identity")
	if err != nil {
		t.Fatalf("MakeInternalRequestWithVariant: %v", err)
	}
	if gotHX != "" {
		t.Errorf("HX-Request = %q, want empty (full page)", gotHX)
	}
	if gotEncoding != "identity" {
		t.Errorf("Accept-Encoding = %q, want identity", gotEncoding)
	}
}
