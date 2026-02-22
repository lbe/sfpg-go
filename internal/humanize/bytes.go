package humanize

import (
	"fmt"
	"strconv"
)

// ByteFormatter formats byte sizes into human-readable strings with binary prefixes.
// Use IBytes to create a ByteFormatter, then optionally call WithPrecision before
// calling String to get the formatted result.
type ByteFormatter struct {
	bytes     int64
	precision int
}

// IBytes creates a new ByteFormatter for the given byte count.
// By default, values are rounded to the nearest integer unit.
// Call WithPrecision to specify decimal places.
//
// Example:
//
//	IBytes(1536000).String()                  // "1 MiB"
//	IBytes(1536000).WithPrecision(2).String() // "1.46 MiB"
func IBytes(b int64) *ByteFormatter {
	return &ByteFormatter{bytes: b, precision: 0}
}

// WithPrecision sets the number of decimal places to display.
// Returns the ByteFormatter for method chaining.
//
// Example:
//
//	IBytes(1536).WithPrecision(2).String() // "1.50 KiB"
func (f *ByteFormatter) WithPrecision(p int) *ByteFormatter {
	f.precision = p
	return f
}

// binaryUnits defines the binary prefixes and their corresponding byte values.
// Uses IEC standard: KiB (kibibyte), MiB (mebibyte), etc.
var binaryUnits = []struct {
	symbol string
	value  uint64
}{
	{"EiB", 1 << 60}, // Exbibyte = 1024^6
	{"PiB", 1 << 50}, // Pebibyte = 1024^5
	{"TiB", 1 << 40}, // Tebibyte = 1024^4
	{"GiB", 1 << 30}, // Gibibyte = 1024^3
	{"MiB", 1 << 20}, // Mebibyte = 1024^2
	{"KiB", 1 << 10}, // Kibibyte = 1024^1
	{"B", 1},         // Byte
}

// String formats the byte count as a human-readable string.
// Uses binary prefixes (KiB, MiB, GiB, etc.) and rounds to the
// precision set by WithPrecision (default 0).
//
// Special cases:
//   - Zero returns "0 B"
//   - Negative values include a minus sign
//   - math.MinInt64 is handled gracefully
func (f *ByteFormatter) String() string {
	if f.bytes == 0 {
		return "0 B"
	}

	// Handle negative values
	negative := f.bytes < 0
	var absVal uint64
	if negative {
		// Convert to unsigned to handle math.MinInt64 correctly
		absVal = uint64(-(f.bytes + 1)) + 1
	} else {
		absVal = uint64(f.bytes)
	}

	// Find the appropriate unit
	var unit string
	var value float64

	for _, u := range binaryUnits {
		if absVal >= u.value {
			unit = u.symbol
			value = float64(absVal) / float64(u.value)
			break
		}
	}

	// Format with the specified precision
	var result string
	if f.precision == 0 {
		// Round to nearest integer
		result = strconv.FormatFloat(value, 'f', 0, 64) + " " + unit
	} else {
		result = strconv.FormatFloat(value, 'f', f.precision, 64) + " " + unit
	}

	if negative {
		result = "-" + result
	}

	return result
}

// Format implements the fmt.Formatter interface for use with printf-style functions.
// The 'v' and 's' verbs are supported.
//
// Example:
//
//	fmt.Printf("%v", IBytes(1536000)) // "1 MiB"
func (f *ByteFormatter) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v', 's':
		fmt.Fprint(st, f.String())
	default:
		fmt.Fprintf(st, "%%!%c(humanize.ByteFormatter=%s)", verb, f.String())
	}
}
