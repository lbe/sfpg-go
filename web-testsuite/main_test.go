//go:build e2eweb

package web_testsuite

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain performs server health check, extracts DB IDs, initializes the
// report file, runs all tests, and writes a summary.
func TestMain(m *testing.M) {
	flag.Parse()

	// Allow overrides via environment variables
	if v := os.Getenv("SERVER_URL"); v != "" {
		serverURL = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		dbPath = v
	}

	// Step 1: Ping health endpoint — fail fast if server not responding
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(serverURL + "/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Server not reachable at %s: %v\n", serverURL, err)
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "❌ Server at %s returned status %d (expected 200)\n", serverURL, resp.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("✅ Server health check passed at %s\n", serverURL)

	// Step 2: Extract folder/file IDs from SQLite database
	// Use -noheader to avoid timing info in output
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if out, err := exec.Command("sqlite3", "-noheader", dbPath, "SELECT id FROM folders LIMIT 1").Output(); err == nil {
			folderID = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
		if out, err := exec.Command("sqlite3", "-noheader", dbPath, "SELECT id FROM files LIMIT 1").Output(); err == nil {
			fileID = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
	}

	if folderID != "" || fileID != "" {
		fmt.Printf("📁 DB at %s: folder_id=%s, file_id=%s\n", dbPath, folderID, fileID)
	} else {
		fmt.Printf("⚠️  DB at %s: no folder/file IDs found (ID-dependent tests will SKIP)\n", dbPath)
	}

	// Step 3: Snapshot config before tests (restore after)
	snapshotConfig()

	// Step 4: Run all tests — results are collected in-memory via report()
	startTime := time.Now()
	exitCode := m.Run()
	finishTime := time.Now()

	// Step 5: Restore config to pre-test state
	restoreConfig()

	// Step 6: Create report directory in module root's tmp/ and write final report
	reportDir := filepath.Join(moduleRoot, "tmp")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		reportDir = "." // fallback
	}

	reportPath := filepath.Join(reportDir, fmt.Sprintf("report-web-functionality-%s.md", startTime.Format("20060102-150405")))
	f, err := os.Create(reportPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to create report file %s: %v\n", reportPath, err)
		os.Exit(1)
	}
	defer f.Close()

	writeReport(f, startTime, finishTime)

	fmt.Printf("📝 Report file: %s\n", reportPath)
	fmt.Printf("\n📊 Results: ✅ %d PASS | ❌ %d FAIL | ⏭️ %d SKIP\n", passCount, failCount, skipCount)

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// writeReport writes the complete report to the given writer using the
// results collected during test execution.
func writeReport(f *os.File, startTime, finishTime time.Time) {
	fmt.Fprintf(f, "# Web Functionality Smoke Report\n\n")
	fmt.Fprintf(f, "**Started:** %s\n", startTime.Format(time.RFC3339))
	fmt.Fprintf(f, "**Finished:** %s\n", finishTime.Format(time.RFC3339))
	fmt.Fprintf(f, "**Server:** %s\n", serverURL)
	fmt.Fprintf(f, "**DB:** %s (folder_id=%s, file_id=%s)\n\n", dbPath, folderID, fileID)
	fmt.Fprintf(f, "## Summary\n\n| Status | Count |\n|--------|-------|\n")
	fmt.Fprintf(f, "| ✅ PASS | %d |\n", passCount)
	fmt.Fprintf(f, "| ❌ FAIL | %d |\n", failCount)
	fmt.Fprintf(f, "| ⏭️ SKIP | %d |\n", skipCount)

	if failCount > 0 {
		fmt.Fprintf(f, "\n## ⚠️  %d test(s) FAILED\n\n", failCount)
	}

	fmt.Fprintf(f, "\n## All Results\n\n")
	fmt.Fprintf(f, "| Status | Route | Auth | Expected | Actual | Note |\n")
	fmt.Fprintf(f, "|--------|-------|------|----------|--------|------|\n")

	reportMu.Lock()
	defer reportMu.Unlock()
	for _, line := range reportLines {
		f.WriteString(line)
	}
}

// reportLines stores all result lines written during test execution.
// Written to report file in writeReport() after all tests complete.
var reportLines []string

// reportResult stores a test result in the in-memory buffer and updates
// the summary counters. The actual file write happens in writeReport().
func reportResult(t *testing.T, num int, route, method, authState string, expected, actual int, status, note string) {
	t.Helper()

	routeStr := fmt.Sprintf("#%d %s %s", num, method, route)

	reportMu.Lock()
	switch status {
	case "PASS":
		passCount++
	case "FAIL":
		failCount++
	case "SKIP":
		skipCount++
	}

	icon := "✅"
	if status == "FAIL" {
		icon = "❌"
	} else if status == "SKIP" {
		icon = "⏭️"
	}
	line := fmt.Sprintf("| %s | %s | %s | %d | %d | %s |\n",
		icon, routeStr, authState, expected, actual, note)
	reportLines = append(reportLines, line)
	reportMu.Unlock()

	if status == "FAIL" {
		t.Errorf("#%d %s %s: expected %d, got %d — %s", num, method, route, expected, actual, note)
	}
}
