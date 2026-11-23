package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectNative(t *testing.T) {
	tests := []struct {
		name        string
		output      string // what the exiftool script prints; empty means no exiftool
		wantVersion string
		wantOK      bool
	}{
		{name: "recent", output: "13.55", wantVersion: "13.55", wantOK: true},
		{name: "old", output: "12.76", wantVersion: "12.76"},
		{name: "not a number", output: "junk", wantVersion: "junk"},
		{name: "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var wantPath string
			if tt.output != "" {
				wantPath = filepath.Join(dir, "exiftool")
				script := "#!/bin/sh\necho " + tt.output + "\n"
				if err := os.WriteFile(wantPath, []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)

			path, version, ok := detectNative()
			if path != wantPath || version != tt.wantVersion || ok != tt.wantOK {
				t.Errorf("detectNative() = %q, %q, %v; want %q, %q, %v",
					path, version, ok, wantPath, tt.wantVersion, tt.wantOK)
			}
		})
	}
}
