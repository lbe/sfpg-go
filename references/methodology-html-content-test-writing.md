# Go HTTP Handler Testing with Structural HTML Assertions

## Helper Functions (testutil/html.go)

```go
package testutil

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// FindElementByID finds an element by its id attribute.
func FindElementByID(n *html.Node, id string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		return GetAttr(n, "id") == id
	})
}

// FindElementByClass finds the first element containing the given class.
func FindElementByClass(n *html.Node, class string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		classes := strings.Fields(GetAttr(n, "class"))
		for _, c := range classes {
			if c == class {
				return true
			}
		}
		return false
	})
}

// FindElementByTag finds the first element with the given tag name.
func FindElementByTag(n *html.Node, tag string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == tag
	})
}

// FindElement traverses the tree and returns the first node matching the predicate.
func FindElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := FindElement(c, match); found != nil {
			return found
		}
	}
	return nil
}

// FindAllElements returns all nodes matching the predicate.
func FindAllElements(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var results []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && match(n) {
			results = append(results, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return results
}

// GetAttr returns the value of an attribute, or empty string if not found.
func GetAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// GetTextContent returns all text content within a node.
func GetTextContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// ParseHTML parses an io.Reader into an html.Node.
func ParseHTML(r io.Reader) (*html.Node, error) {
	return html.Parse(r)
}
```

## Example Handler Test

```go
package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"yourproject/handlers"
	"yourproject/testutil"

	"golang.org/x/net/html"
)

func TestUserProfileHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/user/123", nil)
	rec := httptest.NewRecorder()

	handlers.UserProfileHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	doc, err := testutil.ParseHTML(rec.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	// Assert structure, not string content
	profileDiv := testutil.FindElementByID(doc, "user-profile")
	if profileDiv == nil {
		t.Fatal("missing #user-profile element")
	}

	nameEl := testutil.FindElementByClass(profileDiv, "user-name")
	if nameEl == nil {
		t.Fatal("missing .user-name element inside #user-profile")
	}

	// Assert element attributes
	if testutil.GetAttr(profileDiv, "data-user-id") != "123" {
		t.Error("expected data-user-id='123' on #user-profile")
	}

	// Assert text content when needed
	if got := testutil.GetTextContent(nameEl); got == "" {
		t.Error("expected .user-name to have text content")
	}

	// Assert correct number of list items
	listItems := testutil.FindAllElements(doc, func(n *html.Node) bool {
		return n.Data == "li" && testutil.GetAttr(n, "class") == "activity-item"
	})
	if len(listItems) < 1 {
		t.Error("expected at least one .activity-item")
	}
}
```

## Implementation Instructions

### Task 1: Writing New Tests

When writing HTTP handler tests:

1. Use `httptest.NewRequest` and `httptest.NewRecorder` to call the handler
2. Parse the response body with `testutil.ParseHTML(rec.Body)`
3. Assert structure using:
   - `FindElementByID(doc, "id")` — element must exist
   - `FindElementByClass(doc, "class")` — element must exist
   - `GetAttr(node, "attr")` — attribute value check
   - `GetTextContent(node)` — only when text matters
4. Never use `strings.Contains(rec.Body.String(), "...")` for HTML assertions

### Task 2: Refactoring Existing Tests

Find and replace patterns like:

```go
// BAD - brittle, tests content not structure
if !strings.Contains(rec.Body.String(), "Welcome, John") {
    t.Error("missing welcome message")
}
```

Refactor to:

```go
// GOOD - tests structure
doc, err := testutil.ParseHTML(rec.Body)
if err != nil {
    t.Fatalf("parse error: %v", err)
}
welcomeEl := testutil.FindElementByID(doc, "welcome-message")
if welcomeEl == nil {
    t.Fatal("missing #welcome-message element")
}
```

### Refactoring Checklist

1. Find all `strings.Contains(*.Body.String())` calls in `_test.go` files
2. Identify what structural element should contain that content
3. Replace with appropriate `FindElementByID`/`FindElementByClass` + optional `GetTextContent`
4. Add element IDs or classes to templates if they don't exist

### Patterns to find and refactor

**Search Commands**

```bash
# Run from project root - finds all variations
rg -t go --glob '*_test.go' -n '\.Body\.String\(\)|\.Body\.Bytes\(\)|strings\.Contains|bytes\.Contains'

# More targeted searches
rg -t go --glob '*_test.go' -n 'strings\.Contains.*Body'
rg -t go --glob '*_test.go' -n 'body\s*:=.*\.Body\.String\(\)'
rg -t go --glob '*_test.go' -n 'html\.Parse.*strings\.NewReader'
```

**Pattern Catalog**

| Pattern             | Example                                                    | Issue                             |
| ------------------- | ---------------------------------------------------------- | --------------------------------- |
| Direct Contains     | `strings.Contains(w.Body.String(), "text")`                | Brittle content check             |
| Variable assignment | `body := w.Body.String() then strings.Contains(body, ...)` | Same, just indirect               |
| Inefficient parse   | `html.Parse(strings.NewReader(w.Body.String()))`           | Correct idea, wasteful conversion |
| Bytes check         | `bytes.Contains(w.Body.Bytes(), []byte("text"))`           | Same as strings.Contains          |
| Regex match         | `regexp.MatchString("pattern", w.Body.String())`           | Usually testing content           |
| Exact equality      | `if w.Body.String() != expected`                           | Extremely brittle                 |
| Count occurrences   | `strings.Count(w.Body.String(), "<li>")`                   | Testing structure via string hack |

**Refactoring Examples**

#### Pattern 1: Variable assignment + Contains

```go
// BEFORE
body := w.Body.String()
if !strings.Contains(body, "Restore Configuration") {
    t.Error("missing restore config")
}

// AFTER
doc, err := testutil.ParseHTML(w.Body)
if err != nil {
    t.Fatalf("parse error: %v", err)
}
restoreBtn := testutil.FindElementByID(doc, "restore-config-btn")
if restoreBtn == nil {
    t.Fatal("missing #restore-config-btn element")
}
```

#### Pattern 2: Inefficient html.Parse

```go
// BEFORE
doc, err := html.Parse(strings.NewReader(w2.Body.String()))

// AFTER - Body already implements io.Reader
doc, err := html.Parse(w2.Body)
// or
doc, err := testutil.ParseHTML(w2.Body)
```

#### Pattern 3: Multiple Contains checks

```go
// BEFORE
body := rec.Body.String()
if !strings.Contains(body, "Welcome") {
    t.Error("missing welcome")
}
if !strings.Contains(body, "John Doe") {
    t.Error("missing name")
}
if !strings.Contains(body, "Admin") {
    t.Error("missing role")
}

// AFTER
doc, _ := testutil.ParseHTML(rec.Body)

header := testutil.FindElementByID(doc, "welcome-header")
if header == nil {
    t.Fatal("missing #welcome-header")
}

userName := testutil.FindElementByClass(doc, "user-name")
if userName == nil {
    t.Fatal("missing .user-name")
}
if got := testutil.GetTextContent(userName); got != "John Doe" {
    t.Errorf("user-name = %q, want %q", got, "John Doe")
}

roleBadge := testutil.FindElementByClass(doc, "role-badge")
if testutil.GetAttr(roleBadge, "data-role") != "admin" {
    t.Error("expected data-role='admin'")
}
```

#### Pattern 4: Counting elements via string hack

```go
// BEFORE
body := w.Body.String()
if strings.Count(body, "<tr>") != 5 {
    t.Error("expected 5 table rows")
}

// AFTER
doc, _ := testutil.ParseHTML(w.Body)
rows := testutil.FindAllElements(doc, func(n *html.Node) bool {
    return n.Data == "tr"
})
if len(rows) != 5 {
    t.Errorf("got %d rows, want 5", len(rows))
}
```

#### Pattern 5: Regex matching HTML

```go
// BEFORE
matched, _ := regexp.MatchString(`<a href="/user/\d+"`, w.Body.String())
if !matched {
    t.Error("missing user link")
}

// AFTER
doc, _ := testutil.ParseHTML(w.Body)
link := testutil.FindElementByClass(doc, "user-link")
if link == nil {
    t.Fatal("missing .user-link")
}
href := testutil.GetAttr(link, "href")
if !strings.HasPrefix(href, "/user/") {
    t.Errorf("unexpected href: %s", href)
}
```

### Checklist for Refactoring

1. Search using the rg commands above
2. For each match, determine:
   - What element should contain this content?
   - Does that element have an ID or class? If not, add one to the template
3. Replace string check with structural assertion
4. Run tests to verify behavior unchanged
5. Note: If `w.Body` is read multiple times, you may need to buffer it first since `html.Parse` consumes the reader
