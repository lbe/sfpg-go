package files

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/evanoberholster/imagemeta/imagetype"
	"github.com/evanoberholster/imagemeta/meta/exif"
	"github.com/evanoberholster/imagemeta/meta/jpeg"
	metalog "github.com/evanoberholster/imagemeta/meta/logging"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// extractXMPGPSStrings scans raw XMP XML bytes for GPS latitude and longitude
// strings. It accepts both attribute and element forms (with any namespace
// prefix) and returns the raw, unnormalized strings.
func extractXMPGPSStrings(xmpXML []byte) (latStr, lonStr string, ok bool) {
	if len(xmpXML) == 0 {
		return "", "", false
	}
	dec := xml.NewDecoder(bytes.NewReader(xmpXML))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", false
		}
		se, isStart := tok.(xml.StartElement)
		if !isStart {
			continue
		}
		for _, attr := range se.Attr {
			switch attr.Name.Local {
			case "GPSLatitude":
				latStr = strings.TrimSpace(attr.Value)
			case "GPSLongitude":
				lonStr = strings.TrimSpace(attr.Value)
			}
		}
		switch se.Name.Local {
		case "GPSLatitude":
			if latStr == "" {
				latStr = strings.TrimSpace(readElementText(dec, se))
			}
		case "GPSLongitude":
			if lonStr == "" {
				lonStr = strings.TrimSpace(readElementText(dec, se))
			}
		}
	}
	return latStr, lonStr, latStr != "" && lonStr != ""
}

// readElementText reads CharData until the matching EndElement for start.
func readElementText(dec *xml.Decoder, start xml.StartElement) string {
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return strings.TrimSpace(b.String())
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name == start.Name {
				return strings.TrimSpace(b.String())
			}
		}
	}
}

// populateFileXMPFromRaw stores the raw XMP XML on the file and, when GPS
// strings are present, records them as XMP properties (raw, unnormalized).
func populateFileXMPFromRaw(f *File, xmpXML []byte) {
	if len(xmpXML) == 0 {
		return
	}
	f.XmpRaw = gallerydb.UpsertXMPRawParams{
		RawXml: sql.NullString{String: string(xmpXML), Valid: true},
	}
	latStr, lonStr, ok := extractXMPGPSStrings(xmpXML)
	if !ok {
		return
	}
	f.XmpProps = []gallerydb.UpsertXMPPropertyParams{
		{
			Namespace: xmpNamespaceExif,
			Property:  xmpPropGPSLatitude,
			Value:     sql.NullString{String: latStr, Valid: true},
		},
		{
			Namespace: xmpNamespaceExif,
			Property:  xmpPropGPSLongitude,
			Value:     sql.NullString{String: lonStr, Valid: true},
		},
	}
}

// applyExifGPSFromXMP applies GPS coordinates from the file's XMP properties
// when the file's EXIF GPS fields are not already populated. It reads only
// f.XmpProps (already populated by populateFileXMPFromRaw) and delegates
// parsing to SetGPSFromStrings.
func applyExifGPSFromXMP(f *File) {
	if f.Exif.Latitude.Valid && f.Exif.Longitude.Valid {
		return
	}
	latStr, lonStr, ok := xmpGPSStringsFromProps(f.XmpProps)
	if !ok {
		return
	}
	if err := SetGPSFromStrings(f, latStr, lonStr); err != nil {
		slog.Debug("XMP GPS string parse failed", "err", err, "path", f.Path)
	}
}

// xmpGPSStringsFromProps extracts the exif-namespace GPS latitude/longitude
// raw strings from XMP properties.
func xmpGPSStringsFromProps(props []gallerydb.UpsertXMPPropertyParams) (lat, lon string, ok bool) {
	for _, p := range props {
		if p.Namespace != xmpNamespaceExif {
			continue
		}
		switch p.Property {
		case xmpPropGPSLatitude:
			if p.Value.Valid {
				lat = p.Value.String
			}
		case xmpPropGPSLongitude:
			if p.Value.Valid {
				lon = p.Value.String
			}
		}
	}
	return lat, lon, lat != "" && lon != ""
}

// metadataDecodeWithXMP is an injectable seam that decodes image metadata and
// returns any embedded XMP payload alongside the decoded EXIF. Production code
// uses defaultMetadataDecodeWithXMP; tests may override this to simulate XMP
// fallbacks without touching the real decode path.
var metadataDecodeWithXMP = defaultMetadataDecodeWithXMP

// defaultMetadataDecodeWithXMP mirrors imagemeta.Decode for non-JPEG/unknown
// files, and uses jpeg.ScanJPEGWithSourceContext with an XMP reader for JPEG
// files so embedded XMP extension segments are captured. The context cancels
// the JPEG scan when extraction exceeds its deadline (see ExtractExifData).
func defaultMetadataDecodeWithXMP(ctx context.Context, r io.ReadSeeker) (exif.Exif, []byte, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return exif.Exif{}, nil, err
	}

	rr := bufio.NewReaderSize(r, 4*1024)
	ir := exif.AcquirePooledReader(metalog.GetLogger())
	defer exif.ReleasePooledReader(ir)

	it, err := imagetype.ScanBuf(rr)
	if err != nil || it == imagetype.ImageUnknown {
		// Short/unknown files: delegate to imageMetaDecode so existing test
		// stubs keep working.
		if _, seekErr := r.Seek(0, io.SeekStart); seekErr != nil {
			return exif.Exif{}, nil, seekErr
		}
		m, decErr := imageMetaDecode(r)
		return m, nil, decErr
	}
	ir.Exif.ImageType = it

	if it != imagetype.ImageJPEG {
		if _, seekErr := r.Seek(0, io.SeekStart); seekErr != nil {
			return exif.Exif{}, nil, seekErr
		}
		m, decErr := imageMetaDecode(r)
		return m, nil, decErr
	}

	var xmpBuf bytes.Buffer
	xmpReader := func(xr io.Reader) error {
		b, err := io.ReadAll(xr)
		if err != nil {
			return err
		}
		_, err = xmpBuf.Write(b)
		return err
	}

	if err := jpeg.ScanJPEGWithSourceContext(ctx, rr, r, ir.DecodeJPEGIfd, xmpReader); err != nil {
		return ir.Exif, xmpBuf.Bytes(), err
	}
	return ir.Exif, xmpBuf.Bytes(), nil
}
