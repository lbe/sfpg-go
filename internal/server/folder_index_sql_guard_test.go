package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the module root by walking up from this test file until it
// finds go.mod. go test runs the binary from the package directory, so relative
// paths (e.g. sqlc.yaml, internal/server/...) are not stable from the caller's cwd.
func repoRoot(t *testing.T) string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate module root from %s", thisFile)
		}
		dir = parent
	}
}

// failOnSubstr fails t if content contains any of the forbidden substrings.
func failOnSubstr(t *testing.T, name, content string, forbidden ...string) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(content, f) {
			t.Errorf("%s must not contain %q (G1/G3 write-path SQL guard)", name, f)
		}
	}
}

// TestNoRawSQLOnFolderIndexWritePath guards G1/G3 on the INSERT write path.
// It is a source-text guard (not integration): it reads the relevant files and
// fails if the flush or the rebuild populate path still carry raw SQL strings.
func TestNoRawSQLOnFolderIndexWritePath(t *testing.T) {
	root := repoRoot(t)
	batcherPath := filepath.Join(root, "internal", "server", "infrastructure_batcher.go")
	batcherSrc, err := os.ReadFile(batcherPath)
	if err != nil {
		t.Fatalf("read %s: %v", batcherPath, err)
	}
	batcher := string(batcherSrc)
	// infrastructure_batcher.go must not contain INSERT INTO file_folder_index,
	// an ExecContext with an SQL string, or sqlite_master.
	failOnSubstr(t, batcherPath, batcher,
		"INSERT INTO file_folder_index",
		"ExecContext",
		"sqlite_master",
	)

	folderIndexPath := filepath.Join(root, "internal", "server", "files", "folder_index.go")
	folderIndexSrc, err := os.ReadFile(folderIndexPath)
	if err != nil {
		t.Fatalf("read %s: %v", folderIndexPath, err)
	}
	folderIndex := string(folderIndexSrc)
	// The rebuild populate path must not reference the old rebuild SELECT constant
	// or the dest INSERT string.
	failOnSubstr(t, folderIndexPath, folderIndex,
		"rebuildSelectFileFolderIndexSQL",
		"INSERT INTO file_folder_index_new",
	)
}

// TestSqlcYamlOmitsFolderIndexRebuildSql guards G1: sqlc.yaml's generate queries
// list must omit file_folder_index_rebuild.sql and keep all 14 original query
// files. The rebuild SQL is embed-only, not sqlc-generated.
func TestSqlcYamlOmitsFolderIndexRebuildSql(t *testing.T) {
	root := repoRoot(t)
	yamlPath := filepath.Join(root, "sqlc.yaml")
	src, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read %s: %v", yamlPath, err)
	}
	yamlText := string(src)

	if strings.Contains(yamlText, "file_folder_index_rebuild.sql") {
		t.Errorf("%s must not list file_folder_index_rebuild.sql in the generate queries list (G1)", yamlPath)
	}

	required := []string{
		"sqlc/queries/files.sql",
		"sqlc/queries/folders.sql",
		"sqlc/queries/http_cache.sql",
		"sqlc/queries/config.sql",
		"sqlc/queries/module_state.sql",
		"sqlc/queries/thumbnails.sql",
		"sqlc/queries/xmp.sql",
		"sqlc/queries/login_attempts.sql",
		"sqlc/queries/preload_routes.sql",
		"sqlc/queries/file_paths.sql",
		"sqlc/queries/folder_paths.sql",
		"sqlc/queries/iptc.sql",
		"sqlc/queries/exif.sql",
		"sqlc/queries/invalid_files.sql",
	}
	for _, f := range required {
		if !strings.Contains(yamlText, f) {
			t.Errorf("%s must list %q in the generate queries list", yamlPath, f)
		}
	}
}
