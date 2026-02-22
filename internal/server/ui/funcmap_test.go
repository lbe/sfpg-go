package ui

import (
	"bytes"
	"html/template"
	"testing"
	"time"

	"go.local/sfpg/internal/server/metrics"
	"go.local/sfpg/web"
)

// TestFuncMap_Plus tests the plus function in funcMap
func TestFuncMap_Plus(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{"positive numbers", 5, 3, 8},
		{"negative numbers", -5, -3, -8},
		{"mixed", 5, -3, 2},
		{"zero", 0, 0, 0},
		{"large numbers", 1000000, 2000000, 3000000},
	}

	plusFunc := funcMap["plus"].(func(int, int) int)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := plusFunc(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("plus(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_Sub tests the sub function in funcMap
func TestFuncMap_Sub(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{"positive numbers", 5, 3, 2},
		{"negative numbers", -5, -3, -2},
		{"mixed", 5, -3, 8},
		{"zero", 0, 0, 0},
		{"result negative", 3, 5, -2},
	}

	subFunc := funcMap["sub"].(func(int, int) int)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subFunc(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("sub(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_FormatUnix tests the formatUnix function in funcMap
func TestFuncMap_FormatUnix(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		ts       int64
		contains string
	}{
		{"zero timestamp", 0, "1970"},
		{"specific timestamp", 1640995200, "2022"}, // Jan 1, 2022
		{"negative timestamp", -1, "1969"},
	}

	formatUnixFunc := funcMap["formatUnix"].(func(int64) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatUnixFunc(tt.ts)
			if result == "" {
				t.Errorf("formatUnix(%d) returned empty string", tt.ts)
			}
			// Verify it's in ANSIC format (contains expected year)
			expected := time.Unix(tt.ts, 0).Format(time.ANSIC)
			if result != expected {
				t.Errorf("formatUnix(%d) = %q, want %q", tt.ts, result, expected)
			}
		})
	}
}

// TestFuncMap_FormatInt tests the formatInt function in funcMap
func TestFuncMap_FormatInt(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		input    int64
		expected string
	}{
		{"zero", 0, "0"},
		{"small number", 123, "123"},
		{"thousand", 1000, "1,000"},
		{"million", 1000000, "1,000,000"},
		{"negative", -1000, "-1,000"},
	}

	formatIntFunc := funcMap["formatInt"].(func(int64) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatIntFunc(tt.input)
			if result != tt.expected {
				t.Errorf("formatInt(%d) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_FormatCount tests the formatCount function with various types
func TestFuncMap_FormatCount(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"int", int(1000), "1,000"},
		{"int8", int8(127), "127"},
		{"int16", int16(1000), "1,000"},
		{"int32", int32(100000), "100,000"},
		{"int64", int64(1000000), "1,000,000"},
		{"uint", uint(1000), "1,000"},
		{"uint8", uint8(255), "255"},
		{"uint16", uint16(1000), "1,000"},
		{"uint32", uint32(100000), "100,000"},
		{"uint64", uint64(1000000), "1,000,000"},
		{"float32", float32(1234.5), "1,234.5"},
		{"float64", float64(1234567.89), "1,234,567.89"},
		{"string", "test", "test"},
		{"bool", true, "true"},
	}

	formatCountFunc := funcMap["formatCount"].(func(any) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatCountFunc(tt.input)
			if result != tt.expected {
				t.Errorf("formatCount(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_WriteBatcherQueuePercent tests the writeBatcherQueuePercent function
func TestFuncMap_WriteBatcherQueuePercent(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		wb       metrics.WriteBatcherMetrics
		expected int
	}{
		{
			name: "50% full",
			wb: metrics.WriteBatcherMetrics{
				PendingCount: 50,
				ChannelSize:  100,
			},
			expected: 50,
		},
		{
			name: "100% full",
			wb: metrics.WriteBatcherMetrics{
				PendingCount: 100,
				ChannelSize:  100,
			},
			expected: 100,
		},
		{
			name: "zero capacity",
			wb: metrics.WriteBatcherMetrics{
				PendingCount: 10,
				ChannelSize:  0,
			},
			expected: 0,
		},
		{
			name: "empty",
			wb: metrics.WriteBatcherMetrics{
				PendingCount: 0,
				ChannelSize:  100,
			},
			expected: 0,
		},
	}

	wbFunc := funcMap["writeBatcherQueuePercent"].(func(metrics.WriteBatcherMetrics) int)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wbFunc(tt.wb)
			if result != tt.expected {
				t.Errorf("writeBatcherQueuePercent(%+v) = %d, want %d", tt.wb, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_QueueUtilizationPercent tests the queueUtilizationPercent function
func TestFuncMap_QueueUtilizationPercent(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		length   int
		capacity int
		expected int
	}{
		{"50% full", 50, 100, 50},
		{"100% full", 100, 100, 100},
		{"zero capacity", 10, 0, 0},
		{"empty", 0, 100, 0},
		{"75% full", 75, 100, 75},
	}

	queueFunc := funcMap["queueUtilizationPercent"].(func(int, int) int)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := queueFunc(tt.length, tt.capacity)
			if result != tt.expected {
				t.Errorf("queueUtilizationPercent(%d, %d) = %d, want %d", tt.length, tt.capacity, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_QueueUtilizationColor tests the queueUtilizationColor function
func TestFuncMap_QueueUtilizationColor(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		length   int
		capacity int
		expected string
	}{
		{"under 50% - success", 40, 100, "progress-success"},
		{"exactly 50% - warning", 50, 100, "progress-warning"},
		{"under 80% - warning", 70, 100, "progress-warning"},
		{"exactly 80% - error", 80, 100, "progress-error"},
		{"over 80% - error", 90, 100, "progress-error"},
		{"zero capacity", 10, 0, "progress"},
		{"empty", 0, 100, "progress-success"},
	}

	colorFunc := funcMap["queueUtilizationColor"].(func(int, int) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := colorFunc(tt.length, tt.capacity)
			if result != tt.expected {
				t.Errorf("queueUtilizationColor(%d, %d) = %q, want %q", tt.length, tt.capacity, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_HttpCacheUtilizationPercent tests the httpCacheUtilizationPercent function
func TestFuncMap_HttpCacheUtilizationPercent(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		cache    metrics.HTTPCacheMetrics
		expected int
	}{
		{
			name: "50% full",
			cache: metrics.HTTPCacheMetrics{
				SizeBytes:    50,
				MaxTotalSize: 100,
			},
			expected: 50,
		},
		{
			name: "100% full",
			cache: metrics.HTTPCacheMetrics{
				SizeBytes:    100,
				MaxTotalSize: 100,
			},
			expected: 100,
		},
		{
			name: "zero max size",
			cache: metrics.HTTPCacheMetrics{
				SizeBytes:    50,
				MaxTotalSize: 0,
			},
			expected: 0,
		},
		{
			name: "empty",
			cache: metrics.HTTPCacheMetrics{
				SizeBytes:    0,
				MaxTotalSize: 100,
			},
			expected: 0,
		},
	}

	cacheFunc := funcMap["httpCacheUtilizationPercent"].(func(metrics.HTTPCacheMetrics) int)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cacheFunc(tt.cache)
			if result != tt.expected {
				t.Errorf("httpCacheUtilizationPercent(%+v) = %d, want %d", tt.cache, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_CacheVersion tests the cacheVersion function
func TestFuncMap_CacheVersion(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Set a known cache version
	SetCacheVersion("test-version-123")

	cacheVersionFunc := funcMap["cacheVersion"].(func() string)
	result := cacheVersionFunc()
	if result != "test-version-123" {
		t.Errorf("cacheVersion() = %q, want %q", result, "test-version-123")
	}
}

// TestFuncMap_FormatBytes tests formatBytes function from metrics
func TestFuncMap_FormatBytes(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name  string
		input uint64
	}{
		{"zero bytes", 0},
		{"bytes", 500},
		{"kilobytes", 1024},
		{"megabytes", 1024 * 1024},
		{"gigabytes", 1024 * 1024 * 1024},
	}

	formatBytesFunc := funcMap["formatBytes"].(func(uint64) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatBytesFunc(tt.input)
			if result == "" {
				t.Errorf("formatBytes(%d) returned empty string", tt.input)
			}
			// Just verify it returns something
			t.Logf("formatBytes(%d) = %q", tt.input, result)
		})
	}
}

// TestFuncMap_FormatBytesInt64 tests formatBytesInt64 function
func TestFuncMap_FormatBytesInt64(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name  string
		input int64
	}{
		{"zero bytes", 0},
		{"bytes", 500},
		{"kilobytes", 1024},
		{"megabytes", 1024 * 1024},
	}

	formatBytesInt64Func := funcMap["formatBytesInt64"].(func(int64) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatBytesInt64Func(tt.input)
			if result == "" {
				t.Errorf("formatBytesInt64(%d) returned empty string", tt.input)
			}
			t.Logf("formatBytesInt64(%d) = %q", tt.input, result)
		})
	}
}

// TestFuncMap_FormatDuration tests formatDuration function
func TestFuncMap_FormatDuration(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name  string
		input time.Duration
	}{
		{"zero", 0},
		{"milliseconds", 1 * time.Millisecond},
		{"seconds", 1 * time.Second},
		{"minutes", 1 * time.Minute},
		{"hours", 1 * time.Hour},
		{"negative", -1 * time.Second},
	}

	formatDurationFunc := funcMap["formatDuration"].(func(time.Duration) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDurationFunc(tt.input)
			if result == "" {
				t.Errorf("formatDuration(%v) returned empty string", tt.input)
			}
			t.Logf("formatDuration(%v) = %q", tt.input, result)
		})
	}
}

// TestFuncMap_Basename tests the basename function
func TestFuncMap_Basename(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple path", "/path/to/file.txt", "file.txt"},
		{"no path", "file.txt", "file.txt"},
		{"empty", "", "."},
		{"root", "/", "/"},
	}

	basenameFunc := funcMap["basename"].(func(string) string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := basenameFunc(tt.input)
			if result != tt.expected {
				t.Errorf("basename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestFuncMap_InTemplate tests that all funcMap functions work in actual templates
func TestFuncMap_InTemplate(t *testing.T) {
	// Initialize templates to populate funcMap
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Test template that uses various functions
	tmplStr := `
{{- $sum := plus 5 3 -}}
{{- $diff := sub 10 4 -}}
{{- $formatted := formatInt 1000000 -}}
{{- $count := formatCount 5000 -}}
{{- $hash := escapeHash "test#value" -}}
{{- $basename := basename "/path/to/file.txt" -}}
{{- $basenameNoExt := basenameWithoutExt "/path/to/file.txt" -}}
Sum: {{$sum}}
Diff: {{$diff}}
Formatted: {{$formatted}}
Count: {{$count}}
Hash: {{$hash}}
Basename: {{$basename}}
BasenameNoExt: {{$basenameNoExt}}
`

	tmpl, err := template.New("test").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		t.Fatalf("Failed to parse test template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatalf("Failed to execute test template: %v", err)
	}

	output := buf.String()

	// Verify expected values in output
	expectedPairs := map[string]string{
		"Sum: 8":               "plus function",
		"Diff: 6":              "sub function",
		"Formatted: 1,000,000": "formatInt function",
		"Count: 5,000":         "formatCount function",
		"Hash: test%23value":   "escapeHash function",
		"Basename: file.txt":   "basename function",
		"BasenameNoExt: file":  "basenameWithoutExt function",
	}

	for expected, funcName := range expectedPairs {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("Output missing expected value for %s: %q\nOutput:\n%s", funcName, expected, output)
		}
	}
}
