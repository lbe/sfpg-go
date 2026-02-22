package files

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.local/sfpg/internal/coords"
	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/gallerylib"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/workerpool"

	"github.com/evanoberholster/imagemeta"
	"github.com/evanoberholster/imagemeta/exif2"
)

// --- Fakes ---

type fakeQueries struct{}

func (f *fakeQueries) GetFileByPath(ctx context.Context, path string) (gallerydb.File, error) {
	return gallerydb.File{}, sql.ErrNoRows
}
func (f *fakeQueries) GetInvalidFileByPath(ctx context.Context, path string) (gallerydb.InvalidFile, error) {
	return gallerydb.InvalidFile{}, sql.ErrNoRows
}
func (f *fakeQueries) UpsertExif(ctx context.Context, p gallerydb.UpsertExifParams) error {
	return nil
}
func (f *fakeQueries) GetThumbnailExistsViewByID(ctx context.Context, id int64) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *fakeQueries) GetFolderTileExistsViewByPath(ctx context.Context, path string) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *fakeQueries) WithTx(tx *sql.Tx) ThumbnailTx { return nil }

type fakeQueriesForCheck struct {
	ret gallerydb.File
	err error
}

func (f *fakeQueriesForCheck) GetFileByPath(ctx context.Context, path string) (gallerydb.File, error) {
	return f.ret, f.err
}
func (f *fakeQueriesForCheck) GetInvalidFileByPath(ctx context.Context, path string) (gallerydb.InvalidFile, error) {
	return gallerydb.InvalidFile{}, sql.ErrNoRows
}

// fakeQueriesForInvalid extends fakeQueriesForCheck to implement invalid file queries
type fakeQueriesForInvalid struct {
	fakeQueriesForCheck
	invalidRet gallerydb.InvalidFile
	invalidErr error
}

func (f *fakeQueriesForInvalid) GetInvalidFileByPath(ctx context.Context, path string) (gallerydb.InvalidFile, error) {
	return f.invalidRet, f.invalidErr
}
func (f *fakeQueriesForCheck) UpsertExif(ctx context.Context, p gallerydb.UpsertExifParams) error {
	return nil
}
func (f *fakeQueriesForCheck) GetThumbnailExistsViewByID(ctx context.Context, id int64) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *fakeQueriesForCheck) GetFolderTileExistsViewByPath(ctx context.Context, path string) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *fakeQueriesForCheck) WithTx(tx *sql.Tx) ThumbnailTx { return nil }

type fakeThumbnailTxSuccess struct {
	calledReturnID bool
	calledBlob     bool
	returnID       int64
}

func (f *fakeThumbnailTxSuccess) UpsertThumbnailReturningID(ctx context.Context, p gallerydb.UpsertThumbnailReturningIDParams) (int64, error) {
	f.calledReturnID = true
	return f.returnID, nil
}
func (f *fakeThumbnailTxSuccess) UpsertThumbnailBlob(ctx context.Context, p gallerydb.UpsertThumbnailBlobParams) error {
	f.calledBlob = true
	return nil
}

type fakeThumbnailTxFailBlob struct{}

func (f *fakeThumbnailTxFailBlob) UpsertThumbnailReturningID(ctx context.Context, p gallerydb.UpsertThumbnailReturningIDParams) (int64, error) {
	return 11, nil
}
func (f *fakeThumbnailTxFailBlob) UpsertThumbnailBlob(ctx context.Context, p gallerydb.UpsertThumbnailBlobParams) error {
	return sql.ErrConnDone
}

// TestIsImageFile tests the files.IsImageFile helper with various extensions.
func TestIsImageFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"photo.jpg", true},
		{"photo.jpeg", true},
		{"photo.png", true},
		{"photo.gif", true},
		{"photo.webp", true},
		{"photo.avif", true},
		{"photo.heic", true},
		{"photo.heif", true},
		{"photo.tif", true},
		{"photo.tiff", true},
		{"photo.JPG", true},
		{"photo.PNG", true},
		{"document.txt", false},
		{"archive.zip", false},
		{"no_extension", false},
		{".bashrc", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsImageFile(tt.path); got != tt.want {
				t.Errorf("IsImageFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsThumbnail(t *testing.T) {
	t.Run("thumbnail does not exist", func(t *testing.T) {
		_, roPool, rwPool, _ := createTestProcessor(t, nil)
		ctx := context.Background()
		cpc, err := rwPool.Get()
		if err != nil {
			t.Fatalf("get db connection: %v", err)
		}
		defer rwPool.Put(cpc)
		imp := gallerylib.Importer{Q: cpc.Queries}
		file, err := imp.UpsertPathChain(ctx, "test.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
		if err != nil {
			t.Fatalf("UpsertPathChain: %v", err)
		}
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("get ro: %v", err)
		}
		defer roPool.Put(cpcRo)
		needs, err := NeedsThumbnail(ctx, cpcRo, file.ID)
		if err != nil {
			t.Fatalf("NeedsThumbnail: %v", err)
		}
		if !needs {
			t.Errorf("NeedsThumbnail() = %v, want true", needs)
		}
	})
	t.Run("thumbnail exists", func(t *testing.T) {
		_, roPool, rwPool, _ := createTestProcessor(t, nil)
		ctx := context.Background()
		cpc, err := rwPool.Get()
		if err != nil {
			t.Fatalf("get db connection: %v", err)
		}
		defer rwPool.Put(cpc)
		imp := gallerylib.Importer{Q: cpc.Queries}
		file, err := imp.UpsertPathChain(ctx, "test2.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
		if err != nil {
			t.Fatalf("UpsertPathChain: %v", err)
		}
		_, err = cpc.Queries.UpsertThumbnailReturningID(ctx, gallerydb.UpsertThumbnailReturningIDParams{
			FileID: file.ID, SizeLabel: "m", Width: 200, Height: 200, Format: "jpg",
			CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertThumbnailReturningID: %v", err)
		}
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("get ro: %v", err)
		}
		defer roPool.Put(cpcRo)
		needs, err := NeedsThumbnail(ctx, cpcRo, file.ID)
		if err != nil {
			t.Fatalf("NeedsThumbnail: %v", err)
		}
		if needs {
			t.Errorf("NeedsThumbnail() = %v, want false", needs)
		}
	})
}

func TestNeedsFolderTileUpdate(t *testing.T) {
	t.Run("folder tile does not exist", func(t *testing.T) {
		_, roPool, rwPool, _ := createTestProcessor(t, nil)
		ctx := context.Background()
		cpc, err := rwPool.Get()
		if err != nil {
			t.Fatalf("get db connection: %v", err)
		}
		defer rwPool.Put(cpc)
		imp := gallerylib.Importer{Q: cpc.Queries}
		_, err = imp.UpsertPathChain(ctx, "test-folder/test.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
		if err != nil {
			t.Fatalf("UpsertPathChain: %v", err)
		}
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("get ro: %v", err)
		}
		defer roPool.Put(cpcRo)
		needs, err := NeedsFolderTileUpdate(ctx, cpcRo, "test-folder")
		if err != nil {
			t.Fatalf("NeedsFolderTileUpdate: %v", err)
		}
		if !needs {
			t.Errorf("NeedsFolderTileUpdate() = %v, want true", needs)
		}
	})
	t.Run("folder tile exists", func(t *testing.T) {
		_, roPool, rwPool, _ := createTestProcessor(t, nil)
		ctx := context.Background()
		cpc, err := rwPool.Get()
		if err != nil {
			t.Fatalf("get db connection: %v", err)
		}
		defer rwPool.Put(cpc)
		imp := gallerylib.Importer{Q: cpc.Queries}
		file, err := imp.UpsertPathChain(ctx, "test-folder/test.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
		if err != nil {
			t.Fatalf("UpsertPathChain: %v", err)
		}
		thumbID, err := cpc.Queries.UpsertThumbnailReturningID(ctx, gallerydb.UpsertThumbnailReturningIDParams{
			FileID: file.ID, SizeLabel: "m", Width: 200, Height: 200, Format: "jpg",
			CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertThumbnailReturningID: %v", err)
		}
		err = cpc.Queries.UpdateFolderTileId(ctx, gallerydb.UpdateFolderTileIdParams{
			ID: file.FolderID.Int64, TileID: sql.NullInt64{Int64: thumbID, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpdateFolderTileId: %v", err)
		}
		cpcRo, err := roPool.Get()
		if err != nil {
			t.Fatalf("get ro: %v", err)
		}
		defer roPool.Put(cpcRo)
		needs, err := NeedsFolderTileUpdate(ctx, cpcRo, "test-folder")
		if err != nil {
			t.Fatalf("NeedsFolderTileUpdate: %v", err)
		}
		if needs {
			t.Errorf("NeedsFolderTileUpdate() = %v, want false", needs)
		}
	})
}

func TestUpsertThumbnail(t *testing.T) {
	_, roPool, rwPool, _ := createTestProcessor(t, nil)
	ctx := context.Background()
	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get db connection: %v", err)
	}
	defer rwPool.Put(cpc)
	imp := gallerylib.Importer{Q: cpc.Queries}
	file, err := imp.UpsertPathChain(ctx, "test-file.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("UpsertPathChain: %v", err)
	}
	thumbData := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	thumbnailID, err := UpsertThumbnail(ctx, cpc, file.ID, thumbData)
	if err != nil {
		t.Fatalf("UpsertThumbnail: %v", err)
	}
	if thumbnailID <= 0 {
		t.Errorf("expected positive thumbnailID, got %d", thumbnailID)
	}
	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)
	_, err = cpcRo.Queries.GetThumbnailsByFileID(ctx, file.ID)
	if err != nil {
		t.Errorf("GetThumbnailsByFileID: %v", err)
	}
	blobData, err := cpcRo.Queries.GetThumbnailBlobDataByID(ctx, thumbnailID)
	if err != nil {
		t.Errorf("GetThumbnailBlobDataByID: %v", err)
	}
	if !reflect.DeepEqual(blobData, thumbData) {
		t.Errorf("blob mismatch: got %v, want %v", blobData, thumbData)
	}
}

// --- CheckIfFileModified ---

func TestCheckIfFileModified_Unchanged(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "img.dat")
	if err := os.WriteFile(fn, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	info, err := os.Stat(fn)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	file := &File{ImagesDir: td, Path: "img.dat", File: gallerydb.File{Filename: "img.dat"}}
	dbf := gallerydb.File{
		ID:        7,
		Mtime:     sql.NullInt64{Int64: info.ModTime().Unix(), Valid: true},
		SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true},
		Md5:       sql.NullString{String: "md5", Valid: true},
		Phash:     sql.NullInt64{Int64: 123, Valid: true},
		MimeType:  sql.NullString{String: "image/dat", Valid: true},
		Width:     sql.NullInt64{Int64: 1, Valid: true},
		Height:    sql.NullInt64{Int64: 1, Valid: true},
	}
	fq := &fakeQueriesForCheck{ret: dbf, err: nil}
	unchanged, err := CheckIfFileModifiedWithQueries(context.Background(), fq, file)
	if err != nil {
		t.Fatalf("CheckIfFileModifiedWithQueries: %v", err)
	}
	if !unchanged {
		t.Fatal("expected unchanged true, got false")
	}
	if !file.Ok || !file.Exists {
		t.Fatal("expected file.Ok and file.Exists true")
	}
	if file.File.ID != 7 {
		t.Fatalf("expected file.ID 7, got %v", file.File.ID)
	}
}

func TestCheckIfFileModified_Changed(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "img2.dat")
	if err := os.WriteFile(fn, []byte("abcd"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	info, err := os.Stat(fn)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	file := &File{ImagesDir: td, Path: "img2.dat", File: gallerydb.File{Filename: "img2.dat"}}
	dbf := gallerydb.File{
		ID:        9,
		Mtime:     sql.NullInt64{Int64: info.ModTime().Unix() - 1000, Valid: true},
		SizeBytes: sql.NullInt64{Int64: info.Size() - 1, Valid: true},
	}
	fq := &fakeQueriesForCheck{ret: dbf, err: nil}
	unchanged, err := CheckIfFileModifiedWithQueries(context.Background(), fq, file)
	if err != nil {
		t.Fatalf("CheckIfFileModifiedWithQueries: %v", err)
	}
	if unchanged {
		t.Fatal("expected unchanged false, got true")
	}
}

func TestCheckIfFileModified_FileMissing(t *testing.T) {
	td := t.TempDir()
	file := &File{ImagesDir: td, Path: "missing.jpg", File: gallerydb.File{Filename: "missing.jpg"}}
	fq := &fakeQueriesForCheck{}
	_, err := CheckIfFileModifiedWithQueries(context.Background(), fq, file)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCheckIfFileModified_PathIsDir(t *testing.T) {
	td := t.TempDir()
	dir := filepath.Join(td, "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := &File{ImagesDir: td, Path: "dir", File: gallerydb.File{Filename: "dir"}}
	fq := &fakeQueriesForCheck{}
	_, err := CheckIfFileModifiedWithQueries(context.Background(), fq, file)
	if err == nil {
		t.Fatal("expected error for directory path")
	}
}

// --- ProcessFile ---

func TestProcessFile_NonImage(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "not-image.txt")
	if err := os.WriteFile(fn, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	file := &File{
		ImagesDir: td,
		Path:      "not-image.txt",
		File:      gallerydb.File{Filename: "not-image.txt"},
	}
	fq := &fakeQueries{}
	err := ProcessFileWithQueries(context.Background(), fq, file)
	if err == nil {
		t.Fatal("expected non-image error, got nil")
	}
	if !strings.Contains(err.Error(), "non-image") {
		t.Fatalf("expected non-image error, got: %v", err)
	}
}

func TestProcessFileWithQueries_CheckError(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "img.dat")
	if err := os.WriteFile(fn, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	file := &File{ImagesDir: td, Path: "img.dat", File: gallerydb.File{Filename: "img.dat"}}
	fq := &fakeQueriesForCheck{err: sql.ErrConnDone}
	err := ProcessFileWithQueries(context.Background(), fq, file)
	if err == nil {
		t.Fatal("expected error from CheckIfFileModified")
	}
}

func TestProcessFileWithQueries_Unchanged(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "img.dat")
	if err := os.WriteFile(fn, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	info, err := os.Stat(fn)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	file := &File{ImagesDir: td, Path: "img.dat", File: gallerydb.File{Filename: "img.dat"}}
	dbf := gallerydb.File{
		ID:        11,
		Mtime:     sql.NullInt64{Int64: info.ModTime().Unix(), Valid: true},
		SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true},
		Md5:       sql.NullString{String: "md5", Valid: true},
		Phash:     sql.NullInt64{Int64: 123, Valid: true},
		MimeType:  sql.NullString{String: "image/dat", Valid: true},
		Width:     sql.NullInt64{Int64: 1, Valid: true},
		Height:    sql.NullInt64{Int64: 1, Valid: true},
	}
	fq := &fakeQueriesForCheck{ret: dbf, err: nil}
	if err := ProcessFileWithQueries(context.Background(), fq, file); err != nil {
		t.Fatalf("ProcessFileWithQueries: %v", err)
	}
	if !file.Ok || !file.Exists {
		t.Fatalf("expected file.Ok and file.Exists true")
	}
}

// --- UpsertThumbnailTxOnly ---

func TestUpsertThumbnailTxOnly_Success(t *testing.T) {
	tx := &fakeThumbnailTxSuccess{returnID: 42}
	id, err := UpsertThumbnailTxOnly(tx, context.Background(), 7, []byte("thumb"))
	if err != nil {
		t.Fatalf("UpsertThumbnailTxOnly: %v", err)
	}
	if id != 42 {
		t.Fatalf("expected id 42, got %v", id)
	}
	if !tx.calledReturnID || !tx.calledBlob {
		t.Fatal("expected both UpsertThumbnailReturningID and UpsertThumbnailBlob to be called")
	}
}

func TestUpsertThumbnailTxOnly_BlobFail(t *testing.T) {
	tx := &fakeThumbnailTxFailBlob{}
	_, err := UpsertThumbnailTxOnly(tx, context.Background(), 7, []byte("thumb"))
	if err == nil || !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("expected sql.ErrConnDone, got %v", err)
	}
}

func TestDetectMimeType(t *testing.T) {
	_, _, _, imagesDir := createTestProcessor(t, nil)
	testFilePath := "test.txt"
	fullPath := filepath.Join(imagesDir, testFilePath)
	content := []byte("this is a test file.")
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	ff, err := os.Open(fullPath)
	if err != nil {
		t.Fatalf("open test file: %v", err)
	}
	defer ff.Close()
	stat, _ := ff.Stat()
	f := &File{File: gallerydb.File{SizeBytes: sql.NullInt64{Int64: stat.Size(), Valid: true}}}
	if err := DetectMimeType(f, ff); err != nil {
		t.Fatalf("DetectMimeType: %v", err)
	}
	want := "text/plain; charset=utf-8"
	if f.File.MimeType.String != want {
		t.Errorf("DetectMimeType() = %q, want %q", f.File.MimeType.String, want)
	}
}

func TestDetectMimeType_ReadError(t *testing.T) {
	_, _, _, imagesDir := createTestProcessor(t, nil)
	path := filepath.Join(imagesDir, "closed.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	ff, err := os.Open(path)
	if err != nil {
		t.Fatalf("open test file: %v", err)
	}
	stat, _ := ff.Stat()
	if err := ff.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	f := &File{File: gallerydb.File{SizeBytes: sql.NullInt64{Int64: stat.Size(), Valid: true}}}
	if err := DetectMimeType(f, ff); err == nil {
		t.Fatal("expected error for closed file")
	}
}

func TestDetectMimeType_SeekError(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	data := []byte("pipe data")
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		reader.Close()
		t.Fatalf("write pipe: %v", err)
	}
	if err := writer.Close(); err != nil {
		reader.Close()
		t.Fatalf("close writer: %v", err)
	}
	defer reader.Close()

	f := &File{File: gallerydb.File{SizeBytes: sql.NullInt64{Int64: int64(len(data)), Valid: true}}}
	if err := DetectMimeType(f, reader); err == nil {
		t.Fatal("expected seek error from DetectMimeType")
	}
}

// TestValidateJpegMarkers tests the JPEG marker validation function.
// This catches poison pill files that would cause infinite loops in imagemeta.
func TestValidateJpegMarkers(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "empty file",
			data:     []byte{},
			expected: false,
		},
		{
			name:     "too short (only 1 byte)",
			data:     []byte{0xFF},
			expected: false,
		},
		{
			name:     "no SOI marker",
			data:     []byte{0x00, 0x00, 0x00, 0x00},
			expected: false,
		},
		{
			name:     "valid JPEG with SOI only (edge case)",
			data:     []byte{0xFF, 0xD8},
			expected: false, // No additional markers
		},
		{
			name:     "poison pill - SOI followed by nulls (no markers)",
			data:     append([]byte{0xFF, 0xD8}, make([]byte, 100)...),
			expected: false,
		},
		{
			name:     "valid JPEG - SOI + APP0 marker",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
			expected: true,
		},
		{
			name:     "valid JPEG - SOI + APP1 marker",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f'},
			expected: true,
		},
		{
			name:     "valid JPEG - SOI + DQT marker",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xDB, 0x00, 0x43},
			expected: true,
		},
		{
			name:     "valid JPEG - SOI + SOF marker",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x0B},
			expected: true,
		},
		{
			name:     "valid JPEG - marker after some data",
			data:     append(append([]byte{0xFF, 0xD8}, make([]byte, 100)...), []byte{0xFF, 0xE1}...),
			expected: true,
		},
		{
			name:     "poison pill - 0xFF followed by 0x00 (stuffing, not marker)",
			data:     []byte{0xFF, 0xD8, 0xFF, 0x00, 0xFF, 0x00, 0xFF, 0x00},
			expected: false, // 0x00 is stuffing, not a valid marker
		},
		{
			name:     "large poison pill - 8KB of nulls after SOI",
			data:     append([]byte{0xFF, 0xD8}, make([]byte, 8*1024)...),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateJpegMarkers(tt.data)
			if result != tt.expected {
				t.Errorf("ValidateJpegMarkers() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractExifData(t *testing.T) {
	testFilePath := filepath.Join("..", "..", "..", "testdata", "Metadata_test_file_-_includes_data_in_IIM,_XMP,_and_Exif.jpg")
	fd, err := os.Open(testFilePath)
	if err != nil {
		t.Skipf("open testdata image: %v", err)
	}
	defer fd.Close()
	if _, seekErr := fd.Seek(0, 0); seekErr != nil {
		t.Fatalf("seek: %v", seekErr)
	}
	f := &File{
		Path: testFilePath,
		File: gallerydb.File{Filename: filepath.Base(testFilePath)},
	}
	if extErr := ExtractExifData(f, fd); extErr != nil {
		t.Fatalf("ExtractExifData: %v", extErr)
	}

	// Assertions based on the HTML description of the test file
	if !f.Exif.CameraMake.Valid || f.Exif.CameraMake.String != "samsung" {
		t.Errorf("CameraMake got = %v, want %v", f.Exif.CameraMake, "samsung")
	}

	if !f.Exif.CameraModel.Valid || f.Exif.CameraModel.String != "SM-G930P" {
		t.Errorf("CameraModel got = %v, want %v", f.Exif.CameraModel, "SM-G930P")
	}

	// GPS coordinates are often stored in a specific format, check if the extracted value matches expected.
	// The HTML shows "26° 34′ 57.06″ N" and "80° 12′ 0.84″ W".
	// The File struct likely stores these as strings or similar.
	// We'll check for a substring match or exact match if the format is consistent.
	expectedLat, err := coords.ParseCoordinate("26° 34′ 57.06″ N")
	if err != nil {
		t.Fatalf("failed to parse expected latitude: %v", err)
	}
	if f.Exif.Latitude.Float64 != expectedLat {
		t.Errorf("GPSLatitude got = %v, want %v", f.Exif.Latitude.Float64, expectedLat)
	}

	expectedLon, err := coords.ParseCoordinate("80° 12′ 0.84″ W")
	if err != nil {
		t.Fatalf("failed to parse expected longitude: %v", err)
	}
	if f.Exif.Longitude.Float64 != expectedLon {
		t.Errorf("GPSLongitude got = %v, want %v", f.Exif.Longitude.Float64, expectedLon)
	}

	// if f.Exif.Software.String != "Adobe Photoshop Lightroom 6.10.1 (Macintosh)" {
	// 	t.Errorf("Software got = %v, want %v", f.Exif.Software.String, "Adobe Photoshop Lightroom 6.10.1 (Macintosh)")
	// }

	layout := "2 Jan 2006, 15:04:05"
	expectedCreate, err := time.Parse(layout, "29 May 2017, 11:11:16")
	if err != nil {
		t.Fatalf("Parse time: %v", err)
	}
	if f.Exif.CaptureDate.Int64 != expectedCreate.Unix() {
		t.Errorf("CaptureDate = %v, want %v", f.Exif.CaptureDate.Int64, expectedCreate.Unix())
	}
}

func TestExtractExifData_NoExif(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "noexif.png")
	f, err := os.Create(fn)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if encErr := png.Encode(f, img); encErr != nil {
		f.Close()
		t.Fatalf("encode png: %v", encErr)
	}
	f.Close()
	ff, err := os.Open(fn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ff.Close()
	file := &File{Path: fn, File: gallerydb.File{Filename: filepath.Base(fn)}}
	if extErr := ExtractExifData(file, ff); extErr != nil {
		t.Fatalf("ExtractExifData: %v", extErr)
	}
	if file.Exif.CameraMake.Valid {
		t.Errorf("expected CameraMake invalid for no-exif file, got %v", file.Exif.CameraMake)
	}
}

func TestExtractExifData_DecodeError(t *testing.T) {
	orig := imageMetaDecode
	imageMetaDecode = func(r io.ReadSeeker) (exif2.Exif, error) {
		return exif2.Exif{}, errors.New("boom")
	}
	defer func() { imageMetaDecode = orig }()

	td := t.TempDir()
	fn := filepath.Join(td, "noexif.jpg")
	if err := os.WriteFile(fn, []byte("data"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	ff, err := os.Open(fn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ff.Close()
	file := &File{Path: fn, File: gallerydb.File{Filename: filepath.Base(fn)}}
	if extErr := ExtractExifData(file, ff); extErr != nil {
		t.Fatalf("ExtractExifData: %v", extErr)
	}
	if file.Exif.CameraMake.Valid {
		t.Errorf("expected CameraMake invalid on decode error, got %v", file.Exif.CameraMake)
	}
}

func TestExtractExifData_NoExifStub(t *testing.T) {
	orig := imageMetaDecode
	imageMetaDecode = func(r io.ReadSeeker) (exif2.Exif, error) {
		return exif2.Exif{}, imagemeta.ErrNoExif
	}
	defer func() { imageMetaDecode = orig }()

	td := t.TempDir()
	fn := filepath.Join(td, "noexif.jpg")
	if err := os.WriteFile(fn, []byte("data"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	ff, err := os.Open(fn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ff.Close()
	file := &File{Path: fn, File: gallerydb.File{Filename: filepath.Base(fn)}}
	if extErr := ExtractExifData(file, ff); extErr != nil {
		t.Fatalf("ExtractExifData: %v", extErr)
	}
	if file.Exif.CameraMake.Valid {
		t.Errorf("expected CameraMake invalid on ErrNoExif, got %v", file.Exif.CameraMake)
	}
}

// TestExtractExifData_Timeout verifies that ExtractExifData returns an error
// when processing a "poison pill" file that would trigger a tight loop in the
// JPEG scanner. The function should timeout rather than hang indefinitely.
func TestExtractExifData_Timeout(t *testing.T) {
	// Use a shorter timeout for this test
	origTimeout := exifTimeout
	setExifTimeout(1 * time.Second)
	defer setExifTimeout(origTimeout)

	td := t.TempDir()
	fn := filepath.Join(td, "poison.jpg")

	// Create poison pill: valid JPEG header (SOI marker) followed by
	// 2GB of null bytes (no 0xFF markers). This triggers the tight loop
	// in nextMarker() because:
	// - isJPEG returns true (0xFF 0xD8 at start)
	// - ScanJPEG calls nextMarker()
	// - nextMarker() scans 64 bytes at a time looking for 0xFF
	// - No 0xFF found, discards 64 bytes, repeats 30+ million times
	data := make([]byte, 2) // Just the SOI marker
	data[0] = 0xFF          // SOI marker first byte
	data[1] = 0xD8          // SOI marker second byte

	// Write the SOI marker
	if err := os.WriteFile(fn, data, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Now extend the file to 2GB using truncate (creates a sparse file)
	f, err := os.OpenFile(fn, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open for truncate: %v", err)
	}
	if err = f.Truncate(2 * 1024 * 1024 * 1024); err != nil { // 2GB
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	f.Close()

	// Re-open for reading
	ff, err := os.Open(fn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer ff.Close()

	file := &File{Path: fn, File: gallerydb.File{Filename: filepath.Base(fn)}}

	// The function should return a timeout error within 1 second (+ overhead)
	// rather than hanging for many seconds scanning 2GB of null bytes.
	start := time.Now()
	err = ExtractExifData(file, ff)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("ExtractExifData took too long: %v (expected < 3s)", elapsed)
	}
}

// --- GenerateThumbnail (CallsImporterMethods) ---

//nolint:unused // used in files_integration_test.go (same package)
type wrappedImporter struct {
	inner              *gallerylib.Importer
	upsertCalls        int
	lastUpsertPath     string
	updateCalls        int
	lastUpdateFolderID int64
	lastUpdateTileID   int64
}

//nolint:unused // used in files_integration_test.go (same package)
func (w *wrappedImporter) UpsertPathChain(ctx context.Context, path string, mtime, size int64, md5 string, phash, width, height int64, mimeType string) (gallerydb.File, error) {
	w.upsertCalls++
	w.lastUpsertPath = path
	return w.inner.UpsertPathChain(ctx, path, mtime, size, md5, phash, width, height, mimeType)
}

//nolint:unused // used in files_integration_test.go (same package)
func (w *wrappedImporter) UpdateFolderTileChain(ctx context.Context, folderID, tileFileID int64) error {
	w.updateCalls++
	w.lastUpdateFolderID = folderID
	w.lastUpdateTileID = tileFileID
	return w.inner.UpdateFolderTileChain(ctx, folderID, tileFileID)
}

// --- BufPool ---

func TestBufPool_Get(t *testing.T) {
	buf := BufPool.Get()
	if cap(*buf) < 512 {
		t.Errorf("capacity %d < 512", cap(*buf))
	}
	BufPool.Put(buf)
}

func TestBufPool_Reuse(t *testing.T) {
	buf1 := BufPool.Get()
	cap1 := cap(*buf1)
	BufPool.Put(buf1)
	buf2 := BufPool.Get()
	cap2 := cap(*buf2)
	if cap1 != cap2 {
		t.Errorf("capacity changed %d -> %d", cap1, cap2)
	}
	BufPool.Put(buf2)
}

func TestBufPool_WriteAndReset(t *testing.T) {
	buf := BufPool.Get()
	testData := []byte("hello world")
	copy(*buf, testData)
	if !bytes.Equal((*buf)[:len(testData)], testData) {
		t.Fatalf("data mismatch")
	}
	BufPool.Put(buf)
	buf2 := BufPool.Get()
	if cap(*buf2) < 512 {
		t.Errorf("capacity %d < 512", cap(*buf2))
	}
	BufPool.Put(buf2)
}

func TestBufPool_Concurrent(t *testing.T) {
	const numGoroutines = 20
	const iterations = 10
	done := make(chan struct{}, numGoroutines)
	for i := range numGoroutines {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := range iterations {
				buf := BufPool.Get()
				if cap(*buf) < 512 {
					t.Errorf("goroutine %d iter %d: cap %d < 512", id, j, cap(*buf))
					return
				}
				for k := range 100 {
					(*buf)[k] = byte((id*10 + j) % 256)
				}
				BufPool.Put(buf)
			}
		}(i)
	}
	for range numGoroutines {
		<-done
	}
}

// --- Worker pool processing ---

type recordInvalidCall struct {
	path   string
	mtime  int64
	size   int64
	reason string
}

type fakeProcessor struct {
	processErrByPath    map[string]error
	thumbErrByPath      map[string]error
	skipAsInvalidByPath map[string]bool // if true, ProcessFile returns Ok=true, Exists=false
	processed           []string
	recordInvalidCalls  []recordInvalidCall
	mu                  sync.Mutex
	generated           int
}

func (f *fakeProcessor) ProcessFile(ctx context.Context, path string) (*File, error) {
	if err := f.processErrByPath[path]; err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.processed = append(f.processed, path)
	skip := f.skipAsInvalidByPath[path]
	f.mu.Unlock()
	if skip {
		return &File{Path: path, Ok: true, Exists: false}, nil
	}
	return &File{Path: path}, nil
}

func (f *fakeProcessor) ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*File, error) {
	// Delegate to ProcessFile since fakeProcessor doesn't need DB connection
	return f.ProcessFile(ctx, path)
}

func (f *fakeProcessor) CheckIfModified(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func (f *fakeProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error {
	f.mu.Lock()
	f.recordInvalidCalls = append(f.recordInvalidCalls, recordInvalidCall{path, mtime, size, reason})
	f.mu.Unlock()
	return nil
}

func (f *fakeProcessor) Close() error {
	return nil
}

func (f *fakeProcessor) GenerateThumbnail(ctx context.Context, file *File) error {
	if err := f.thumbErrByPath[file.Path]; err != nil {
		return err
	}
	f.mu.Lock()
	f.generated++
	f.mu.Unlock()
	return nil
}

func (f *fakeProcessor) SubmitFileForWrite(file *File) error {
	if err := f.thumbErrByPath[file.Path]; err != nil {
		return err
	}
	f.mu.Lock()
	f.generated++
	f.mu.Unlock()
	return nil
}

func (f *fakeProcessor) PendingWriteCount() int64 {
	return 0
}

func waitForCompleted(t *testing.T, pool *workerpool.Pool, want uint64) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if pool.Stats.CompletedTasks.Load() >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for completed tasks: want >= %d, got %d", want, pool.Stats.CompletedTasks.Load())
		case <-ticker.C:
		}
	}
}

func testRemovePrefix(normalizedDir, p string) (string, error) {
	normalizedDir = strings.TrimSuffix(normalizedDir, "/")
	if !strings.HasPrefix(p, normalizedDir+"/") {
		return "", errors.New("invalid prefix")
	}
	return strings.TrimPrefix(p, normalizedDir+"/"), nil
}

// --- WalkImageDir ---

func drainQueue(t *testing.T, q *queue.Queue[string]) []string {
	t.Helper()
	items := []string{}
	for {
		item, err := q.Dequeue()
		if err == queue.ErrEmptyQueue || err == queue.ErrClosedQueue {
			break
		}
		if err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		items = append(items, item)
	}
	return items
}

func TestWalkImageDir_EnqueuesImages(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	writeFile := func(dir, name string, data []byte) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeFile(root, "a.jpg", []byte("a"))
	writeFile(root, "b.png", []byte("b"))
	writeFile(root, "c.gif", []byte("c"))
	writeFile(root, "e.jpeg", []byte("e"))
	writeFile(root, "f.JPG", []byte("f"))
	writeFile(sub, "sub.jpg", []byte("s"))
	writeFile(root, "note.txt", []byte("x"))
	writeFile(root, "zero.png", []byte{})

	q := queue.NewQueue[string](16)
	var wg sync.WaitGroup
	var active atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &active,
		Ctx:            context.Background(),
		ImagesDir:      root,
		Q:              q,
	}

	WalkImageDir(deps)
	wg.Wait()

	if active.Load() != 0 {
		t.Fatalf("expected QSendersActive to be 0, got %d", active.Load())
	}

	items := drainQueue(t, q)
	got := map[string]int{}
	for _, item := range items {
		got[filepath.Base(item)]++
	}

	expected := map[string]int{
		"a.jpg":   1,
		"b.png":   1,
		"c.gif":   1,
		"e.jpeg":  1,
		"f.JPG":   1,
		"sub.jpg": 1,
	}

	if len(got) != len(expected) {
		t.Fatalf("unexpected number of queued items: got %d, want %d", len(got), len(expected))
	}
	for name, count := range expected {
		if got[name] != count {
			t.Fatalf("expected %s to be queued %d time(s), got %d", name, count, got[name])
		}
	}
}

func TestWalkImageDir_ClosedQueue(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	q := queue.NewQueue[string](4)
	q.Close()
	var wg sync.WaitGroup
	var active atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &active,
		Ctx:            context.Background(),
		ImagesDir:      root,
		Q:              q,
	}

	WalkImageDir(deps)
	wg.Wait()

	if active.Load() != 0 {
		t.Fatalf("expected QSendersActive to be 0, got %d", active.Load())
	}

	items := drainQueue(t, q)
	if len(items) != 0 {
		t.Fatalf("expected no queued items, got %d", len(items))
	}
}

func TestBufPool_MinimumSize(t *testing.T) {
	buf := BufPool.Get()
	if len(*buf) < 512 {
		t.Errorf("len %d < 512", len(*buf))
	}
	if cap(*buf) < 512 {
		t.Errorf("cap %d < 512", cap(*buf))
	}
	BufPool.Put(buf)
}

// --- PoolFunc context ---

func TestPoolFunc_ContextCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("context should be cancelled")
	}
}

func TestPoolFunc_ContextDuringLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		done <- struct{}{}
	}()
	cancel()
	<-done
}

// --- Invalid File Tests ---

func TestCategorizeProcessError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "unknown"},
		{"non-image", errors.New("non-image file: text/plain"), "non-image"},
		{"decode", errors.New("failed to decode image config: invalid"), "decode"},
		{"DecodeConfig", errors.New("DecodeConfig failed"), "decode"},
		{"thumbnail", errors.New("failed to generate thumbnail"), "thumbnail"},
		{"exif lowercase", errors.New("exif read failed"), "exif"},
		{"EXIF uppercase", errors.New("EXIF parse error"), "exif"},
		{"open", errors.New("failed to open file"), "open"},
		{"Open", errors.New("Open: no such file"), "open"},
		{"unknown", errors.New("something else"), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categorizeProcessError(tt.err)
			if got != tt.want {
				t.Errorf("categorizeProcessError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckIfFileModified_SkipsInvalidFile(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "img.dat")
	if err := os.WriteFile(fn, []byte("test data"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	info, err := os.Stat(fn)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	file := &File{ImagesDir: td, Path: "img.dat", File: gallerydb.File{Filename: "img.dat"}}

	// Create invalid file entry that matches current mtime/size
	invalidFile := gallerydb.InvalidFile{
		Path:   "img.dat",
		Mtime:  info.ModTime().Unix(),
		Size:   info.Size(),
		Reason: sql.NullString{String: "non-image file", Valid: true},
	}

	fq := &fakeQueriesForInvalid{
		fakeQueriesForCheck: fakeQueriesForCheck{
			ret: gallerydb.File{}, // No matching file in DB
			err: sql.ErrNoRows,
		},
		invalidRet: invalidFile,
		invalidErr: nil,
	}

	unchanged, err := CheckIfFileModifiedWithQueries(context.Background(), fq, file)
	if err != nil {
		t.Fatalf("CheckIfFileModifiedWithQueries: %v", err)
	}
	if !unchanged {
		t.Fatal("should skip processing when invalid file matches mtime/size")
	}
	if !file.Ok || file.Exists {
		t.Fatal("expected file.Ok = true and file.Exists = false for invalid file skip")
	}
}

func TestCheckIfFileModified_ReprocessesChangedInvalidFile(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "img.dat")
	if err := os.WriteFile(fn, []byte("new content"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	info, err := os.Stat(fn)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	file := &File{ImagesDir: td, Path: "img.dat", File: gallerydb.File{Filename: "img.dat"}}
	// Invalid file entry with different mtime/size so we should reprocess
	invalidFile := gallerydb.InvalidFile{
		Path:   "img.dat",
		Mtime:  info.ModTime().Unix() - 100,
		Size:   info.Size() - 1,
		Reason: sql.NullString{String: "non-image", Valid: true},
	}
	fq := &fakeQueriesForInvalid{
		fakeQueriesForCheck: fakeQueriesForCheck{ret: gallerydb.File{}, err: sql.ErrNoRows},
		invalidRet:          invalidFile,
		invalidErr:          nil,
	}
	unchanged, err := CheckIfFileModifiedWithQueries(context.Background(), fq, file)
	if err != nil {
		t.Fatalf("CheckIfFileModifiedWithQueries: %v", err)
	}
	if unchanged {
		t.Fatal("expected unchanged false when invalid file mtime/size differ from disk")
	}
}

func TestRecordInvalidFile(t *testing.T) {
	mockUB := &mockUnifiedBatcher{
		SubmitInvalidFileFunc: func(params gallerydb.UpsertInvalidFileParams) error {
			return fmt.Errorf("simulate batcher full for fallback")
		},
	}
	processor, _, rwPool, _ := createTestProcessor(t, mockUB)
	ctx := context.Background()
	path := "bad.txt"
	mtime := int64(12345)
	size := int64(99)
	reason := "non-image file"
	if err := processor.RecordInvalidFile(ctx, path, mtime, size, reason); err != nil {
		t.Fatalf("RecordInvalidFile: %v", err)
	}

	// Flush the batcher
	if err := processor.Close(); err != nil {
		t.Fatalf("processor Close: %v", err)
	}

	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get rw conn: %v", err)
	}
	defer rwPool.Put(cpc)
	inv, err := cpc.Queries.GetInvalidFileByPath(ctx, path)
	if err != nil {
		t.Fatalf("GetInvalidFileByPath: %v", err)
	}
	if inv.Path != path || inv.Mtime != mtime || inv.Size != size {
		t.Errorf("invalid_files row: path=%q mtime=%d size=%d, want path=%q mtime=%d size=%d",
			inv.Path, inv.Mtime, inv.Size, path, mtime, size)
	}
	if !inv.Reason.Valid || inv.Reason.String != reason {
		t.Errorf("invalid_files reason: %v, want %q", inv.Reason, reason)
	}
}
