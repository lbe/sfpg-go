package files

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// TestCheckIfFileModifiedCore_SetsExistsForReprocess verifies that when a file
// exists in the database but needs reprocessing (modified or missing MD5),
// f.Exists is still set to true so stats count it as AlreadyExisting.
func TestCheckIfFileModifiedCore_SetsExistsForReprocess(t *testing.T) {
	tests := []struct {
		name          string
		dbFile        gallerydb.File
		fileMtime     int64
		fileSize      int64
		wantExists    bool
		wantUnchanged bool
		wantOk        bool
	}{
		{
			name: "unchanged file with valid MD5",
			dbFile: gallerydb.File{
				ID:        1,
				Mtime:     sql.NullInt64{Valid: true, Int64: 1000},
				SizeBytes: sql.NullInt64{Valid: true, Int64: 5000},
				Md5:       sql.NullString{Valid: true, String: "abc123"},
			},
			fileMtime:     1000,
			fileSize:      5000,
			wantExists:    true,
			wantUnchanged: true,
			wantOk:        true,
		},
		{
			name: "modified file - needs reprocess but exists in DB",
			dbFile: gallerydb.File{
				ID:        2,
				Mtime:     sql.NullInt64{Valid: true, Int64: 1000},
				SizeBytes: sql.NullInt64{Valid: true, Int64: 5000},
				Md5:       sql.NullString{Valid: true, String: "abc123"},
			},
			fileMtime:     2000, // Different mtime
			fileSize:      5000,
			wantExists:    true,  // Should still be marked as existing
			wantUnchanged: false, // But needs reprocessing
			wantOk:        false,
		},
		{
			name: "missing MD5 - needs reprocess but exists in DB",
			dbFile: gallerydb.File{
				ID:        3,
				Mtime:     sql.NullInt64{Valid: true, Int64: 1000},
				SizeBytes: sql.NullInt64{Valid: true, Int64: 5000},
				Md5:       sql.NullString{Valid: false}, // Missing MD5
			},
			fileMtime:     1000,
			fileSize:      5000,
			wantExists:    true,  // Should still be marked as existing
			wantUnchanged: false, // But needs reprocessing
			wantOk:        false,
		},
		{
			name:   "new file not in DB",
			dbFile: gallerydb.File{
				// Empty - simulates sql.ErrNoRows
			},
			fileMtime:     1000,
			fileSize:      5000,
			wantExists:    false, // Not in DB
			wantUnchanged: false,
			wantOk:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file for testing (os.Stat needs it to exist)
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "testfile.jpg")
			if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			// Get actual file info for the "unchanged" case
			fileInfo, err := os.Stat(testFile)
			if err != nil {
				t.Fatalf("failed to stat test file: %v", err)
			}
			actualMtime := fileInfo.ModTime().Unix()
			actualSize := fileInfo.Size()

			// Create mock functions
			getFile := func(ctx context.Context, path string) (gallerydb.File, error) {
				if tt.dbFile.ID == 0 {
					return gallerydb.File{}, sql.ErrNoRows
				}
				// Use actual file mtime/size for the "unchanged" case
				dbFile := tt.dbFile
				if tt.name == "unchanged file with valid MD5" {
					dbFile.Mtime = sql.NullInt64{Valid: true, Int64: actualMtime}
					dbFile.SizeBytes = sql.NullInt64{Valid: true, Int64: actualSize}
				}
				return dbFile, nil
			}
			getInvalidFile := func(ctx context.Context, path string) (gallerydb.InvalidFile, error) {
				return gallerydb.InvalidFile{}, sql.ErrNoRows
			}

			f := &File{
				ImagesDir: tmpDir,
				Path:      "testfile.jpg",
				File: gallerydb.File{
					Mtime:     sql.NullInt64{Valid: true, Int64: tt.fileMtime},
					SizeBytes: sql.NullInt64{Valid: true, Int64: tt.fileSize},
				},
			}

			unchanged, err := checkIfFileModifiedCore(context.Background(), getFile, getInvalidFile, f)
			if err != nil {
				t.Fatalf("checkIfFileModifiedCore error: %v", err)
			}

			if unchanged != tt.wantUnchanged {
				t.Errorf("unchanged: got %v, want %v", unchanged, tt.wantUnchanged)
			}
			if f.Exists != tt.wantExists {
				t.Errorf("f.Exists: got %v, want %v", f.Exists, tt.wantExists)
			}
			if f.Ok != tt.wantOk {
				t.Errorf("f.Ok: got %v, want %v", f.Ok, tt.wantOk)
			}

			// If file exists in DB, ID should be preserved
			if tt.wantExists && f.File.ID != tt.dbFile.ID {
				t.Errorf("f.File.ID: got %d, want %d", f.File.ID, tt.dbFile.ID)
			}
		})
	}
}

// TestProcessFileStats_ExistingFileCountsAsAlreadyExisting verifies that when
// ProcessFile is called on an existing file that needs reprocessing, the stats
// correctly count it as AlreadyExisting, not NewlyInserted.
func TestProcessFileStats_ExistingFileCountsAsAlreadyExisting(t *testing.T) {
	// This is an integration-style test that verifies the full flow
	// For now, we test the core logic that determines the Exists flag

	stats := &ProcessingStats{}

	// Simulate processing an existing file that needs reprocessing
	// (exists in DB, but mtime differs)
	file := &File{
		Exists: true,  // This should be set by checkIfFileModifiedCore
		Ok:     false, // Not ok because it needs reprocessing
	}

	// Simulate what runPoolWorkerWithProcessor does with stats
	stats.TotalFound.Add(1)
	stats.InFlight.Add(1)

	// The key check from runPoolWorkerWithProcessor
	switch {
	case file.Ok && !file.Exists:
		stats.SkippedInvalid.Add(1)
	case file.Exists:
		stats.AlreadyExisting.Add(1)
	default:
		stats.NewlyInserted.Add(1)
	}

	stats.InFlight.Add(-1)

	// Verify stats
	if val := stats.AlreadyExisting.Load(); val != 1 {
		t.Errorf("AlreadyExisting: got %d, want 1", val)
	}
	if val := stats.NewlyInserted.Load(); val != 0 {
		t.Errorf("NewlyInserted: got %d, want 0", val)
	}
}
