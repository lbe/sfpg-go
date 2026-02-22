package thumbnail

import (
	"bytes"
	"database/sql"
	"io"
)

// MockGenerator is a mock implementation of Generator for testing.
type MockGenerator struct {
	Thumbnail *bytes.Buffer
	MD5       *sql.NullString
	PHash     *sql.NullInt64
	Err       error
}

// GenerateThumbnailAndHashes returns the mock values.
func (m *MockGenerator) GenerateThumbnailAndHashes(r io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	if m.Err != nil {
		return nil, nil, nil, m.Err
	}
	return m.Thumbnail, m.MD5, m.PHash, nil
}

// Ensure MockGenerator implements Generator
var _ Generator = (*MockGenerator)(nil)
