package humanize

import (
	"fmt"
	"strconv"
	"strings"
)

// Number is a constraint that permits any integer or floating-point type.
// This includes signed integers, unsigned integers, and floats.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

// CommaFormatter formats numbers with comma separators for thousands.
// Use Comma to create a CommaFormatter, then optionally call WithPrecision
// before calling String to get the formatted result.
type CommaFormatter[T Number] struct {
	value     T
	precision int
	isFloat   bool
}

// Comma creates a new CommaFormatter for the given numeric value.
// The type parameter T can be any integer or floating-point type.
// By default, integers are displayed without decimals and floats are
// displayed with their natural precision.
//
// Example:
//
//	Comma(int64(1234567)).String()                 // "1,234,567"
//	Comma(1234567.89).String()                     // "1,234,567.89"
//	Comma(1234567.89).WithPrecision(1).String()    // "1,234,567.9"
func Comma[T Number](v T) *CommaFormatter[T] {
	// Determine if this is a float type
	var isFloat bool
	switch any(v).(type) {
	case float32, float64:
		isFloat = true
	}
	return &CommaFormatter[T]{value: v, precision: -1, isFloat: isFloat}
}

// WithPrecision sets the number of decimal places to display.
// For integers, this adds a decimal point and trailing zeros.
// Returns the CommaFormatter for method chaining.
//
// Example:
//
//	Comma(1234).WithPrecision(2).String()        // "1,234.00"
//	Comma(1234.5678).WithPrecision(2).String()   // "1,234.57"
func (f *CommaFormatter[T]) WithPrecision(p int) *CommaFormatter[T] {
	f.precision = p
	return f
}

// String formats the number with comma separators.
// Integer types are formatted without a decimal point.
// Float types preserve their natural precision unless WithPrecision was called.
func (f *CommaFormatter[T]) String() string {
	// Convert to float64 for unified handling
	var floatVal float64
	switch v := any(f.value).(type) {
	case int:
		floatVal = float64(v)
	case int8:
		floatVal = float64(v)
	case int16:
		floatVal = float64(v)
	case int32:
		floatVal = float64(v)
	case int64:
		floatVal = float64(v)
	case uint:
		floatVal = float64(v)
	case uint8:
		floatVal = float64(v)
	case uint16:
		floatVal = float64(v)
	case uint32:
		floatVal = float64(v)
	case uint64:
		floatVal = float64(v)
	case uintptr:
		floatVal = float64(v)
	case float32:
		floatVal = float64(v)
	case float64:
		floatVal = v
	}

	// Handle negative values
	negative := floatVal < 0
	if negative {
		floatVal = -floatVal
	}

	// Determine precision to use
	precision := f.precision
	if precision < 0 {
		// Default: no precision for integers, natural for floats
		if f.isFloat {
			precision = -1 // Use natural precision
		} else {
			precision = 0 // No decimal for integers
		}
	}

	// Format the number
	var numStr string
	if precision >= 0 {
		numStr = strconv.FormatFloat(floatVal, 'f', precision, 64)
	} else {
		// Use natural precision for floats, stripping trailing zeros
		numStr = strconv.FormatFloat(floatVal, 'f', -1, 64)
	}

	// Split into integer and decimal parts
	parts := strings.Split(numStr, ".")
	intPart := parts[0]
	var decPart string
	if len(parts) > 1 {
		decPart = parts[1]
	}

	// Add commas to the integer part
	intPart = addCommas(intPart)

	// Reassemble
	result := intPart
	if decPart != "" {
		result += "." + decPart
	}

	if negative {
		result = "-" + result
	}

	return result
}

// addCommas inserts comma separators into a string representation of an integer.
// The input must be a plain integer without sign or decimal point.
func addCommas(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}

	// Calculate result length: original + commas
	// Commas are inserted every 3 digits from the right
	commaCount := (n - 1) / 3
	result := make([]byte, n+commaCount)

	// Fill from right to left
	j := len(result) - 1
	digitCount := 0
	for i := n - 1; i >= 0; i-- {
		if digitCount > 0 && digitCount%3 == 0 {
			result[j] = ','
			j--
		}
		result[j] = s[i]
		j--
		digitCount++
	}

	return string(result)
}

// Format implements the fmt.Formatter interface for use with printf-style functions.
// The 'v' and 's' verbs are supported.
//
// Example:
//
//	fmt.Printf("%v", Comma(1234567)) // "1,234,567"
func (f *CommaFormatter[T]) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v', 's':
		fmt.Fprint(st, f.String())
	default:
		fmt.Fprintf(st, "%%!%c(humanize.CommaFormatter=%s)", verb, f.String())
	}
}
