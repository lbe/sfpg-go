package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
)

// FileProcessor provides a high-level interface for file processing operations.
// It abstracts away the details of processing files, checking modifications, and generating thumbnails.
type FileProcessor interface {
	// ProcessFile processes a file at the given path, extracting metadata and generating
	// an in-memory thumbnail. Returns the processed File struct for further operations.
	ProcessFile(ctx context.Context, path string) (*File, error)

	// ProcessFileWithConn processes a file using an existing database connection.
	// This avoids per-file connection Get/Put overhead when the caller manages the connection lifecycle.
	ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*File, error)

	// CheckIfModified checks if a file has been modified since it was last processed.
	CheckIfModified(ctx context.Context, path string) (bool, error)

	// GenerateThumbnail generates a thumbnail for the given file and updates the database.
	GenerateThumbnail(ctx context.Context, file *File) error

	// RecordInvalidFile records a path in the invalid_files table so it can be skipped on future runs.
	RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error

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
	SubmitInvalidFile(params gallerydb.UpsertInvalidFileParams) error
	PendingCount() int64
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

func (s *fileProcessor) ProcessFile(ctx context.Context, path string) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cpcRo, err := s.dbRoPool.Get()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, dbconnpool.ErrPoolClosed) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, err
		}
		return nil, fmt.Errorf("get RO connection: %w", err)
	}
	defer s.dbRoPool.Put(cpcRo)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file := &File{ImagesDir: s.imagesDir, Path: path}
	if err := ProcessFile(ctx, cpcRo, file); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *fileProcessor) ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cpcRo == nil {
		return nil, fmt.Errorf("nil connection")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file := &File{ImagesDir: s.imagesDir, Path: path}
	if err := ProcessFile(ctx, cpcRo, file); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *fileProcessor) CheckIfModified(ctx context.Context, path string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("empty path")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	cpcRo, err := s.dbRoPool.Get()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, dbconnpool.ErrPoolClosed) {
			if e := ctx.Err(); e != nil {
				return false, e
			}
			return false, err
		}
		return false, fmt.Errorf("get RO connection: %w", err)
	}
	defer s.dbRoPool.Put(cpcRo)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	file := &File{ImagesDir: s.imagesDir, Path: path}
	return CheckIfFileModified(ctx, cpcRo, file)
}

func (s *fileProcessor) GenerateThumbnail(ctx context.Context, file *File) error {
	if file == nil {
		return fmt.Errorf("nil file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, dbconnpool.ErrPoolClosed) {
			if e := ctx.Err(); e != nil {
				return e
			}
			return err
		}
		return fmt.Errorf("get RW connection: %w", err)
	}
	defer s.dbRwPool.Put(cpcRw)
	cpcRo, err := s.dbRoPool.Get()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, dbconnpool.ErrPoolClosed) {
			if e := ctx.Err(); e != nil {
				return e
			}
			return err
		}
		return fmt.Errorf("get RO connection: %w", err)
	}
	defer s.dbRoPool.Put(cpcRo)
	if err := ctx.Err(); err != nil {
		return err
	}
	return GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, file, s.importerFactory)
}

func (s *fileProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error {
	reasonVal := sql.NullString{String: reason, Valid: reason != ""}
	params := gallerydb.UpsertInvalidFileParams{
		Path: path, Mtime: mtime, Size: size, Reason: reasonVal,
	}

	if err := s.unifiedBatcher.SubmitInvalidFile(params); err == nil {
		return nil
	}

	// Final fallback: synchronous write
	cpcRw, getErr := s.dbRwPool.Get()
	if getErr != nil {
		return fmt.Errorf("get RW connection (fallback): %w", getErr)
	}
	defer s.dbRwPool.Put(cpcRw)
	return cpcRw.Queries.UpsertInvalidFile(ctx, params)
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
