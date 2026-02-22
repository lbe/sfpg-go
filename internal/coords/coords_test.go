package coords

import (
	"math"
	"testing"
)

func TestParseCoordinate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		wantErr  bool
	}{
		// Decimal formats
		{
			name:     "positive decimal",
			input:    "40.7128",
			expected: 40.7128,
			wantErr:  false,
		},
		{
			name:     "negative decimal",
			input:    "-74.0060",
			expected: -74.0060,
			wantErr:  false,
		},
		{
			name:     "decimal with spaces",
			input:    "  51.5074  ",
			expected: 51.5074,
			wantErr:  false,
		},

		// DMS formats (degrees, minutes, seconds)
		{
			name:     "DMS with N",
			input:    "40°42'46\"N",
			expected: 40.712777777777777,
			wantErr:  false,
		},
		{
			name:     "DMS with W",
			input:    "74°0'21\"W",
			expected: -74.00583333333333,
			wantErr:  false,
		},
		{
			name:     "DMS with S",
			input:    "33°51'54\"S",
			expected: -33.865,
			wantErr:  false,
		},
		{
			name:     "DMS with E",
			input:    "151°12'26\"E",
			expected: 151.20722222222223,
			wantErr:  false,
		},
		{
			name:     "DMS with spaces",
			input:    "40° 42' 46\" N",
			expected: 40.712777777777777,
			wantErr:  false,
		},
		{
			name:     "DMS with decimal seconds",
			input:    "40°42'46.8\"N",
			expected: 40.713,
			wantErr:  false,
		},

		// DM formats (degrees, minutes)
		{
			name:     "DM with N",
			input:    "40°42.767'N",
			expected: 40.71278333333333,
			wantErr:  false,
		},
		{
			name:     "DM with W",
			input:    "74°0.35'W",
			expected: -74.00583333333333,
			wantErr:  false,
		},
		{
			name:     "DM with spaces",
			input:    "51° 30.45' N",
			expected: 51.5075,
			wantErr:  false,
		},

		// Decimal with compass direction
		{
			name:     "decimal with N",
			input:    "40.7128N",
			expected: 40.7128,
			wantErr:  false,
		},
		{
			name:     "decimal with W",
			input:    "74.0060W",
			expected: -74.0060,
			wantErr:  false,
		},
		{
			name:     "decimal with S",
			input:    "33.865S",
			expected: -33.865,
			wantErr:  false,
		},
		{
			name:     "decimal with E",
			input:    "151.2072E",
			expected: 151.2072,
			wantErr:  false,
		},
		{
			name:     "decimal with space and N",
			input:    "40.7128 N",
			expected: 40.7128,
			wantErr:  false,
		},

		// Edge cases
		{
			name:     "zero",
			input:    "0",
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "zero with direction",
			input:    "0°N",
			expected: 0,
			wantErr:  false,
		},

		// Error cases
		{
			name:     "empty string",
			input:    "",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "invalid format",
			input:    "invalid",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "just letters",
			input:    "ABC",
			expected: 0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseCoordinate(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf(
						"ParseCoordinate() expected error but got none",
					)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseCoordinate() unexpected error: %v", err)
				return
			}

			// Use approximate comparison for floating point
			if !almostEqual(result, tt.expected, 0.000001) {
				t.Errorf(
					"ParseCoordinate() = %v, want %v",
					result,
					tt.expected,
				)
			}
		})
	}
}

func TestParseCoordinatePairs(t *testing.T) {
	// Test common coordinate pairs to ensure consistency
	pairs := []struct {
		name string
		lat  string
		lon  string
		eLat float64
		eLon float64
	}{
		{
			name: "New York City",
			lat:  "40°42'46\"N",
			lon:  "74°0'21\"W",
			eLat: 40.712777777777777,
			eLon: -74.00583333333333,
		},
		{
			name: "Sydney",
			lat:  "33°51'54\"S",
			lon:  "151°12'26\"E",
			eLat: -33.865,
			eLon: 151.20722222222223,
		},
		{
			name: "London",
			lat:  "51.5074",
			lon:  "-0.1278",
			eLat: 51.5074,
			eLon: -0.1278,
		},
	}

	for _, tt := range pairs {
		t.Run(tt.name, func(t *testing.T) {
			lat, err := ParseCoordinate(tt.lat)
			if err != nil {
				t.Errorf("Failed to parse latitude: %v", err)
			}

			lon, err := ParseCoordinate(tt.lon)
			if err != nil {
				t.Errorf("Failed to parse longitude: %v", err)
			}

			if !almostEqual(lat, tt.eLat, 0.000001) {
				t.Errorf("Latitude = %v, want %v", lat, tt.eLat)
			}

			if !almostEqual(lon, tt.eLon, 0.000001) {
				t.Errorf("Longitude = %v, want %v", lon, tt.eLon)
			}
		})
	}
}

// almostEqual checks if two float64 values are approximately equal
func almostEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}
