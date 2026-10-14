package testutil

import (
	"strings"
	"testing"
)

func TestFirstLiteralDocumentShellTag(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantTag string
		wantErr bool
	}{
		{
			name:    "htmx_fragment_no_shell",
			body:    `<div id="dashboard-container"><h1>Performance &amp; Health Dashboard</h1></div>`,
			wantTag: "",
		},
		{
			name:    "full_document_html_first",
			body:    `<!DOCTYPE html><html><head><title>x</title></head><body><p>y</p></body></html>`,
			wantTag: "html",
		},
		{
			name:    "body_end_tag_in_fragment",
			body:    `<div></div></body>`,
			wantTag: "body",
		},
		{
			name:    "head_start_in_fragment",
			body:    `<head><meta charset="utf-8"></head><div id="x"></div>`,
			wantTag: "head",
		},
		{
			name:    "leading_whitespace_fragment",
			body:    "\n  <div><h1>title</h1></div>\n",
			wantTag: "",
		},
		{
			name:    "word_html_in_text_not_a_tag",
			body:    `<div>The word html appears in text</div>`,
			wantTag: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag, err := FirstLiteralDocumentShellTag([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("FirstLiteralDocumentShellTag: %v", err)
			}
			if tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", tag, tt.wantTag)
			}
		})
	}
}

func TestFirstLiteralDocumentShellTag_ParseTreeDiffersFromWire(t *testing.T) {
	fragment := `<h1 id="dashboard-title">Performance &amp; Health Dashboard</h1>`
	tag, err := FirstLiteralDocumentShellTag([]byte(fragment))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if tag != "" {
		t.Fatalf("wire has no shell tag, got %q", tag)
	}
	doc, err := ParseHTML(strings.NewReader(fragment))
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	if FindElementByTag(doc, "html") == nil {
		t.Fatal("ParseHTML synthesizes <html> for a fragment; wire scan must not use this as partial detection")
	}
}
