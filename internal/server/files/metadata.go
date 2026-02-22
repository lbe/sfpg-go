// Package files provides file discovery, metadata extraction, and
// thumbnail generation for the photo gallery. It processes image files
// from the filesystem and stores metadata in the database.
package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/lbe/sfpg-go/internal/gensyncpool"

	"github.com/evanoberholster/imagemeta"
	"github.com/evanoberholster/imagemeta/exif2"
)

// imageMetaDecode is an injectable function used to decode image metadata (EXIF/IPTC/etc).
// Tests can override this to simulate different metadata scenarios (for example, no EXIF).
var imageMetaDecode = imagemeta.Decode

// BufPool is a pool for temporary byte slices to reduce allocations.
// Uses 8KB to support both MIME type detection and JPEG marker validation.
var BufPool = gensyncpool.New(
	func() *[]byte {
		b := make([]byte, 8192) // 8KB for MIME + JPEG marker detection
		return &b
	},
	func(*[]byte) {}, // No-op reset: byte slices are overwritten on use
)

// DetectMimeType reads the first 8KB of a file to determine its MIME type.
// For JPEG files, it also validates that the file contains valid JPEG markers
// to detect poison pill files that would cause infinite loops in imagemeta.
func DetectMimeType(f *File, imageFile *os.File) error {
	// Read up to 8KB (minimum 512 for MIME, rest for JPEG marker validation)
	l := 8192
	if f.File.SizeBytes.Int64 < 8192 {
		l = int(f.File.SizeBytes.Int64)
	}
	bufPtr := BufPool.Get()
	bytesRead, err := io.ReadAtLeast(imageFile, *bufPtr, l)
	if err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			slog.Error("error while detecting MIME type", "err", err, "path", f.Path)
			BufPool.Put(bufPtr)
			return fmt.Errorf("failed to read file for MIME type detection: %w", err)
		}
	}
	f.File.MimeType = sql.NullString{Valid: true, String: http.DetectContentType((*bufPtr)[:bytesRead])}

	// Validate JPEG markers for JPEG files to catch poison pill files
	if f.File.MimeType.String == "image/jpeg" {
		f.HasValidJpegMarkers = ValidateJpegMarkers((*bufPtr)[:bytesRead])
	}

	BufPool.Put(bufPtr)

	// Reset read pointer to beginning of file
	_, err = imageFile.Seek(0, 0)
	if err != nil {
		slog.Error("detectMimeType seek", "err", err)
		return fmt.Errorf("failed to seek to beginning of file: %w", err)
	}
	return nil
}

// ValidateJpegMarkers checks if the buffer contains valid JPEG markers after SOI.
// Returns true if at least one valid JPEG marker (0xFF followed by non-0x00/non-0xFF byte)
// is found within the buffer. This detects poison pill files that would cause
// infinite loops in the imagemeta JPEG scanner.
func ValidateJpegMarkers(buf []byte) bool {
	if len(buf) < 2 {
		return false
	}

	// Check for JPEG SOI marker (0xFF 0xD8)
	if buf[0] != 0xFF || buf[1] != 0xD8 {
		return false
	}

	// Scan for additional JPEG markers (0xFF followed by valid marker byte)
	// Skip the first 2 bytes (SOI) and look for other markers
	for i := 2; i < len(buf)-1; i++ {
		if buf[i] == 0xFF {
			// Found potential marker start
			nextByte := buf[i+1]
			// Valid markers are 0x01-0xFE (exclude 0x00=stuffing, 0xFF=pad)
			// Also exclude 0xD8 (another SOI - shouldn't happen mid-file)
			// and 0xD9 (EOI - end of image, not followed by length)
			if nextByte >= 0x01 && nextByte <= 0xFE && nextByte != 0xD8 {
				return true
			}
		}
	}

	return false
}

// exifTimeout is the maximum time allowed for EXIF extraction to prevent
// tight loops on corrupted files. This value is exported for testing.
var exifTimeout = 5 * time.Second

// setExifTimeout allows tests to adjust the timeout duration.
func setExifTimeout(d time.Duration) {
	exifTimeout = d
}

// ExtractExifData uses the `imagemeta` library to extract EXIF and other
// metadata from an image file. It includes a timeout to prevent tight loops
// on corrupted files that have JPEG magic bytes but no valid markers.
func ExtractExifData(f *File, imageFile *os.File) error {
	defer func() { // seek back to BOF on exit
		_, err := imageFile.Seek(0, 0)
		if err != nil {
			slog.Error("extractExifData seek", "err", err)
		}
	}()

	// Run EXIF extraction with a timeout to prevent tight loops on corrupted files.
	// The imagemeta library can get stuck scanning for JPEG markers in malformed files.
	ctx, cancel := context.WithTimeout(context.Background(), exifTimeout)
	defer cancel()

	type result struct {
		meta exif2.Exif
		err  error
	}
	resultChan := make(chan result, 1)

	go func() {
		m, err := imageMetaDecode(imageFile)
		resultChan <- result{meta: m, err: err}
	}()

	var m exif2.Exif
	var err error

	select {
	case r := <-resultChan:
		m, err = r.meta, r.err
	case <-ctx.Done():
		slog.Warn("ExtractExifData timed out - possible corrupted file", "path", f.Path)
		return fmt.Errorf("EXIF extraction timed out after %v", exifTimeout)
	}

	if err != nil {
		if !errors.Is(err, imagemeta.ErrNoExif) {
			slog.Warn("imagemeta.Decode failed", "err", err, "path", f.Path)
		} else {
			slog.Debug("imagemeta.Decode: no exif", "err", err, "path", f.Path)
		}
		return nil // It's okay if there's no exif, we just return
	}

	f.Exif.CameraMake = sql.NullString{String: m.Make, Valid: m.Make != ""}
	f.Exif.CameraModel = sql.NullString{String: m.Model, Valid: m.Model != ""}
	f.Exif.LensModel = sql.NullString{String: m.LensModel, Valid: m.LensModel != ""}
	f.Exif.FocalLength = sql.NullString{String: m.FocalLength.String(), Valid: m.FocalLength.String() != ""}
	f.Exif.Aperture = sql.NullString{String: m.FNumber.String(), Valid: m.FNumber.String() != ""}
	f.Exif.ShutterSpeed = sql.NullString{String: m.ExposureTime.String(), Valid: m.ExposureTime.String() != ""}
	f.Exif.Iso = sql.NullInt64{Int64: int64(m.ISOSpeed), Valid: m.ISOSpeed != 0}
	f.Exif.Latitude = sql.NullFloat64{Float64: m.GPS.Latitude(), Valid: true}
	f.Exif.Longitude = sql.NullFloat64{Float64: m.GPS.Longitude(), Valid: true}
	f.Exif.Altitude = sql.NullFloat64{Float64: float64(m.GPS.Altitude()), Valid: true}
	if !m.CreateDate().IsZero() {
		f.Exif.CaptureDate = sql.NullInt64{Int64: m.CreateDate().Unix(), Valid: true}
	}

	return nil
}
