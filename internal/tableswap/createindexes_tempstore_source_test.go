package tableswap

import (
	"os"
	"strings"
	"testing"
)

// TestCreateIndexes_TempStoreFileBeforeBeginTx is a source-order guard:
// it reads db.go as text and asserts that within CreateIndexes:
//   - BeginTx appears after a temp_store=FILE (or temp_store=1) ExecContext
//   - the previous temp_store is restored inside a defer using strconv.Itoa
func TestCreateIndexes_TempStoreFileBeforeBeginTx(t *testing.T) {
	src, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}

	text := string(src)

	// Extract func CreateIndexes to the next top-level func.
	start := strings.Index(text, "\nfunc CreateIndexes")
	if start < 0 {
		start = strings.Index(text, "func CreateIndexes")
	}
	if start < 0 {
		t.Fatal("CreateIndexes not found in db.go")
	}

	after := text[start+1:] // skip leading newline (or index 0 for first func)
	nextFunc := strings.Index(after[len("func "):], "\nfunc ")
	if nextFunc < 0 {
		t.Fatal("could not find end of CreateIndexes (no next top-level func)")
	}
	body := after[:len("func ")+nextFunc]

	// BeginTx must appear after temp_store=FILE or temp_store=1.
	before0, _, ok := strings.Cut(body, "BeginTx")
	if !ok {
		t.Fatal("BeginTx not found in CreateIndexes")
	}
	before := before0
	if !strings.Contains(before, "temp_store=FILE") && !strings.Contains(before, "temp_store=1") {
		t.Fatal("temp_store=FILE (or temp_store=1) not found before BeginTx in CreateIndexes")
	}

	// Restore must convert the saved integer via strconv.Itoa.
	if !strings.Contains(body, "strconv.Itoa") {
		t.Fatal("strconv.Itoa not found in CreateIndexes (must convert saved integer for restore)")
	}

	// Restore of temp_store must be inside a defer.
	deferIdx := strings.Index(body, "defer func()")
	if deferIdx < 0 {
		t.Fatal("defer func() not found in CreateIndexes")
	}
	deferSlice := body[deferIdx:]
	if !strings.Contains(deferSlice, "temp_store=") {
		t.Fatal("restore of temp_store not found inside defer in CreateIndexes")
	}
}
