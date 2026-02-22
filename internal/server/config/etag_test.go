package config

import (
	"testing"
	"time"
)

func TestIncrementETagVersion(t *testing.T) {
	// Get today's date in YYYYMMDD format for assertions
	today := time.Now().Format("20060102")

	tests := []struct {
		name     string
		current  string
		expected string
	}{
		{
			name:     "same date increments suffix",
			current:  today + "-01",
			expected: today + "-02",
		},
		{
			name:     "same date increments from 09 to 10",
			current:  today + "-09",
			expected: today + "-10",
		},
		{
			name:     "old date resets to today-01",
			current:  "20260101-99",
			expected: today + "-01",
		},
		{
			name:     "invalid format defaults to today-01",
			current:  "invalid-format",
			expected: today + "-01",
		},
		{
			name:     "empty string defaults to today-01",
			current:  "",
			expected: today + "-01",
		},
		{
			name:     "malformed suffix defaults to today-01",
			current:  today + "-abc",
			expected: today + "-01",
		},
		{
			name:     "missing suffix defaults to today-01",
			current:  today,
			expected: today + "-01",
		},
		{
			name:     "preserves v prefix if present",
			current:  "v" + today + "-05",
			expected: "v" + today + "-06",
		},
		{
			name:     "old date with v prefix resets",
			current:  "v20260101-99",
			expected: "v" + today + "-01",
		},
		{
			name:     "future date resets to today-01",
			current:  "20300101-01",
			expected: today + "-01",
		},
		{
			name:     "future date with v prefix resets to today-01",
			current:  "v20300101-01",
			expected: "v" + today + "-01",
		},
		{
			name:     "invalid prefix resets to today-01 without prefix",
			current:  "x20260130-01",
			expected: today + "-01",
		},
		{
			name:     "v prefix with invalid format resets and keeps v",
			current:  "v-invalid",
			expected: "v" + today + "-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IncrementETagVersion(tt.current)
			if result != tt.expected {
				t.Errorf("IncrementETagVersion(%q) = %q, want %q",
					tt.current, result, tt.expected)
			}
		})
	}
}

func TestParseETagVersion(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantPrefix string
		wantDate   string
		wantNumber int
		wantValid  bool
	}{
		{
			name:       "valid without prefix",
			input:      "20260129-05",
			wantPrefix: "",
			wantDate:   "20260129",
			wantNumber: 5,
			wantValid:  true,
		},
		{
			name:       "valid with v prefix",
			input:      "v20260129-05",
			wantPrefix: "v",
			wantDate:   "20260129",
			wantNumber: 5,
			wantValid:  true,
		},
		{
			name:      "invalid format",
			input:     "invalid",
			wantValid: false,
		},
		{
			name:      "empty string",
			input:     "",
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, date, number, valid := parseETagVersion(tt.input)
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
			if valid {
				if prefix != tt.wantPrefix {
					t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
				}
				if date != tt.wantDate {
					t.Errorf("date = %q, want %q", date, tt.wantDate)
				}
				if number != tt.wantNumber {
					t.Errorf("number = %d, want %d", number, tt.wantNumber)
				}
			}
		})
	}
}
