// Package conditional provides pure functions for HTTP conditional request matching.
package conditional

import (
	"database/sql"
	"strings"
	"time"
)

// MatchesETag checks if If-None-Match matches ETag (supports weak comparison).
// Returns true if the ETag matches one of the values in If-None-Match.
func MatchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}

	// Wildcard matches any ETag
	if ifNoneMatch == "*" {
		return true
	}

	// Normalize ETags by removing W/ prefix for weak comparison
	normalizeETag := func(e string) string {
		e = strings.TrimSpace(e)
		return strings.TrimPrefix(e, "W/")
	}

	normalizedETag := normalizeETag(etag)

	// Parse comma-separated ETag values
	values := strings.SplitSeq(ifNoneMatch, ",")
	for val := range values {
		if normalizeETag(val) == normalizedETag {
			return true
		}
	}

	return false
}

// MatchesLastModified checks if If-Modified-Since matches Last-Modified.
// Returns true if Last-Modified <= If-Modified-Since (not modified).
func MatchesLastModified(ifModifiedSince string, lastModified sql.NullString) bool {
	if !lastModified.Valid || ifModifiedSince == "" {
		return false
	}

	lastMod, err := time.Parse(time.RFC1123, lastModified.String)
	if err != nil {
		return false
	}

	ifMod, err := time.Parse(time.RFC1123, ifModifiedSince)
	if err != nil {
		return false
	}

	// Truncate to seconds for comparison (HTTP times don't include nanoseconds)
	return lastMod.Truncate(time.Second).Before(ifMod.Add(time.Second))
}
