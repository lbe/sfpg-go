# Middleware Test Requirements for Package Extraction

This document outlines the test requirements for each middleware when extracted to `internal/server/middleware/` package.

## Compression Middleware (`compress.go`)

### Unit Tests Required:

1. **Encoding Negotiation**:
   - `TestNegotiateEncoding_PreferBrotli` - Brotli preferred over gzip
   - `TestNegotiateEncoding_PreferGzip` - Gzip when brotli unavailable
   - `TestNegotiateEncoding_Identity` - Fallback to identity
   - `TestNegotiateEncoding_Wildcard` - Wildcard accept-encoding
   - `TestNegotiateEncoding_IgnoresUnknown` - Unknown encodings ignored

2. **Content Type Compression Checks**:
   - `TestShouldCompressContentType_TextHTML` - text/html compressible
   - `TestShouldCompressContentType_TextJSON` - application/json compressible
   - `TestShouldCompressContentType_ImageJPEG` - image/jpeg not compressible
   - `TestShouldCompressContentType_ImagePNG` - image/png not compressible

3. **Path Compression Checks**:
   - `TestShouldCompressPath_JPEGFile` - .jpg files not compressible
   - `TestShouldCompressPath_PNGFile` - .png files not compressible
   - `TestShouldCompressPath_HTMLFile` - .html files compressible
   - `TestShouldCompressPath_GZFile` - .gz files not compressible

4. **Middleware Behavior**:
   - `TestCompressMiddleware_GzipResponse` - Gzip compression works
   - `TestCompressMiddleware_BrotliResponse` - Brotli compression works
   - `TestCompressMiddleware_SkipsCompression_NoAcceptEncoding` - No compression without Accept-Encoding
   - `TestCompressMiddleware_SmallResponse_Compresses` - Small responses behavior
   - `TestCompressMiddleware_WildcardNegotiation_BR` - Wildcard negotiates brotli
   - `TestCompressMiddleware_HeadNoBody` - HEAD requests handled correctly
   - `TestCompressMiddleware_SkipsCompression_Image` - Images not compressed
   - `TestCompressMiddleware_PreservesBody_Gzip` - Decompressed body matches original
   - `TestCompressMiddleware_SetVaryHeader` - Vary header always set
   - `TestCompressMiddleware_SmallResponse` - Small response handling
   - `TestCompressMiddleware_SkipsCompression_PreexistingContentEncoding` - Pre-existing Content-Encoding preserved
   - `TestCompressMiddleware_HeadersSetBeforeBody` - Headers set before body

### Dependencies:

- No external dependencies (pure HTTP middleware)
- Accepts `http.Handler` as input
- Returns `http.Handler`

### Constructor Pattern:

- `CompressMiddleware(next http.Handler) http.Handler` - Simple function, no constructor needed

---

## Conditional Middleware (`conditional.go`)

### Unit Tests Required:

1. **ETag Matching**:
   - `TestMatchesETag_ExactMatch` - Exact ETag match
   - `TestMatchesETag_NoMatch` - ETag mismatch
   - `TestMatchesETag_Wildcard` - Wildcard match
   - `TestMatchesETag_MultipleValues` - Multiple ETag values
   - `TestMatchesETag_WeakMatch` - Weak ETag matching

2. **Last-Modified Matching**:
   - `TestMatchesLastModified_Before` - Modified before check date
   - `TestMatchesLastModified_After` - Modified after check date
   - `TestMatchesLastModified_Exact` - Exact modification time match

3. **Middleware Behavior**:
   - `TestConditionalMiddleware_ETag_304Response` - Handler can return 304
   - `TestConditionalMiddleware_ETag_200Response` - 200 on ETag mismatch
   - `TestConditionalMiddleware_LastModified_304Response` - 304 on Last-Modified match
   - `TestConditionalMiddleware_LastModified_304Response_Middleware` - Middleware-triggered 304
   - `TestConditionalMiddleware_NoCacheHeaders` - Pass-through without validators
   - `TestConditionalMiddleware_PreserveHeaders` - 304 preserves cache headers
   - `TestConditionalMiddleware_HEAD_SkipsBody` - HEAD requests don't send body
   - `TestConditionalMiddleware_SkipsNonGetHead` - POST/PUT bypass 304 checks
   - `TestConditionalMiddleware_EntityHeadersOmittedOn304` - Content-Type/Length stripped on 304
   - `TestConditionalMiddleware_Auto304_ETag` - Middleware returns 304 based on ETag
   - `TestConditionalMiddleware_Auto304_LastModified` - Middleware returns 304 based on Last-Modified

### Integration Tests Required:

- `TestMiddlewareApplication_SelectiveConditional` - Verifies conditional middleware applied selectively to routes

### Dependencies:

- No external dependencies (pure HTTP middleware)
- Accepts `http.Handler` as input
- Returns `http.Handler`

### Constructor Pattern:

- `ConditionalMiddleware(next http.Handler) http.Handler` - Simple function, no constructor needed

---

## Logging Middleware (`logging.go`)

### Unit Tests Required:

1. **Basic Logging**:
   - `TestLoggingMiddleware_LogsEveryRequestAndResponse` - Logs request and response with status code

2. **Security**:
   - `TestLoggingMiddleware_SanitizesSensitiveHeaders` - Cookie and Authorization headers redacted

### Dependencies:

- Requires logger (currently uses `slog` package-level logger)
- Should accept logger as dependency via constructor

### Constructor Pattern:

- `NewLoggingMiddleware(logger *log.Logger) func(http.Handler) http.Handler` - Constructor that returns middleware function
- OR: `LoggingMiddleware(logger *log.Logger, next http.Handler) http.Handler` - Direct middleware function

### Integration Considerations:

- Tests should verify logs are written to logger
- Tests should verify sensitive headers are redacted
- Tests should verify request ID, method, URL, status, bytes, duration are logged

---

## Test File Organization

### Unit Tests:

- `middleware/middleware_test.go` - All unit tests for compress, conditional, logging

### Integration Tests:

- `middleware/middleware_integration_test.go` - Integration tests that verify middleware chain behavior

### Test Migration Strategy:

1. Move unit tests to `middleware/middleware_test.go`
2. Move integration tests to `middleware/middleware_integration_test.go`
3. Update package declarations from `package server` to `package middleware`
4. Update imports to use `middleware` package
5. Ensure all tests pass in isolated package

---

## Notes

- All middleware should be pure functions or constructor functions
- No direct App struct dependencies
- Dependencies injected via constructor parameters
- Tests should be isolated and not depend on App struct
- Integration tests may need App struct for full middleware chain verification
