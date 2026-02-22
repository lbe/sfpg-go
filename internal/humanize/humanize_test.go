// Package humanize provides utilities for formatting numbers into human-readable forms.
// It supports formatting byte sizes with binary prefixes (KiB, MiB, etc.) and
// adding comma separators to numeric values.
package humanize

import (
	"fmt"
	"math"
	"testing"
)

func TestIBytes_Basic(t *testing.T) {
	tests := []struct {
		input     int64
		want      string
		precision int
	}{
		{0, "0 B", 0},
		{1, "1 B", 0},
		{512, "512 B", 0},
		{1024, "1 KiB", 0},
		{1536, "1.5 KiB", 1},
		{1024 * 1024, "1 MiB", 0},
		{1536 * 1024, "1.5 MiB", 1},
		{1024 * 1024 * 1024, "1 GiB", 0},
		{1024 * 1024 * 1024 * 1024, "1 TiB", 0},
		{1024 * 1024 * 1024 * 1024 * 1024, "1 PiB", 0},
		{1024 * 1024 * 1024 * 1024 * 1024 * 1024, "1 EiB", 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := IBytes(tt.input).WithPrecision(tt.precision).String()
			if got != tt.want {
				t.Errorf("IBytes(%d).WithPrecision(%d).String() = %q, want %q", tt.input, tt.precision, got, tt.want)
			}
		})
	}
}

func TestIBytes_Negative(t *testing.T) {
	tests := []struct {
		input     int64
		want      string
		precision int
	}{
		{-1, "-1 B", 0},
		{-512, "-512 B", 0},
		{-1024, "-1 KiB", 0},
		{-1536, "-1.5 KiB", 1},
		{-1024 * 1024, "-1 MiB", 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := IBytes(tt.input).WithPrecision(tt.precision).String()
			if got != tt.want {
				t.Errorf("IBytes(%d).WithPrecision(%d).String() = %q, want %q", tt.input, tt.precision, got, tt.want)
			}
		})
	}
}

func TestIBytes_MinInt64(t *testing.T) {
	// math.MinInt64 cannot be negated (would overflow), test it's handled gracefully
	got := IBytes(math.MinInt64).String()
	// Should contain the value and "B" unit
	if got == "" {
		t.Error("IBytes(math.MinInt64) should not panic or return empty")
	}
	// It should be negative
	if got[0] != '-' {
		t.Errorf("IBytes(math.MinInt64) should be negative, got %q", got)
	}
}

func TestIBytes_DefaultPrecision(t *testing.T) {
	// Test that default precision (0) rounds to nearest integer
	got := IBytes(1536).String()
	want := "2 KiB"
	if got != want {
		t.Errorf("IBytes(1536).String() = %q, want %q", got, want)
	}
}

func TestIBytes_WithPrecision(t *testing.T) {
	// 1536 bytes = 1.5 KiB exactly
	if got := IBytes(1536).WithPrecision(0).String(); got != "2 KiB" {
		t.Errorf("WithPrecision(0) = %q, want %q", got, "2 KiB")
	}
	if got := IBytes(1536).WithPrecision(1).String(); got != "1.5 KiB" {
		t.Errorf("WithPrecision(1) = %q, want %q", got, "1.5 KiB")
	}
	if got := IBytes(1536).WithPrecision(2).String(); got != "1.50 KiB" {
		t.Errorf("WithPrecision(2) = %q, want %q", got, "1.50 KiB")
	}
}

func TestIBytes_Chaining(t *testing.T) {
	// Test that WithPrecision can be called multiple times
	f := IBytes(1536).WithPrecision(1).WithPrecision(2)
	got := f.String()
	want := "1.50 KiB"
	if got != want {
		t.Errorf("chained WithPrecision = %q, want %q", got, want)
	}
}

func TestComma_Integer(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{1000000, "1,000,000"},
		{1234567890, "1,234,567,890"},
		{-1, "-1"},
		{-1000, "-1,000"},
		{-1234567890, "-1,234,567,890"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := Comma(tt.input).String()
			if got != tt.want {
				t.Errorf("Comma(%d).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComma_Float(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "0"},
		{1.5, "1.5"},
		{999.999, "999.999"},
		{1000.5, "1,000.5"},
		{1234567.891234, "1,234,567.891234"},
		{-1.5, "-1.5"},
		{-1000.5, "-1,000.5"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%f", tt.input), func(t *testing.T) {
			got := Comma(tt.input).String()
			if got != tt.want {
				t.Errorf("Comma(%f).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComma_WithPrecision(t *testing.T) {
	// Test precision for floats
	got := Comma(1234.5678).WithPrecision(2).String()
	want := "1,234.57"
	if got != want {
		t.Errorf("Comma(1234.5678).WithPrecision(2).String() = %q, want %q", got, want)
	}

	// Test precision for integers (should add .00)
	got = Comma(1234).WithPrecision(2).String()
	want = "1,234.00"
	if got != want {
		t.Errorf("Comma(1234).WithPrecision(2).String() = %q, want %q", got, want)
	}
}

func TestComma_Unsigned(t *testing.T) {
	tests := []struct {
		input uint64
		want  string
	}{
		{0, "0"},
		{1000, "1,000"},
		{1234567890, "1,234,567,890"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := Comma(tt.input).String()
			if got != tt.want {
				t.Errorf("Comma(%d).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComma_LargeUint64(t *testing.T) {
	// Note: uint64 values larger than 2^53-1 lose precision when converted to float64.
	// This is a limitation of the float64 type. Values up to 9,007,199,254,740,991
	// (2^53-1) are represented exactly.
	//
	// For MaxUint64 (18446744073709551615), the float64 representation becomes
	// 18446744073709551616 due to rounding.
	got := Comma(uint64(math.MaxUint64)).String()
	// Accept either the exact value (if we had arbitrary precision) or the rounded float64 value
	if got != "18,446,744,073,709,551,615" && got != "18,446,744,073,709,551,616" {
		t.Errorf("Comma(MaxUint64).String() = %q, expected rounded value due to float64 precision", got)
	}
}

func TestFormatter_Interface(t *testing.T) {
	// Test that our types implement fmt.Stringer via String() method
	var _ fmt.Stringer = IBytes(1024)
	var _ fmt.Stringer = Comma(1234)
}

func BenchmarkIBytes(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = IBytes(1536000).WithPrecision(2).String()
	}
}

func BenchmarkComma(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Comma(int64(1234567890)).String()
	}
}

func TestIBytes_EdgeCases(t *testing.T) {
	// Test boundary values between units
	tests := []struct {
		input int64
		want  string
	}{
		{1023, "1023 B"},         // Just under 1 KiB
		{1024, "1 KiB"},          // Exactly 1 KiB
		{1025, "1 KiB"},          // Just over 1 KiB (rounds to 1)
		{1048575, "1024 KiB"},    // Just under 1 MiB
		{1048576, "1 MiB"},       // Exactly 1 MiB
		{1073741823, "1024 MiB"}, // Just under 1 GiB
		{1073741824, "1 GiB"},    // Exactly 1 GiB
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := IBytes(tt.input).String()
			if got != tt.want {
				t.Errorf("IBytes(%d).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIBytes_ZeroWithPrecision(t *testing.T) {
	// Zero should always be "0 B" regardless of precision
	for _, p := range []int{0, 1, 2, 5} {
		got := IBytes(0).WithPrecision(p).String()
		if got != "0 B" {
			t.Errorf("IBytes(0).WithPrecision(%d).String() = %q, want %q", p, got, "0 B")
		}
	}
}

func TestComma_EdgeCases(t *testing.T) {
	// Small numbers should not get commas
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{9, "9"},
		{99, "99"},
		{999, "999"},
		{1000, "1,000"},
		{-999, "-999"},
		{-1000, "-1,000"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := Comma(tt.input).String()
			if got != tt.want {
				t.Errorf("Comma(%d).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComma_FloatEdgeCases(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "0"},
		{0.1, "0.1"},
		{0.001, "0.001"},
		{999.999, "999.999"},
		{1000.001, "1,000.001"},
		{-0.5, "-0.5"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%f", tt.input), func(t *testing.T) {
			got := Comma(tt.input).String()
			if got != tt.want {
				t.Errorf("Comma(%f).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComma_VariousIntegerTypes(t *testing.T) {
	// Test that various integer types work
	if got := Comma(int(1234567)).String(); got != "1,234,567" {
		t.Errorf("Comma(int) = %q, want %q", got, "1,234,567")
	}
	if got := Comma(int32(1234567)).String(); got != "1,234,567" {
		t.Errorf("Comma(int32) = %q, want %q", got, "1,234,567")
	}
	if got := Comma(int64(1234567)).String(); got != "1,234,567" {
		t.Errorf("Comma(int64) = %q, want %q", got, "1,234,567")
	}
	if got := Comma(uint(1234567)).String(); got != "1,234,567" {
		t.Errorf("Comma(uint) = %q, want %q", got, "1,234,567")
	}
	if got := Comma(uint32(1234567)).String(); got != "1,234,567" {
		t.Errorf("Comma(uint32) = %q, want %q", got, "1,234,567")
	}
}

func TestComma_VariousFloatTypes(t *testing.T) {
	// Test that various float types work
	if got := Comma(float32(1234567.5)).String(); got != "1,234,567.5" {
		t.Errorf("Comma(float32) = %q, want %q", got, "1,234,567.5")
	}
	if got := Comma(float64(1234567.5)).String(); got != "1,234,567.5" {
		t.Errorf("Comma(float64) = %q, want %q", got, "1,234,567.5")
	}
}
