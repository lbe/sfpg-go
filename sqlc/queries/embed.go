package sqlcqueries

import (
	_ "embed"
	"strings"
)

//go:embed file_folder_index_rebuild.sql
var fileFolderIndexRebuildSQL string

func QueryFilesForFolderIndexRebuildSQL() string {
	return strings.TrimSpace(strings.Split(fileFolderIndexRebuildSQL, "-- statement-break")[0])
}

func InsertFileFolderIndexNewSQL() string {
	parts := strings.Split(fileFolderIndexRebuildSQL, "-- statement-break")
	return strings.TrimSpace(parts[1])
}

func CountFilesForFolderIndexRebuildSQL() string {
	parts := strings.Split(fileFolderIndexRebuildSQL, "-- statement-break")
	return strings.TrimSpace(parts[2])
}
