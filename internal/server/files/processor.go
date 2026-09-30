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

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/thumbnail"
	"github.com/lbe/sfpg-go/internal/workerpool"
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

// GetStats returns the current file-processing counters in the shape used by the metrics dashboard.
func (s *ProcessingStats) GetStats() metrics.FileProcessingMetrics {
	return metrics.FileProcessingMetrics{
		TotalFound:      s.TotalFound.Load(),
		AlreadyExisting: s.AlreadyExisting.Load(),
		NewlyInserted:   s.NewlyInserted.Load(),
		SkippedInvalid:  s.SkippedInvalid.Load(),
		InFlight:        s.InFlight.Load(),
	}
}

// categorizeProcessError returns a short reason string for recording in invalid_files.
func categorizeProcessError(err error) string {
	if err == nil {
		return "unknown"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "non-image"):
		return "non-image"
	case strings.Contains(s, "invalid JPEG markers"):
		return "jpeg-markers"
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
	fp, ok := processor.(*fileProcessor)
	if !ok {
		return fmt.Errorf("record invalid file: folder_id resolution requires fileProcessor")
	}
	folderID, err := folderIDForInvalidGalleryPath(ctx, fp, path)
	if err != nil {
		return err
	}
	return processor.RecordInvalidFile(ctx, path, info.ModTime().Unix(), info.Size(), reason, folderID)
}

// processDiscoveryWorkerFile processes a dequeued discovery item. Walk metadata
// must already be on file; no DB modification check or existence pre-lookup.
func processDiscoveryWorkerFile(file *File) error {
	if !file.File.Mtime.Valid || !file.File.SizeBytes.Valid {
		return fmt.Errorf("file walk metadata missing: mtime and size required")
	}
	file.Exists = false
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

	// Reject JPEG files with invalid markers (poison pill files) so the worker
	// records them in invalid_files instead of inserting into files.
	if file.File.MimeType.String == "image/jpeg" && !file.HasValidJpegMarkers {
		slog.Warn("processFile invalid JPEG markers (possible poison pill)", "path", file.Path)
		return fmt.Errorf("invalid JPEG markers: %s", file.Path)
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

	thumbBytesBuffer, md5, phash, err := thumbnail.GenerateThumbnailAndHashes(imageFile, int(config.Width), int(config.Height))
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

// NewPoolFuncWithProcessor returns a worker pool function that uses FileProcessor.
// runPoolWorkerWithProcessor dequeues DiscoveryPathWork, copies walk mtime/size
// onto *File, and invokes ProcessDiscoveryFile — never path-only ProcessFile without metadata.
func NewPoolFuncWithProcessor(processor FileProcessor, q queue.Dequeuer[DiscoveryPathWork], normalizedImagesDir string, removePrefix func(normalizedDir, path string) (string, error), stats *ProcessingStats, onFileInserted func(int64)) workerpool.PoolFunc {
	return func(ctx context.Context, wc workerpool.WorkerContext, dbRoPool, _ dbconnpool.ConnectionPool, queueLength func() int, id int) error {
		return runPoolWorkerWithProcessor(ctx, wc, dbRoPool, queueLength, id, processor, q, normalizedImagesDir, removePrefix, stats, onFileInserted)
	}
}

func runPoolWorkerWithProcessor(ctx context.Context,
	wc workerpool.WorkerContext,
	_ dbconnpool.ConnectionPool,
	queueLength func() int,
	id int,
	processor FileProcessor,
	q queue.Dequeuer[DiscoveryPathWork],
	normalizedImagesDir string,
	removePrefix func(normalizedDir, path string) (string, error),
	stats *ProcessingStats,
	onFileInserted func(int64)) error {
	select {
	case <-ctx.Done():
		slog.Debug("Context cancelled before starting worker", "id", id)
		return nil
	default:
	}

	var (
		work DiscoveryPathWork
		err  error
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

		work, err = q.Dequeue()
		if err != nil {
			if errors.Is(err, queue.ErrEmptyQueue) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if errors.Is(err, queue.ErrClosedQueue) {
				return nil
			}
			slog.Error("failed to dequeue file", "err", err)
			return nil
		}

		if stats != nil {
			stats.InFlight.Add(1)
		}

		fullPath := string(work.Path)
		path, err := removePrefix(normalizedImagesDir, fullPath)
		if err != nil {
			if stats != nil {
				stats.InFlight.Add(-1)
			}
			slog.Error("invalid file path detected", "file", fullPath, "err", err)
			wc.AddFailed()
			wc.AddCompleted()
			continue
		}

		file := &File{
			Path: path,
			File: gallerydb.File{
				Mtime:     sql.NullInt64{Valid: true, Int64: work.MtimeUnix},
				SizeBytes: sql.NullInt64{Valid: true, Int64: work.SizeBytes},
			},
		}

		file, err = processor.ProcessDiscoveryFile(ctx, file)
		if err != nil {
			if stats != nil {
				stats.InFlight.Add(-1)
			}
			slog.Error("failed to process file", "file", fullPath, "err", err)
			// Record so we skip this file on future runs when mtime/size unchanged
			if recordErr := recordInvalidFileFromPath(ctx, processor, fullPath, path, err); recordErr != nil {
				slog.Error("record invalid file", "path", path, "err", recordErr)
			} else if stats != nil {
				stats.SkippedInvalid.Add(1)
			}
			wc.AddFailed()
			wc.AddCompleted()
			continue
		}

		// Walk-only gate: every dequeued path that reaches submit counts as newly processed.
		if stats != nil {
			stats.NewlyInserted.Add(1)
		}

		// Skipped invalid file: no thumbnail to generate
		if file.Ok && !file.Exists {
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
			if onFileInserted != nil && file.File.SizeBytes.Valid {
				onFileInserted(file.File.SizeBytes.Int64)
			}
		}
		if stats != nil {
			stats.InFlight.Add(-1)
		}
		wc.AddCompleted()
	}
}
