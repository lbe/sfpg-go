// Package parallelwalkdir_test contains tests for the parallelwalkdir package.
package parallelwalkdir

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// Helper to create directories, failing the test on error
func mustMkdir(t *testing.T, root string, path ...string) {
	fullPath := filepath.Join(path...)
	if err := os.MkdirAll(filepath.Join(root, fullPath), 0o755); err != nil {
		t.Fatalf("Failed to create directory %s: %v", fullPath, err)
	}
}

// Helper to create files, failing the test on error
func mustWriteFile(t *testing.T, root string, fileName string, path ...string) {
	fullPath := filepath.Join(path...)
	r := regexp.MustCompile(`empty`)
	content := []byte{}
	if !r.MatchString(fileName) {
		content = []byte(fileName)
	}
	if err := os.WriteFile(filepath.Join(root, fullPath, fileName), content, 0o644); err != nil {
		t.Fatalf("Failed to write file %s: %v", fullPath, err)
	}
}

// Helper to create symlinks, failing the test on error
func mustSymlink(t *testing.T, root string, target, linkPath string) {
	if runtime.GOOS == "windows" {
		t.Logf("Skipping symlink creation on Windows: %s -> %s", linkPath, target)
		return
	}
	if err := os.Symlink(target, filepath.Join(root, linkPath)); err != nil {
		t.Fatalf("Failed to create symlink %s -> %s: %v", linkPath, target, err)
	}
}

// createComplexTestDirStructure sets up a temporary directory with a predefined
// file and directory structure, including symlinks and a deliberately
// unreadable directory to test error handling. It returns the list of
// expected file paths that the walker should find, excluding those in the
// unreadable directory, and a cleanup function.
func createComplexTestDirStructure(t *testing.T, root string) ([]string, func()) {
	// 1. Create all directories
	mustMkdir(t, root, "dir3")
	mustMkdir(t, root, "dir4")
	mustMkdir(t, root, "rootDir", "dir1", "dir1a")
	mustMkdir(t, root, "rootDir", "dir2", "dir2a")

	// 2. Define file structure and expected paths
	var expectedFiles []string
	// realDirToReportedDir maps the actual location of files to the path
	// the walker should report, accounting for symlinks.
	var realDirToReportedDir map[string]string
	if runtime.GOOS == "windows" {
		realDirToReportedDir = map[string]string{
			"dir3":               "dir3",
			"dir4":               "dir4",
			"rootDir/dir1":       "rootDir/dir1",
			"rootDir/dir2":       "rootDir/dir2",
			"rootDir/dir2/dir2a": "rootDir/dir2/dir2a",
		}
	} else {
		realDirToReportedDir = map[string]string{
			"dir3":               "rootDir/alinkdir",
			"dir4":               "rootDir/alinkdir2",
			"rootDir/dir1":       "rootDir/dir1",
			"rootDir/dir2":       "rootDir/dir2",
			"rootDir/dir2/dir2a": "rootDir/dir2/dir2a",
		}
	}

	filesToCreate := map[string][]string{
		"dir3":               {"file32.txt", "file33.jpg", "file34.html", "file35.png", "file36.webp", "file37.gif", "file38.jpeg", "file9.jpg"},
		"dir4":               {"file42.txt", "file43.jpg", "file44.html", "file45.png", "file46.webp", "file47.gif", "file48.jpeg", "file10.png"},
		"rootDir/dir1":       {"file12.txt", "file13.jpg", "file14.html", "file15.png", "file16.webp", "file17.gif", "file18.jpeg"},
		"rootDir/dir2":       {"file22.txt", "file23.jpg", "file24.html", "file25.png", "file26.webp", "file27.gif", "file28.jpeg"},
		"rootDir/dir2/dir2a": {"file2a2.txt", "file2a3.jpg", "file2a4.html", "file2a5.png", "file2a6.webp", "file2a7.gif", "file2a8.jpeg"},
	}

	// Create files in their real directories and populate the expectedFiles list
	// with the paths as they should be seen from the walk root.
	for realDir, files := range filesToCreate {
		reportedDir := realDirToReportedDir[realDir]
		for _, file := range files {
			mustWriteFile(t, root, file, realDir)
			// Do not add files from the unreadable directory to the expected list
			// when we intend to make it unreadable (non-Windows). On Windows the
			// permission test is skipped, so include those files in expectations.
			if realDir != "rootDir/dir2/dir2a" || runtime.GOOS == "windows" {
				expectedFiles = append(expectedFiles, filepath.ToSlash(filepath.Join(reportedDir, file)))
			}
		}
	}

	// This file is outside the walk root, so it should not be found by the walker.
	mustWriteFile(t, root, "file1.txt", "")

	// 3. Create all symlinks
	mustSymlink(t, root, "../dir3", "rootDir/alinkdir")
	mustSymlink(t, root, "../dir4", "rootDir/alinkdir2")
	mustSymlink(t, root, "../../dir1", "rootDir/dir1/dir1a/alinkdir_dup")

	// 4. Change permissions on a directory to make its content unaccessible.
	unreadableDir := filepath.Join(root, "rootDir", "dir2", "dir2a")
	if runtime.GOOS != "windows" {
		// On Unix systems, make the directory unreadable
		if err := os.Chmod(unreadableDir, 0o111); err != nil {
			t.Fatalf("os.Chmod on %s returned %v\n", unreadableDir, err)
		}
	} else {
		// On Windows, permission tests are skipped as they work differently
		t.Log("Skipping permission test on Windows")
	}

	// Give the filesystem a moment to settle
	time.Sleep(100 * time.Millisecond)

	sort.Strings(expectedFiles)

	// Return a cleanup function to restore permissions.
	cleanup := func() {
		if err := os.Chmod(unreadableDir, 0o755); err != nil {
			t.Errorf("Failed to restore permissions on %s: %v", unreadableDir, err)
		}
	}
	return expectedFiles, cleanup
}

// testWalkWithOptions is a helper function to reduce code duplication in option tests.
func testWalkWithOptions(t *testing.T, root string, opts []Option, expectedFiles []string, expectedErrors int, cleanup func()) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Failed to change directory to %s: %v", root, err)
	}
	defer func() {
		// Execute the provided cleanup function (if any).
		if cleanup != nil {
			cleanup()
		}
		// Change back to the original working directory.
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("Failed to change back to original directory %s: %v", oldWd, err)
		}
	}()

	walker := NewWalker(opts...)
	resultsChan, errChan := walker.ParallelWalk("rootDir")

	var actualFiles []string
	var errs []error

	for {
		select {
		case path, ok := <-resultsChan:
			if !ok {
				resultsChan = nil
			} else {
				actualFiles = append(actualFiles, filepath.ToSlash(path))
			}
		case err, ok := <-errChan:
			if !ok {
				errChan = nil
			} else {
				errs = append(errs, err)
			}
		}
		if resultsChan == nil && errChan == nil {
			break
		}
	}

	// --- Verify Errors ---
	if len(errs) != expectedErrors {
		t.Fatalf("Expected %d errors, but got %d: %v", expectedErrors, len(errs), errs)
	}

	// Only check details of the error if errors are expected.
	if expectedErrors > 0 {
		pathErr, ok := errs[0].(*fs.PathError)
		if !ok {
			t.Fatalf("Expected error to be of type *fs.PathError, but got %T", errs[0])
		}
		if pathErr.Op != "ReadDir" {
			t.Errorf("Expected PathError.Op to be 'ReadDir', but got %q", pathErr.Op)
		}
		expectedErrorPath := filepath.Join("rootDir", "dir2", "dir2a")
		if pathErr.Path != expectedErrorPath {
			t.Errorf("Expected PathError.Path to be %q, but got %q", expectedErrorPath, pathErr.Path)
		}
	}

	// --- Verify Files ---
	sort.Strings(actualFiles)
	sort.Strings(expectedFiles)
	if !reflect.DeepEqual(actualFiles, expectedFiles) {
		t.Errorf("Walk results mismatch:\nGot (%d files):\n%v\n\nWant (%d files):\n%v", len(actualFiles), actualFiles, len(expectedFiles), expectedFiles)
	}
}

// createSimpleTestDirStructure creates a basic directory structure without
// any unreadable directories or complex symlinks, suitable for testing
// filtering options. It returns the list of expected file paths.
func createSimpleTestDirStructure(t *testing.T, root string, filesToCreate map[string][]string) []string {
	var expectedFiles []string
	for dir, files := range filesToCreate {
		mustMkdir(t, root, dir)
		for _, file := range files {
			mustWriteFile(t, root, file, dir)
			expectedFiles = append(expectedFiles, filepath.ToSlash(filepath.Join(dir, file)))
		}
	}
	sort.Strings(expectedFiles)
	return expectedFiles
}

// TestParallelWalk verifies the functionality of the ParallelWalk method,
// including parallel traversal, symlink handling, loop detection, and
// error reporting for unreadable directories.
func TestParallelWalk(t *testing.T) {
	d := t.TempDir() // Create a temporary directory for the test.

	allFiles, cleanup := createComplexTestDirStructure(t, d)

	// Filter expected files based on platform
	var expectedFiles []string
	if runtime.GOOS == "windows" {
		// On Windows without symlinks, we only see files under rootDir
		for _, f := range allFiles {
			if strings.HasPrefix(f, "rootDir/") {
				expectedFiles = append(expectedFiles, f)
			}
		}
	} else {
		expectedFiles = allFiles
	}

	// On Windows, directory permissions work differently, and we might not get
	// the same permission errors as on Unix systems
	expectedErrors := 0
	if runtime.GOOS != "windows" {
		expectedErrors = 1 // Expect error from unreadable directory on Unix systems
	}

	testWalkWithOptions(t, d, nil, expectedFiles, expectedErrors, cleanup)
}

func TestWithMaxNumGoroutines(t *testing.T) {
	d := t.TempDir()
	defer func() {
		if err := os.RemoveAll(d); err != nil {
			t.Errorf("Failed to clean up temporary directory %s: %v", d, err)
		}
	}() // Clean up the temp directory.

	// Create a simple structure for this test.
	filesToCreate := map[string][]string{
		"rootDir": {"file1.txt", "file2.txt"},
	}
	expectedFiles := createSimpleTestDirStructure(t, d, filesToCreate)

	// Test with a specific number of goroutines (e.g., 1 for serial execution)
	testWalkWithOptions(t, d, []Option{WithMaxNumGoroutines(1)}, expectedFiles, 0, nil)

	// Test with 0 or negative, should use default
	testWalkWithOptions(t, d, []Option{WithMaxNumGoroutines(0)}, expectedFiles, 0, nil)
	testWalkWithOptions(t, d, []Option{WithMaxNumGoroutines(-5)}, expectedFiles, 0, nil)
}

func TestWithRegexpInclude(t *testing.T) {
	d := t.TempDir()
	defer func() {
		if err := os.RemoveAll(d); err != nil {
			t.Errorf("Failed to clean up temporary directory %s: %v", d, err)
		}
	}() // Clean up the temp directory.

	// Create a simple structure for this test.
	filesToCreate := map[string][]string{
		"rootDir":        {"test.txt", "image.jpg"},
		"rootDir/subDir": {"another.txt", "document.pdf"},
	}
	// The expected files for this test are defined manually below.
	createSimpleTestDirStructure(t, d, filesToCreate)

	// Test for .txt files
	txtRegex := regexp.MustCompile(`\.txt$`)
	expectedTxtFiles := []string{
		"rootDir/test.txt",
		"rootDir/subDir/another.txt",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpInclude(txtRegex)}, expectedTxtFiles, 0, nil)

	// Test for .jpg files
	jpgRegex := regexp.MustCompile(`\.jpg$`)
	expectedJpgFiles := []string{
		"rootDir/image.jpg",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpInclude(jpgRegex)}, expectedJpgFiles, 0, nil)

	// Test for files starting with 'doc'
	docRegex := regexp.MustCompile(`^doc`)
	expectedDocFiles := []string{
		"rootDir/subDir/document.pdf",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpInclude(docRegex)}, expectedDocFiles, 0, nil)
}

func TestWithRegexpExclude(t *testing.T) {
	d := t.TempDir()
	defer func() {
		if err := os.RemoveAll(d); err != nil {
			t.Errorf("Failed to clean up temporary directory %s: %v", d, err)
		}
	}() // Clean up the temp directory.

	// Create a simple structure for this test.
	filesToCreate := map[string][]string{
		"rootDir":            {"file1.txt", "file2.log"},
		"rootDir/excludeDir": {"file3.txt", "file4.log"},
		"rootDir/includeDir": {"file5.txt"},
	}
	createSimpleTestDirStructure(t, d, filesToCreate)

	// Exclude .log files
	logRegex := regexp.MustCompile(`\.log$`)
	expectedNoLogFiles := []string{
		"rootDir/file1.txt",
		"rootDir/excludeDir/file3.txt",
		"rootDir/includeDir/file5.txt",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpExclude(logRegex)}, expectedNoLogFiles, 0, nil)

	// Exclude directories named 'excludeDir' (and their contents)
	excludeDirRegex := regexp.MustCompile(`excludeDir`)
	expectedNoExcludeDirFiles := []string{
		"rootDir/file1.txt",
		"rootDir/file2.log",
		"rootDir/includeDir/file5.txt",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpExclude(excludeDirRegex)}, expectedNoExcludeDirFiles, 0, nil)

	// Exclude all .txt files
	txtRegex := regexp.MustCompile(`\.txt$`)
	expectedNoTxtFiles := []string{
		"rootDir/file2.log",
		"rootDir/excludeDir/file4.log",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpExclude(txtRegex)}, expectedNoTxtFiles, 0, nil)
}

func TestWithSizeNotZero(t *testing.T) {
	d := t.TempDir()
	defer func() {
		if err := os.RemoveAll(d); err != nil {
			t.Errorf("Failed to clean up temporary directory %s: %v", d, err)
		}
	}() // Clean up the temp directory.

	// Create a simple structure for this test.
	mustMkdir(t, d, "rootDir")
	mustWriteFile(t, d, "file1.txt", "rootDir")         // Size > 0
	mustWriteFile(t, d, "empty.txt", "rootDir")         // Size = 0
	mustWriteFile(t, d, "file2.log", "rootDir")         // Size > 0
	mustWriteFile(t, d, "another_empty.txt", "rootDir") // Size = 0

	expectedNonZeroFiles := []string{
		"rootDir/file1.txt",
		"rootDir/file2.log",
	}
	testWalkWithOptions(t, d, []Option{WithSizeNotZero()}, expectedNonZeroFiles, 0, nil)
}

func TestWithValidationFunc(t *testing.T) {
	d := t.TempDir()
	defer func() {
		if err := os.RemoveAll(d); err != nil {
			t.Errorf("Failed to clean up temporary directory %s: %v", d, err)
		}
	}() // Clean up the temp directory.

	// Create a simple structure for this test.
	mustMkdir(t, d, "rootDir")
	mustWriteFile(t, d, "apple.txt", "rootDir")
	mustWriteFile(t, d, "banana.jpg", "rootDir")
	mustWriteFile(t, d, "cherry.pdf", "rootDir")
	mustWriteFile(t, d, "date.txt", "rootDir")

	// Custom validation: only return files with "a" in their name and size > 5 bytes
	customValidation := func(path string, info fs.FileInfo) bool {
		return strings.Contains(filepath.Base(path), "a") && info.Size() > 5
	}
	expectedCustomFiles := []string{
		"rootDir/apple.txt",
		"rootDir/banana.jpg",
		"rootDir/date.txt",
	}
	testWalkWithOptions(t, d, []Option{WithValidationFunc(customValidation)}, expectedCustomFiles, 0, nil)

	// Custom validation: only return .pdf files
	pdfValidation := func(path string, info fs.FileInfo) bool {
		return filepath.Ext(path) == ".pdf"
	}
	expectedPdfFiles := []string{
		"rootDir/cherry.pdf",
	}
	testWalkWithOptions(t, d, []Option{WithValidationFunc(pdfValidation)}, expectedPdfFiles, 0, nil)
}

func TestMutualExclusivity(t *testing.T) {
	// Test WithValidationFunc vs WithRegexpInclude
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when WithValidationFunc and WithRegexpInclude are used together")
			}
		}()
		NewWalker(WithValidationFunc(func(path string, info fs.FileInfo) bool { return true }), WithRegexpInclude(regexp.MustCompile(".")))
	}()

	// Test WithValidationFunc vs WithSizeNotZero
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when WithValidationFunc and WithSizeNotZero are used together")
			}
		}()
		NewWalker(WithValidationFunc(func(path string, info fs.FileInfo) bool { return true }), WithSizeNotZero())
	}()
}

// TestParallelWalkWithContext_CancelStopsWalk verifies that cancelling the
// context stops the walk promptly, even when the directory tree is large.
func TestParallelWalkWithContext_CancelStopsWalk(t *testing.T) {
	d := t.TempDir()

	// Create a deep directory tree: 20 nested dirs, 10 files each = 200 files
	root := filepath.Join(d, "rootDir")
	mustMkdir(t, d, "rootDir")

	currentDir := "rootDir"
	for range 20 {
		dirName := filepath.Join(currentDir, "subdir")
		mustMkdir(t, d, dirName)
		// Create 10 files in each directory
		for j := range 10 {
			fileName := filepath.Join(d, dirName, "file"+string(rune('0'+j))+".txt")
			if err := os.WriteFile(fileName, []byte("content"), 0o644); err != nil {
				t.Fatalf("Failed to write file %s: %v", fileName, err)
			}
		}
		currentDir = dirName
	}

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start walk with context
	walker := NewWalker(WithContext(ctx))
	resultsChan, errChan := walker.ParallelWalk(root)

	// Drain a few results to confirm walk started
	receivedCount := 0
	for range 5 {
		select {
		case _, ok := <-resultsChan:
			if ok {
				receivedCount++
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Timed out waiting for initial results")
		}
	}

	if receivedCount == 0 {
		t.Fatal("Walk did not start - received no results")
	}

	// Cancel the context
	cancel()

	// Verify both channels close within deadline
	deadline := time.After(2 * time.Second)
	resultsClosed := false
	errsClosed := false

	// Drain remaining results and wait for channels to close
	totalReceived := receivedCount
drainLoop:
	for {
		select {
		case _, ok := <-resultsChan:
			if !ok {
				resultsClosed = true
				if errsClosed {
					break drainLoop
				}
			} else {
				totalReceived++
			}
		case _, ok := <-errChan:
			if !ok {
				errsClosed = true
				if resultsClosed {
					break drainLoop
				}
			}
		case <-deadline:
			t.Fatal("Channels did not close within 2 seconds after cancellation")
		}
	}

	// Verify we received fewer results than total files (walk was interrupted)
	if totalReceived >= 200 {
		t.Errorf("Expected walk to be interrupted (< 200 files), but received %d files", totalReceived)
	}

	t.Logf("Walk interrupted successfully after receiving %d/%d files", totalReceived, 200)
}

// TestParallelWalkWithContext_AlreadyCancelled verifies that starting a walk
// with a pre-cancelled context completes promptly with minimal results.
func TestParallelWalkWithContext_AlreadyCancelled(t *testing.T) {
	d := t.TempDir()

	// Create a simple directory structure
	mustMkdir(t, d, "rootDir")
	for i := range 10 {
		fileName := filepath.Join(d, "rootDir", "file"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(fileName, []byte("content"), 0o644); err != nil {
			t.Fatalf("Failed to write file %s: %v", fileName, err)
		}
	}

	// Create pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Start walk with pre-cancelled context
	walker := NewWalker(WithContext(ctx))
	resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))

	// Verify both channels close promptly
	deadline := time.After(1 * time.Second)
	var results []string
	resultsClosed := false
	errsClosed := false

drainLoop:
	for {
		select {
		case path, ok := <-resultsChan:
			if !ok {
				resultsClosed = true
				if errsClosed {
					break drainLoop
				}
			} else {
				results = append(results, path)
			}
		case _, ok := <-errChan:
			if !ok {
				errsClosed = true
				if resultsClosed {
					break drainLoop
				}
			}
			// Note: error value is intentionally discarded - this test verifies prompt exit, not error handling
		case <-deadline:
			t.Fatal("Channels did not close within 1 second with pre-cancelled context")
		}
	}

	// Verify zero or near-zero results (walk should exit immediately)
	if len(results) > 5 {
		t.Errorf("Expected near-zero results with pre-cancelled context, got %d", len(results))
	}

	t.Logf("Pre-cancelled walk exited promptly with %d results", len(results))
}

// TestParallelWalkWithContext_NilContext_DefaultsToBackground verifies that
// existing behavior (no WithContext option) still works identically.
func TestParallelWalkWithContext_NilContext_DefaultsToBackground(t *testing.T) {
	d := t.TempDir()

	// Create a simple structure
	filesToCreate := map[string][]string{
		"rootDir":        {"file1.txt", "file2.txt"},
		"rootDir/subDir": {"file3.txt", "file4.txt"},
	}
	expectedFiles := createSimpleTestDirStructure(t, d, filesToCreate)

	// Test without WithContext option (should use context.Background() internally)
	testWalkWithOptions(t, d, nil, expectedFiles, 0, nil)

	t.Log("Backwards compatibility verified - walk completes fully without context")
}

// TestParallelWalkWithContext_CancelDoesNotDeadlockOnFullBuffers verifies
// that cancellation doesn't deadlock when channel buffers are full and no
// consumer is draining them.
func TestParallelWalkWithContext_CancelDoesNotDeadlockOnFullBuffers(t *testing.T) {
	d := t.TempDir()

	// Create enough files to fill the 100-item channel buffer
	mustMkdir(t, d, "rootDir")
	for i := range 150 {
		fileName := filepath.Join(d, "rootDir", "file"+string(rune('0'+(i%10)))+string(rune('0'+(i/10)))+".txt")
		if err := os.WriteFile(fileName, []byte("content"), 0o644); err != nil {
			t.Fatalf("Failed to write file %s: %v", fileName, err)
		}
	}

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use single goroutine to serialize (makes test deterministic)
	walker := NewWalker(
		WithContext(ctx),
		WithMaxNumGoroutines(1),
	)
	resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))

	// Do NOT drain channels immediately - simulate a stopped consumer
	// Let the walker run for a moment to fill buffers
	time.Sleep(200 * time.Millisecond)

	// Cancel context
	cancel()

	// Verify walk goroutines exit within deadline (no deadlock)
	deadline := time.After(2 * time.Second)
	resultsClosed := false
	errsClosed := false

	// Now drain channels and verify they close
drainLoop:
	for {
		select {
		case _, ok := <-resultsChan:
			if !ok {
				resultsClosed = true
				if errsClosed {
					break drainLoop
				}
			}
		case _, ok := <-errChan:
			if !ok {
				errsClosed = true
				if resultsClosed {
					break drainLoop
				}
			}
		case <-deadline:
			t.Fatal("Walk goroutines did not exit within 2 seconds - likely deadlock on full buffers")
		}
	}

	t.Log("Walk exited without deadlock despite full channel buffers")
}
