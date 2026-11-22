package metadata

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// xmpOf returns the XMP for packet, with the GPS strings found in it.
func xmpOf(packet []byte) XMP {
	if len(packet) == 0 {
		return XMP{}
	}
	x := XMP{Packet: packet}
	if lat, lon, ok := gpsStrings(packet); ok {
		x.GPSLatitude, x.GPSLongitude = lat, lon
	}
	return x
}

// gpsStrings scans an XMP packet for GPSLatitude and GPSLongitude in attribute
// or element form, with any namespace prefix, and returns them as written.
func gpsStrings(packet []byte) (lat, lon string, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(packet))
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
				lat = strings.TrimSpace(attr.Value)
			case "GPSLongitude":
				lon = strings.TrimSpace(attr.Value)
			}
		}
		switch se.Name.Local {
		case "GPSLatitude":
			if lat == "" {
				lat = elementText(dec, se)
			}
		case "GPSLongitude":
			if lon == "" {
				lon = elementText(dec, se)
			}
		}
	}
	return lat, lon, lat != "" && lon != ""
}

// elementText reads the text up to the end of start, trimmed.
func elementText(dec *xml.Decoder, start xml.StartElement) string {
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
