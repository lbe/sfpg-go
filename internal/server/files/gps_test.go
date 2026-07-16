package files

import (
	"math"
	"testing"
)

func TestSetGPSFromStrings_DMS(t *testing.T) {
	f := &File{}
	if err := SetGPSFromStrings(f, "26° 34′ 57.06″ N", "80° 12′ 0.84″ W"); err != nil {
		t.Fatalf("SetGPSFromStrings: %v", err)
	}

	wantLat := 26.582517
	wantLon := -80.200233

	if !f.Exif.Latitude.Valid {
		t.Fatal("Latitude.Valid = false, want true")
	}
	if math.Abs(f.Exif.Latitude.Float64-wantLat) > 1e-4 {
		t.Errorf("Latitude = %v, want %v", f.Exif.Latitude.Float64, wantLat)
	}
	if !f.Exif.Longitude.Valid {
		t.Fatal("Longitude.Valid = false, want true")
	}
	if math.Abs(f.Exif.Longitude.Float64-wantLon) > 1e-4 {
		t.Errorf("Longitude = %v, want %v", f.Exif.Longitude.Float64, wantLon)
	}
}

func TestSetGPSFromStrings_ZeroNotValid(t *testing.T) {
	f := &File{}
	if err := SetGPSFromStrings(f, "0", "0"); err != nil {
		t.Fatalf("SetGPSFromStrings(0,0): %v", err)
	}
	if f.Exif.Latitude.Valid {
		t.Errorf("Latitude.Valid = true for 0,0; want false")
	}
	if f.Exif.Longitude.Valid {
		t.Errorf("Longitude.Valid = true for 0,0; want false")
	}

	if err := SetGPSFromStrings(f, "", "0"); err == nil {
		t.Error("SetGPSFromStrings(\"\", \"0\") = nil, want error")
	}
	if err := SetGPSFromStrings(f, "0", "not-a-number"); err == nil {
		t.Error("SetGPSFromStrings(\"0\", \"not-a-number\") = nil, want error")
	}
}
