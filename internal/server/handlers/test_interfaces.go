// Package handlers defines HTTP handlers and their test interfaces.
//
// This file defines interfaces used by handler unit tests (see HANDLER_DEPENDENCIES.md).
// Existing interfaces (ConfigService, SessionManager, HandlerQueries, AuthService)
// live in config, session, server/interfaces, and auth packages respectively.
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
