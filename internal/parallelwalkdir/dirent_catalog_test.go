package parallelwalkdir_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/server/files"
)

var catalogWalkImageInclude = regexp.MustCompile(`(?i)(?:jpe?g|gif|png)$`)

func catalogOpts(getMap parallelwalkdir.GetDirEntMapFunc, check parallelwalkdir.CheckIfFileModifiedFunc, extra ...parallelwalkdir.Option) []parallelwalkdir.Option {
	opts := []parallelwalkdir.Option{
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithDirEntCatalog(getMap, check),
	}
	return append(opts, extra...)
}

func noopGetMap(context.Context, []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
	return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{}, nil
}

func noopCheck(_ []byte, _ fs.FileInfo, _ parallelwalkdir.DirEntState, _ bool) (bool, error) {
	return false, nil
}

func TestNewWalker_DirEntCatalog_panicsWhenCheckWithoutGet(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithDirEntCatalog(nil, noopCheck),
	)
}

func TestNewWalker_DirEntCatalog_panicsWhenGetWithoutCheck(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithDirEntCatalog(noopGetMap, nil),
	)
}

func TestNewWalker_DirEntCatalog_nilHooksNoPanic(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatalf("unexpected panic: %v", recover())
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
	)
}

func TestNewWalker_DirEntCatalog_bothHooksNoPanic(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatalf("unexpected panic: %v", recover())
		}
	}()
	parallelwalkdir.NewWalker(catalogOpts(noopGetMap, noopCheck)...)
}

func TestNewWalker_DirEntCatalog_composesWithBasenameIncludeAndSizeNotZero(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Fatalf("unexpected panic: %v", recover())
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithDirEntCatalog(noopGetMap, noopCheck),
	)
}

func TestNewWalker_DirEntCatalog_panicsWithRegexpInclude(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithRegexpInclude(regexp.MustCompile(".")),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithDirEntCatalog(noopGetMap, noopCheck),
	)
}

func TestNewWalker_DirEntCatalog_panicsWithValidationFunc(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithValidationFunc(func(_ []byte, _ fs.FileInfo) bool { return true }),
		parallelwalkdir.WithDirEntCatalog(noopGetMap, noopCheck),
	)
}

func TestNewWalker_DirEntCatalog_panicsWithoutBasenameInclude(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithDirEntCatalog(noopGetMap, noopCheck),
	)
}

func dirEntUnchangedState(info fs.FileInfo) parallelwalkdir.DirEntState {
	return parallelwalkdir.DirEntState{
		FileIDValid:    true,
		FileMtimeValid: true,
		FileMtime:      info.ModTime().Unix(),
		FileSizeValid:  true,
		FileSize:       info.Size(),
		FileMD5Valid:   true,
	}
}

func drainParallelWalk(root string, opts ...parallelwalkdir.Option) (results []parallelwalkdir.ReportedFile, errs []error) {
	w := parallelwalkdir.NewWalker(opts...)
	resCh, errCh := w.ParallelWalk(root)
	for rf := range resCh {
		results = append(results, rf)
	}
	for err := range errCh {
		errs = append(errs, err)
	}
	return results, errs
}

func TestParallelWalk_DirEntCatalog_unchangedSkipsReport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(path, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	getMap := func(_ context.Context, _ []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{
			parallelwalkdir.HashPathBytes([]byte("photo.jpg")): dirEntUnchangedState(info),
		}, nil
	}
	opts := catalogOpts(getMap, files.DiscoveryDirEntModifiedWithStats(nil))
	results, errs := drainParallelWalk(root, opts...)
	if len(errs) != 0 || len(results) != 0 {
		t.Fatalf("results=%d errs=%v", len(results), errs)
	}
}

func TestParallelWalk_DirEntCatalog_modifiedReports(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(path, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := dirEntUnchangedState(info)
	st.FileSize = info.Size() + 1
	getMap := func(_ context.Context, _ []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{
			parallelwalkdir.HashPathBytes([]byte("photo.jpg")): st,
		}, nil
	}
	opts := catalogOpts(getMap, files.DiscoveryDirEntModifiedWithStats(nil))
	results, errs := drainParallelWalk(root, opts...)
	if len(errs) != 0 || len(results) != 1 {
		t.Fatalf("results=%d errs=%v", len(results), errs)
	}
	rp := string(results[0].Path)
	if !strings.HasSuffix(rp, "photo.jpg") {
		t.Fatalf("path %q", rp)
	}
	if results[0].ModTimeUnix != info.ModTime().Unix() || results[0].SizeBytes != info.Size() {
		t.Fatalf("metadata mismatch")
	}
}

func TestParallelWalk_DirEntCatalog_getMapError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(path, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	getMap := func(_ context.Context, _ []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		return nil, errors.New("boom")
	}
	opts := catalogOpts(getMap, files.DiscoveryDirEntModifiedWithStats(nil))
	results, errs := drainParallelWalk(root, opts...)
	if len(results) != 0 || len(errs) < 1 {
		t.Fatalf("results=%d errs=%v", len(results), errs)
	}
}

func TestParallelWalk_DirEntCatalog_legacyReportsWithoutHooks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(path, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, errs := drainParallelWalk(root,
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
	)
	if len(errs) != 0 || len(results) != 1 {
		t.Fatalf("results=%d errs=%v", len(results), errs)
	}
}

func TestParallelWalk_DirEntCatalog_readDirFailOnFilePath(t *testing.T) {
	root := t.TempDir()
	solo := filepath.Join(root, "solo.jpg")
	if err := os.WriteFile(solo, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(solo)
	if err != nil {
		t.Fatal(err)
	}
	getMap := func(_ context.Context, reportedDir []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		if string(reportedDir) != solo {
			t.Fatalf("getMap reportedDir=%q want %q", reportedDir, solo)
		}
		return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{
			parallelwalkdir.HashPathBytes([]byte("solo.jpg")): dirEntUnchangedState(info),
		}, nil
	}
	opts := catalogOpts(getMap, files.DiscoveryDirEntModifiedWithStats(nil))
	results, errs := drainParallelWalk(solo, opts...)
	if len(errs) != 0 || len(results) != 0 {
		t.Fatalf("results=%d errs=%v", len(results), errs)
	}
}

func TestParallelWalk_DirEntCatalog_symlinkToFileUsesBasenameKey(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("symlinks")
	}
	root := t.TempDir()
	target := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(target, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.jpg")
	if err := os.Symlink("payload.bin", link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	getMap := func(_ context.Context, _ []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{
			parallelwalkdir.HashPathBytes([]byte("link.jpg")): dirEntUnchangedState(info),
		}, nil
	}
	opts := catalogOpts(getMap, files.DiscoveryDirEntModifiedWithStats(nil))
	results, errs := drainParallelWalk(root, opts...)
	if len(errs) != 0 || len(results) != 0 {
		t.Fatalf("results=%d errs=%v", len(results), errs)
	}
}

func TestParallelWalk_DirEntCatalog_noSendReportedAllocs(t *testing.T) {
	emptyRoot := t.TempDir()
	treeRoot := t.TempDir()
	prebuilt := make(map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, 8)
	for i := range 8 {
		name := fmt.Sprintf("photo%d.jpg", i)
		path := filepath.Join(treeRoot, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		prebuilt[parallelwalkdir.HashPathBytes([]byte(name))] = dirEntUnchangedState(info)
	}
	getMap := func(_ context.Context, _ []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		return prebuilt, nil
	}
	opts := []parallelwalkdir.Option{
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithWalkPathStore(parallelwalkdir.NewWalkPathStore()),
		parallelwalkdir.WithDirEntCatalog(getMap, files.DiscoveryDirEntModifiedWithStats(nil)),
	}
	baseline := testing.AllocsPerRun(5, func() { drainParallelWalk(emptyRoot, opts...) })
	catalog := testing.AllocsPerRun(5, func() { drainParallelWalk(treeRoot, opts...) })
	if catalog > baseline+2 {
		t.Fatalf("catalog allocs %v > baseline+2 (%v)", catalog, baseline+2)
	}
}

func TestNewWalker_DirEntCatalog_panicsWithoutSizeNotZero(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	parallelwalkdir.NewWalker(
		parallelwalkdir.WithBasenameInclude(catalogWalkImageInclude),
		parallelwalkdir.WithDirEntCatalog(noopGetMap, noopCheck),
	)
}
