// Package compress provides pure functions for content encoding negotiation
// and compression decision making.
package compress

import (
	"strings"
)

// NegotiateEncoding selects best encoding from Accept-Encoding header.
// Preference: brotli > gzip > identity (no encoding).
func NegotiateEncoding(acceptEncoding string) string {
	if acceptEncoding == "" {
		return "identity"
	}

	// Handle wildcard
	if strings.Contains(acceptEncoding, "*") {
		return "br"
	}

	// Parse Accept-Encoding header (ignoring quality factors for simplicity)
	encodings := strings.SplitSeq(acceptEncoding, ",")
	for enc := range encodings {
		enc = strings.TrimSpace(enc)
		// Remove quality factor if present (e.g., "gzip;q=0.8" -> "gzip")
		if idx := strings.Index(enc, ";"); idx > 0 {
			enc = enc[:idx]
		}
		enc = strings.TrimSpace(enc)

		switch enc {
		case "br":
			return "br"
		case "gzip":
			return "gzip"
		}
	}

	return "identity"
}

// ShouldCompressContentType checks if content type should be compressed.
func ShouldCompressContentType(contentType string) bool {
	// If handler didn't set Content-Type, default to compressible (most HTML/template responses).
	if contentType == "" {
		return true
	}
	// Compressible types
	compressible := []string{
		"text/",
		"application/json",
		"application/javascript",
		"application/xml",
		"application/x-www-form-urlencoded",
	}

	contentType = strings.ToLower(contentType)

	for _, ct := range compressible {
		if strings.HasPrefix(contentType, ct) {
			return true
		}
	}

	return false
}

// ShouldCompressPath checks if path extension should be compressed.
// Returns true if path should be compressed, false if it should not.
func ShouldCompressPath(path string) bool {
	// Non-compressible extensions
	noCompress := []string{
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico",
		".mp4", ".webm", ".ogg", ".mp3", ".wav",
		".zip", ".gz", ".tar", ".rar",
		".exe", ".dll", ".so",
		".woff", ".woff2", ".ttf", ".otf",
	}

	path = strings.ToLower(path)

	for _, ext := range noCompress {
		if strings.HasSuffix(path, ext) {
			return false // Should NOT compress
		}
	}

	return true // Should compress by default
}
