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

// generatorFunc adapts the package-level GenerateThumbnailAndHashes function to
// the Generator interface.
type generatorFunc func(io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error)

// GenerateThumbnailAndHashes implements Generator by calling the wrapped function.
func (f generatorFunc) GenerateThumbnailAndHashes(r io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	return f(r)
}

// Compile-time check that GenerateThumbnailAndHashes can be wired into Generator.
var _ Generator = generatorFunc(GenerateThumbnailAndHashes)
