package handlers

import "path/filepath"

// fixupDirectoryName cleans and formats a directory name for display.
// Names longer than maxLen (23) are truncated with a center-ellipsis so that
// both the start and end of the name remain visible:
//
//	"my-very-long-dir...ectory-name" → preserved prefix + "..." + preserved suffix
//
// A folder emoji prefix (📁︎) is always prepended.
func fixupDirectoryName(name string) string {
	const maxLen = 23
	name = filepath.Base(name)
	if len(name) <= maxLen {
		return "📁︎ " + name
	}
	if maxLen <= 3 {
		return "📁︎ " + name[:maxLen]
	}
	idxLeft := (maxLen - 3) / 2
	idxRight := maxLen - idxLeft - 3
	return "📁︎ " + name[:idxLeft] + "..." + name[len(name)-idxRight:]
}

// fixupFileName cleans and formats a file name for display.
// Names longer than maxLen (23) are truncated with a center-ellipsis so that
// both the start and end of the filename (including extension) remain visible:
//
//	"verylongfilename0123456.jpg" → "verylon...456.jpg"
//
// Unlike right-truncation, center-ellipsis preserves the file extension and
// provides enough context to identify files with long numeric suffixes.
func fixupFileName(name string) string {
	const maxLen = 23
	name = filepath.Base(name)
	if len(name) <= maxLen {
		return name
	}
	if maxLen <= 3 {
		return name[:maxLen]
	}
	idxLeft := (maxLen - 3) / 2
	idxRight := maxLen - idxLeft - 3
	return name[:idxLeft] + "..." + name[len(name)-idxRight:]
}
