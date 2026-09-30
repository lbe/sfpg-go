// Package osza provides allocation-free helpers that mirror parts of the
// standard os package. Callers supply slices and structs; hot paths avoid heap
// allocations per directory entry (or per future operation). osza is not a
// replacement for os, io/fs, or filepath.
//
// # Import path
//
// Application and library code in this module should import:
//
//	github.com/lbe/sfpg-go/internal/osza
//
// and call osza.ReadDir, osza.Entry, osza.ErrOverflow, osza.FileMeta,
// osza.LstatAt, osza.StatAt, osza.LstatJoin, osza.EvalSymlinksAt,
// and related sentinel errors. Do not import internal/osza/readdir,
// internal/osza/stat, or internal/osza/evalsymlinks
// from production code.
//
// # Layout
//
//   - This directory (package osza): stable API via doc.go and *_export.go files.
//   - Subpackages (e.g. readdir): platform-specific syscalls and tests.
//
// # ReadDir
//
// ReadDir lists one directory into caller-provided storage. Entry.Name is a byte
// slice subordinate to nameBuf; it remains valid until the next ReadDir call
// that reuses the same buffers. Entry.IsDir, Type, and Info match fs.DirEntry
// semantics on the current GOOS. Call entries[i].Info(joinBuf) (not Info on a
// copied Entry) so Unix lazy stat results are cached. Use errors.Is for
// osza.ErrOverflow, osza.ErrPathBuffer, osza.ErrShortScratch, and
// osza.ErrTooManySymlinks when sizing buffers or handling EvalSymlinksAt.
// On Windows, EvalSymlinksAt delegates to filepath.EvalSymlinks and may allocate. On Unix, pass scratch of at least 8192 bytes; on Windows scratch
// may be nil. dirPathBuf, nameBuf, joinBuf, and scratch must not alias each
// other; reuse one buffer set per goroutine only.
//
// ReadDir uses len(entries), not cap(entries). When ErrOverflow is returned,
// double len(entries) and len(nameBuf), then call ReadDir again from scratch
// until err is nil. When ErrPathBuffer is returned during ReadDir (unknown
// dirent type on Unix), enlarge joinBuf and re-read from scratch.
// See internal/osza/example/read_dir_retry/main.go.
//
// Example:
//
//	dirPath := make([]byte, len(dir)+1)
//	dirLen, err := osza.CopyDirPath(dirPath, dir)
//	entries := make([]osza.Entry, 256)
//	nameBuf := make([]byte, 64*1024)
//	scratch := make([]byte, 8192)
//	joinBuf := make([]byte, 64*1024)
//	n, err := osza.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
//	if errors.Is(err, osza.ErrOverflow) {
//	    // grow buffers and retry; see example/read_dir_retry
//	}
//
// # Adding a capability
//
// Implement internal/osza/<name>/ with build-tagged files, unit tests in that
// directory, then add internal/osza/<name>_export.go re-exporting types,
// sentinel errors, and functions. Match buffer-passing style and document
// minimum buffer sizes in godoc. See README.md in this directory for maintainers.
package osza
