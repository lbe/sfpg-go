package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// IncrementETagVersion increments an ETag version string following these rules:
//   - Format: [prefix]YYYYMMDD-NN (e.g., "20260129-01" or "v20260129-01")
//   - If date < today: reset to today-01
//   - If date == today: increment NN
//   - If invalid format: default to today-01
func IncrementETagVersion(current string) string {
	prefix, dateStr, number, valid := parseETagVersion(current)

	today := time.Now().Format("20060102")

	if !valid {
		// If input has 'v' prefix but otherwise invalid, preserve prefix
		if len(current) > 0 && current[0] == 'v' {
			return "v" + today + "-01"
		}
		return today + "-01"
	}

	if dateStr < today {
		return prefix + today + "-01"
	}

	if dateStr == today {
		return fmt.Sprintf("%s%s-%02d", prefix, dateStr, number+1)
	}

	// dateStr > today (shouldn't happen, but handle gracefully)
	return prefix + today + "-01"
}

// parseETagVersion parses an ETag version string into components.
// Returns: prefix (e.g., "v" or ""), dateStr (YYYYMMDD), number (NN), and valid flag.
func parseETagVersion(etag string) (prefix string, dateStr string, number int, valid bool) {
	// Pattern: optional prefix + YYYYMMDD + "-" + NN
	re := regexp.MustCompile(`^(v?)(\d{8})-(\d{2})$`)
	matches := re.FindStringSubmatch(etag)

	if matches == nil {
		return "", "", 0, false
	}

	prefix = matches[1]
	dateStr = matches[2]
	numStr := matches[3]

	num, err := strconv.Atoi(numStr)
	if err != nil {
		return "", "", 0, false
	}

	return prefix, dateStr, num, true
}
