package gallerydb

import (
	"os"
	"strings"
	"testing"
)

// TestCountFilesForFolderIndexRebuildSQL_IsParts2 verifies the COUNT SQL is
// embedded as parts[2] and the SELECT/INSERT indices are unchanged.
func TestCountFilesForFolderIndexRebuildSQL_IsParts2(t *testing.T) {
	content, err := os.ReadFile("../../sqlc/queries/embed.go")
	if err != nil {
		t.Fatalf("read embed.go: %v", err)
	}
	text := string(content)

	if !strings.Contains(text, "CountFilesForFolderIndexRebuildSQL") {
		t.Fatal("CountFilesForFolderIndexRebuildSQL function missing in embed.go")
	}
	if !strings.Contains(text, "parts[2]") {
		t.Fatal("CountFilesForFolderIndexRebuildSQL must use parts[2]")
	}

	// SELECT/INSERT indices must be unchanged.
	if !strings.Contains(text, "strings.Split(fileFolderIndexRebuildSQL, \"-- statement-break\")[0]") {
		t.Error("QueryFilesForFolderIndexRebuildSQL must remain parts[0]")
	}
	if !strings.Contains(text, "parts[1]") {
		t.Error("InsertFileFolderIndexNewSQL must remain parts[1]")
	}
}
