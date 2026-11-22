package files

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// errMetadata marks a failure to read a file's metadata, so it is classified
// as exif even when the underlying message mentions another stage.
var errMetadata = errors.New("failed to extract EXIF data")

// InvalidReason returns the reason recorded in invalid_files for a file
// ProcessDiscoveryFile failed on: non-image, decode, thumbnail, exif, open or
// unknown.
func InvalidReason(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, errMetadata) {
		return "exif"
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

// processFileContents reads a file once, extracts its metadata, and makes its
// thumbnail and hashes.
func processFileContents(ctx context.Context, file *File, extractor *metadata.Extractor, generators *thumbnail.Pool) error {
	path := filepath.Join(file.ImagesDir, filepath.FromSlash(file.Path))
	imageFile, err := os.Open(path)
	if err != nil {
		slog.Error("processFile os.Open", "err", err)
		return fmt.Errorf("failed to open file %q: %w", file.Path, err)
	}
	defer func() {
		if errd := imageFile.Close(); errd != nil {
			slog.Error("failed to close file", "err", errd)
		}
	}()

	g := generators.Get()
	defer generators.Put(g)
	data, err := g.Read(ctx, imageFile, file.File.SizeBytes.Int64)
	if err != nil {
		return fmt.Errorf("read source file %q: %w", file.Path, err)
	}

	file.File.MimeType = sql.NullString{Valid: true, String: http.DetectContentType(data)}
	if len(file.File.MimeType.String) < 5 || file.File.MimeType.String[:5] != "image" {
		fmt.Print("\r")
		slog.Warn("processFile non image file.MimeType - Skipping", "file.MimeType", file.File.MimeType, "path", file.Path)
		return fmt.Errorf("non-image file: %v", file.File.MimeType.String)
	}

	// ExifTool reads the file itself, by path.
	if file.Metadata, err = extractor.Extract(ctx, path); err != nil {
		return fmt.Errorf("%w: %w", errMetadata, err)
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		slog.Error("processFile image.DecodeConfig", "err", err)
		return fmt.Errorf("failed to decode image config: %w", err)
	}
	file.File.Width = sql.NullInt64{Int64: int64(config.Width), Valid: true}
	file.File.Height = sql.NullInt64{Int64: int64(config.Height), Valid: true}

	thumb, md5, phash, err := g.GenerateThumbnailAndHashes(ctx, config.Width, config.Height)
	if err != nil {
		slog.Error("failed to generate thumbnail", "file", file.Path, "err", err)
		return fmt.Errorf("failed to generate thumbnail: %w", err)
	}
	file.Thumbnail = thumb
	file.File.Md5 = sql.NullString{String: md5, Valid: true}
	file.File.Phash = sql.NullInt64{Int64: phash, Valid: true}
	return nil
}
