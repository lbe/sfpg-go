//go:build e2eweb

package web_testsuite

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// =========================================================================
// Section 9: Lightbox Navigation Tests
//
// These tests verify the lightbox rendering and navigation behavior:
// - Lightbox loads for the current file
// - Prev/Next links point to the correct neighbors (filename order)
// - Loop-around works from first<->last
// - Following a navigation link returns a new lightbox UI
// =========================================================================

// lightboxFile holds a file id and its filename for navigation assertions.
type lightboxFile struct {
	ID       int64
	Filename string
}

// lightboxFixture loads a folder with at least minImages and returns the
// sorted file list plus the first file. It skips the test if no suitable
// folder exists.
func lightboxFixture(t *testing.T, minImages int) ([]lightboxFile, lightboxFile) {
	t.Helper()

	out, err := exec.Command("sqlite3", "-noheader", dbPath,
		fmt.Sprintf(`SELECT f.id, f.filename
					 FROM files f
					 WHERE f.folder_id = (
						 SELECT folder_id FROM files
						 GROUP BY folder_id
						 HAVING COUNT(*) >= %d
						 ORDER BY folder_id
						 LIMIT 1
					 )
					 ORDER BY f.filename`, minImages)).Output()
	if err != nil {
		t.Skipf("SKIP: could not query lightbox fixture: %v", err)
	}

	var files []lightboxFile
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		files = append(files, lightboxFile{ID: id, Filename: parts[1]})
	}

	if len(files) < minImages {
		t.Skipf("SKIP: no folder with at least %d images", minImages)
	}

	return files, files[0]
}

// TestLightbox_RendersForCurrentFile verifies GET /lightbox/{id} (HX) returns
// 200 and contains the expected lightbox structure.
func TestLightbox_RendersForCurrentFile(t *testing.T) {
	files, first := lightboxFixture(t, 1)
	_ = files

	client := newClient()
	resp := doRequest(t, client, http.MethodGet, fmt.Sprintf("/lightbox/%d", first.ID), nil, true)
	defer resp.Body.Close()

	status := "PASS"
	note := "OK"
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		status = "FAIL"
		note = fmt.Sprintf("expected 200, got %d: %s", resp.StatusCode, string(body))
		reportResult(t, 74, fmt.Sprintf("/lightbox/%d", first.ID), "GET", "No", http.StatusOK, resp.StatusCode, status, note)
		t.Fatalf("#74 GET /lightbox: %s", note)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse lightbox HTML: %v", err)
	}

	if FindElementByID(doc, "lightbox-ui") == nil {
		status = "FAIL"
		note = "missing #lightbox-ui"
	}
	if FindElementByID(doc, "lightbox-images") == nil {
		status = "FAIL"
		note = "missing #lightbox-images"
	}

	reportResult(t, 74, fmt.Sprintf("/lightbox/%d", first.ID), "GET", "No", http.StatusOK, resp.StatusCode, status, note)
	if status == "FAIL" {
		t.Fatalf("#74 GET /lightbox: %s", note)
	}
}

// TestLightbox_NextAndPrevLinks verifies the rendered lightbox contains
// hx-get links pointing to the correct next/prev file IDs (filename order,
// with wrap-around).
func TestLightbox_NextAndPrevLinks(t *testing.T) {
	files, first := lightboxFixture(t, 2)
	last := files[len(files)-1]
	second := files[1]

	client := newClient()
	path := fmt.Sprintf("/lightbox/%d", first.ID)
	resp := doRequest(t, client, http.MethodGet, path, nil, true)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s expected 200, got %d: %s", path, resp.StatusCode, string(body))
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse lightbox HTML: %v", err)
	}

	currentPath, nextPath, prevPath := extractLightboxInitArgs(t, doc)

	status := "PASS"
	note := "OK"

	expectedCurrent := fmt.Sprintf("/raw-image/%d", first.ID)
	expectedNext := fmt.Sprintf("/raw-image/%d", second.ID)
	expectedPrev := fmt.Sprintf("/raw-image/%d", last.ID)

	switch {
	case currentPath != expectedCurrent:
		status = "FAIL"
		note = fmt.Sprintf("current path: expected %q, got %q", expectedCurrent, currentPath)
	case nextPath != expectedNext:
		status = "FAIL"
		note = fmt.Sprintf("next path: expected %q, got %q", expectedNext, nextPath)
	case prevPath != expectedPrev:
		status = "FAIL"
		note = fmt.Sprintf("prev path: expected %q, got %q", expectedPrev, prevPath)
	}

	reportResult(t, 75, path, "GET", "No", http.StatusOK, resp.StatusCode, status, note)
	if status == "FAIL" {
		t.Fatalf("#75 GET /lightbox next/prev links: %s", note)
	}

	// Also verify the navigation buttons have hx-get pointing to the same IDs.
	nextBtn := FindElementByID(doc, "lightbox-next-btn")
	prevBtn := FindElementByID(doc, "lightbox-prev-btn")
	if nextBtn == nil || prevBtn == nil {
		t.Fatal("missing lightbox-next-btn or lightbox-prev-btn")
	}

	nextHref := GetAttr(nextBtn, "hx-get")
	prevHref := GetAttr(prevBtn, "hx-get")
	if !strings.Contains(nextHref, fmt.Sprintf("/lightbox/%d", second.ID)) {
		t.Fatalf("next button hx-get expected to contain /lightbox/%d, got %q", second.ID, nextHref)
	}
	if !strings.Contains(prevHref, fmt.Sprintf("/lightbox/%d", last.ID)) {
		t.Fatalf("prev button hx-get expected to contain /lightbox/%d, got %q", last.ID, prevHref)
	}
}

// TestLightbox_FollowNextLink verifies that following the rendered next
// hx-get link returns a new lightbox UI for the next image.
func TestLightbox_FollowNextLink(t *testing.T) {
	files, first := lightboxFixture(t, 2)
	second := files[1]

	client := newClient()

	// Load first lightbox.
	resp := doRequest(t, client, http.MethodGet, fmt.Sprintf("/lightbox/%d", first.ID), nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /lightbox/%d expected 200, got %d: %s", first.ID, resp.StatusCode, string(body))
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse lightbox HTML: %v", err)
	}

	nextBtn := FindElementByID(doc, "lightbox-next-btn")
	if nextBtn == nil {
		t.Fatal("missing lightbox-next-btn")
	}
	nextPath := GetAttr(nextBtn, "hx-get")
	if nextPath == "" {
		t.Fatal("lightbox-next-btn missing hx-get")
	}

	// Follow the next link (simulates HTMX navigation).
	nextResp := doRequest(t, client, http.MethodGet, nextPath, nil, true)
	defer nextResp.Body.Close()

	status := "PASS"
	note := "OK"
	if nextResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(nextResp.Body)
		status = "FAIL"
		note = fmt.Sprintf("expected 200, got %d: %s", nextResp.StatusCode, string(body))
	}

	reportResult(t, 76, nextPath, "GET", "No", http.StatusOK, nextResp.StatusCode, status, note)
	if status == "FAIL" {
		t.Fatalf("#76 GET %s: %s", nextPath, note)
	}

	// Verify the returned UI is for the second image.
	nextDoc, err := html.Parse(nextResp.Body)
	if err != nil {
		t.Fatalf("failed to parse next lightbox HTML: %v", err)
	}
	currentPath, _, _ := extractLightboxInitArgs(t, nextDoc)
	expectedCurrent := fmt.Sprintf("/raw-image/%d", second.ID)
	if currentPath != expectedCurrent {
		t.Fatalf("expected next lightbox current path %q, got %q", expectedCurrent, currentPath)
	}
}

// TestLightbox_LoopAround_FirstToLast verifies that the prev link from the
// first image wraps to the last image.
func TestLightbox_LoopAround_FirstToLast(t *testing.T) {
	files, first := lightboxFixture(t, 2)
	last := files[len(files)-1]

	client := newClient()
	resp := doRequest(t, client, http.MethodGet, fmt.Sprintf("/lightbox/%d", first.ID), nil, true)
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse lightbox HTML: %v", err)
	}

	_, _, prevPath := extractLightboxInitArgs(t, doc)
	expectedPrev := fmt.Sprintf("/raw-image/%d", last.ID)

	status := "PASS"
	note := "OK"
	if prevPath != expectedPrev {
		status = "FAIL"
		note = fmt.Sprintf("expected prev path %q, got %q", expectedPrev, prevPath)
	}

	reportResult(t, 77, fmt.Sprintf("/lightbox/%d", first.ID), "GET", "No", http.StatusOK, http.StatusOK, status, note)
	if status == "FAIL" {
		t.Fatalf("#77 loop first->last: %s", note)
	}
}

// TestLightbox_LoopAround_LastToFirst verifies that the next link from the
// last image wraps to the first image.
func TestLightbox_LoopAround_LastToFirst(t *testing.T) {
	files, _ := lightboxFixture(t, 2)
	last := files[len(files)-1]
	first := files[0]

	client := newClient()
	resp := doRequest(t, client, http.MethodGet, fmt.Sprintf("/lightbox/%d", last.ID), nil, true)
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse lightbox HTML: %v", err)
	}

	_, nextPath, _ := extractLightboxInitArgs(t, doc)
	expectedNext := fmt.Sprintf("/raw-image/%d", first.ID)

	status := "PASS"
	note := "OK"
	if nextPath != expectedNext {
		status = "FAIL"
		note = fmt.Sprintf("expected next path %q, got %q", expectedNext, nextPath)
	}

	reportResult(t, 78, fmt.Sprintf("/lightbox/%d", last.ID), "GET", "No", http.StatusOK, http.StatusOK, status, note)
	if status == "FAIL" {
		t.Fatalf("#78 loop last->first: %s", note)
	}
}

// TestLightbox_NavigationSequence verifies stepping forward through all
// images wraps back to the starting image.
func TestLightbox_NavigationSequence(t *testing.T) {
	files, first := lightboxFixture(t, 3)

	client := newClient()
	currentID := first.ID
	seen := make(map[int64]bool)

	for i := 0; i < len(files); i++ {
		seen[currentID] = true
		resp := doRequest(t, client, http.MethodGet, fmt.Sprintf("/lightbox/%d", currentID), nil, true)
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET /lightbox/%d expected 200, got %d: %s", currentID, resp.StatusCode, string(body))
		}

		doc, err := html.Parse(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("failed to parse lightbox HTML: %v", err)
		}

		_, nextPath, _ := extractLightboxInitArgs(t, doc)
		// nextPath is /raw-image/{id}; extract the id.
		nextID, err := extractIDFromRawImagePath(nextPath)
		if err != nil {
			t.Fatalf("invalid next path %q: %v", nextPath, err)
		}
		currentID = nextID
	}

	status := "PASS"
	note := "OK"
	if currentID != first.ID {
		status = "FAIL"
		note = fmt.Sprintf("expected to wrap to %d, got %d", first.ID, currentID)
	}
	if len(seen) != len(files) {
		status = "FAIL"
		note = fmt.Sprintf("expected %d unique images, saw %d", len(files), len(seen))
	}

	reportResult(t, 79, "/lightbox/{id}", "GET", "No", http.StatusOK, http.StatusOK, status, note)
	if status == "FAIL" {
		t.Fatalf("#79 navigation sequence: %s", note)
	}
}

// extractLightboxInitArgs parses the #lightbox-ui _="init ..." attribute and
// returns the current, next, and prev raw-image paths in order.
func extractLightboxInitArgs(t *testing.T, doc *html.Node) (current, next, prev string) {
	t.Helper()

	ui := FindElementByID(doc, "lightbox-ui")
	if ui == nil {
		t.Fatal("#lightbox-ui not found")
	}
	script := GetAttr(ui, "_")

	re := regexp.MustCompile(`initLightboxRouter\s*\(\s*me\s*,\s*'([^']+)'\s*,\s*'([^']+)'\s*,\s*'([^']+)'\s*,\s*'([^']+)'\s*\)`)
	matches := re.FindStringSubmatch(script)
	if len(matches) != 5 {
		t.Fatalf("could not parse initLightboxRouter args from: %s", script)
	}
	return matches[2], matches[3], matches[4]
}

// extractIDFromRawImagePath parses "/raw-image/{id}" and returns the id.
func extractIDFromRawImagePath(p string) (int64, error) {
	p = strings.TrimPrefix(p, "/raw-image/")
	p = strings.TrimSpace(p)
	return strconv.ParseInt(p, 10, 64)
}
