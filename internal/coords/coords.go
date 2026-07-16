// Package coords provides functions for parsing latitude and longitude
// coordinates in various string formats.
package coords

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	dmsRegex            = regexp.MustCompile(`^(-?\d+)[°\s]+(\d+)['′\s]+(\d+(?:\.\d+)?)["″\s]*([NSEW])?`)
	dmRegex             = regexp.MustCompile(`^(-?\d+)[°\s]+(\d+(?:\.\d+)?)['′\s]*([NSEW])?$`)
	decimalCompassRegex = regexp.MustCompile(`^(-?\d+(?:\.\d+)?)[°\s]*([NSEW])$`)
	xmpCommaDMRegex     = regexp.MustCompile(`^(\d+(?:\.\d+)?),(\d+(?:\.\d+)?)([NSEWnsew])$`)
)

// parseXMPGPS converts Canon XMP comma-DM ("11,57.1312N") to DM ("11°57.1312'N").
// Non-matching input is returned trimmed, unchanged.
func parseXMPGPS(coord string) string {
	coord = strings.TrimSpace(coord)
	m := xmpCommaDMRegex.FindStringSubmatch(coord)
	if m == nil {
		return coord
	}
	return fmt.Sprintf("%s°%s'%s", m[1], m[2], strings.ToUpper(m[3]))
}

// ParseCoordinate converts various lat/lon string formats to float64
// Supports formats like:
//   - Decimal: "40.7128", "-74.0060"
//   - DMS: "40°42'46\"N", "74°0'21\"W"
//   - DM: "40°42.767'N", "74°0.35'W"
//   - Compass: "40.7128 N", "74.0060 W"
//   - XMP comma-DM: "11,57.1312N", "120,11.573E"
func ParseCoordinate(coord string) (float64, error) {
	coord = strings.TrimSpace(coord)

	if coord == "" {
		return 0, fmt.Errorf("empty coordinate string")
	}

	coord = parseXMPGPS(coord)

	// Check for DMS format (degrees, minutes, seconds)
	if matches := dmsRegex.FindStringSubmatch(coord); matches != nil {
		return parseDMS(matches)
	}

	// Check for DM format (degrees, minutes)
	if matches := dmRegex.FindStringSubmatch(coord); matches != nil {
		return parseDM(matches)
	}

	// Check for decimal with compass direction
	if matches := decimalCompassRegex.FindStringSubmatch(coord); matches != nil {
		return parseDecimalCompass(matches)
	}

	// Try simple decimal format
	value, err := strconv.ParseFloat(coord, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid coordinate format: %s", coord)
	}

	return value, nil
}

// parseDMS converts a Degrees, Minutes, Seconds (DMS) coordinate from string matches
// into a float64 decimal value.
func parseDMS(matches []string) (float64, error) {
	degrees, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.ParseFloat(matches[2], 64)
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(matches[3], 64)
	if err != nil {
		return 0, err
	}
	direction := strings.ToUpper(matches[4])

	decimal := degrees + minutes/60 + seconds/3600

	if direction == "S" || direction == "W" || degrees < 0 {
		decimal = -decimal
	}

	return decimal, nil
}

// parseDM converts a Degrees, Minutes (DM) coordinate from string matches
// into a float64 decimal value.
func parseDM(matches []string) (float64, error) {
	degrees, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.ParseFloat(matches[2], 64)
	if err != nil {
		return 0, err
	}
	direction := strings.ToUpper(matches[3])

	decimal := degrees + minutes/60

	if direction == "S" || direction == "W" || degrees < 0 {
		decimal = -decimal
	}

	return decimal, nil
}

// parseDecimalCompass adjusts a decimal coordinate value based on a compass direction
// (N, S, E, W) to return a float64 decimal value.
func parseDecimalCompass(matches []string) (float64, error) {
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}
	direction := strings.ToUpper(matches[2])

	if direction == "S" || direction == "W" {
		value = -value
	}

	return value, nil
}
