package tableswap

import (
	"strconv"
	"strings"
)

// stripIndexSuffix removes a trailing _<digits> suffix from name, returning
// the base name. If name does not end with _<digits>, it is returned unchanged.
func stripIndexSuffix(name string) string {
	if i := strings.LastIndex(name, "_"); i >= 0 && isDigits(name[i+1:]) {
		return name[:i]
	}
	return name
}

// isDigits reports whether s is non-empty and consists entirely of ASCII digits.
func isDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// allocateIndexName derives a non-colliding index name for the rotated table.
// It strips any trailing _<digits> from active to recover the base name, then
// appends _1, _2, ... until a name not present in existing is found.
func allocateIndexName(active string, existing []string) string {
	base := stripIndexSuffix(active)

	existingSet := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		existingSet[name] = struct{}{}
	}

	if _, ok := existingSet[base]; !ok {
		return base
	}

	for i := 1; ; i++ {
		candidate := base + "_" + strconv.Itoa(i)
		if _, ok := existingSet[candidate]; !ok {
			return candidate
		}
	}
}
