# Optional Testing Enhancements for `internal/server`

This document lists optional, lower-priority testing enhancements that can be implemented to further improve test coverage for the `internal/server` package.

## Remaining Gaps (Optional / Lower Priority)

1.  **Error path tests**:
    - File seek/read errors
    - Database errors

2.  **Happy path integration tests**:
    - `processFileWithQueries` with real image files

3.  **Thumbnail generation failure scenarios**

4.  **Concurrency edge cases**:
    - Queue and DB pool closure scenarios
    - Worker pool stress tests

5.  **Handler expansion**:
    - Comprehensive table-driven tests for all HTTP response codes (301, 400, 403, 404, 500)
    - Authentication and authorization failure scenarios

6.  **UI template expansion**:
    - Full rendering tests for all templates
    - Cross-Site Scripting (XSS) prevention tests (ensure HTML in user input is escaped)
    - Template function tests

7.  **Configuration validation edge cases**
