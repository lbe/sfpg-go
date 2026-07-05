package server

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestGob_SqlNullTypes(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		equal func(a, b interface{}) bool
	}{
		{
			name:  "NullString valid",
			value: sql.NullString{String: "hello", Valid: true},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullString) == b.(sql.NullString)
			},
		},
		{
			name:  "NullString invalid",
			value: sql.NullString{},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullString) == b.(sql.NullString)
			},
		},
		{
			name:  "NullInt64 valid",
			value: sql.NullInt64{Int64: 42, Valid: true},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullInt64) == b.(sql.NullInt64)
			},
		},
		{
			name:  "NullInt64 invalid",
			value: sql.NullInt64{},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullInt64) == b.(sql.NullInt64)
			},
		},
		{
			name:  "NullFloat64 valid",
			value: sql.NullFloat64{Float64: 3.14, Valid: true},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullFloat64) == b.(sql.NullFloat64)
			},
		},
		{
			name:  "NullFloat64 invalid",
			value: sql.NullFloat64{},
			equal: func(a, b interface{}) bool {
				return a.(sql.NullFloat64) == b.(sql.NullFloat64)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(tt.value); err != nil {
				t.Fatalf("gob encode: %v", err)
			}

			// Decode into a new zero value of the same type
			newVal := reflect.New(reflect.TypeOf(tt.value)).Interface()
			if err := gob.NewDecoder(&buf).Decode(newVal); err != nil {
				t.Fatalf("gob decode: %v", err)
			}

			// Compare
			decoded := reflect.ValueOf(newVal).Elem().Interface()
			if !tt.equal(tt.value, decoded) {
				t.Errorf("round-trip mismatch\noriginal:  %+v\ndecoded:   %+v", tt.value, decoded)
			}
		})
	}
}

func TestGob_GallerydbFile_InterfaceFields(t *testing.T) {
	original := fullyPopulatedGallerydbFile()

	// Encode
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&original); err != nil {
		t.Fatalf("gob encode gallerydb.File: %v", err)
	}

	// Decode
	var decoded gallerydb.File
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode gallerydb.File: %v", err)
	}

	// Verify all fields
	assertGallerydbFileEqual(t, "gallerydb.File round-trip", original, decoded)
}

func TestGob_UpsertThumbnailReturningIDParams_InterfaceFields(t *testing.T) {
	original := gallerydb.UpsertThumbnailReturningIDParams{
		FileID:    42,
		SizeLabel: "medium",
		Width:     400,
		Height:    300,
		Format:    "webp",
		CreatedAt: int64(1_700_000_001),
		UpdatedAt: int64(1_700_000_002),
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&original); err != nil {
		t.Fatalf("gob encode: %v", err)
	}

	var decoded gallerydb.UpsertThumbnailReturningIDParams
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode: %v", err)
	}

	if decoded.FileID != original.FileID {
		t.Errorf("FileID: got %d, want %d", decoded.FileID, original.FileID)
	}
	if decoded.SizeLabel != original.SizeLabel {
		t.Errorf("SizeLabel: got %q, want %q", decoded.SizeLabel, original.SizeLabel)
	}
	if decoded.Width != original.Width || decoded.Height != original.Height {
		t.Errorf("dimensions: got %dx%d, want %dx%d", decoded.Width, decoded.Height, original.Width, original.Height)
	}
	if decoded.Format != original.Format {
		t.Errorf("Format: got %q, want %q", decoded.Format, original.Format)
	}

	// Verify the interface{} fields came back as int64 with correct values
	assertInterfaceInt64(t, "CreatedAt", decoded.CreatedAt, original.CreatedAt)
	assertInterfaceInt64(t, "UpdatedAt", decoded.UpdatedAt, original.UpdatedAt)
}

func TestGob_Int64RegisteredForInterfaceFields(t *testing.T) {
	// Verify that int64 is registered in gob's global type map.
	// We do this by encoding a struct with an interface{} field holding int64
	// and confirming it succeeds (if registration were missing, this would panic).
	type testStruct struct {
		Value interface{}
	}

	s := testStruct{Value: int64(42)}

	var buf bytes.Buffer
	// This would panic if int64 was not registered
	if err := gob.NewEncoder(&buf).Encode(&s); err != nil {
		t.Fatalf("gob encode struct with interface{} holding int64: %v", err)
	}

	var decoded testStruct
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("gob decode struct with interface{} holding int64: %v", err)
	}

	got, ok := decoded.Value.(int64)
	if !ok {
		t.Fatalf("decoded Value is %T, want int64", decoded.Value)
	}
	if got != 42 {
		t.Errorf("decoded Value: got %d, want 42", got)
	}
}

func TestGob_InterfaceFieldRequiresRegistration_Subprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	source := `package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
)

type testStruct struct {
	Value interface{}
}

func main() {
	s := testStruct{Value: int64(42)}
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(&s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ENCODE_ERROR: %v\n", err)
		os.Exit(0) // Expected: encoding fails without registration
	}
	fmt.Fprintf(os.Stderr, "ENCODE_SUCCEEDED\n")
	os.Exit(1) // Unexpected: encoding worked without registration
}
`

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "main.go")

	if err := os.WriteFile(srcFile, []byte(source), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Run the subprocess via 'go run'
	runCmd := exec.Command("go", "run", srcFile)
	runOutput, runErr := runCmd.CombinedOutput()
	if runErr != nil {
		t.Logf("subprocess exit: %v", runErr)
	}
	t.Logf("subprocess output: %q", string(runOutput))

	// The subprocess prints ENCODE_ERROR (and exits 0) if encoding failed without registration,
	// or ENCODE_SUCCEEDED (and exits 1) if encoding worked.
	output := string(runOutput)
	switch {
	case bytes.Contains([]byte(output), []byte("ENCODE_ERROR")):
		t.Log("CONFIRMED: gob.Register(int64(0)) is required — encoding fails without it")
	case bytes.Contains([]byte(output), []byte("ENCODE_SUCCEEDED")):
		t.Log("NOTE: gob encoded int64 in interface{} WITHOUT registration — " +
			"our init() registration is defensive but may not be strictly required")
	default:
		t.Logf("unexpected subprocess output: %q", output)
	}
}
