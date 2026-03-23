package cachepreload

import (
	"io"
	"net/http"
)

// DiscardingTransport wraps an http.RoundTripper and replaces response bodies
// with discarding readers to avoid buffering large response bodies in memory.
// This is useful for cache warming where we need to trigger the handler/middleware
// chain but don't need to keep the response body.
type DiscardingTransport struct {
	Transport http.RoundTripper
}

// RoundTrip executes the HTTP request and returns a response with a discarding body.
func (t *DiscardingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Transport == nil {
		t.Transport = http.DefaultTransport
	}

	resp, err := t.Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Replace response body with discarding reader
	resp.Body = &discardingReader{src: resp.Body}
	return resp, nil
}

// discardingReader wraps an io.ReadCloser and discards the data as it's read.
// This prevents large response bodies from being held in memory.
type discardingReader struct {
	src io.ReadCloser
}

// Read reads from the source and returns the data, but the data is immediately
// discarded (not retained) to avoid memory buildup.
func (r *discardingReader) Read(p []byte) (int, error) {
	return r.src.Read(p)
}

// Close closes the underlying reader.
func (r *discardingReader) Close() error {
	return r.src.Close()
}
