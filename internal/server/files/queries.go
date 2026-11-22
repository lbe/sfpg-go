package files

import (
	"bytes"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
)

// File represents a file being processed, including its metadata and status.
type File struct {
	ImagesDir string
	Path      string
	File      gallerydb.File
	Thumbnail *bytes.Buffer
	Metadata  metadata.Info
}
