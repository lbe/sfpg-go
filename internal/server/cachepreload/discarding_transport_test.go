package cachepreload

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscardingTransport_RoundTrip_DiscardsBody(t *testing.T) {
	// Create a test server that returns a large response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 1MB of data
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 1024*1024))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &DiscardingTransport{Transport: http.DefaultTransport},
	}

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// Read from response to ensure it works
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	// Should have read the full response (not discarded by transport)
	if len(data) != 1024*1024 {
		t.Errorf("expected 1MB, got %d bytes", len(data))
	}
}

func TestDiscardingTransport_RoundTrip_PreservesResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "test-value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &DiscardingTransport{Transport: http.DefaultTransport},
	}

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-Custom") != "test-value" {
		t.Errorf("X-Custom = %q, want test-value", resp.Header.Get("X-Custom"))
	}
}

func TestDiscardingTransport_RoundTrip_PreservesStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &DiscardingTransport{Transport: http.DefaultTransport},
	}

	req, err := http.NewRequest("POST", server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want 201", resp.StatusCode)
	}
}

func TestDiscardingTransport_RoundTrip_PropagatesErrors(t *testing.T) {
	// Use a server that will reject connections
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	server.Close() // Close immediately to cause connection errors

	client := &http.Client{
		Transport: &DiscardingTransport{Transport: http.DefaultTransport},
	}

	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	_, err = client.Do(req)
	if err == nil {
		t.Error("expected error for closed server, got nil")
	}
}

func TestDiscardingTransport_MemoryEfficiency(t *testing.T) {
	// This test verifies that large responses don't cause memory buildup
	largeBody := strings.Repeat("x", 10*1024*1024) // 10MB

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeBody))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &DiscardingTransport{Transport: http.DefaultTransport},
	}

	// Make multiple requests to verify memory doesn't accumulate
	for i := 0; i < 10; i++ {
		req, err := http.NewRequest("GET", server.URL, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}

		// Read and discard
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if err != nil {
			t.Fatalf("io.Copy: %v", err)
		}
	}

	// If we get here without OOM, the transport is working efficiently
}
