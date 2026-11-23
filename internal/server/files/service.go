package files

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// FileProcessor makes what the gallery stores for an image file: its
// metadata, thumbnail and hashes.
type FileProcessor interface {
	// ProcessDiscoveryFile processes the file at path, relative to the images
	// directory, with the mtime and size the walk saw. InvalidReason
	// classifies its error.
	ProcessDiscoveryFile(ctx context.Context, path string, mtime, size int64) (*File, error)

	// Close releases the thumbnail generators' memory. It does not close the
	// metadata extractor, which the caller owns.
	Close() error
}

// InvalidFile is a file the processor could not import.
type InvalidFile struct {
	Path   string // gallery path
	Mtime  int64
	Size   int64
	Reason string // empty is stored as NULL
}

type fileProcessor struct {
	imagesDir  string
	extractor  *metadata.Extractor
	generators *thumbnail.Pool
}

// NewFileProcessor returns a FileProcessor for the files under imagesDir that
// extracts metadata with extractor. The caller owns extractor and closes it
// after the processor is no longer used.
func NewFileProcessor(imagesDir string, extractor *metadata.Extractor) FileProcessor {
	return &fileProcessor{
		imagesDir:  imagesDir,
		extractor:  extractor,
		generators: thumbnail.NewPool(),
	}
}

func (s *fileProcessor) ProcessDiscoveryFile(ctx context.Context, path string, mtime, size int64) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file := &File{
		ImagesDir: s.imagesDir,
		Path:      path,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: mtime, Valid: true},
			SizeBytes: sql.NullInt64{Int64: size, Valid: true},
		},
	}
	if err := processFileContents(ctx, file, s.extractor, s.generators); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *fileProcessor) Close() error {
	s.generators.Close()
	return nil
}
