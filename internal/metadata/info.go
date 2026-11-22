package metadata

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/coords"
)

// Info is the metadata of one image. A zero field was not present.
type Info struct {
	Exif Exif
	IPTC IPTC
	XMP  XMP
}

// Exif holds the EXIF values the gallery stores.
type Exif struct {
	CameraMake   string // lower case
	CameraModel  string
	LensModel    string
	FocalLength  string // "50.00mm"
	Aperture     string // "2", "1.70"
	ShutterSpeed string // "1/1600", "2.50"
	ISO          int64
	Orientation  int64 // 1 to 8
	CaptureDate  time.Time

	// Latitude and Longitude are both 0 when there is no position.
	Latitude    float64
	Longitude   float64
	Altitude    float64
	HasAltitude bool
}

// IPTC holds the descriptive metadata, from XMP where present and from
// IPTC-IIM otherwise.
type IPTC struct {
	Title       string
	Description string
	Creator     string
	Copyright   string
	Credit      string
	Source      string
	Keywords    []string
	Created     time.Time
}

// XMP holds the raw XMP packet and the GPS position written in it.
type XMP struct {
	Packet []byte
	// GPSLatitude and GPSLongitude are the packet's strings, set only when
	// both are present.
	GPSLatitude  string
	GPSLongitude string
}

// parse turns ExifTool's JSON output for path into Info.
func parse(out []byte, path string) (Info, error) {
	var objects []map[string]any
	if err := json.Unmarshal(out, &objects); err != nil {
		return Info{}, fmt.Errorf("metadata: exiftool output: %w", err)
	}
	if len(objects) != 1 || objects[0] == nil {
		return Info{}, fmt.Errorf("metadata: exiftool returned %d results, want 1", len(objects))
	}
	tags := objects[0]
	if msg, ok := tags["ExifTool:Error"]; ok && msg != nil && msg != "" {
		return Info{}, fmt.Errorf("metadata: exiftool: %v", msg)
	}
	packet, err := rawXMP(tags["XMP:XMP"])
	if err != nil {
		return Info{}, err
	}

	info := Info{Exif: exifOf(tags), IPTC: iptcOf(tags), XMP: xmpOf(packet)}
	// The XMP position fills in for a missing EXIF one.
	x := &info.XMP
	if info.Exif.Latitude == 0 && info.Exif.Longitude == 0 && x.GPSLatitude != "" {
		lat, latErr := coords.ParseCoordinate(x.GPSLatitude)
		lon, lonErr := coords.ParseCoordinate(x.GPSLongitude)
		if err := errors.Join(latErr, lonErr); err != nil {
			slog.Debug("XMP GPS string parse failed", "err", err, "path", path)
		} else {
			info.Exif.Latitude, info.Exif.Longitude = lat, lon
		}
	}
	return info, nil
}

// rawXMP returns the XMP packet in value. ExifTool writes a packet that is not
// plain text as a "base64:" string.
func rawXMP(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("metadata: XMP:XMP is %T, want a string", value)
	}
	if encoded, ok := strings.CutPrefix(text, "base64:"); ok {
		packet, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("metadata: XMP packet: %w", err)
		}
		return packet, nil
	}
	return []byte(text), nil
}

func exifOf(tags map[string]any) Exif {
	var e Exif
	e.CameraMake = strings.ToLower(str(tags, "IFD0:Make"))
	e.CameraModel = str(tags, "IFD0:Model")
	e.LensModel = str(tags, "ExifIFD:LensModel")
	// ExifTool reports these as float32 EXIF rationals; format them as such.
	if n, ok := float32Tag(tags, "ExifIFD:FocalLength"); ok && n >= 0 {
		e.FocalLength = strconv.FormatFloat(float64(n), 'f', 2, 32) + "mm"
	}
	if n, ok := float32Tag(tags, "ExifIFD:FNumber"); ok && n >= 0 {
		digits := 2
		if n*100 < math.MaxUint32 && uint32(n*100)%100 == 0 {
			digits = 0
		}
		e.Aperture = strconv.FormatFloat(float64(n), 'f', digits, 32)
	}
	if n, ok := float32Tag(tags, "ExifIFD:ExposureTime"); ok && n > 0 {
		e.ShutterSpeed = strconv.FormatFloat(float64(n), 'f', 2, 32)
		if n < 1 {
			e.ShutterSpeed = ""
			if d := math.Round(1/float64(n)*1000) / 1000; d < math.MaxUint64 {
				e.ShutterSpeed = "1/" + strconv.FormatUint(uint64(d), 10)
			}
		}
	}
	if n, ok := intTag(tags, "ExifIFD:ISO"); ok {
		e.ISO = n
	}
	if n, ok := intTag(tags, "IFD0:Orientation"); ok && n >= 1 && n <= 8 {
		e.Orientation = n
	}
	gpsOf(&e, tags)
	for _, d := range [...]struct{ date, offset string }{
		{"ExifIFD:DateTimeOriginal", "ExifIFD:OffsetTimeOriginal"},
		{"ExifIFD:CreateDate", "ExifIFD:OffsetTimeDigitized"},
	} {
		value := str(tags, d.date)
		if value == "" {
			continue
		}
		if t, ok := parseExifDate(value, str(tags, d.offset)); ok {
			e.CaptureDate = t
			break
		}
	}
	return e
}

// gpsOf sets the position and altitude. A latitude beyond ±90 or a longitude
// beyond ±180 is discarded; a position of 0, 0 is no position.
func gpsOf(e *Exif, tags map[string]any) {
	lat, hasLat := floatTag(tags, "GPS:GPSLatitude")
	lon, hasLon := floatTag(tags, "GPS:GPSLongitude")
	alt, hasAlt := float32Tag(tags, "GPS:GPSAltitude")
	if hasLat && math.Abs(lat) > 90 {
		lat = 0
	}
	if hasLon && math.Abs(lon) > 180 {
		lon = 0
	}
	if strings.EqualFold(str(tags, "GPS:GPSLatitudeRef"), "S") {
		lat = -math.Abs(lat)
	}
	if strings.EqualFold(str(tags, "GPS:GPSLongitudeRef"), "W") {
		lon = -math.Abs(lon)
	}
	if ref, ok := intTag(tags, "GPS:GPSAltitudeRef"); ok && ref == 1 {
		alt = -float32(math.Abs(float64(alt)))
	}
	e.Latitude, e.Longitude = lat, lon
	if hasAlt {
		e.Altitude, e.HasAltitude = float64(alt), true
	}
}

func iptcOf(tags map[string]any) IPTC {
	return IPTC{
		Title:       firstText(tags, "XMP-dc:Title", "IPTC:ObjectName"),
		Description: firstText(tags, "XMP-dc:Description", "IPTC:Caption-Abstract"),
		Creator:     firstCreator(tags, "XMP-dc:Creator", "IPTC:By-line"),
		Copyright:   firstText(tags, "XMP-dc:Rights", "IPTC:CopyrightNotice"),
		Credit:      firstText(tags, "XMP-photoshop:Credit", "IPTC:Credit"),
		Source:      firstText(tags, "XMP-photoshop:Source", "IPTC:Source"),
		Keywords:    keywords(tags, "XMP-dc:Subject", "IPTC:Keywords"),
		Created:     created(tags),
	}
}

// created returns XMP-photoshop:DateCreated, or IPTC:DateCreated with
// IPTC:TimeCreated appended when that is present.
func created(tags map[string]any) time.Time {
	if s, ok := text(tags["XMP-photoshop:DateCreated"]); ok {
		if t, ok := parseDate(s); ok {
			return t
		}
	}
	date, ok := text(tags["IPTC:DateCreated"])
	if !ok {
		return time.Time{}
	}
	if v, present := tags["IPTC:TimeCreated"]; present {
		clock, ok := text(v)
		if !ok {
			return time.Time{}
		}
		date += " " + clock
	}
	t, _ := parseDate(date)
	return t
}

// firstText returns the first of keys whose value is non-blank text.
func firstText(tags map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := text(tags[k]); ok {
			return s
		}
	}
	return ""
}

// firstCreator is firstText, except that a list value is joined with "; ".
func firstCreator(tags map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := tags[k].(type) {
		case string:
			if s, ok := text(v); ok {
				return s
			}
		case []any:
			if parts := texts(v); len(parts) > 0 {
				return strings.Join(parts, "; ")
			}
		}
	}
	return ""
}

// keywords returns the keywords from the first of keys that is present. A
// string is one keyword. A list counts as present even when empty, so an empty
// XMP list overrides IPTC-IIM keywords. Duplicates are dropped; order and case
// are kept, and keywords are never split on commas or semicolons.
func keywords(tags map[string]any, keys ...string) []string {
	for _, k := range keys {
		switch v := tags[k].(type) {
		case string:
			if s, ok := text(v); ok {
				return []string{s}
			}
		case []any:
			var out []string
			seen := make(map[string]bool, len(v))
			for _, e := range v {
				if s, ok := text(e); ok && !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// texts returns the non-blank text entries of list, trimmed, in order.
func texts(list []any) []string {
	var out []string
	for _, v := range list {
		if s, ok := text(v); ok {
			out = append(out, s)
		}
	}
	return out
}

// text returns v trimmed if it is a non-blank string.
func text(v any) (string, bool) {
	s, ok := v.(string)
	s = strings.TrimSpace(s)
	return s, ok && s != ""
}

func str(tags map[string]any, key string) string {
	s, _ := text(tags[key])
	return s
}

// floatTag returns a finite numeric tag. JSON numbers decode as float64.
func floatTag(tags map[string]any, key string) (float64, bool) {
	n, ok := tags[key].(float64)
	return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func float32Tag(tags map[string]any, key string) (float32, bool) {
	n, ok := floatTag(tags, key)
	return float32(n), ok && math.Abs(n) <= math.MaxFloat32
}

func intTag(tags map[string]any, key string) (int64, bool) {
	n, ok := floatTag(tags, key)
	if !ok || n < math.MinInt64 || n >= -math.MinInt64 {
		return 0, false
	}
	return int64(n), true
}

// parseExifDate parses an EXIF date, using offset when the date has no zone.
func parseExifDate(value, offset string) (time.Time, bool) {
	for _, layout := range [...]string{
		time.RFC3339Nano,
		"2006:01:02 15:04:05.999999999Z07:00",
		"2006:01:02 15:04:05.999999999Z0700",
		"2006-01-02T15:04:05.999999999Z0700",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return parseDate(value + offset)
}

// parseDate parses the date forms found in IPTC and XMP.
func parseDate(value string) (time.Time, bool) {
	for _, layout := range [...]string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999Z0700",
		"2006-01-02 15:04:05.999999999Z0700",
		"2006:01:02 15:04:05.999999999Z0700",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999",
		"2006:01:02 15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006:01:02 15:04:05",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006:01:02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006:01:02 15:04:05Z07:00",
		"2006-01-02",
		"2006:01:02",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
