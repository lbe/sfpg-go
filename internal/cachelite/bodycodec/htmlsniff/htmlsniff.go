package htmlsniff

import "bytes"

// HTMLScanLimit is the maximum number of bytes scanned to determine whether
// content looks like HTML.
const HTMLScanLimit = 4 << 10 // 4096

// LooksLikeHTML reports whether b appears to be an HTML document by examining
// its leading bytes up to HTMLScanLimit.
func LooksLikeHTML(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	scan := b
	if len(scan) > HTMLScanLimit {
		scan = scan[:HTMLScanLimit]
	}
	// Skip leading whitespace: space, tab, LF, CR.
	i := 0
	for i < len(scan) {
		c := scan[i]
		if c != 0x20 && c != 0x09 && c != 0x0A && c != 0x0D {
			break
		}
		i++
	}
	scan = scan[i:]
	if len(scan) == 0 {
		return false
	}
	// Check for <!DOCTYPE (case-insensitive).
	if len(scan) >= 9 && bytes.EqualFold(scan[:9], []byte("<!DOCTYPE")) {
		return true
	}
	// Check for <html (case-insensitive).
	if len(scan) >= 5 && bytes.EqualFold(scan[:5], []byte("<html")) {
		return true
	}
	// Check for <letter (any ASCII letter).
	if scan[0] == '<' && len(scan) >= 2 &&
		((scan[1] >= 'a' && scan[1] <= 'z') || (scan[1] >= 'A' && scan[1] <= 'Z')) {
		return true
	}
	return false
}
