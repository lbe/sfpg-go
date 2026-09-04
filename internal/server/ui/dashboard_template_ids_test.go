package ui

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/web"
)

// TestDashboardTemplates_NoDuplicateIDAttributesInSource walks the embedded
// dashboard template sources (via web.FS embed paths, not filesystem paths
// from the test cwd) and asserts each id attribute appears at most once.
//
// Status-badge ids (preload-status, batch-status, http-status) must appear
// exactly once — one element per status id, not one per if/else branch (L2).
// card-gallery-stats is allowlisted: the loading and loaded branches of a
// mutually exclusive if/else intentionally share the id, so only one instance
// ever renders.
func TestDashboardTemplates_NoDuplicateIDAttributesInSource(t *testing.T) {
	source, err := readDashboardTemplateSource()
	if err != nil {
		t.Fatalf("read dashboard template source from web.FS: %v", err)
	}

	statusBadgeIDs := []string{"preload-status", "batch-status", "http-status"}
	const allowlistedCardGalleryStats = "card-gallery-stats"

	counts := countIDAttributes(source)

	for _, id := range statusBadgeIDs {
		if counts[id] != 1 {
			t.Errorf("status badge id %q appears %d times in dashboard template source; want exactly 1 (single element, not if/else branch duplicates)", id, counts[id])
		}
	}

	for id, count := range counts {
		if count > 1 && id != allowlistedCardGalleryStats {
			t.Errorf("id %q appears %d times in dashboard template source; want at most 1", id, count)
		}
	}
	if counts[allowlistedCardGalleryStats] > 2 {
		t.Errorf("allowlisted %q appears %d times in dashboard template source; want at most 2 (loading and loaded if/else branches, one renders)", allowlistedCardGalleryStats, counts[allowlistedCardGalleryStats])
	}
}

// readDashboardTemplateSource concatenates the embedded sources of
// templates/dashboard.html.tmpl and every template under
// templates/dashboard-ui/ using the web.FS embed file system.
func readDashboardTemplateSource() (string, error) {
	var sb strings.Builder
	appendFile := func(path string) error {
		b, err := fs.ReadFile(web.FS, path)
		if err != nil {
			return err
		}
		sb.Write(b)
		return nil
	}
	if err := appendFile("templates/dashboard.html.tmpl"); err != nil {
		return "", err
	}
	err := fs.WalkDir(web.FS, "templates/dashboard-ui", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		return appendFile(path)
	})
	if err != nil {
		return "", err
	}
	return sb.String(), nil
}

// countIDAttributes counts every double-quoted id="..." attribute value in the
// source text. Dashboard templates use the id="<name>" form only.
func countIDAttributes(source string) map[string]int {
	counts := make(map[string]int)
	for i := 0; i < len(source); {
		start := strings.Index(source[i:], `id="`)
		if start < 0 {
			break
		}
		valueStart := i + start + len(`id="`)
		end := strings.IndexByte(source[valueStart:], '"')
		if end < 0 {
			break
		}
		counts[source[valueStart:valueStart+end]]++
		i = valueStart + end + 1
	}
	return counts
}
