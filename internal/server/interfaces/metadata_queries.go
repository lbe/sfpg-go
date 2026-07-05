// Package interfaces holds shared contracts consumed by both the server
// orchestrator (App) and the handlers package. These interfaces live here
// to avoid circular dependencies while keeping the contracts in a neutral,
// stable location.
package interfaces

import (
	"context"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// MetadataQueries abstracts EXIF and IPTC reads for a file.
// Used by GalleryHandlers (InfoBoxImage).
type MetadataQueries interface {
	GetExifByFile(ctx context.Context, fileID int64) (gallerydb.ExifMetadatum, error)
	GetIPTCByFile(ctx context.Context, fileID int64) (gallerydb.IptcMetadatum, error)
}
