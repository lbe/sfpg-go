// Package humanize provides utilities for formatting numbers into human-readable forms.
//
// This package offers two main formatting capabilities:
//
//  1. Byte size formatting (IBytes): Converts byte counts to human-readable strings
//     with binary prefixes (KiB, MiB, GiB, etc.).
//
//  2. Number formatting (Comma): Adds comma separators to numeric values for improved
//     readability.
//
// Both formatters support a fluent API with method chaining for setting precision.
//
// # Byte Formatting
//
// Use IBytes to format byte counts with binary (IEC) prefixes:
//
//	IBytes(1024).String()                           // "1 KiB"
//	IBytes(1536000).String()                        // "1 MiB"
//	IBytes(1536000).WithPrecision(2).String()       // "1.46 MiB"
//
// Supported units range from bytes (B) to exbibytes (EiB):
//   - B (bytes)
//   - KiB (kibibytes, 1024 bytes)
//   - MiB (mebibytes, 1024 KiB)
//   - GiB (gibibytes, 1024 MiB)
//   - TiB (tebibytes, 1024 GiB)
//   - PiB (pebibytes, 1024 TiB)
//   - EiB (exbibytes, 1024 PiB)
//
// # Number Formatting
//
// Use Comma to format numbers with comma separators:
//
//	Comma(int64(1234567)).String()                  // "1,234,567"
//	Comma(1234567.89).String()                      // "1,234,567.89"
//	Comma(1234567.89).WithPrecision(1).String()     // "1,234,567.9"
//
// The Comma function is generic and accepts any integer or floating-point type:
//   - Signed integers: int, int8, int16, int32, int64
//   - Unsigned integers: uint, uint8, uint16, uint32, uint64, uintptr
//   - Floating-point: float32, float64
//
// # Precision Control
//
// Both formatters support the WithPrecision method to control decimal places:
//
//	// IBytes: default rounds to nearest integer
//	IBytes(1536).String()                           // "2 KiB"
//	IBytes(1536).WithPrecision(1).String()          // "1.5 KiB"
//	IBytes(1536).WithPrecision(2).String()          // "1.50 KiB"
//
//	// Comma: integers default to no decimal, floats to natural precision
//	Comma(1234).String()                            // "1,234"
//	Comma(1234).WithPrecision(2).String()           // "1,234.00"
//	Comma(1234.567).WithPrecision(2).String()       // "1,234.57"
//
// # Negative Values
//
// Both formatters correctly handle negative values:
//
//	IBytes(-1024).String()                          // "-1 KiB"
//	Comma(-1234567).String()                        // "-1,234,567"
//
// # Edge Cases
//
// Special values are handled gracefully:
//   - Zero values: IBytes(0) returns "0 B"
//   - math.MinInt64: Handled without overflow
//   - Very large values (uint64 max): Correctly formatted
//
// # fmt.Formatter Interface
//
// Both formatters implement fmt.Formatter for use with printf-style functions:
//
//	fmt.Printf("Size: %v\n", IBytes(1024))           // "Size: 1 KiB"
//	fmt.Printf("Count: %v\n", Comma(1234567))        // "Count: 1,234,567"
//
// This package is inspired by github.com/dustin/go-humanize but provides
// a simpler API optimized for internal use with full control over formatting behavior.
package humanize
