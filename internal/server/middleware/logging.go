package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

var requestID atomic.Int64

// loggingResponseWriter wraps http.ResponseWriter to capture status code and bytes written for logging
type loggingResponseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int
	wroteHeader  bool
}

func (rw *loggingResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *loggingResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// sanitizeHeaders removes sensitive headers from a header map to prevent logging credentials or session tokens.
// It creates a copy of the headers and removes Cookie and Authorization headers.
func sanitizeHeaders(headers http.Header) http.Header {
	sanitized := make(http.Header)
	for k, v := range headers {
		// Skip sensitive headers that could contain credentials or session tokens
		lowerKey := strings.ToLower(k)
		if lowerKey == "cookie" || lowerKey == "authorization" {
			sanitized[k] = []string{"[REDACTED]"}
			continue
		}
		sanitized[k] = v
	}
	return sanitized
}

// LoggingMiddleware creates a middleware that logs every request received and response sent.
// It uses the default slog logger. For custom logger support, use NewLoggingMiddleware.
func LoggingMiddleware(next http.Handler) http.Handler {
	return NewLoggingMiddleware(nil)(next)
}

// NewLoggingMiddleware creates a logging middleware function that accepts a logger.
// If logger is nil, it uses the default slog logger.
func NewLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := requestID.Add(1)

			log := logger
			if log == nil {
				log = slog.Default()
			}

			log.Debug("Request received",
				"ID", reqID,
				"Method", r.Method,
				"URL", r.URL.Path,
				"Query", r.URL.RawQuery,
				"RemoteAddr", r.RemoteAddr,
				"UserAgent", r.UserAgent(),
				"Headers", sanitizeHeaders(r.Header))

			rw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			log.Debug("Request completed",
				"ID", reqID,
				"Method", r.Method,
				"URL", r.URL.Path,
				"Query", r.URL.RawQuery,
				"Status", rw.status,
				"Bytes", rw.bytesWritten,
				"Duration", time.Since(start),
				"Headers", sanitizeHeaders(rw.Header()))
		})
	}
}
