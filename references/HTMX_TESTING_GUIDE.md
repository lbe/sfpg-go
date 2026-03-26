# HTMX Response Testing Guide

## Problem

HTMX responses must have specific structural requirements that aren't always obvious:

- When using `hx-swap="outerHTML"`, the response body must contain exactly **one root element**
- OOB swaps (`hx-swap-oob`) must have valid `id` attributes
- Multiple root elements cause HTMX swap errors like `TypeError: Cannot read properties of null (reading 'insertBefore')`

These issues often only manifest in the browser console, not in unit tests that just parse HTML content.

## Solution

Use the `validateHTMXResponseStructure()` helper function in tests to catch structural issues before they reach the browser.

## Usage

Add validation to any test that checks HTMX responses:

```go
// After getting the response but before parsing HTML
if err := validateHTMXResponseStructure(w.Body.String(), "outerHTML", "config-error-message"); err != nil {
    t.Errorf("HTMX response structure validation failed: %v", err)
}
```

### Parameters

- `body`: The response body string
- `swapType`: The HTMX swap type (e.g., `"outerHTML"`, `"innerHTML"`, `"beforeend"`)
- `targetID`: Optional target element ID (for validating OOB swaps)

### What It Validates

1. **Single Root Element for `outerHTML`**: Ensures the response has exactly one root element that HTMX can swap
2. **OOB Swap IDs**: Ensures all elements with `hx-swap-oob` have valid `id` attributes

## When to Use

**Always validate HTMX responses in tests when:**

- The handler returns HTML for HTMX swaps
- Using `hx-swap="outerHTML"` (most common case)
- Using OOB swaps (`hx-swap-oob`)
- The response structure is complex or generated dynamically

**Example test pattern:**

```go
func TestMyHTMXHandler(t *testing.T) {
    // ... setup ...

    app.myHandler(w, req)

    if w.Code != http.StatusOK {
        t.Fatalf("Expected 200, got %d", w.Code)
    }

    // Validate HTMX structure BEFORE parsing content
    if err := validateHTMXResponseStructure(w.Body.String(), "outerHTML", "my-target-id"); err != nil {
        t.Errorf("HTMX response structure validation failed: %v", err)
    }

    // Then parse and check content
    doc, err := html.Parse(strings.NewReader(w.Body.String()))
    // ... rest of test ...
}
```

## Common Issues Caught

1. **Multiple root elements**: Response has `<div>...</div><div>...</div>` instead of a single wrapper
2. **Missing OOB IDs**: OOB swap elements without `id` attributes
3. **Invalid HTML structure**: Malformed HTML that causes HTMX parsing errors

## Integration with Existing Tests

The validation function is in `htmx_validation_test.go` and can be used across all test files in the `server` package.

## Future Enhancements

Consider adding:

- Validation for `hx-swap="innerHTML"` (can have multiple roots)
- Validation for `hx-swap="beforeend"` / `"afterend"` (can have multiple roots)
- Validation that OOB target IDs actually exist in the target page
- Browser-based integration tests using Playwright/Selenium to catch runtime HTMX errors
