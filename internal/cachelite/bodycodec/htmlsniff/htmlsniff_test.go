package htmlsniff

import "testing"

func TestLooksLikeHTML(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  bool
	}{
		{"empty", []byte(""), false},
		{"whitespace then html", []byte("   \n  <html>"), true},
		{"doctype", []byte("<!DOCTYPE html>"), true},
		{"partial div", []byte("<div>partial"), true},
		{"zstd magic", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00}, false},
		{"gzip magic", []byte{0x1F, 0x8B, 0x08}, false},
		{"brace not html", []byte("{not html"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LooksLikeHTML(tt.input)
			if got != tt.want {
				t.Errorf("LooksLikeHTML(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
