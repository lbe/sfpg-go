package web

import "testing"

func TestWebEmbed(t *testing.T) {
	files, err := FS.ReadDir("static")
	if err != nil {
		t.Fatalf("failed to read embedded static files: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no embedded static files found")
	}
	files, err = FS.ReadDir("templates")
	if err != nil {
		t.Fatalf("failed to read embedded template files: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no embedded template files found")
	}
}
