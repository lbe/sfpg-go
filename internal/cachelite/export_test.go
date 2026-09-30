package cachelite

// Test-only exports for external test packages (cachelite_test, server tests).
var (
	NewHTTPCacheMiddlewareForTest = newHTTPCacheMiddlewareForTest
	HTTPCacheCountersForTest      = httpCacheCountersForTest
)
