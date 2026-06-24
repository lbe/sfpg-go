// Package handlers defines HTTP handlers and their test interfaces.
//
// This file defines the MetadataQueries interface for reading EXIF and IPTC metadata.
// It lives in the handlers package to avoid circular dependencies between server and
// the metadata packages. HandlerQueries (server/interfaces package) does not include
// these; handlers depend on MetadataQueries via the GetMetadataQueries callback.
package handlers

import (
	"context"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// MetadataQueries abstracts EXIF and IPTC reads for a file.
// Used by GalleryHandlers (InfoBoxImage). HandlerQueries (server package) does not
// include these; handlers depend on MetadataQueries via GetMetadataQueries callback.
type MetadataQueries interface {
	GetExifByFile(ctx context.Context, fileID int64) (gallerydb.ExifMetadatum, error)
	GetIPTCByFile(ctx context.Context, fileID int64) (gallerydb.IptcMetadatum, error)
}
