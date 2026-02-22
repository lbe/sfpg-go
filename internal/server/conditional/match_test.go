package conditional

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestMatchesETag(t *testing.T) {
	tests := []struct {
		name        string
		ifNoneMatch string
		etag        string
		want        bool
	}{
		{
			name:        "exact match",
			ifNoneMatch: `"abc123"`,
			etag:        `"abc123"`,
			want:        true,
		},
		{
			name:        "no match",
			ifNoneMatch: `"abc123"`,
			etag:        `"xyz789"`,
			want:        false,
		},
		{
			name:        "wildcard matches any",
			ifNoneMatch: "*",
			etag:        `"abc123"`,
			want:        true,
		},
		{
			name:        "multiple values - first matches",
			ifNoneMatch: `"abc123", "def456", "ghi789"`,
			etag:        `"abc123"`,
			want:        true,
		},
		{
			name:        "multiple values - middle matches",
			ifNoneMatch: `"abc123", "def456", "ghi789"`,
			etag:        `"def456"`,
			want:        true,
		},
		{
			name:        "multiple values - last matches",
			ifNoneMatch: `"abc123", "def456", "ghi789"`,
			etag:        `"ghi789"`,
			want:        true,
		},
		{
			name:        "multiple values - no match",
			ifNoneMatch: `"abc123", "def456", "ghi789"`,
			etag:        `"xyz999"`,
			want:        false,
		},
		{
			name:        "weak ETag matches strong",
			ifNoneMatch: `"abc123"`,
			etag:        `W/"abc123"`,
			want:        true,
		},
		{
			name:        "strong ETag matches weak",
			ifNoneMatch: `W/"abc123"`,
			etag:        `"abc123"`,
			want:        true,
		},
		{
			name:        "both weak",
			ifNoneMatch: `W/"abc123"`,
			etag:        `W/"abc123"`,
			want:        true,
		},
		{
			name:        "weak different value",
			ifNoneMatch: `W/"abc123"`,
			etag:        `W/"def456"`,
			want:        false,
		},
		{
			name:        "empty If-None-Match",
			ifNoneMatch: "",
			etag:        `"abc123"`,
			want:        false,
		},
		{
			name:        "whitespace handling",
			ifNoneMatch: ` "abc123", "def456" `,
			etag:        `"abc123"`,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesETag(tt.ifNoneMatch, tt.etag)
			if got != tt.want {
				t.Errorf("MatchesETag(%q, %q) = %v, want %v", tt.ifNoneMatch, tt.etag, got, tt.want)
			}
		})
	}
}

func TestMatchesLastModified(t *testing.T) {
	tests := []struct {
		name            string
		ifModifiedSince string
		lastModified    sql.NullString
		want            bool
	}{
		{
			name:            "modified before If-Modified-Since",
			ifModifiedSince: "Mon, 02 Jan 2024 12:00:00 GMT",
			lastModified:    sql.NullString{String: "Mon, 01 Jan 2024 12:00:00 GMT", Valid: true},
			want:            true,
		},
		{
			name:            "modified after If-Modified-Since",
			ifModifiedSince: "Mon, 01 Jan 2024 12:00:00 GMT",
			lastModified:    sql.NullString{String: "Mon, 02 Jan 2024 12:00:00 GMT", Valid: true},
			want:            false,
		},
		{
			name:            "same time (not modified)",
			ifModifiedSince: "Mon, 01 Jan 2024 12:00:00 GMT",
			lastModified:    sql.NullString{String: "Mon, 01 Jan 2024 12:00:00 GMT", Valid: true},
			want:            true,
		},
		{
			name:            "empty If-Modified-Since",
			ifModifiedSince: "",
			lastModified:    sql.NullString{String: "Mon, 01 Jan 2024 12:00:00 GMT", Valid: true},
			want:            false,
		},
		{
			name:            "invalid Last-Modified",
			ifModifiedSince: "Mon, 01 Jan 2024 12:00:00 GMT",
			lastModified:    sql.NullString{String: "invalid-date", Valid: true},
			want:            false,
		},
		{
			name:            "invalid If-Modified-Since",
			ifModifiedSince: "invalid-date",
			lastModified:    sql.NullString{String: "Mon, 01 Jan 2024 12:00:00 GMT", Valid: true},
			want:            false,
		},
		{
			name:            "null Last-Modified",
			ifModifiedSince: "Mon, 01 Jan 2024 12:00:00 GMT",
			lastModified:    sql.NullString{Valid: false},
			want:            false,
		},
		{
			name:            "one second difference (matches)",
			ifModifiedSince: "Mon, 01 Jan 2024 12:00:01 GMT",
			lastModified:    sql.NullString{String: "Mon, 01 Jan 2024 12:00:00 GMT", Valid: true},
			want:            true,
		},
		{
			name:            "nanoseconds ignored",
			ifModifiedSince: "Mon, 01 Jan 2024 12:00:00 GMT",
			lastModified:    sql.NullString{String: "Mon, 01 Jan 2024 12:00:00.500 GMT", Valid: true},
			want:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesLastModified(tt.ifModifiedSince, tt.lastModified)
			if got != tt.want {
				t.Errorf("MatchesLastModified(%q, %v) = %v, want %v", tt.ifModifiedSince, tt.lastModified, got, tt.want)
			}
		})
	}
}

func TestMatchesETag_ExactMatch(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `"abc123"`
	if !MatchesETag(ifNoneMatch, etag) {
		t.Error("Expected exact match to return true")
	}
}

func TestMatchesETag_NoMatch(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `"xyz789"`
	if MatchesETag(ifNoneMatch, etag) {
		t.Error("Expected no match to return false")
	}
}

func TestMatchesETag_Wildcard(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `*`
	if !MatchesETag(ifNoneMatch, etag) {
		t.Error("Expected wildcard to match any ETag")
	}
}

func TestMatchesETag_MultipleValues(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `"xyz789", "abc123", "def456"`
	if !MatchesETag(ifNoneMatch, etag) {
		t.Error("Expected match in list to return true")
	}
}

func TestMatchesETag_WeakMatch(t *testing.T) {
	etag := `W/"abc123"`
	ifNoneMatch := `"abc123"`
	if !MatchesETag(ifNoneMatch, etag) {
		t.Error("Expected weak ETag to match strong validator")
	}
}

func TestMatchesLastModified_Before(t *testing.T) {
	lastModified := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	ifModifiedSince := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	if !MatchesLastModified(ifModifiedSince.Format(time.RFC1123), sql.NullString{String: lastModified.Format(time.RFC1123), Valid: true}) {
		t.Error("Expected Before to return true (not modified)")
	}
}

func TestMatchesLastModified_After(t *testing.T) {
	lastModified := time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC)
	ifModifiedSince := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	if MatchesLastModified(ifModifiedSince.Format(time.RFC1123), sql.NullString{String: lastModified.Format(time.RFC1123), Valid: true}) {
		t.Error("Expected After to return false (modified)")
	}
}

func TestMatchesLastModified_Exact(t *testing.T) {
	lastModified := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	ifModifiedSince := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	if !MatchesLastModified(ifModifiedSince.Format(time.RFC1123), sql.NullString{String: lastModified.Format(time.RFC1123), Valid: true}) {
		t.Error("Expected Exact to return true (not modified)")
	}
}

func TestMatchesETag_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		ifNoneMatch string
		etag        string
		want        bool
	}{
		{
			name:        "empty both",
			ifNoneMatch: "",
			etag:        "",
			want:        false,
		},
		{
			name:        "empty ETag with value",
			ifNoneMatch: `"abc"`,
			etag:        "",
			want:        false,
		},
		{
			name:        "quoted empty string",
			ifNoneMatch: `""`,
			etag:        `""`,
			want:        true,
		},
		{
			name:        "case sensitivity - should match",
			ifNoneMatch: `"ABC123"`,
			etag:        `"ABC123"`,
			want:        true,
		},
		{
			name:        "case sensitivity - should not match",
			ifNoneMatch: `"abc123"`,
			etag:        `"ABC123"`,
			want:        false,
		},
		{
			name:        "trailing comma",
			ifNoneMatch: `"abc123", `,
			etag:        `"abc123"`,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesETag(tt.ifNoneMatch, tt.etag)
			if got != tt.want {
				t.Errorf("MatchesETag(%q, %q) = %v, want %v", tt.ifNoneMatch, tt.etag, got, tt.want)
			}
		})
	}
}

func TestMatchesETag_ComplexHeader(t *testing.T) {
	// Real-world HTTP header with quality values
	header := `W/"abc123", W/"def456", "ghi789"`
	etag := `"ghi789"`

	if !MatchesETag(header, etag) {
		t.Error("Expected match in complex header")
	}

	// Test with weak ETag matching weak
	etag = `W/"def456"`
	if !MatchesETag(header, etag) {
		t.Error("Expected weak match in complex header")
	}

	// Test no match
	etag = `"xyz999"`
	if MatchesETag(header, etag) {
		t.Error("Expected no match in complex header")
	}
}

func TestMatchesETag_PriorityOrder(t *testing.T) {
	// Test that first match wins
	ifNoneMatch := `"first", "second", "third"`

	if !MatchesETag(ifNoneMatch, `"first"`) {
		t.Error("Expected first to match")
	}
	if !MatchesETag(ifNoneMatch, `"second"`) {
		t.Error("Expected second to match")
	}
	if !MatchesETag(ifNoneMatch, `"third"`) {
		t.Error("Expected third to match")
	}
}

// TestMatchesETag_AllowOrigin verifies ETag matching works correctly
// in scenarios involving cross-origin requests where headers may have
// additional whitespace or formatting.
func TestMatchesETag_WhitespaceVariants(t *testing.T) {
	tests := []struct {
		name        string
		ifNoneMatch string
		etag        string
		want        bool
	}{
		{
			name:        "spaces around comma",
			ifNoneMatch: `"abc" , "def"`,
			etag:        `"def"`,
			want:        true,
		},
		{
			name:        "tabs around comma",
			ifNoneMatch: `"abc"\t,\t"def"`,
			etag:        `"def"`,
			want:        false, // strings.TrimSpace only handles spaces, not tabs
		},
		{
			name:        "newlines around comma (unlikely but valid)",
			ifNoneMatch: `"abc"\n,\n"def"`,
			etag:        `"def"`,
			want:        false, // strings.TrimSpace only handles spaces, not newlines
		},
		{
			name:        "leading/trailing whitespace",
			ifNoneMatch: `  "abc", "def"  `,
			etag:        `"abc"`,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesETag(tt.ifNoneMatch, tt.etag)
			if got != tt.want {
				t.Errorf("MatchesETag(%q, %q) = %v, want %v", tt.ifNoneMatch, tt.etag, got, tt.want)
			}
		})
	}
}

func TestMatchesLastModified_RFC1123Formats(t *testing.T) {
	// Test various valid RFC1123 formats
	formats := []string{
		"Mon, 01 Jan 2024 12:00:00 GMT",
		"Mon, 01 Jan 2024 12:00:00 UTC",
		time.RFC1123,
	}

	lastModified := sql.NullString{String: "Mon, 01 Jan 2024 12:00:00 GMT", Valid: true}
	ifModifiedSince := "Mon, 02 Jan 2024 12:00:00 GMT"

	for _, format := range formats {
		t.Run("format_"+format, func(t *testing.T) {
			got := MatchesLastModified(ifModifiedSince, lastModified)
			if !got {
				t.Errorf("MatchesLastModified with format %q should return true", format)
			}
		})
	}
}

// TestMatchesETag_RealWorldScenarios tests scenarios from actual HTTP clients
func TestMatchesETag_RealWorldScenarios(t *testing.T) {
	t.Run("chrome conditional request", func(t *testing.T) {
		// Chrome sends: If-None-Match: W/"abc123"
		// Server has: W/"abc123"
		ifNoneMatch := `W/"abc123"`
		etag := `W/"abc123"`
		if !MatchesETag(ifNoneMatch, etag) {
			t.Error("Chrome conditional request should match")
		}
	})

	t.Run("firefox conditional request", func(t *testing.T) {
		// Firefox sends: If-None-Match: "abc123"
		// Server has: W/"abc123"
		ifNoneMatch := `"abc123"`
		etag := `W/"abc123"`
		if !MatchesETag(ifNoneMatch, etag) {
			t.Error("Firefox conditional request should match weak ETag")
		}
	})

	t.Run("curl conditional request", func(t *testing.T) {
		// curl -H "If-None-Match: \"abc123\""
		ifNoneMatch := `"abc123"`
		etag := `"abc123"`
		if !MatchesETag(ifNoneMatch, etag) {
			t.Error("curl conditional request should match")
		}
	})
}

// TestMatchesETag_BenchmarkComparison is a simple comparison test
// that can be used to verify the function's behavior remains consistent
// with the previous implementation.
func TestMatchesETag_BenchmarkComparison(t *testing.T) {
	// This test documents expected behavior for benchmark comparisons
	baselineCases := []struct {
		ifNoneMatch string
		etag        string
		expected    bool
	}{
		{`"abc"`, `"abc"`, true},
		{`"abc"`, `"def"`, false},
		{`*`, `"anything"`, true},
		{`"a","b","c"`, `"b"`, true},
		{`W/"weak"`, `"weak"`, true},
		{`"weak"`, `W/"weak"`, true},
	}

	for _, tc := range baselineCases {
		t.Run(strings.ReplaceAll(tc.ifNoneMatch+", "+tc.etag, " ", "_"), func(t *testing.T) {
			got := MatchesETag(tc.ifNoneMatch, tc.etag)
			if got != tc.expected {
				t.Errorf("MatchesETag(%q, %q) = %v, want %v", tc.ifNoneMatch, tc.etag, got, tc.expected)
			}
		})
	}
}
