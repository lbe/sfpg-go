package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/thumbnail"
	"go.local/sfpg/internal/workerpool"
)

// ProcessingStats holds statistics about the file processing job.
type ProcessingStats struct {
	TotalFound      atomic.Uint64
	AlreadyExisting atomic.Uint64
	NewlyInserted   atomic.Uint64
	SkippedInvalid  atomic.Uint64
	InFlight        atomic.Int64
}

// Reset clears all stats counters to zero.
// Call this when starting a new discovery run.
func (s *ProcessingStats) Reset() {
	s.TotalFound.Store(0)
	s.AlreadyExisting.Store(0)
	s.NewlyInserted.Store(0)
	s.SkippedInvalid.Store(0)
	s.InFlight.Store(0)
}

// getFileByPathFunc is used by checkIfFileModifiedCore to fetch a file from the DB.
type getFileByPathFunc func(ctx context.Context, path string) (gallerydb.File, error)

// getInvalidFileByPathFunc is used by checkIfFileModifiedCore to check for invalid files in the DB.
type getInvalidFileByPathFunc func(ctx context.Context, path string) (gallerydb.InvalidFile, error)

// categorizeProcessError returns a short reason string for recording in invalid_files.
func categorizeProcessError(err error) string {
	if err == nil {
		return "unknown"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "non-image"):
		return "non-image"
	case strings.Contains(s, "decode") || strings.Contains(s, "DecodeConfig"):
		return "decode"
	case strings.Contains(s, "thumbnail"):
		return "thumbnail"
	case strings.Contains(s, "EXIF") || strings.Contains(s, "exif"):
		return "exif"
	case strings.Contains(s, "open") || strings.Contains(s, "Open"):
		return "open"
	default:
		return "unknown"
	}
}

// recordInvalidFileFromPath stats fullPath and records the path in invalid_files via processor.
func recordInvalidFileFromPath(ctx context.Context, processor FileProcessor, fullPath, path string, processErr error) error {
	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", fullPath, err)
	}
	reason := categorizeProcessError(processErr)
	return processor.RecordInvalidFile(ctx, path, info.ModTime().Unix(), info.Size(), reason)
}

// checkIfFileModifiedCore implements the common logic for both CheckIfFileModified
// and CheckIfFileModifiedWithQueries. It avoids requiring *gallerydb.CustomQueries
// to implement QueriesForFiles (WithTx return type differs).
func checkIfFileModifiedCore(ctx context.Context, getFile getFileByPathFunc, getInvalidFile getInvalidFileByPathFunc, f *File) (bool, error) {
	fn := filepath.Join(f.ImagesDir, filepath.FromSlash(f.Path))

	fileinfo, err := os.Stat(fn)
	if err != nil {
		slog.Error("checkIfFileModified os.Stat", "err", err)
		return false, err
	}

	if fileinfo.IsDir() {
		slog.Error("checkIfFileModified is a directory, skipping", "path", fn)
		return false, fmt.Errorf("is a directory")
	}

	f.File.Mtime = sql.NullInt64{Valid: true, Int64: fileinfo.ModTime().Unix()}
	f.File.SizeBytes = sql.NullInt64{Valid: true, Int64: fileinfo.Size()}

	// Check invalid_files before files table: skip if known invalid and mtime/size unchanged
	if getInvalidFile != nil {
		inv, invErr := getInvalidFile(ctx, f.Path)
		if invErr == nil && inv.Mtime == f.File.Mtime.Int64 && inv.Size == f.File.SizeBytes.Int64 {
			f.Ok = true
			f.Exists = false
			return true, nil
		}
		if invErr != nil && !errors.Is(invErr, sql.ErrNoRows) {
			slog.Error("checkIfFileModified GetInvalidFileByPath", "err", invErr)
		}
	}

	dbFile, err := getFile(ctx, f.Path)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("checkIfFileModified GetFileByPath", "err", err)
		return false, err
	}
	if err == nil {
		// File exists in database - mark as existing and preserve ID
		f.Exists = true
		f.File.ID = dbFile.ID

		if dbFile.Mtime.Valid && f.File.Mtime.Valid && dbFile.Mtime.Int64 == f.File.Mtime.Int64 &&
			dbFile.SizeBytes.Valid && f.File.SizeBytes.Valid && dbFile.SizeBytes.Int64 == f.File.SizeBytes.Int64 &&
			dbFile.Md5.Valid {
			f.Ok = true
			f.File.Md5 = sql.NullString{Valid: true, String: dbFile.Md5.String}
			f.File.Phash = sql.NullInt64{Valid: true, Int64: dbFile.Phash.Int64}
			f.File.MimeType = sql.NullString{Valid: true, String: dbFile.MimeType.String}
			f.File.Width = sql.NullInt64{Valid: true, Int64: dbFile.Width.Int64}
			f.File.Height = sql.NullInt64{Valid: true, Int64: dbFile.Height.Int64}
			return true, nil
		}
		// Log if Md5 is missing - file will be re-processed to calculate it
		if !dbFile.Md5.Valid {
			slog.Debug("checkIfFileModified: re-processing file with missing Md5", "path", f.Path)
		}
	}
	return false, nil
}

// CheckIfFileModified checks if a file on disk has been modified since it was
// last recorded in the database by comparing its modification time and size.
func CheckIfFileModified(ctx context.Context, cpcRo *dbconnpool.CpConn, f *File) (bool, error) {
	return checkIfFileModifiedCore(ctx, cpcRo.Queries.GetFileByPath, cpcRo.Queries.GetInvalidFileByPath, f)
}

// CheckIfFileModifiedWithQueries is a testable variant of CheckIfFileModified that
// uses a QueriesForFiles interface instead of a concrete database connection.
func CheckIfFileModifiedWithQueries(ctx context.Context, q QueriesForFiles, f *File) (bool, error) {
	return checkIfFileModifiedCore(ctx, q.GetFileByPath, q.GetInvalidFileByPath, f)
}

// ProcessFile checks if a file has been modified since its last processing,
// then extracts metadata and generates an in-memory thumbnail if needed.
func ProcessFile(ctx context.Context, cpcRo *dbconnpool.CpConn, file *File) error {
	return processFileCore(ctx, cpcRo.Queries.GetFileByPath, cpcRo.Queries.GetInvalidFileByPath, file)
}

// ProcessFileWithQueries is a testable variant of ProcessFile that accepts
// QueriesForFiles. It doesn't depend on *dbconnpool.CpConn, making it easier
// to test with mock queries.
func ProcessFileWithQueries(ctx context.Context, q QueriesForFiles, file *File) error {
	return processFileCore(ctx, q.GetFileByPath, q.GetInvalidFileByPath, file)
}

// processFileCore implements the common logic for ProcessFile and ProcessFileWithQueries.
// It checks if the file has been modified and processes it if needed.
func processFileCore(ctx context.Context, getFile getFileByPathFunc, getInvalidFile getInvalidFileByPathFunc, file *File) error {
	unchanged, err := checkIfFileModifiedCore(ctx, getFile, getInvalidFile, file)
	if err != nil {
		return fmt.Errorf("failed to check if file modified: %w", err)
	}
	if unchanged {
		return nil
	}
	return processFileContents(file)
}

// processFileContents extracts metadata and generates a thumbnail for a file.
func processFileContents(file *File) error {
	imageFile, err := os.Open(filepath.Join(file.ImagesDir, filepath.FromSlash(file.Path)))
	if err != nil {
		slog.Error("processFile os.Open", "err", err)
		return fmt.Errorf("failed to open file %q: %w", file.Path, err)
	}
	defer func() {
		if errd := imageFile.Close(); errd != nil {
			slog.Error("failed to close file", "err", errd)
		}
	}()

	if mimeErr := DetectMimeType(file, imageFile); mimeErr != nil {
		return fmt.Errorf("failed to detect MIME type: %w", mimeErr)
	}

	if len(file.File.MimeType.String) < 5 || file.File.MimeType.String[:5] != "image" {
		fmt.Print("\r")
		slog.Warn("processFile non image file.MimeType - Skipping", "file.MimeType", file.File.MimeType, "path", file.Path)
		return fmt.Errorf("non-image file: %v", file.File.MimeType.String)
	}

	// Skip EXIF extraction for JPEG files with invalid markers (poison pill files)
	if file.File.MimeType.String == "image/jpeg" && !file.HasValidJpegMarkers {
		slog.Warn("processFile invalid JPEG markers (possible poison pill) - Skipping EXIF", "path", file.Path)
		// Continue processing without EXIF - file will be recorded as invalid
		return nil
	}

	if exifErr := ExtractExifData(file, imageFile); exifErr != nil {
		return fmt.Errorf("failed to extract EXIF data: %w", exifErr)
	}

	config, _, err := image.DecodeConfig(imageFile)
	if err != nil {
		slog.Error("processFile image.DecodeConfig", "err", err)
		return fmt.Errorf("failed to decode image config: %w", err)
	}
	file.File.Width = sql.NullInt64{Int64: int64(config.Width), Valid: true}
	file.File.Height = sql.NullInt64{Int64: int64(config.Height), Valid: true}

	if _, seekErr := imageFile.Seek(0, 0); seekErr != nil {
		slog.Error("processFile seek before thumbnail generation", "err", seekErr)
		return fmt.Errorf("failed to seek to beginning of file: %w", seekErr)
	}

	thumbBytesBuffer, md5, phash, err := thumbnail.GenerateThumbnailAndHashes(imageFile)
	if err != nil {
		slog.Error("failed to generate thumbnail", "file", file.Path, "err", err)
		return fmt.Errorf("failed to generate thumbnail: %w", err)
	}
	file.Thumbnail = thumbBytesBuffer
	file.File.Md5 = *md5
	file.File.Phash = *phash
	thumbnail.PutNullInt64(phash)
	thumbnail.PutNullString(md5)
	return nil
}

// NewPoolFuncWithProcessor returns a worker pool function that uses FileProcessor service.
// The returned function matches the signature expected by workerpool.StartWorkerPool.
func NewPoolFuncWithProcessor(processor FileProcessor, q queue.Dequeuer[string], normalizedImagesDir string, removePrefix func(normalizedDir, path string) (string, error), stats *ProcessingStats) workerpool.PoolFunc {
	return func(ctx context.Context, wc workerpool.WorkerContext, dbRoPool, dbRwPool dbconnpool.ConnectionPool, queueLength func() int, id int) error {
		return runPoolWorkerWithProcessor(ctx, wc, dbRoPool, queueLength, id, processor, q, normalizedImagesDir, removePrefix, stats)
	}
}

func runPoolWorkerWithProcessor(ctx context.Context,
	wc workerpool.WorkerContext,
	dbRoPool dbconnpool.ConnectionPool,
	queueLength func() int,
	id int,
	processor FileProcessor,
	q queue.Dequeuer[string],
	normalizedImagesDir string,
	removePrefix func(normalizedDir, path string) (string, error),
	stats *ProcessingStats) error {
	select {
	case <-ctx.Done():
		slog.Debug("Context cancelled before starting worker", "id", id)
		return nil
	default:
	}

	// Get database connection ONCE for this worker - reuse for all files
	var cpcRo *dbconnpool.CpConn
	if dbRoPool != nil {
		var err error
		cpcRo, err = dbRoPool.Get()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, dbconnpool.ErrPoolClosed) {
				return ctx.Err()
			}
			return fmt.Errorf("get RO connection: %w", err)
		}
		defer dbRoPool.Put(cpcRo)
	}

	var (
		fn  string
		err error
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if wc.ShouldIStop(queueLength() + 1) {
			return nil
		}

		wc.AddSubmitted()

		fn, err = q.Dequeue()
		if err != nil {
			if err == queue.ErrEmptyQueue {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if err == queue.ErrClosedQueue {
				return nil
			}
			slog.Error("failed to dequeue file", "err", err)
			return nil
		}

		if stats != nil {
			stats.TotalFound.Add(1)
			stats.InFlight.Add(1)
		}

		path, err := removePrefix(normalizedImagesDir, fn)
		if err != nil {
			if stats != nil {
				stats.InFlight.Add(-1)
			}
			slog.Error("invalid file path detected", "file", fn, "err", err)
			wc.AddFailed()
			wc.AddCompleted()
			continue
		}

		var file *File
		if cpcRo != nil {
			file, err = processor.ProcessFileWithConn(ctx, path, cpcRo)
		} else {
			file, err = processor.ProcessFile(ctx, path)
		}
		if err != nil {
			if stats != nil {
				stats.InFlight.Add(-1)
			}
			slog.Error("failed to process file", "file", fn, "err", err)
			// Record so we skip this file on future runs when mtime/size unchanged
			if recordErr := recordInvalidFileFromPath(ctx, processor, fn, path, err); recordErr != nil {
				slog.Error("record invalid file", "path", path, "err", recordErr)
			}
			wc.AddFailed()
			wc.AddCompleted()
			continue
		}

		// file.Ok/file.Exists are set by ProcessFile in this goroutine; no race.
		if stats != nil {
			switch {
			case file.Ok && !file.Exists:
				stats.SkippedInvalid.Add(1)
			case file.Exists:
				stats.AlreadyExisting.Add(1)
			default:
				stats.NewlyInserted.Add(1)
			}
		}

		// Skipped invalid file: no thumbnail to generate
		if file.Ok && !file.Exists {
			if stats != nil {
				stats.InFlight.Add(-1)
			}
			wc.AddCompleted()
			continue
		}

		// Already in DB and unchanged: no write needed; skip SubmitFileForWrite.
		if file.Exists {
			if stats != nil {
				stats.InFlight.Add(-1)
			}
			wc.AddCompleted()
			continue
		}

		if err := processor.SubmitFileForWrite(file); err != nil {
			slog.Error("submit file for write", "path", file.Path, "err", err)
			// Return thumbnail buffer since batcher won't handle it
			if file.Thumbnail != nil {
				thumbnail.PutBytesBuffer(file.Thumbnail)
				file.Thumbnail = nil
			}
			wc.AddFailed()
		} else {
			wc.AddSuccessful()
		}
		if stats != nil {
			stats.InFlight.Add(-1)
		}
		wc.AddCompleted()
	}
}
