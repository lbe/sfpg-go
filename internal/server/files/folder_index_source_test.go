package files

import (
	"os"
	"strings"
	"testing"
)

// TestRebuildFileFolderIndex_WriteFolderNotDuringStream guards against the
// regression where writeFolder runs inside the streaming for rows.Next() loop
// (pipelining Submit while the RO cursor is still open). After the fix, the
// loop only materializes (file_id, folder_id) pairs; writeFolder runs after
// Close. A strings.Contains of both writeFolder and rows.Next() over the whole
// function would stay red forever (both still appear in RebuildFileFolderIndex),
// so this test brace-matches only the for rows.Next() body.
func TestRebuildFileFolderIndex_WriteFolderNotDuringStream(t *testing.T) {
	src, err := os.ReadFile("folder_index.go")
	if err != nil {
		t.Fatalf("read folder_index.go: %v", err)
	}
	text := string(src)

	body, ok := forRowsNextBody(t, text)
	if !ok {
		t.Fatal("could not locate for rows.Next() loop inside RebuildFileFolderIndex")
	}
	if strings.Contains(body, "writeFolder") {
		t.Fatalf("writeFolder must not run inside the for rows.Next() loop; found in body:\n%s", body)
	}
}

// forRowsNextBody returns the body of the first for rows.Next() loop in
// RebuildFileFolderIndex, brace-matched only to the loop's closing brace (nested
// blocks are counted, not cut early). It does not brace-match the whole function.
func forRowsNextBody(t *testing.T, text string) (string, bool) {
	t.Helper()

	funcIdx := strings.Index(text, "func RebuildFileFolderIndex(")
	if funcIdx < 0 {
		return "", false
	}
	loopIdx := strings.Index(text[funcIdx:], "for rows.Next()")
	if loopIdx < 0 {
		return "", false
	}
	loopIdx += funcIdx

	open := strings.Index(text[loopIdx:], "{")
	if open < 0 {
		return "", false
	}
	open += loopIdx

	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open+1 : i], true
			}
		}
	}
	return "", false
}
