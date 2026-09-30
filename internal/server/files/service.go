package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// FileProcessor provides a high-level interface for discovery file processing.
type FileProcessor interface {
	// ProcessDiscoveryFile processes a dequeued discovery item. file must include
	// walk mtime/size from DiscoveryPathWork.
	ProcessDiscoveryFile(ctx context.Context, file *File) (*File, error)

	// RecordInvalidFile records a path in the invalid_files table so it can be skipped on future runs.
	RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string, folderID int64) error

	// SubmitFileForWrite submits a fully-processed *File to the write batcher.
	// The batcher handles all DB writes (UpsertPathChain, UpsertExif, UpsertThumbnail,
	// etc.) asynchronously in a single transaction. Returns ErrFull if the batcher's
	// channel is at capacity. There is no fallback on ErrFull; the file will be
	// retried on the next discovery run (self-healing).
	SubmitFileForWrite(file *File) error

	// PendingWriteCount returns the number of files currently enqueued in the
	// write batcher and not yet flushed. Used by completion monitors to avoid
	// considering processing done before batcher flushes.
	PendingWriteCount() int64

	// Close flushes any pending batches and shuts down internal workers.
	Close() error
}

// ImporterFactory creates an Importer from a DB connection and custom queries.
type ImporterFactory func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer

// UnifiedBatcher is an interface for submitting mixed write types to the App-level batcher.
// This avoids circular dependency (files importing server).
type UnifiedBatcher interface {
	SubmitFile(file *File) error
	SubmitFolderIndex(row FolderIndexRow) error
	PendingCount() int64
	FolderIndexInflight() int64
	SetFolderIndexRebuildActive(active bool)
	BumpFolderIndexGeneration() int64
	// SetFolderIndexRebuildScanHeld marks the RO scan cursor open/closed during a
	// rebuild. While held, the infrastructure service skips WAL TRUNCATE
	// checkpoints (the cursor pins the WAL write lock).
	SetFolderIndexRebuildScanHeld(held bool)
}

type fileProcessor struct {
	dbRoPool        *dbconnpool.DbSQLConnPool
	dbRwPool        *dbconnpool.DbSQLConnPool
	importerFactory ImporterFactory
	imagesDir       string

	unifiedBatcher UnifiedBatcher
}

// NewFileProcessor returns a FileProcessor implementation that uses the given
// DB pools, importer factory, and images directory.
func NewFileProcessor(
	dbRoPool, dbRwPool *dbconnpool.DbSQLConnPool,
	importerFactory ImporterFactory,
	imagesDir string,
	unifiedBatcher UnifiedBatcher,
) FileProcessor {
	return &fileProcessor{
		dbRoPool:        dbRoPool,
		dbRwPool:        dbRwPool,
		importerFactory: importerFactory,
		imagesDir:       imagesDir,
		unifiedBatcher:  unifiedBatcher,
	}
}

// ProcessDiscoveryFile runs the discovery worker pipeline on file (walk mtime/size
// required). Does not look up DB existence; WriteFileInTx sets Exists at upsert time.
func (s *fileProcessor) ProcessDiscoveryFile(ctx context.Context, file *File) (*File, error) {
	if file == nil || file.Path == "" {
		return nil, fmt.Errorf("empty path")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file.ImagesDir = s.imagesDir
	if !file.File.Mtime.Valid || !file.File.SizeBytes.Valid {
		return nil, fmt.Errorf("file walk metadata missing: mtime and size required")
	}
	if err := processDiscoveryWorkerFile(file); err != nil {
		return nil, err
	}
	return file, nil
}

func galleryParentDirForFile(galleryPath string) string {
	dir := filepath.ToSlash(filepath.Dir(galleryPath))
	if dir == "." {
		return ""
	}
	return strings.TrimPrefix(dir, "/")
}

func (s *fileProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string, folderID int64) error {
	reasonVal := sql.NullString{String: reason, Valid: reason != ""}
	params := gallerydb.UpsertInvalidFileParams{
		Path: path, Mtime: mtime, Size: size, Reason: reasonVal, FolderID: folderID,
	}

	cpcRw, getErr := s.dbRwPool.Get()
	if getErr != nil {
		return fmt.Errorf("get RW connection: %w", getErr)
	}
	defer s.dbRwPool.Put(cpcRw)
	return cpcRw.Queries.UpsertInvalidFile(ctx, params)
}

func folderIDForInvalidGalleryPath(ctx context.Context, fp *fileProcessor, galleryPath string) (int64, error) {
	parentDir := galleryParentDirForFile(galleryPath)
	cpcRw, err := fp.dbRwPool.Get()
	if err != nil {
		return 0, fmt.Errorf("get RW connection: %w", err)
	}
	defer fp.dbRwPool.Put(cpcRw)
	folderID, err := cpcRw.Queries.GetFolderIDByPath(ctx, parentDir)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("no folder for gallery path parent %q", parentDir)
		}
		return 0, err
	}
	return folderID, nil
}

func (s *fileProcessor) SubmitFileForWrite(file *File) error {
	return s.unifiedBatcher.SubmitFile(file)
}

func (s *fileProcessor) PendingWriteCount() int64 {
	return s.unifiedBatcher.PendingCount()
}

func (s *fileProcessor) Close() error {
	return nil
}
