package thumbnail

import (
	"bytes"
	"database/sql"
	"io"
)

// Generator abstracts thumbnail generation and hash computation.
type Generator interface {
	// GenerateThumbnailAndHashes creates a thumbnail and computes MD5/pHash.
	// The reader should be an image file.
	// Returns thumbnail bytes, MD5 hash, perceptual hash, and any error.
	GenerateThumbnailAndHashes(r io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error)
}

// Ensure the existing function signature is compatible
// The existing GenerateThumbnailAndHashes takes *os.File which implements io.ReadSeeker
