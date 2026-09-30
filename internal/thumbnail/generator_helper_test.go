package thumbnail

import (
	"bytes"
	"database/sql"
	"io"
)

// Generator abstracts thumbnail generation and hash computation.
type Generator interface {
	GenerateThumbnailAndHashes(r io.ReadSeeker, srcW, srcH int) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error)
}

type generatorFunc func(io.ReadSeeker, int, int) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error)

func (f generatorFunc) GenerateThumbnailAndHashes(r io.ReadSeeker, srcW, srcH int) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	return f(r, srcW, srcH)
}

var _ Generator = generatorFunc(GenerateThumbnailAndHashes)

// MockGenerator is a mock implementation of Generator for testing.
type MockGenerator struct {
	Thumbnail *bytes.Buffer
	MD5       *sql.NullString
	PHash     *sql.NullInt64
	Err       error
}

func (m *MockGenerator) GenerateThumbnailAndHashes(r io.ReadSeeker, srcW, srcH int) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	if m.Err != nil {
		return nil, nil, nil, m.Err
	}
	return m.Thumbnail, m.MD5, m.PHash, nil
}

var _ Generator = (*MockGenerator)(nil)
