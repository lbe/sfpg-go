package parallelwalkdir

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/osza"
)

type fakeFileInfo struct {
	name string
	size int64
	mode os.FileMode
	dir  bool
}

func (f *fakeFileInfo) Name() string       { return f.name }
func (f *fakeFileInfo) Size() int64        { return f.size }
func (f *fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f *fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f *fakeFileInfo) IsDir() bool        { return f.dir }
func (f *fakeFileInfo) Sys() any           { return nil }

func readDirAll(dir string, b *readDirBuffers) (n int, dirLen int, err error) {
	if cap(b.pathBuf) > len(b.pathBuf) {
		b.pathBuf = b.pathBuf[:cap(b.pathBuf)]
	}
	dirLen, err = writePathBuf(b.pathBuf, dir)
	if err != nil {
		return 0, 0, err
	}
	return readDirAllFromPathBuf(b, dirLen)
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

// drainChannels collects all results and errors from the channels, waiting for
// both to close. It returns early if the context cancels or the timeout fires.
func drainChannels(t *testing.T, resultsChan <-chan ReportedFile, errChan <-chan error, timeout time.Duration) ([]string, []error) {
	t.Helper()
	deadline := time.After(timeout)
	var files []string
	var errs []error

	for {
		select {
		case reported, ok := <-resultsChan:
			if !ok {
				resultsChan = nil
			} else {
				files = append(files, string(reported.Path))
			}
		case err, ok := <-errChan:
			if !ok {
				errChan = nil
			} else {
				errs = append(errs, err)
			}
		case <-deadline:
			t.Fatal("timed out draining channels")
		}
		if resultsChan == nil && errChan == nil {
			break
		}
	}
	return files, errs
}

// Helper to create directories, failing the test on error
func mustMkdir(t *testing.T, root string, path ...string) {
	fullPath := filepath.Join(path...)
	if err := os.MkdirAll(filepath.Join(root, fullPath), 0o755); err != nil {
		t.Fatalf("Failed to create directory %s: %v", fullPath, err)
	}
}

// Helper to create files, failing the test on error
func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	return entries
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

// overrideHook replaces the value at target with replacement and returns a
// function that restores the original value. Use with defer or t.Cleanup.
func overrideHook[T any](target *T, replacement T) func() {
	old := *target
	*target = replacement
	return func() { *target = old }
}

func regexpMust(s string) *regexp.Regexp {
	r, err := regexp.Compile(s)
	if err != nil {
		panic(err)
	}
	return r
}

func evalDirRealPathForTest(current, reported string, bufs *readDirBuffers) (PathHash, bool, error) {
	work := &dirWork{
		current:  cloneStringPath(current),
		reported: cloneStringPath(reported),
	}
	if bytes.Equal(work.current, work.reported) {
		work.reported = work.current
	}
	work.visitKey = HashPathBytes(work.current)
	dirLen, err := writePathBytes(bufs.pathBuf, work.current)
	if err != nil {
		return PathHash{}, false, err
	}
	return evalDirRealPath(work, bufs, dirLen)
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
				actualFiles = append(actualFiles, filepath.ToSlash(string(path.Path)))
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
		var pathErr *fs.PathError
		if !errors.As(errs[0], &pathErr) {
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

func TestBasenameInclude_MatchesRegexp(t *testing.T) {
	const pattern = `(?i)(?:jpe?g|png)$`
	re := regexp.MustCompile(pattern)
	match := basenameIncludeForRegexp(re)
	names := [][]byte{
		[]byte("IMG_0001.JPG"),
		[]byte("vacation.jpeg"),
		[]byte("thumb.png"),
		[]byte("notes.txt"),
		[]byte("pic.jpg"),
		[]byte("x.gif"),
		[]byte("noextension"),
		[]byte(".jpg"),
	}
	for _, name := range names {
		want := re.Match(name)
		got := match(name)
		if got != want {
			t.Errorf("name %q: bytes=%v regexp=%v", name, got, want)
		}
	}
}

func TestBasenameInBuf(t *testing.T) {
	tests := []struct {
		path string
		n    int
		want string
	}{
		{path: "", n: 0, want: ""},
		{path: "/a/b/c.jpg", n: len("/a/b/c.jpg"), want: "c.jpg"},
		{path: "file.txt", n: len("file.txt"), want: "file.txt"},
		{path: "/only", n: len("/only"), want: "only"},
	}
	for _, tt := range tests {
		got := string(basenameInBuf([]byte(tt.path), tt.n))
		if got != tt.want {
			t.Errorf("basenameInBuf(%q, %d)=%q want %q", tt.path, tt.n, got, tt.want)
		}
	}
}

func TestBasenameIncludeForRegexp_CachesByPattern(t *testing.T) {
	const pattern = `(?i)png$`
	_ = basenameIncludeForRegexp(regexp.MustCompile(pattern))
	fn := basenameIncludeForRegexp(regexp.MustCompile(pattern))
	if !fn([]byte("photo.png")) {
		t.Fatal("expected match")
	}
	if _, ok := basenameIncludeByPattern.Load(pattern); !ok {
		t.Fatal("expected pattern cached after first compile")
	}
}

func TestBasenameSuffixesFromPattern_Invalid(t *testing.T) {
	_, err := basenameSuffixesFromPattern(`(?i)^prefix`)
	if err == nil {
		t.Fatal("expected error for non-suffix pattern")
	}
	_, err = basenameSuffixesFromPattern(`(?i)(?:`)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestEvalDirRealPath(t *testing.T) {
	bufs := newReadDirBuffers()
	d := t.TempDir()
	plain := filepath.Join(d, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	got, loopGuard, err := evalDirRealPathForTest(plain, plain, bufs)
	if err != nil {
		t.Fatalf("evalDirRealPath: %v", err)
	}
	want := HashPathBytes([]byte(plain))
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if loopGuard {
		t.Fatal("expected loopGuard false for plain dir")
	}

	if runtime.GOOS == "windows" {
		t.Skip("symlink eval on Windows")
	}
	target := filepath.Join(d, "target")
	if err = os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "link")
	if err = os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, loopGuard, err = evalDirRealPathForTest(link, link, bufs)
	if err != nil {
		t.Fatalf("evalDirRealPath symlink dir: %v", err)
	}
	want = HashPathBytes([]byte(target))
	if got != want {
		t.Fatalf("got %s want canonical hash %s", got, want)
	}
	if !loopGuard {
		t.Fatal("expected loopGuard true for symlink dir")
	}

	got, loopGuard, err = evalDirRealPathForTest(link, filepath.Join(d, "reported"), bufs)
	if err != nil {
		t.Fatalf("evalDirRealPath divergent: %v", err)
	}
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if !loopGuard {
		t.Fatal("expected loopGuard true for divergent paths")
	}
}

func TestDefaultWalkLstatJoin(t *testing.T) {
	d := t.TempDir()
	mustWriteFile(t, d, "photo.jpg")

	bufs := newReadDirBuffers()
	info, err := defaultWalkLstatJoin(bufs, d, []byte("photo.jpg"))
	if err != nil {
		t.Fatalf("defaultWalkLstatJoin: %v", err)
	}
	if info.Size() != int64(len("photo.jpg")) {
		t.Fatalf("size=%d", info.Size())
	}
}

func TestFilterAndReportFile(t *testing.T) {
	w := NewWalker(WithRegexpInclude(regexp.MustCompile(`\.txt$`)))
	w.results = make(chan ReportedFile, 1)

	info := &fakeFileInfo{name: "a.txt", size: 10}
	path := []byte(filepath.Join("root", "a.txt"))
	w.filterAndReportFile(path, len(path), info, false, nil, nil)

	select {
	case rf := <-w.results:
		if !strings.HasSuffix(string(rf.Path), "a.txt") {
			t.Fatalf("path=%q", string(rf.Path))
		}
	default:
		t.Fatal("expected result on include match")
	}

	binPath := []byte(filepath.Join("root", "b.bin"))
	w.filterAndReportFile(binPath, len(binPath), &fakeFileInfo{name: "b.bin", size: 1}, false, nil, nil)
	select {
	case <-w.results:
		t.Fatal("non-matching basename must be filtered via basenameInBuf")
	default:
	}

	w2 := NewWalker(WithSizeNotZero())
	w2.results = make(chan ReportedFile, 1)
	w2.filterAndReportFile(path, len(path), &fakeFileInfo{name: "a.txt", size: 0}, true, nil, nil)
	select {
	case <-w2.results:
		t.Fatal("zero size must be dropped")
	default:
	}

	w3 := NewWalker(
		WithBasenameInclude(regexp.MustCompile(`(?i)jpg$`)),
		WithSizeNotZero(),
	)
	w3.results = make(chan ReportedFile, 2)
	jpgPath := []byte("/p/zero.jpg")
	w3.filterAndReportFile(jpgPath, len(jpgPath), &fakeFileInfo{name: "zero.jpg", size: 0}, false, nil, nil)
	select {
	case <-w3.results:
		t.Fatal("zero-length file must not be reported")
	default:
	}
	photoPath := []byte("/p/photo.jpg")
	w3.filterAndReportFile(photoPath, len(photoPath), &fakeFileInfo{name: "photo.jpg", size: 12}, false, nil, nil)
	select {
	case rf := <-w3.results:
		if string(rf.Path) != "/p/photo.jpg" || rf.SizeBytes != 12 {
			t.Fatalf("rf=%+v", rf)
		}
	default:
		t.Fatal("expected reported file")
	}
	txtPath := []byte("/p/notes.txt")
	w3.filterAndReportFile(txtPath, len(txtPath), &fakeFileInfo{name: "notes.txt", size: 3}, false, nil, nil)
	select {
	case <-w3.results:
		t.Fatal("non-matching basename must be skipped")
	default:
	}
}

func TestFileInfoForDirEntry(t *testing.T) {
	d := t.TempDir()
	mustWriteFile(t, d, "photo.jpg")

	bufs := newReadDirBuffers()
	n, _, err := readDirAll(d, bufs)
	if err != nil {
		t.Fatal(err)
	}
	var fileEnt *osza.Entry
	for i := range n {
		if string(bufs.entries[i].Name) == "photo.jpg" {
			fileEnt = &bufs.entries[i]
			break
		}
	}
	if fileEnt == nil {
		t.Fatal("photo.jpg entry not found")
	}

	info, err := fileInfoForDirEntry(bufs, fileEnt)
	if err != nil {
		t.Fatalf("fileInfoForDirEntry: %v", err)
	}
	if info.Size() <= 0 {
		t.Fatalf("expected non-zero size, got %d", info.Size())
	}

	defer overrideHook(&fileInfoForDirEntryFn, func(_ *readDirBuffers, _ *osza.Entry) (fs.FileInfo, error) {
		return nil, errors.New("info denied")
	})()
	_, err = fileInfoForDirEntry(bufs, fileEnt)
	if err == nil {
		t.Fatal("expected error from fileInfoForDirEntryFn hook")
	}
}

func TestIncludeBasenameMatches(t *testing.T) {
	w := NewWalker(WithRegexpInclude(regexp.MustCompile(`\.png$`)))
	if !w.includeBasenameMatchesBytes([]byte("x.png")) {
		t.Fatal("expected png match")
	}
	if w.includeBasenameMatchesBytes([]byte("x.jpg")) {
		t.Fatal("expected jpg non-match")
	}

	wb := NewWalker(WithBasenameInclude(regexp.MustCompile(`(?i)gif$`)))
	if !wb.includeBasenameMatchesBytes([]byte("a.GIF")) {
		t.Fatal("expected basename include match")
	}
}

func TestMutualExclusivity(t *testing.T) {
	// Test WithValidationFunc vs WithRegexpInclude
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when WithValidationFunc and WithRegexpInclude are used together")
			}
		}()
		NewWalker(WithValidationFunc(func(_ []byte, _ fs.FileInfo) bool { return true }), WithRegexpInclude(regexp.MustCompile(".")))
	}()

	// Test WithValidationFunc vs WithSizeNotZero
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when WithValidationFunc and WithSizeNotZero are used together")
			}
		}()
		NewWalker(WithValidationFunc(func(_ []byte, _ fs.FileInfo) bool { return true }), WithSizeNotZero())
	}()
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

func TestParallelWalk_FeederStopEmptyQueue(t *testing.T) {
	d := t.TempDir()
	walker := NewWalker(WithMaxWorkers(4))
	resultsChan, errChan := walker.ParallelWalk(d)

	done := make(chan struct{})
	go func() {
		defer close(done)
		resCh, errCh := resultsChan, errChan
		for resCh != nil || errCh != nil {
			select {
			case _, ok := <-resCh:
				if !ok {
					resCh = nil
				}
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("walk did not finish within 3s (feeder blocked on empty dir queue after stopFeeder)")
	}
}

func TestParallelWalk_NoDeadlockWideSubdirSchedule(t *testing.T) {
	// Serial: stable timing; do not call t.Parallel().
	// Four wide siblings with tiny dirCh reproduces pre-feeder deadlock (workers
	// blocked on dirCh send with no receiver); feeder + schedule must complete.
	const wideParents = 4
	const subdirsPerParent = 512 // 4*512 >= 2000 immediate child subdirs under root

	d := t.TempDir()
	root := filepath.Join(d, "wideParent")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	for p := range wideParents {
		parent := filepath.Join(root, fmt.Sprintf("wide%02d", p))
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", parent, err)
		}
		for i := range subdirsPerParent {
			sub := filepath.Join(parent, fmt.Sprintf("subdir%04d", i))
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", sub, err)
			}
			if err := os.WriteFile(filepath.Join(sub, "photo.jpg"), []byte("jpg"), 0o644); err != nil {
				t.Fatalf("write jpg in %s: %v", sub, err)
			}
		}
	}

	jpgRegex := regexp.MustCompile(`(?i)\.jpe?g$`)
	walker := NewWalker(
		WithMaxWorkers(4),
		WithDirChCapacity(8),
		WithRegexpInclude(jpgRegex),
	)
	resultsChan, errChan := walker.ParallelWalk(root)

	walkDone := make(chan struct{})
	go func() {
		defer close(walkDone)
		resCh, errCh := resultsChan, errChan
		for resCh != nil || errCh != nil {
			select {
			case _, ok := <-resCh:
				if !ok {
					resCh = nil
				}
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				}
			}
		}
	}()

	select {
	case <-walkDone:
	case <-time.After(5 * time.Second):
		t.Fatal("walk did not complete within 5s (deadlock on wide subdir schedule)")
	}
}

func TestParallelWalk_WideTreeBoundedGoroutines(t *testing.T) {
	t.Run("250 sibling subdirs", func(t *testing.T) {
		// Serial subtest: do not use t.Parallel(); baseline goroutine count must be stable.
		// goroutineSlack (32) is the max extra goroutines above baseline during ParallelWalk,
		// accounting for workers, drain/poll helpers, and runtime noise.
		const goroutineSlack = 32

		d := t.TempDir()
		root := filepath.Join(d, "wideRoot")
		for i := range 250 {
			sub := filepath.Join(root, fmt.Sprintf("subdir%03d", i))
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", sub, err)
			}
			if err := os.WriteFile(filepath.Join(sub, "photo.jpg"), []byte("jpg"), 0o644); err != nil {
				t.Fatalf("write jpg: %v", err)
			}
		}

		jpgRegex := regexp.MustCompile(`(?i)\.jpe?g$`)
		baseline := runtime.NumGoroutine()

		var peak atomic.Int64
		recordPeak := func() {
			n := int64(runtime.NumGoroutine())
			for {
				old := peak.Load()
				if n <= old {
					return
				}
				if peak.CompareAndSwap(old, n) {
					return
				}
			}
		}
		peak.Store(int64(baseline))

		walker := NewWalker(WithMaxWorkers(4), WithRegexpInclude(jpgRegex))
		resultsChan, errChan := walker.ParallelWalk(root)

		walkDone := make(chan struct{})
		go func() {
			defer close(walkDone)
			resCh, errCh := resultsChan, errChan
			for resCh != nil || errCh != nil {
				select {
				case _, ok := <-resCh:
					if !ok {
						resCh = nil
					}
					recordPeak()
				case _, ok := <-errCh:
					if !ok {
						errCh = nil
					}
					recordPeak()
				}
			}
			recordPeak()
		}()

		pollDone := make(chan struct{})
		go func() {
			defer close(pollDone)
			ticker := time.NewTicker(500 * time.Microsecond)
			defer ticker.Stop()
			for {
				select {
				case <-walkDone:
					recordPeak()
					return
				case <-ticker.C:
					recordPeak()
				}
			}
		}()

		select {
		case <-walkDone:
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for walk to finish")
		}
		<-pollDone

		if got := int(peak.Load()); got > baseline+goroutineSlack {
			t.Fatalf("peak goroutines %d > baseline+slack (%d+%d=%d)", got, baseline, goroutineSlack, baseline+goroutineSlack)
		}
	})
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
				results = append(results, string(path.Path))
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
		WithMaxWorkers(1),
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

func TestReadDirAll(t *testing.T) {
	d := t.TempDir()
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("f%d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	bufs := newReadDirBuffers()
	n, dirLen, err := readDirAll(d, bufs)
	if err != nil {
		t.Fatalf("readDirAll: %v", err)
	}
	if dirLen != len(d) {
		t.Fatalf("dirLen=%d want %d", dirLen, len(d))
	}
	if n != 5 {
		t.Fatalf("n=%d want 5", n)
	}
}

func TestReadDirAllGrowOnOverflow(t *testing.T) {
	d := t.TempDir()
	const nFiles = 300
	for i := range nFiles {
		name := fmt.Sprintf("file_%04d.dat", i)
		if err := os.WriteFile(filepath.Join(d, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	bufs := newReadDirBuffers()
	bufs.entries = make([]osza.Entry, 16)
	bufs.nameBuf = make([]byte, 4096)

	n, dirLen, err := readDirAll(d, bufs)
	if err != nil {
		t.Fatalf("readDirAll: %v", err)
	}
	if dirLen != len(d) {
		t.Fatalf("dirLen=%d want %d", dirLen, len(d))
	}
	if n != nFiles {
		t.Fatalf("n=%d want %d", n, nFiles)
	}
	if len(bufs.entries) < nFiles {
		t.Fatalf("entries cap=%d want at least %d after growth", len(bufs.entries), nFiles)
	}
}

func TestReadDirAllGrowJoinBufOnErrPathBuffer(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "one.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	saved := oszaReadDir
	var calls int
	defer overrideHook(&oszaReadDir, func(dirPath []byte, dirLen int, entries []osza.Entry, nameBuf, scratch, joinBuf []byte) (int, error) {
		calls++
		if calls == 1 {
			return 0, osza.ErrPathBuffer
		}
		return saved(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
	})()

	bufs := newReadDirBuffers()
	bufs.joinBuf = make([]byte, 32)
	n, _, err := readDirAll(d, bufs)
	if err != nil {
		t.Fatalf("readDirAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("n=%d want 1", n)
	}
	if len(bufs.joinBuf) <= 32 {
		t.Fatalf("joinBuf should grow after ErrPathBuffer, len=%d", len(bufs.joinBuf))
	}
}

func TestReadDirAllPropagatesReadError(t *testing.T) {
	bufs := newReadDirBuffers()
	_, _, err := readDirAll(filepath.Join(t.TempDir(), "missing"), bufs)
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestWalkPlainDirTreeSkipsEvalSymlinks(t *testing.T) {
	savedEval := evalSymlinksAt
	var evalCalls atomic.Int32
	defer overrideHook(&evalSymlinksAt, func(pathBuf []byte, pathLen int, dest []byte) (int, error) {
		evalCalls.Add(1)
		return savedEval(pathBuf, pathLen, dest)
	})()

	d := t.TempDir()
	mustMkdir(t, d, "rootDir", "sub")
	mustWriteFile(t, d, "photo.jpg", "rootDir", "sub")

	walker := NewWalker(WithMaxWorkers(1))
	resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(results) != 1 {
		t.Fatalf("results=%d want 1", len(results))
	}
	if evalCalls.Load() != 0 {
		t.Fatalf("EvalSymlinks calls=%d want 0 for aligned non-symlink dirs", evalCalls.Load())
	}
}

func TestWithBasenameInclude_PanicsWithRegexpInclude(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	NewWalker(
		WithRegexpInclude(regexp.MustCompile(".")),
		WithBasenameInclude(regexp.MustCompile(`(?i)png$`)),
	)
}

func TestWithBasenameInclude_InvalidPatternPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unsupported basename pattern")
		}
	}()
	NewWalker(WithBasenameInclude(regexp.MustCompile(`(?i)^prefix`)))
}

func TestWithMaxWorkers(t *testing.T) {
	d := t.TempDir()
	defer func() {
		if err := os.RemoveAll(d); err != nil {
			t.Errorf("Failed to clean up temporary directory %s: %v", d, err)
		}
	}()

	filesToCreate := map[string][]string{
		"rootDir": {"file1.txt", "file2.txt"},
	}
	expectedFiles := createSimpleTestDirStructure(t, d, filesToCreate)

	testWalkWithOptions(t, d, []Option{WithMaxWorkers(1)}, expectedFiles, 0, nil)

	// 0 or negative → default GOMAXPROCS(0) workers (asserted after worker-pool impl).
	testWalkWithOptions(t, d, []Option{WithMaxWorkers(0)}, expectedFiles, 0, nil)
	testWalkWithOptions(t, d, []Option{WithMaxWorkers(-5)}, expectedFiles, 0, nil)
}

func TestWithMaxReportedFiles(t *testing.T) {
	d := t.TempDir()
	mustMkdir(t, d, "rootDir")
	for i := range 5 {
		mustWriteFile(t, d, fmt.Sprintf("f%d.txt", i), "rootDir")
	}

	walker := NewWalker(WithMaxReportedFiles(2), WithMaxWorkers(1))
	resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(results) != 2 {
		t.Fatalf("results=%d want 2", len(results))
	}
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
	excludeTxtRegex := regexp.MustCompile(`\.txt$`)
	expectedNoTxtFiles := []string{
		"rootDir/file2.log",
		"rootDir/excludeDir/file4.log",
	}
	testWalkWithOptions(t, d, []Option{WithRegexpExclude(excludeTxtRegex)}, expectedNoTxtFiles, 0, nil)
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
	customValidation := func(path []byte, info fs.FileInfo) bool {
		base := path
		if i := bytes.LastIndexByte(path, '/'); i >= 0 {
			base = path[i+1:]
		}
		return bytes.Contains(base, []byte("a")) && info.Size() > 5
	}
	expectedCustomFiles := []string{
		"rootDir/apple.txt",
		"rootDir/banana.jpg",
		"rootDir/date.txt",
	}
	testWalkWithOptions(t, d, []Option{WithValidationFunc(customValidation)}, expectedCustomFiles, 0, nil)

	// Custom validation: only return .pdf files
	pdfValidation := func(path []byte, info fs.FileInfo) bool {
		return bytes.HasSuffix(path, []byte(".pdf"))
	}
	expectedPdfFiles := []string{
		"rootDir/cherry.pdf",
	}
	testWalkWithOptions(t, d, []Option{WithValidationFunc(pdfValidation)}, expectedPdfFiles, 0, nil)
}

func TestWalk_ContextCancelled_AfterReadDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer overrideHook(&readDirAllFn, func(b *readDirBuffers, dirLen int) (int, int, error) {
		cancel()
		return readDirAllFromPathBuf(b, dirLen)
	})()

	d := t.TempDir()
	root := filepath.Join(d, "rootDir")
	mustMkdir(t, d, "rootDir")
	for i := range 5 {
		mustWriteFile(t, d, fmt.Sprintf("file%d.txt", i), "rootDir")
	}

	walker := NewWalker(WithContext(ctx), WithMaxWorkers(1))
	resultsChan, errChan := walker.ParallelWalk(root)
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after cancellation, got %d: %v", len(results), results)
	}
}

func TestWalk_ContextCancelled_SkipsErrorSends(t *testing.T) {
	cases := []struct {
		name       string
		setupHooks func()
	}{
		{
			name: "EvalSymlinks error",
			setupHooks: func() {
				overrideHook(&evalSymlinksAt, func(_ []byte, _ int, _ []byte) (int, error) {
					return 0, errors.New("eval denied")
				})()
			},
		},
		{
			name: "ReadDir+Lstat error",
			setupHooks: func() {
				overrideHook(&readDirAllFn, func(_ *readDirBuffers, dirLen int) (int, int, error) {
					return 0, dirLen, errors.New("read dir denied")
				})()
				overrideHook(&walkLstatAt, func(_ *readDirBuffers, _ string) (fs.FileInfo, error) {
					return nil, errors.New("lstat denied")
				})()
			},
		},
		{
			name: "fileInfo error",
			setupHooks: func() {
				overrideHook(&fileInfoForDirEntryFn, func(_ *readDirBuffers, _ *osza.Entry) (fs.FileInfo, error) {
					return nil, errors.New("info denied")
				})()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupHooks()

			d := t.TempDir()
			mustMkdir(t, d, "rootDir")
			mustWriteFile(t, d, "file1.txt", "rootDir")

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			walker := NewWalker(WithContext(ctx), WithMaxWorkers(1))
			resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))
			results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

			if len(errs) != 0 {
				t.Fatalf("expected 0 errors with cancelled context, got %d: %v", len(errs), errs)
			}
			if len(results) != 0 {
				t.Errorf("expected 0 results with cancelled context, got %d", len(results))
			}
		})
	}
}

func TestWalk_EvalSymlinksError(t *testing.T) {
	cases := []struct {
		name       string
		cancelled  bool
		wantErrs   int
		wantOp     string
		wantErrMsg string
	}{
		{
			name:       "context alive",
			cancelled:  false,
			wantErrs:   1,
			wantOp:     "EvalSymlinks",
			wantErrMsg: "eval denied",
		},
		{
			name:      "context cancelled",
			cancelled: true,
			wantErrs:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer overrideHook(&evalSymlinksAt, func(_ []byte, _ int, _ []byte) (int, error) {
				return 0, errors.New("eval denied")
			})()

			d := t.TempDir()
			mustMkdir(t, d, "target")
			mustWriteFile(t, d, "file1.txt", "target")
			mustSymlink(t, d, "target", "rootLink")

			ctx := context.Background()
			if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			walker := NewWalker(WithContext(ctx), WithMaxWorkers(1))
			resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootLink"))
			_, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

			if len(errs) != tc.wantErrs {
				t.Fatalf("expected %d errors, got %d: %v", tc.wantErrs, len(errs), errs)
			}
			if tc.wantErrs > 0 {
				var pathErr *fs.PathError
				if !errors.As(errs[0], &pathErr) {
					t.Fatalf("expected *fs.PathError, got %T", errs[0])
				}
				if pathErr.Op != tc.wantOp {
					t.Errorf("expected PathError.Op %q, got %q", tc.wantOp, pathErr.Op)
				}
				if !strings.Contains(pathErr.Err.Error(), tc.wantErrMsg) {
					t.Errorf("expected error containing %q, got %q", tc.wantErrMsg, pathErr.Err.Error())
				}
			}
		})
	}
}

func TestWalk_IncludeRegexSkipsLstatJoinForNonMatchingNames(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "rootDir")
	mustMkdir(t, d, "rootDir")
	mustWriteFile(t, d, "photo.jpg", "rootDir")
	mustWriteFile(t, d, "notes.txt", "rootDir")

	var photoEntryType fs.FileMode
	for _, e := range mustReadDir(t, root) {
		if e.Name() == "photo.jpg" {
			photoEntryType = e.Type()
			break
		}
	}

	jpgRegex := regexp.MustCompile(`(?i)(?:jpe?g|gif|png)$`)
	var infoCalls atomic.Int32
	defer overrideHook(&fileInfoForDirEntryFn, func(bufs *readDirBuffers, entry *osza.Entry) (fs.FileInfo, error) {
		infoCalls.Add(1)
		if string(entry.Name) == "notes.txt" {
			t.Fatalf("metadata read for notes.txt")
		}
		return defaultFileInfoForDirEntry(bufs, entry)
	})()

	walker := NewWalker(
		WithMaxWorkers(1),
		WithRegexpInclude(jpgRegex),
		WithSizeNotZero(),
	)
	resultsChan, errChan := walker.ParallelWalk(root)
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), results)
	}
	if filepath.Base(results[0]) != "photo.jpg" {
		t.Errorf("expected photo.jpg, got %s", results[0])
	}
	if got := infoCalls.Load(); got != 1 {
		t.Fatalf("fileInfoForDirEntryFn calls=%d want 1 (photo type=%v)", got, photoEntryType)
	}
}

func TestWalk_LstatJoinError(t *testing.T) {
	cases := []struct {
		name            string
		info            os.FileInfo
		infoErr         error
		wantErrs        int
		wantResultCount int
	}{
		{
			name:            "info error",
			info:            nil,
			infoErr:         errors.New("info denied"),
			wantErrs:        1,
			wantResultCount: 0,
		},
		{
			name:            "nil info",
			info:            nil,
			infoErr:         nil,
			wantErrs:        0,
			wantResultCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer overrideHook(&fileInfoForDirEntryFn, func(_ *readDirBuffers, _ *osza.Entry) (fs.FileInfo, error) {
				return tc.info, tc.infoErr
			})()

			d := t.TempDir()
			mustMkdir(t, d, "rootDir")
			mustWriteFile(t, d, "file1.txt", "rootDir")

			walker := NewWalker(WithMaxWorkers(1))
			resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))
			results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

			if len(errs) != tc.wantErrs {
				t.Fatalf("expected %d errors, got %d: %v", tc.wantErrs, len(errs), errs)
			}
			if tc.wantErrs > 0 {
				var pathErr *fs.PathError
				if !errors.As(errs[0], &pathErr) {
					t.Fatalf("expected *fs.PathError, got %T", errs[0])
				}
				if pathErr.Op != "Stat" {
					t.Errorf("expected PathError.Op 'Stat', got %q", pathErr.Op)
				}
			}
			if len(results) != tc.wantResultCount {
				t.Errorf("expected %d results, got %d", tc.wantResultCount, len(results))
			}
		})
	}
}

func TestWalk_ReaddirMetaSkipsWalkStatHooks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows ReadDir fills entry meta via finddata")
	}
	d := t.TempDir()
	root := filepath.Join(d, "imgs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		name := filepath.Join(root, fmt.Sprintf("photo%d.jpg", i))
		if err := os.WriteFile(name, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var joinCalls, atCalls atomic.Int32
	savedJoin := walkLstatJoin
	defer overrideHook(&walkLstatJoin, func(bufs *readDirBuffers, parentPath string, name []byte) (fs.FileInfo, error) {
		joinCalls.Add(1)
		return savedJoin(bufs, parentPath, name)
	})()
	savedAt := walkLstatAt
	defer overrideHook(&walkLstatAt, func(bufs *readDirBuffers, path string) (fs.FileInfo, error) {
		atCalls.Add(1)
		return savedAt(bufs, path)
	})()

	jpgRegex := regexp.MustCompile(`(?i)jpe?g$`)
	walker := NewWalker(WithRegexpInclude(jpgRegex), WithMaxWorkers(1))
	resultsChan, errChan := walker.ParallelWalk(root)
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(results) != 4 {
		t.Fatalf("results=%d want 4", len(results))
	}
	if joinCalls.Load() != 0 || atCalls.Load() != 0 {
		t.Fatalf("walkLstatJoin=%d walkLstatAt=%d want 0", joinCalls.Load(), atCalls.Load())
	}
}

func TestWalk_ReadDirError(t *testing.T) {
	cases := []struct {
		name            string
		cancelled       bool
		lstatErr        error
		lstatIsDir      bool
		wantErrs        int
		wantErrOp       string
		wantResultCount int
	}{
		{
			name:       "lstat also fails",
			cancelled:  false,
			lstatErr:   errors.New("lstat denied"),
			lstatIsDir: true,
			wantErrs:   1,
			wantErrOp:  "ReadDir",
		},
		{
			name:            "path is a file",
			cancelled:       false,
			lstatErr:        nil,
			lstatIsDir:      false,
			wantErrs:        0,
			wantResultCount: 1,
		},
		{
			name:       "context cancelled",
			cancelled:  true,
			lstatErr:   errors.New("lstat denied"),
			lstatIsDir: true,
			wantErrs:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer overrideHook(&readDirAllFn, func(_ *readDirBuffers, dirLen int) (int, int, error) {
				return 0, dirLen, errors.New("read dir denied")
			})()

			defer overrideHook(&walkLstatPathLen, func(_ *readDirBuffers, _ int) (fs.FileInfo, error) {
				if tc.lstatErr != nil {
					return nil, tc.lstatErr
				}
				return &fakeFileInfo{dir: tc.lstatIsDir}, nil
			})()

			d := t.TempDir()
			mustMkdir(t, d, "rootDir")
			mustWriteFile(t, d, "file1.txt", "rootDir")

			ctx := context.Background()
			if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			walker := NewWalker(WithContext(ctx), WithMaxWorkers(1))
			resultsChan, errChan := walker.ParallelWalk(filepath.Join(d, "rootDir"))
			results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

			if len(errs) != tc.wantErrs {
				t.Fatalf("expected %d errors, got %d: %v", tc.wantErrs, len(errs), errs)
			}
			if tc.wantErrs > 0 {
				var pathErr *fs.PathError
				if !errors.As(errs[0], &pathErr) {
					t.Fatalf("expected *fs.PathError, got %T", errs[0])
				}
				if pathErr.Op != tc.wantErrOp {
					t.Errorf("expected PathError.Op %q, got %q", tc.wantErrOp, pathErr.Op)
				}
			}
			if len(results) != tc.wantResultCount {
				t.Errorf("expected %d results, got %d", tc.wantResultCount, len(results))
			}
		})
	}
}

func TestWalk_SymlinkStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests skipped on Windows")
	}

	d := t.TempDir()
	root := filepath.Join(d, "rootDir")
	mustMkdir(t, d, "rootDir")
	mustWriteFile(t, d, "regular.txt", "rootDir")
	mustSymlink(t, d, "/nonexistent/target", "rootDir/badlink")

	savedStat := walkStatAt
	defer overrideHook(&walkStatAt, func(bufs *readDirBuffers, name string) (fs.FileInfo, error) {
		if name == filepath.Join(root, "badlink") {
			return nil, errors.New("stat denied")
		}
		return savedStat(bufs, name)
	})()

	walker := NewWalker(WithMaxWorkers(1))
	resultsChan, errChan := walker.ParallelWalk(root)
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)

	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), results)
	}
	if filepath.Base(results[0]) != "regular.txt" {
		t.Errorf("expected regular.txt, got %s", results[0])
	}
}

func TestWalk_SymlinkFileIncludeFilter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests skipped on Windows")
	}

	d := t.TempDir()
	root := filepath.Join(d, "rootDir")
	mustMkdir(t, d, "rootDir")
	mustWriteFile(t, d, "photo.jpg", "rootDir")
	mustWriteFile(t, d, "notes.txt", "rootDir")
	mustSymlink(t, d, "photo.jpg", "rootDir/link.jpg")

	walker := NewWalker(
		WithMaxWorkers(1),
		WithRegexpInclude(regexp.MustCompile(`(?i)jpe?g$`)),
	)
	resultsChan, errChan := walker.ParallelWalk(root)
	results, errs := drainChannels(t, resultsChan, errChan, 2*time.Second)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	sort.Strings(results)
	want := []string{
		filepath.Join(root, "link.jpg"),
		filepath.Join(root, "photo.jpg"),
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results=%v want %v", results, want)
	}
}

func TestWalkUsesOszaReadDir(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pic.png"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	var oszaCalls int
	saved := oszaReadDir
	oszaReadDir = func(dirPath []byte, dirLen int, entries []osza.Entry, nameBuf, scratch, joinBuf []byte) (int, error) {
		oszaCalls++
		return saved(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
	}
	t.Cleanup(func() { oszaReadDir = saved })

	walker := NewWalker(WithMaxWorkers(1), WithRegexpInclude(regexpMust(`(?i)\.png$`)))
	results, errs := walker.ParallelWalk(root)
	got, errList := drainChannels(t, results, errs, 2*time.Second)
	if len(errList) != 0 {
		t.Fatalf("errors: %v", errList)
	}
	if len(got) != 1 {
		t.Fatalf("results=%v", got)
	}
	if oszaCalls == 0 {
		t.Fatal("expected oszaReadDir to be called during walk")
	}
}

func TestWritePathBytes(t *testing.T) {
	buf := make([]byte, 8)
	n, err := writePathBytes(buf, []byte("short"))
	if err != nil {
		t.Fatalf("writePathBytes: %v", err)
	}
	if n != len("short") {
		t.Fatalf("n=%d", n)
	}
	if _, err = writePathBytes(buf, nil); err == nil {
		t.Fatal("expected error for empty path")
	}
	if _, err = writePathBytes(buf, []byte("012345678")); err == nil {
		t.Fatal("expected error when path exceeds buffer")
	}
}

func TestWritePathBuf(t *testing.T) {
	buf := make([]byte, 8)
	n, err := writePathBuf(buf, "short")
	if err != nil {
		t.Fatalf("writePathBuf: %v", err)
	}
	if n != len("short") {
		t.Fatalf("n=%d", n)
	}
	if _, err = writePathBuf(buf, ""); err == nil {
		t.Fatal("expected error for empty path")
	}
	if _, err = writePathBuf(buf, "012345678"); err == nil {
		t.Fatal("expected error when path exceeds buffer")
	}
}
