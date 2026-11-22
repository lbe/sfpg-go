package metadata

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestParseExif(t *testing.T) {
	tests := []struct {
		name string
		json string
		want Exif
	}{
		{
			name: "camera, lens and exposure",
			json: `{"IFD0:Make":" Canon ","IFD0:Model":"EOS R5","ExifIFD:LensModel":"RF50mm",
				"ExifIFD:FocalLength":50,"ExifIFD:FNumber":1.7,"ExifIFD:ExposureTime":0.000625,
				"ExifIFD:ISO":200,"IFD0:Orientation":6}`,
			want: Exif{CameraMake: "canon", CameraModel: "EOS R5", LensModel: "RF50mm",
				FocalLength: "50.00mm", Aperture: "1.70", ShutterSpeed: "1/1600", ISO: 200, Orientation: 6},
		},
		{
			name: "whole f-number and long exposure",
			json: `{"ExifIFD:FNumber":2,"ExifIFD:ExposureTime":2.5}`,
			want: Exif{Aperture: "2", ShutterSpeed: "2.50"},
		},
		{
			name: "orientation out of range",
			json: `{"IFD0:Orientation":9}`,
			want: Exif{},
		},
		{
			name: "south and west, below sea level",
			json: `{"GPS:GPSLatitude":26.5,"GPS:GPSLatitudeRef":"S","GPS:GPSLongitude":80.25,
				"GPS:GPSLongitudeRef":"W","GPS:GPSAltitude":12.5,"GPS:GPSAltitudeRef":1}`,
			want: Exif{Latitude: -26.5, Longitude: -80.25, Altitude: -12.5, HasAltitude: true},
		},
		{
			name: "latitude out of range",
			json: `{"GPS:GPSLatitude":91,"GPS:GPSLongitude":10}`,
			want: Exif{Longitude: 10},
		},
		{
			name: "capture date with offset",
			json: `{"ExifIFD:DateTimeOriginal":"2024:01:02 03:04:05","ExifIFD:OffsetTimeOriginal":"+02:00"}`,
			want: Exif{CaptureDate: time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("", 2*3600))},
		},
		{
			name: "create date when the original is unusable",
			json: `{"ExifIFD:DateTimeOriginal":"garbage","ExifIFD:CreateDate":"2024:01:02 03:04:05"}`,
			want: Exif{CaptureDate: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parse([]byte("["+tt.json+"]"), "test.jpg")
			if err != nil {
				t.Fatal(err)
			}
			got := info.Exif
			if !got.CaptureDate.Equal(tt.want.CaptureDate) {
				t.Errorf("CaptureDate = %v, want %v", got.CaptureDate, tt.want.CaptureDate)
			}
			got.CaptureDate, tt.want.CaptureDate = time.Time{}, time.Time{}
			if got != tt.want {
				t.Errorf("Exif = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseIPTC(t *testing.T) {
	tests := []struct {
		name string
		json string
		want IPTC
	}{
		{
			name: "XMP wins over IPTC-IIM",
			json: `{"XMP-dc:Title":"xmp title","IPTC:ObjectName":"iim title","IPTC:Caption-Abstract":" iim caption ",
				"XMP-dc:Creator":["Ann"," ","Bob"],"IPTC:By-line":"Carl"}`,
			want: IPTC{Title: "xmp title", Description: "iim caption", Creator: "Ann; Bob"},
		},
		{
			name: "blank XMP falls back",
			json: `{"XMP-dc:Rights":"  ","IPTC:CopyrightNotice":"(c) Me","XMP-photoshop:Credit":"Cred","IPTC:Source":"Src"}`,
			want: IPTC{Copyright: "(c) Me", Credit: "Cred", Source: "Src"},
		},
		{
			name: "keywords trimmed, deduplicated, never split",
			json: `{"XMP-dc:Subject":["b, c"," a ","b, c",""],"IPTC:Keywords":["iim"]}`,
			want: IPTC{Keywords: []string{"b, c", "a"}},
		},
		{
			name: "an empty XMP list overrides IPTC-IIM",
			json: `{"XMP-dc:Subject":[],"IPTC:Keywords":["iim"]}`,
			want: IPTC{},
		},
		{
			name: "a single keyword",
			json: `{"IPTC:Keywords":"solo"}`,
			want: IPTC{Keywords: []string{"solo"}},
		},
		{
			name: "IPTC date and time",
			json: `{"IPTC:DateCreated":"2023:05:06","IPTC:TimeCreated":"07:08:09+01:00"}`,
			want: IPTC{Created: time.Date(2023, 5, 6, 7, 8, 9, 0, time.FixedZone("", 3600))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parse([]byte("["+tt.json+"]"), "test.jpg")
			if err != nil {
				t.Fatal(err)
			}
			got := info.IPTC
			if !slices.Equal(got.Keywords, tt.want.Keywords) {
				t.Errorf("Keywords = %q, want %q", got.Keywords, tt.want.Keywords)
			}
			if !got.Created.Equal(tt.want.Created) {
				t.Errorf("Created = %v, want %v", got.Created, tt.want.Created)
			}
			if g, w := textFields(got), textFields(tt.want); g != w {
				t.Errorf("text fields = %q, want %q", g, w)
			}
		})
	}
}

func TestParseXMP(t *testing.T) {
	const packet = `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description xmlns:exif="http://ns.adobe.com/exif/1.0/" exif:GPSLatitude="26,34.951N">` +
		`<exif:GPSLongitude>80,12.014W</exif:GPSLongitude></rdf:Description></rdf:RDF></x:xmpmeta>`

	info, err := parse([]byte(`[{"XMP:XMP":`+quote(packet)+`}]`), "test.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if string(info.XMP.Packet) != packet {
		t.Errorf("Packet = %q, want the packet unchanged", info.XMP.Packet)
	}
	if info.XMP.GPSLatitude != "26,34.951N" || info.XMP.GPSLongitude != "80,12.014W" {
		t.Errorf("GPS strings = %q, %q", info.XMP.GPSLatitude, info.XMP.GPSLongitude)
	}
	// With no EXIF position, the XMP one is used.
	if info.Exif.Latitude < 26.58 || info.Exif.Latitude > 26.59 || info.Exif.Longitude > -80.2 || info.Exif.Longitude < -80.21 {
		t.Errorf("position = %v, %v, want about 26.5825, -80.2002", info.Exif.Latitude, info.Exif.Longitude)
	}

	// An EXIF position wins.
	info, err = parse([]byte(`[{"GPS:GPSLatitude":1,"GPS:GPSLongitude":2,"XMP:XMP":`+quote(packet)+`}]`), "test.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if info.Exif.Latitude != 1 || info.Exif.Longitude != 2 {
		t.Errorf("position = %v, %v, want 1, 2", info.Exif.Latitude, info.Exif.Longitude)
	}

	// A packet that is not plain text arrives as base64.
	info, err = parse([]byte(`[{"XMP:XMP":"base64:PHg+PC94Pg=="}]`), "test.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if string(info.XMP.Packet) != "<x></x>" {
		t.Errorf("Packet = %q, want <x></x>", info.XMP.Packet)
	}
}

func TestParseErrors(t *testing.T) {
	for _, out := range []string{
		`not json`,
		`[]`,
		`[{},{}]`,
		`[null]`,
		`[{"ExifTool:Error":"File format error"}]`,
		`[{"XMP:XMP":42}]`,
		`[{"XMP:XMP":"base64:!!"}]`,
	} {
		if _, err := parse([]byte(out), "test.jpg"); err == nil {
			t.Errorf("parse(%s) succeeded, want an error", out)
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s) //nolint:errcheck // a string always encodes
	return string(b)
}

// textFields returns the text fields of i in a comparable form.
func textFields(i IPTC) [6]string {
	return [6]string{i.Title, i.Description, i.Creator, i.Copyright, i.Credit, i.Source}
}
