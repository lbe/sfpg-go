package migrations

import (
	"fmt"
	"regexp"
	"strconv"
)

var migrationUpSerialRE = regexp.MustCompile(`^(\d+)_.+\.up\.sql$`)

// LatestSchemaKey returns the highest numeric migration serial from embedded
// main migrations (e.g. "022"), zero-padded to match migration filenames.
func LatestSchemaKey() (string, error) {
	entries, err := FS.ReadDir("migrations")
	if err != nil {
		return "", fmt.Errorf("read migrations dir: %w", err)
	}
	var maxSerial int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationUpSerialRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return "", fmt.Errorf("parse migration serial %q: %w", e.Name(), err)
		}
		if n > maxSerial {
			maxSerial = n
		}
	}
	if maxSerial == 0 {
		return "", fmt.Errorf("no .up.sql migrations found")
	}
	width := 3
	if maxSerial >= 1000 {
		width = len(fmt.Sprintf("%d", maxSerial))
	}
	return fmt.Sprintf("%0*d", width, maxSerial), nil
}
