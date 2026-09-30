package files

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
)

func TestGalleryDirPrefixFromReported(t *testing.T) {
	t.Parallel()

	normalized := "/data/gallery"

	tests := []struct {
		name           string
		reportedDir    []byte
		removePrefix   func(string, string) (string, error)
		wantGalleryDir string
		wantErr        bool
	}{
		{
			name:        "gallery_root",
			reportedDir: []byte("/data/gallery"),
			removePrefix: func(imagesDir, full string) (string, error) {
				if imagesDir != normalized || full != "/data/gallery" {
					t.Fatalf("removePrefix(%q, %q)", imagesDir, full)
				}
				return "", nil
			},
			wantGalleryDir: "",
		},
		{
			name:        "nested_directory",
			reportedDir: []byte("/data/gallery/album/set"),
			removePrefix: func(imagesDir, full string) (string, error) {
				if imagesDir != normalized || full != "/data/gallery/album/set" {
					t.Fatalf("removePrefix(%q, %q)", imagesDir, full)
				}
				return "album/set", nil
			},
			wantGalleryDir: "album/set",
		},
		{
			name:        "bad_prefix_error",
			reportedDir: []byte("/other/outside"),
			removePrefix: func(string, string) (string, error) {
				return "", errors.New("path outside images dir")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := galleryDirPrefixFromReported(tt.reportedDir, normalized, tt.removePrefix)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("galleryDirPrefixFromReported: %v", err)
			}
			if got != tt.wantGalleryDir {
				t.Fatalf("gallery_dir = %q, want %q", got, tt.wantGalleryDir)
			}
		})
	}
}

func TestDiscoveryFileRowToDirEnt(t *testing.T) {
	t.Parallel()

	row := gallerydb.ListDiscoveryFilesByFolderIDRow{
		FileID:        42,
		FileMtime:     sql.NullInt64{Int64: 300, Valid: true},
		FileSizeBytes: sql.NullInt64{Int64: 400, Valid: true},
		FileMd5:       sql.NullString{String: "abc", Valid: true},
	}

	want := parallelwalkdir.DirEntState{
		FileIDValid:    true,
		FileMtimeValid: true,
		FileMtime:      300,
		FileSizeValid:  true,
		FileSize:       400,
		FileMD5Valid:   true,
	}

	got := discoveryFileRowToDirEnt(row)
	if got != want {
		t.Fatalf("DirEntState mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestBuildDirEntMapFromRows(t *testing.T) {
	t.Parallel()

	fileRows := []gallerydb.ListDiscoveryFilesByFolderIDRow{
		{
			FileFilename: "root.jpg",
			FileID:       1,
			FileMd5:      sql.NullString{String: "md5a", Valid: true},
		},
		{
			FileFilename: "child.png",
			FileID:       2,
			FileMd5:      sql.NullString{String: "md5b", Valid: true},
		},
	}

	got := buildDirEntMapFromRows(fileRows, nil)

	wantKeys := map[parallelwalkdir.PathHash]struct{}{
		parallelwalkdir.HashPathBytes([]byte("root.jpg")):  {},
		parallelwalkdir.HashPathBytes([]byte("child.png")): {},
	}
	if len(got) != len(wantKeys) {
		t.Fatalf("map len = %d, want %d", len(got), len(wantKeys))
	}
	for k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing key %v", k)
		}
	}

	rootKey := parallelwalkdir.HashPathBytes([]byte("root.jpg"))
	if !got[rootKey].FileIDValid || !got[rootKey].FileMD5Valid {
		t.Fatalf("root.jpg state: %+v", got[rootKey])
	}
}

func TestBuildDirEntMapFromRows_mergeCollision(t *testing.T) {
	t.Parallel()

	fileRows := []gallerydb.ListDiscoveryFilesByFolderIDRow{
		{
			FileFilename:  "photo.jpg",
			FileID:        10,
			FileSizeBytes: sql.NullInt64{Int64: 100, Valid: true},
			FileMd5:       sql.NullString{String: "md5", Valid: true},
		},
	}
	invalidRows := []gallerydb.ListDiscoveryInvalidByFolderIDRow{
		{
			InvalidPath:  "album/photo.jpg",
			InvalidMtime: 50,
			InvalidSize:  200,
		},
	}

	got := buildDirEntMapFromRows(fileRows, invalidRows)
	key := parallelwalkdir.HashPathBytes([]byte("photo.jpg"))
	st, ok := got[key]
	if !ok {
		t.Fatal("missing merged entry for photo.jpg")
	}
	if !st.FileIDValid || st.FileSize != 100 {
		t.Fatalf("file fields: %+v", st)
	}
	if !st.InvalidPathValid || st.InvalidMtime != 50 || st.InvalidSize != 200 {
		t.Fatalf("invalid fields not merged: %+v", st)
	}
}

func TestBuildDirEntMapFromRows_duplicateBasenameLastWins(t *testing.T) {
	t.Parallel()

	dupRows := []gallerydb.ListDiscoveryFilesByFolderIDRow{
		{
			FileFilename:  "a.jpg",
			FileID:        10,
			FileSizeBytes: sql.NullInt64{Int64: 100, Valid: true},
		},
		{
			FileFilename:  "a.jpg",
			FileID:        20,
			FileSizeBytes: sql.NullInt64{Int64: 200, Valid: true},
		},
	}
	dupMap := buildDirEntMapFromRows(dupRows, nil)
	dupKey := parallelwalkdir.HashPathBytes([]byte("a.jpg"))
	if len(dupMap) != 1 {
		t.Fatalf("duplicate basename map len = %d, want 1", len(dupMap))
	}
	if dupMap[dupKey].FileSize != 200 {
		t.Fatalf("last row wins: FileSize = %d, want 200", dupMap[dupKey].FileSize)
	}
}

func TestBuildDirEntMapFromRows_invalidOnly(t *testing.T) {
	t.Parallel()

	invalidRows := []gallerydb.ListDiscoveryInvalidByFolderIDRow{
		{InvalidPath: "bad.raw", InvalidMtime: 11, InvalidSize: 22},
	}
	got := buildDirEntMapFromRows(nil, invalidRows)
	key := parallelwalkdir.HashPathBytes([]byte("bad.raw"))
	st := got[key]
	if !st.InvalidPathValid || st.InvalidMtime != 11 || st.InvalidSize != 22 {
		t.Fatalf("invalid-only mapping: %+v", st)
	}
	if st.FileIDValid || st.FileMtimeValid || st.FileSizeValid || st.FileMD5Valid {
		t.Fatalf("file fields should be unset: %+v", st)
	}
}
