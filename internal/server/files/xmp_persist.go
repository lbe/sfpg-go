package files

import (
	"context"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

const (
	xmpNamespaceExif    = "exif"
	xmpPropGPSLatitude  = "GPSLatitude"
	xmpPropGPSLongitude = "GPSLongitude"
)

type upsertXMPQueries interface {
	UpsertXMPRaw(ctx context.Context, arg gallerydb.UpsertXMPRawParams) error
	UpsertXMPProperty(ctx context.Context, arg gallerydb.UpsertXMPPropertyParams) error
}

func xmpPropertyID(fileID int64, property string) int64 {
	switch property {
	case xmpPropGPSLatitude:
		return fileID*10 + 1
	case xmpPropGPSLongitude:
		return fileID*10 + 2
	default:
		return fileID*10 + 9
	}
}

// upsertFileXMP writes xmp_raw and xmp_properties when XMP was captured.
// Non-fatal: logs errors like UpsertExif. Requires f.File.ID set.
func upsertFileXMP(ctx context.Context, q upsertXMPQueries, f *File) {
	if !f.XmpRaw.RawXml.Valid {
		return
	}
	f.XmpRaw.FileID = f.File.ID
	if err := q.UpsertXMPRaw(ctx, f.XmpRaw); err != nil {
		slog.Error("upsert xmp raw", "path", f.Path, "err", err)
		return
	}
	for i := range f.XmpProps {
		p := f.XmpProps[i]
		p.FileID = f.File.ID
		p.ID = xmpPropertyID(f.File.ID, p.Property)
		if err := q.UpsertXMPProperty(ctx, p); err != nil {
			slog.Error("upsert xmp property", "path", f.Path, "property", p.Property, "err", err)
		}
	}
}
