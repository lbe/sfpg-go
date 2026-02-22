package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"

	"github.com/andybalholm/brotli"

	"github.com/lbe/sfpg-go/internal/server/compress"
)

const (
	// MinCompressSize is the minimum response size for compression (in bytes)
	MinCompressSize = 512
)

// negotiateEncoding determines the best encoding from Accept-Encoding header
// Preference: brotli > gzip > identity (no encoding)
// Delegates to compress.NegotiateEncoding
func negotiateEncoding(acceptEncoding string) string {
	return compress.NegotiateEncoding(acceptEncoding)
}

// shouldCompressContentType checks if content type should be compressed
// Delegates to compress.ShouldCompressContentType
func shouldCompressContentType(contentType string) bool {
	return compress.ShouldCompressContentType(contentType)
}

// shouldCompressPath checks if path extension should be compressed
// Returns true if path should be compressed, false if it should not
// Delegates to compress.ShouldCompressPath
func shouldCompressPath(path string) bool {
	return compress.ShouldCompressPath(path)
}

// compressWriter wraps http.ResponseWriter and compresses content
type compressWriter struct {
	http.ResponseWriter
	buf            *bytes.Buffer
	compression    string
	contentType    string
	statusCode     int
	shouldCompress bool
	headerWritten  bool
	headerBuffer   http.Header
}

// WriteHeader captures status code and content type; defers actual write until Flush
func (cw *compressWriter) WriteHeader(statusCode int) {
	if cw.headerWritten {
		return
	}
	cw.statusCode = statusCode
	cw.contentType = cw.headerBuffer.Get("Content-Type")

	// Determine if we should compress based on content type and path
	cw.shouldCompress = cw.compression != "identity" &&
		statusCode == 200 &&
		shouldCompressContentType(cw.contentType)

	// Expose compression decision for debugging/logging
	cw.headerBuffer.Set("X-Should-Compress", strconv.FormatBool(cw.shouldCompress))

	// Don't call underlying WriteHeader yet; wait until Flush when we know body size
	cw.headerWritten = true
}

// Header returns the headerBuffer instead of the underlying ResponseWriter's headers
// This ensures all headers are captured for compression decision before WriteHeader is called
func (cw *compressWriter) Header() http.Header {
	return cw.headerBuffer
}

// Write buffers content and returns byte count
func (cw *compressWriter) Write(p []byte) (int, error) {
	if !cw.headerWritten {
		cw.WriteHeader(http.StatusOK)
	}

	// Always buffer data; Flush() will decide compression based on final size
	return cw.buf.Write(p)
}

// Flush writes compressed content and flushes
func (cw *compressWriter) Flush() error {
	// Set headers on the underlying ResponseWriter before calling WriteHeader
	for k, vals := range cw.headerBuffer {
		for _, v := range vals {
			cw.ResponseWriter.Header().Add(k, v)
		}
	}

	willCompress := cw.shouldCompress && cw.buf.Len() >= MinCompressSize
	if willCompress {
		// Compression will happen: set Content-Encoding before WriteHeader
		cw.ResponseWriter.Header().Set("Content-Encoding", cw.compression)
		cw.ResponseWriter.Header().Del("Content-Length")
	} else if cw.shouldCompress {
		// Below threshold: ensure no Content-Encoding
		cw.ResponseWriter.Header().Del("Content-Encoding")
	}

	// Now call WriteHeader with correct headers
	cw.ResponseWriter.WriteHeader(cw.statusCode)

	// Write body
	if willCompress {
		var err error
		switch cw.compression {
		case "br":
			err = cw.writeBrotli()
		case "gzip":
			err = cw.writeGzip()
		}
		if err != nil {
			return err
		}
	} else if cw.buf.Len() > 0 {
		// Write uncompressed
		if _, err := cw.ResponseWriter.Write(cw.buf.Bytes()); err != nil {
			return err
		}
	}

	if flusher, ok := cw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// writeGzip compresses buffer content with gzip
func (cw *compressWriter) writeGzip() error {
	gw := gzip.NewWriter(cw.ResponseWriter)
	defer gw.Close()

	_, err := io.Copy(gw, cw.buf)
	return err
}

// writeBrotli compresses buffer content with brotli
func (cw *compressWriter) writeBrotli() error {
	bw := brotli.NewWriter(cw.ResponseWriter)
	defer bw.Close()

	_, err := io.Copy(bw, cw.buf)
	return err
}

// CompressMiddleware wraps handler with compression support
func CompressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't compress if path suggests non-compressible content
		if !shouldCompressPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Don't compress if Content-Encoding is already set
		if w.Header().Get("Content-Encoding") != "" {
			next.ServeHTTP(w, r)
			return
		}

		// Always signal that response may vary based on Accept-Encoding
		w.Header().Add("Vary", "Accept-Encoding")

		acceptEncoding := r.Header.Get("Accept-Encoding")
		encoding := negotiateEncoding(acceptEncoding)

		if encoding == "identity" {
			// No compression requested
			next.ServeHTTP(w, r)
			return
		}

		// Wrap response writer with compression
		cw := &compressWriter{
			ResponseWriter: w,
			buf:            bytes.NewBuffer(make([]byte, 0, 8192)),
			compression:    encoding,
			headerBuffer:   make(http.Header),
		}

		next.ServeHTTP(cw, r)

		// Flush compressed content
		_ = cw.Flush()
	})
}
