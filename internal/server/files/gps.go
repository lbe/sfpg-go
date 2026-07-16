package files

import (
	"database/sql"
	"fmt"

	"github.com/lbe/sfpg-go/internal/coords"
)

// setGPSFromExif assigns latitude/longitude/altitude from decoded EXIF GPS.
// Does not mark 0,0 as valid (matches infobox zero-GPS suppression).
func setGPSFromExif(f *File, lat, lon float64, alt float32, hasAlt bool) {
	if lat != 0 || lon != 0 {
		f.Exif.Latitude = sql.NullFloat64{Float64: lat, Valid: true}
		f.Exif.Longitude = sql.NullFloat64{Float64: lon, Valid: true}
	}
	if hasAlt {
		f.Exif.Altitude = sql.NullFloat64{Float64: float64(alt), Valid: true}
	}
}

// SetGPSFromStrings parses DMS/decimal GPS strings via coords and assigns f.Exif.
// Production entry point when coordinates arrive as strings (XMP sidecar properties,
// import pipelines, etc.). Returns error if either coordinate fails to parse.
func SetGPSFromStrings(f *File, latStr, lonStr string) error {
	lat, err := coords.ParseCoordinate(latStr)
	if err != nil {
		return fmt.Errorf("parse latitude: %w", err)
	}
	lon, err := coords.ParseCoordinate(lonStr)
	if err != nil {
		return fmt.Errorf("parse longitude: %w", err)
	}
	setGPSFromExif(f, lat, lon, 0, false)
	return nil
}
