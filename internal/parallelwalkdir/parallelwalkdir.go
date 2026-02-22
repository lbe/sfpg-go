// Package parallelwalkdir provides utilities to traverse a directory structure
// in parallel, with support for symbolic links and loop detection.
package parallelwalkdir

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
)

// Walker holds the state for a parallel directory traversal.
// It manages concurrency, tracks visited paths to prevent loops, and returns
// results via a channel.
type Walker struct {
	wg      sync.WaitGroup      // WaitGroup to track active goroutines.
	sem     chan struct{}       // Semaphore to limit concurrency.
	results chan string         // Channel to send found file paths.
	errs    chan error          // Channel to send encountered errors.
	mu      sync.Mutex          // Mutex to protect the visited map.
	visited map[string]struct{} // Set of canonical paths already visited to detect loops.
	ctx     context.Context     // Context for cancellation.

	// Options
	maxNumGoroutines int
	includeRegex     *regexp.Regexp
	excludeRegex     *regexp.Regexp
	sizeNotZero      bool
	validationFunc   WalkDirFunc

	// Internal flags for mutual exclusivity checks
	hasIncludeRegex   bool
	hasSizeNotZero    bool
	hasValidationFunc bool
}

// Option is a function type that allows configuring a Walker.
type Option func(*Walker)

// WalkDirFunc is a function type used for custom file validation.
// It takes the reported path and file info, and returns true if the file
// should be included in the results.
type WalkDirFunc func(path string, info fs.FileInfo) bool

// NewWalker creates and initializes a new Walker with default settings.
// It accepts a variadic list of Option functions to customize its behavior.
// Panics if mutually exclusive options are provided.
func NewWalker(opts ...Option) *Walker {
	w := &Walker{
		maxNumGoroutines: 2 * runtime.NumCPU(),   // Default value
		results:          make(chan string, 100), // Buffered channel for results
		errs:             make(chan error, 100),  // Buffered channel for errors
		visited:          make(map[string]struct{}),
	}

	// Apply options
	for _, opt := range opts {
		opt(w)
	}

	// If maxNumGoroutines was set to <= 0, use default.
	if w.maxNumGoroutines <= 0 {
		w.maxNumGoroutines = 2 * runtime.NumCPU()
	}

	// Default context to Background if not set (backwards compatibility)
	if w.ctx == nil {
		w.ctx = context.Background()
	}

	// Initialize semaphore with potentially overridden maxNumGoroutines.
	w.sem = make(chan struct{}, w.maxNumGoroutines)

	// Mutual exclusivity checks
	if w.hasValidationFunc && (w.hasIncludeRegex || w.hasSizeNotZero) {
		panic("WithValidationFunc is mutually exclusive with WithRegexpInclude and WithSizeNotZero")
	}

	return w
}

// WithMaxNumGoroutines allows the consumer to override the default number of
// parallel goroutines. If i is less than or equal to 0, the default value
// (2 * runtime.NumCPU()) will be used.
func WithMaxNumGoroutines(i int) Option {
	return func(w *Walker) {
		w.maxNumGoroutines = i
	}
}

// WithRegexpInclude allows the consumer to specify a regular expression that
// the basename of filenames must match. Only files matching the regex will be returned.
// This option is mutually exclusive with WithValidationFunc.
func WithRegexpInclude(r *regexp.Regexp) Option {
	return func(w *Walker) {
		w.includeRegex = r
		w.hasIncludeRegex = true
	}
}

// WithRegexpExclude allows the consumer to specify a regular expression.
// Any path (file or directory) matching this regex will be skipped and not traversed.
func WithRegexpExclude(r *regexp.Regexp) Option {
	return func(w *Walker) {
		w.excludeRegex = r
	}
}

// WithContext sets the cancellation context for the walker. When the context
// is cancelled, the walk stops as soon as possible. If not set, defaults to
// context.Background() (walk runs to completion).
func WithContext(ctx context.Context) Option {
	return func(w *Walker) {
		w.ctx = ctx
	}
}

// WithSizeNotZero allows the consumer to specify that only files with a size
// greater than 0 bytes should be returned. This option is mutually exclusive
// with WithValidationFunc.
func WithSizeNotZero() Option {
	return func(w *Walker) {
		w.sizeNotZero = true
		w.hasSizeNotZero = true
	}
}

// WithValidationFunc allows the consumer to specify a custom function to validate
// if a file should be returned. This option is mutually exclusive with
// WithRegexpInclude and WithSizeNotZero.
func WithValidationFunc(vf WalkDirFunc) Option {
	return func(w *Walker) {
		w.validationFunc = vf
		w.hasValidationFunc = true
	}
}

// ParallelWalk starts the directory traversal from the given root path.
// It returns a read-only channel of strings for file paths, and a read-only
// channel for any errors encountered. Both channels will be closed once the
// entire traversal is complete or the context (set via WithContext) is cancelled.
func (w *Walker) ParallelWalk(rootPath string) (<-chan string, <-chan error) {
	w.wg.Add(1)
	go w.walk(rootPath, rootPath)

	// Create a goroutine to wait for all workers to finish, then close channels.
	// This ensures channels are closed even if caller doesn't drain them completely.
	go func() {
		w.wg.Wait()
		close(w.results)
		close(w.errs)
	}()

	return w.results, w.errs
}

// walk is the internal core worker function that traverses a given path.
// currentPath is the absolute path of the directory or file currently being processed.
// reportedPath is the path that should be reported to the caller, reflecting
// the original path including any symlinks in the traversal.
func (w *Walker) walk(currentPath string, reportedPath string) {
	// Check context BEFORE acquiring semaphore to avoid blocking on a full
	// semaphore when cancellation is already requested.
	// NOTE: wg.Done() must be called explicitly here because defer hasn't
	// been registered yet.
	if w.ctx.Err() != nil {
		w.wg.Done()
		return
	}

	// Acquire a semaphore slot and mark this goroutine as done on exit.
	w.sem <- struct{}{}
	defer func() { <-w.sem }()
	defer w.wg.Done()

	// --- Loop Detection ---
	// Resolve the current path to its canonical form to detect loops.
	realPath, err := filepath.EvalSymlinks(currentPath)
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Error("EvalSymlinks error", "currentPath", currentPath, "err", err)
		}
		select {
		case w.errs <- &fs.PathError{Op: "EvalSymlinks", Path: currentPath, Err: err}:
		case <-w.ctx.Done():
			return
		}
		return
	}

	w.mu.Lock()
	if realPath != currentPath {
		if _, ok := w.visited[realPath]; ok {
			slog.Debug("Detected loop, already visited", "realPath", realPath, "currentPath", currentPath, "reportedPath", reportedPath)
			w.mu.Unlock()
			return // Already visited, so this is a loop. Stop traversal for this path.
		}
	}
	w.visited[realPath] = struct{}{}
	w.mu.Unlock()

	// --- Exclude Filter ---
	if w.excludeRegex != nil && w.excludeRegex.MatchString(reportedPath) {
		return // Skip this path as it matches the exclude regex.
	}

	// Read the directory entries.
	entries, err := os.ReadDir(currentPath)
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Error("ReadDir error", "currentPath", currentPath, "err", err)
		}
		// If it's not a directory, it could be a file. Check for this case.
		info, statErr := os.Lstat(currentPath)
		if statErr == nil && !info.IsDir() {
			// It's a file, apply file-specific filters before reporting.
			w.filterAndReportFile(reportedPath, info)
			return
		}
		// It's a directory we couldn't read, or some other error. Report the error.
		select {
		case w.errs <- &fs.PathError{Op: "ReadDir", Path: currentPath, Err: err}:
		case <-w.ctx.Done():
			return
		}
		return
	}

	// Before iterating entries, check context again
	if w.ctx.Err() != nil {
		return
	}

	// Process each directory entry.
	for _, entry := range entries {
		// Check at the start of each iteration
		if w.ctx.Err() != nil {
			return
		}

		// Construct the full path for the child entry, and the path to report.
		childPath := filepath.Join(currentPath, entry.Name())
		reportedChildPath := filepath.Join(reportedPath, entry.Name())

		// --- Exclude Filter ---
		if w.excludeRegex != nil && w.excludeRegex.MatchString(reportedChildPath) {
			continue // Skip this path as it matches the exclude regex.
		}

		// If the entry is a directory, delegate it to a new goroutine.
		if entry.IsDir() {
			w.wg.Add(1)
			go w.walk(childPath, reportedChildPath)
			continue
		}

		// If the entry is a symlink, we need to check if it points to a directory.
		// If it does, delegate it to a new goroutine.
		if entry.Type()&fs.ModeSymlink != 0 {
			info, statErr := os.Stat(childPath) // os.Stat follows the symlink.
			if statErr == nil && info.IsDir() {
				w.wg.Add(1)
				go w.walk(childPath, reportedChildPath)
				continue
			}
			// It's a symlink to a file. Apply file-specific filters before reporting.
			w.filterAndReportFile(reportedChildPath, info)
			continue
		}

		// If we reach here, it's a regular file. Apply file-specific filters before reporting.
		info, statErr := entry.Info()
		if statErr != nil {
			select {
			case w.errs <- &fs.PathError{Op: "Stat", Path: childPath, Err: statErr}:
			case <-w.ctx.Done():
				return
			}
			continue
		}
		w.filterAndReportFile(reportedChildPath, info)
	}
}

// filterAndReportFile applies the configured file filters and sends the path to the results channel if it passes.
func (w *Walker) filterAndReportFile(reportedPath string, info fs.FileInfo) {
	// If a custom validation function is provided, use it.
	if w.validationFunc != nil {
		if w.validationFunc(reportedPath, info) {
			select {
			case w.results <- reportedPath:
			case <-w.ctx.Done():
				return
			}
		}
		return
	}

	// Apply include regex filter if set.
	if w.includeRegex != nil && !w.includeRegex.MatchString(filepath.Base(reportedPath)) {
		return // Does not match include regex, so skip.
	}

	// Assume that there is a problem with the file and do not include it, log a warning
	if info == nil {
		return
	}

	// Apply size not zero filter if set.
	if w.sizeNotZero && info.Size() == 0 {
		return // Size is zero, so skip.
	}

	// If all filters pass, report the file.
	select {
	case w.results <- reportedPath:
	case <-w.ctx.Done():
		return
	}
}
