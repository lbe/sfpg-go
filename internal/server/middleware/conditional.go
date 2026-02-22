package middleware

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/server/conditional"
)

// matchesETag checks if the provided ETag matches the If-None-Match value
// Supports exact match, weak match, and wildcard
// Delegates to conditional.MatchesETag
func matchesETag(etag string, ifNoneMatch string) bool {
	return conditional.MatchesETag(ifNoneMatch, etag)
}

// matchesLastModified checks if the resource was modified before the given time
// Returns true if Last-Modified <= If-Modified-Since (not modified)
// Delegates to conditional.MatchesLastModified
func matchesLastModified(lastModified time.Time, ifModifiedSince time.Time) bool {
	// Truncate to seconds for comparison (HTTP times don't include nanoseconds)
	return lastModified.Truncate(time.Second).Before(ifModifiedSince.Add(time.Second))
}

// ConditionalMiddleware handles If-None-Match and If-Modified-Since requests
// Returns 304 Not Modified if validators match, otherwise calls the next handler
// This middleware must be placed AFTER handlers that set validators (ETag, Last-Modified)
// For now, we'll let handlers execute and then check validators in a wrapper
func ConditionalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}

		wrapped := newConditionalResponseWriter(w)
		next.ServeHTTP(wrapped, r)
		wrapped.Finalize(r)
	})
}

// conditionalResponseWriter wraps http.ResponseWriter to check validators before sending response
type conditionalResponseWriter struct {
	http.ResponseWriter
	header      http.Header
	body        bytes.Buffer
	statusCode  int
	wroteHeader bool
}

// newConditionalResponseWriter creates a wrapped response writer
func newConditionalResponseWriter(w http.ResponseWriter) *conditionalResponseWriter {
	return &conditionalResponseWriter{
		ResponseWriter: w,
		header:         make(http.Header),
		statusCode:     http.StatusOK,
	}
}

// Header returns the header map
func (crw *conditionalResponseWriter) Header() http.Header {
	return crw.header
}

// WriteHeader writes the status code, checking validators first
func (crw *conditionalResponseWriter) WriteHeader(statusCode int) {
	if crw.wroteHeader {
		return
	}
	crw.wroteHeader = true
	crw.statusCode = statusCode
}

// Write writes data to the response
func (crw *conditionalResponseWriter) Write(p []byte) (int, error) {
	if !crw.wroteHeader {
		crw.WriteHeader(http.StatusOK)
	}
	return crw.body.Write(p)
}

// Finalize checks validators after handler completes but BEFORE response is sent
func (crw *conditionalResponseWriter) Finalize(r *http.Request) {
	// Default status when handler never wrote
	if !crw.wroteHeader {
		crw.statusCode = http.StatusOK
	}

	// Only attempt conditional handling on 200 responses
	if crw.statusCode == http.StatusOK {
		etag := crw.header.Get("ETag")
		lastModifiedHeader := crw.header.Get("Last-Modified")

		// ETag has priority per RFC 7232
		if inm := r.Header.Get("If-None-Match"); inm != "" && etag != "" && matchesETag(etag, inm) {
			crw.writeNotModified()
			return
		}

		// Fallback to Last-Modified when no ETag match
		if ims := r.Header.Get("If-Modified-Since"); ims != "" && lastModifiedHeader != "" {
			if lastMod, err := time.Parse(time.RFC1123, lastModifiedHeader); err == nil {
				if imsTime, err := time.Parse(time.RFC1123, ims); err == nil && matchesLastModified(lastMod, imsTime) {
					crw.writeNotModified()
					return
				}
			}
		}
	}

	crw.writeNormal(r)
}

func (crw *conditionalResponseWriter) writeNotModified() {
	dst := crw.ResponseWriter.Header()
	for k, vals := range crw.header {
		switch strings.ToLower(k) {
		case "content-length", "content-type", "content-encoding", "content-range":
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
	crw.ResponseWriter.WriteHeader(http.StatusNotModified)
}

func (crw *conditionalResponseWriter) writeNormal(r *http.Request) {
	dst := crw.ResponseWriter.Header()
	for k, vals := range crw.header {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
	crw.ResponseWriter.WriteHeader(crw.statusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = crw.ResponseWriter.Write(crw.body.Bytes())
}
