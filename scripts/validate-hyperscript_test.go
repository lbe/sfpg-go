// validate-hyperscript_test.go - Tests for the hyperscript validator
package main

import (
	"os"
	"strings"
	"testing"
)

// TestExtractHyperscript_SingleLineAttribute tests single-line _="..." attributes
func TestExtractHyperscript_SingleLineAttribute(t *testing.T) {
	content := `<div _="on click add .active to me"></div>`
	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	if sources[0].SourceType != "attribute" {
		t.Errorf("expected source type 'attribute', got %s", sources[0].SourceType)
	}

	if !strings.Contains(sources[0].Code, "on click add .active to me") {
		t.Errorf("unexpected code: %s", sources[0].Code)
	}
}

// TestExtractHyperscript_SingleLineAttributeSingleQuotes tests single-line _='...' attributes
func TestExtractHyperscript_SingleLineAttributeSingleQuotes(t *testing.T) {
	content := `<div _='on click remove .hidden from #modal'></div>`
	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	if !strings.Contains(sources[0].Code, "on click remove .hidden from #modal") {
		t.Errorf("unexpected code: %s", sources[0].Code)
	}
}

// TestExtractHyperscript_MultiLineAttribute tests multi-line _="..." attributes (CURRENTLY FAILING)
func TestExtractHyperscript_MultiLineAttribute(t *testing.T) {
	content := `<div _="on htmx:afterRequest
  if event.detail.successful
    set timeString to js return new Date().toLocaleTimeString() end
    put timeString into #last-updated
  end"></div>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source for multi-line attribute, got %d", len(sources))
	}

	expectedParts := []string{
		"on htmx:afterRequest",
		"if event.detail.successful",
		"set timeString",
		"put timeString into #last-updated",
		"end",
	}

	for _, part := range expectedParts {
		if !strings.Contains(sources[0].Code, part) {
			t.Errorf("multi-line code missing expected part %q: got %s", part, sources[0].Code)
		}
	}
}

// TestExtractHyperscript_MultiLineAttributeWithNewlines tests proper newline preservation
func TestExtractHyperscript_MultiLineAttributeWithNewlines(t *testing.T) {
	content := `<body _="on keydown(key) from window
  if event.target.tagName is in ['INPUT', 'TEXTAREA']
    exit
  end
  log 'key pressed'"></body>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	// Verify newlines are preserved in the extracted code
	if !strings.Contains(sources[0].Code, "\n") {
		t.Error("multi-line attribute should preserve newlines in extracted code")
	}
}

// TestExtractHyperscript_ScriptBlock tests <script type="text/hyperscript"> blocks
func TestExtractHyperscript_ScriptBlock(t *testing.T) {
	content := `<script type="text/hyperscript">
def greet(name)
  return "Hello, " + name
end
</script>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	if sources[0].SourceType != "script_block" {
		t.Errorf("expected source type 'script_block', got %s", sources[0].SourceType)
	}

	if !strings.Contains(sources[0].Code, "def greet(name)") {
		t.Errorf("unexpected code: %s", sources[0].Code)
	}
}

// TestExtractHyperscript_MultiLineScriptBlock tests multi-line script blocks
func TestExtractHyperscript_MultiLineScriptBlock(t *testing.T) {
	content := `<script type="text/hyperscript">
def buildConfigDiff(fieldList, targetElement, includeThemes)
  set changesHTML to '<table>'
  for field in fieldList
    set changesHTML to changesHTML + field
  end
  put changesHTML into targetElement
end
</script>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	expectedParts := []string{
		"def buildConfigDiff",
		"set changesHTML",
		"for field in fieldList",
		"end",
		"put changesHTML into targetElement",
	}

	for _, part := range expectedParts {
		if !strings.Contains(sources[0].Code, part) {
			t.Errorf("multi-line script block missing expected part %q", part)
		}
	}
}

// TestExtractHyperscript_SingleLineScriptBlock tests script blocks on one line
func TestExtractHyperscript_SingleLineScriptBlock(t *testing.T) {
	content := `<script type="text/hyperscript">def greet(name) return "Hello" end</script>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}

	if !strings.Contains(sources[0].Code, "def greet(name)") {
		t.Errorf("unexpected code: %s", sources[0].Code)
	}
}

// TestExtractHyperscript_MultipleAttributes tests multiple _="..." attributes in one file
func TestExtractHyperscript_MultipleAttributes(t *testing.T) {
	content := `<div>
  <button _="on click add .active to me">Button 1</button>
  <button _="on mouseenter add .hover to me">Button 2</button>
  <button _="on mouseleave remove .hover from me">Button 3</button>
</div>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(sources))
	}
}

// TestExtractHyperscript_MixedContent tests mix of attributes and script blocks
func TestExtractHyperscript_MixedContent(t *testing.T) {
	content := `<div>
  <button _="on click log 'clicked'">Click</button>
  <script type="text/hyperscript">
    def helper()
      return "help"
    end
  </script>
  <span _="on load log 'loaded'">Text</span>
</div>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 3 {
		t.Fatalf("expected 3 sources (2 attributes + 1 script block), got %d", len(sources))
	}

	// Check types
	attrCount := 0
	scriptCount := 0
	for _, s := range sources {
		switch s.SourceType {
		case "attribute":
			attrCount++
		case "script_block":
			scriptCount++
		}
	}

	if attrCount != 2 {
		t.Errorf("expected 2 attributes, got %d", attrCount)
	}
	if scriptCount != 1 {
		t.Errorf("expected 1 script block, got %d", scriptCount)
	}
}

// TestExtractHyperscript_NoHyperscript tests files without hyperscript
func TestExtractHyperscript_NoHyperscript(t *testing.T) {
	content := `<div class="test">
  <p>Hello world</p>
  <button onclick="alert('test')">Click</button>
</div>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 0 {
		t.Errorf("expected 0 sources for file without hyperscript, got %d", len(sources))
	}
}

// TestExtractHyperscript_EmptyAttribute tests empty _="" attributes
func TestExtractHyperscript_EmptyAttribute(t *testing.T) {
	content := `<div _=""></div>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 0 {
		t.Errorf("expected 0 sources for empty attribute, got %d", len(sources))
	}
}

// TestExtractHyperscript_WhitespaceOnlyAttribute tests whitespace-only _="   " attributes
func TestExtractHyperscript_WhitespaceOnlyAttribute(t *testing.T) {
	content := `<div _="   "></div>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 0 {
		t.Errorf("expected 0 sources for whitespace-only attribute, got %d", len(sources))
	}
}

// TestExtractHyperscript_HTMLCommentsInScript tests script blocks with HTML comments
func TestExtractHyperscript_ScriptBlockWithComments(t *testing.T) {
	content := `<script type="text/hyperscript">
  -- This is a comment
  def test()
    log "hello"
  end
</script>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
}

// TestExtractHyperscript_ComplexMultiLineAttribute tests the keyboard handler pattern
func TestExtractHyperscript_ComplexMultiLineAttribute(t *testing.T) {
	content := `<body _="on keydown(key) from window
  -- Skip handler if typing in input/textarea fields
  if event.target.tagName is in ['INPUT', 'TEXTAREA']
    exit
  end
  
  -- 1. LOGIN/LOGOUT MODALS - only handle Escape
  if #login_modal.checked or #logout_modal.checked
    if key is 'Escape'
      if #login_modal.checked set #login_modal.checked to false end
      if #logout_modal.checked set #logout_modal.checked to false end
      halt the event
    end
    exit
  end">
</body>`

	sources, err := extractHyperscriptFromString("test.html", content)
	if err != nil {
		t.Fatalf("extractHyperscript failed: %v", err)
	}

	if len(sources) != 1 {
		t.Fatalf("expected 1 source for complex multi-line attribute, got %d", len(sources))
	}

	// Verify the code contains all expected parts
	code := sources[0].Code
	expectedParts := []string{
		"on keydown(key) from window",
		"-- Skip handler if typing",
		"if event.target.tagName is in",
		"exit",
		"-- 1. LOGIN/LOGOUT MODALS",
		"if #login_modal.checked",
		"if key is 'Escape'",
		"halt the event",
	}

	for _, part := range expectedParts {
		if !strings.Contains(code, part) {
			t.Errorf("complex multi-line code missing expected part %q", part)
		}
	}
}

// TestDecodeHTMLEntities tests HTML entity decoding
func TestDecodeHTMLEntities(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`&quot;`, `"`},
		{`&amp;`, `&`},
		{`&lt;`, `<`},
		{`&gt;`, `>`},
		{`&apos;`, `'`},
		{`&quot;hello&quot;`, `"hello"`},
		{`no entities`, `no entities`},
	}

	for _, tt := range tests {
		result := decodeHTMLEntities(tt.input)
		if result != tt.expected {
			t.Errorf("decodeHTMLEntities(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestHasMatchingExtension tests file extension matching
func TestHasMatchingExtension(t *testing.T) {
	extensions := map[string]bool{
		".html":   true,
		".tmpl":   true,
		".gohtml": true,
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{"test.html", true},
		{"test.tmpl", true},
		{"test.gohtml", true},
		{"test.html.tmpl", true},
		{"test.txt", false},
		{"test", false},
		{"path/to/file.html", true},
		{"path/to/file.html.tmpl", true},
	}

	for _, tt := range tests {
		result := hasMatchingExtension(tt.path, extensions)
		if result != tt.expected {
			t.Errorf("hasMatchingExtension(%q) = %v, want %v", tt.path, result, tt.expected)
		}
	}
}

// TestParseExtensions tests extension parsing from flag
func TestParseExtensions(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]bool
	}{
		{".html,.tmpl", map[string]bool{".html": true, ".tmpl": true}},
		{"html,tmpl", map[string]bool{".html": true, ".tmpl": true}},
		{" .html , .tmpl ", map[string]bool{".html": true, ".tmpl": true}},
	}

	for _, tt := range tests {
		result := parseExtensions(tt.input)
		for k, v := range tt.expected {
			if result[k] != v {
				t.Errorf("parseExtensions(%q)[%q] = %v, want %v", tt.input, k, result[k], v)
			}
		}
	}
}

// Helper function to test extraction from string without file I/O
func extractHyperscriptFromString(filename, content string) ([]HyperscriptSource, error) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "hyperscript-test-*.html")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		return nil, err
	}
	tmpFile.Close()

	return extractHyperscript(tmpFile.Name())
}
