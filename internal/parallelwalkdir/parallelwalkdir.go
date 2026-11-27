// Package parallelwalkdir walks directory trees with a bounded pool of directory
// workers and streams matching files on channels.
//
// # Concurrency
//
// Workers read dirWork only from a buffered dirCh. Subdirectories are enqueued on
// an unbounded schedule FIFO (internal/queue.Queue[*dirWork]). One feeder goroutine
// dequeues with DequeueWait and is the sole dirCh writer. Each directory is counted
// before enqueue until processing or discard finishes, including feeder-held and
// buffered work. The coordinator waits for this count to reach zero or cancellation,
// cancels and joins the feeder, drains queued work, closes dirCh, joins workers,
// and closes results and errs.
//
// Visit tracking uses FNV-1a 128-bit hashes of directory path bytes to avoid
// symlink loops. The root is resolved with EvalSymlinks once, so directories below
// it are keyed by their path without EvalSymlinks; symlinked directories are keyed
// by their canonical path. Reported paths keep the root as given. Hash collisions
// may silently skip a directory (accepted policy).
//
// # Filtering
//
// WithBasenameInclude matches entry basenames with zero-alloc suffix checks
// derived from the compiled regexp pattern (cached by pattern string).
// Unsupported patterns panic when the option is applied. WithSizeNotZero drops
// zero-length files.
//
// WithDirEntCatalog: GetDirEntMapFunc runs before readDirAllFn per directory;
// CheckIfFileModifiedFunc gates sendReported on PathHash keys from
// HashPathBytes(entry.Name). Requires WithBasenameInclude and WithSizeNotZero.
// The discovery walk passes its per-file check;
// unchanged files are dropped at walk time and never reach discovery workers.
// FNV-128 key collisions may mis-skip files (accepted).
//
// # Results
//
// ParallelWalk returns buffered results and errs channels. Drain both until closed.
// Walk errors are non-fatal; callers log or count them. Scheduled directory paths
// are carried as owned []byte inside dirWork. ReportedFile.Path is a subslice of
// the walk's WalkPathStore arena (symlink-aware reported path), plus mtime and size from stat.
//
// # Test seams
//
// Package vars evalSymlinksAt, oszaReadDir, readDirAllFn, fileInfoForDirEntryFn,
// walkLstatPathLen, and walkStatPathLen default to production implementations and
// may be overridden in tests. Tests also set Walker.maxWorkers and
// Walker.dirChCapacity directly.
package parallelwalkdir

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"regexp"
	"regexp/syntax"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
	"github.com/lbe/sfpg-go/internal/osza"
	"github.com/lbe/sfpg-go/internal/queue"
)

const (
	initialReadDirEntries   = 256
	initialReadDirNameBytes = 64 << 10
	readDirScratchBytes     = 8192
	maxReadDirEntries       = 1 << 20
	maxReadDirNameBytes     = 64 << 20
	// Grown entries/nameBuf above these sizes are dropped after each directory
	// so an idle worker does not pin a huge listing's buffers until the walk ends.
	retainReadDirEntries   = 4 * initialReadDirEntries
	retainReadDirNameBytes = 4 * initialReadDirNameBytes
)

var (
	basenameIncludeByPattern sync.Map // pattern string -> basenameIncludeFunc
	fileInfoForDirEntryFn    = defaultFileInfoForDirEntry
	evalSymlinksAt           = defaultEvalSymlinksAt // hook: osza.EvalSymlinksAt
	oszaReadDir              = osza.ReadDir          // hook: osza.ReadDir
	readDirAllFn             = readDirAllFromPathBuf // hook: readDirAllFromPathBuf
	walkLstatPathLen         = defaultWalkLstatPathLen
	walkStatPathLen          = defaultWalkStatPathLen
)

type basenameIncludeFunc func(name []byte) bool

type dirWork struct {
	// current is the path read: below the root it starts from the canonical root.
	current []byte
	// reported is the same directory under the root path as given to ParallelWalk.
	reported []byte
	// visitKey is HashPathBytes(work.current) at schedule time; used when EvalSymlinks
	// is skipped so eval does not re-hash or canonicalize.
	visitKey PathHash
	// root marks the walk root, whose path processDir replaces with its canonical form.
	root bool
}

var dirWorkPool = gensyncpool.New(
	func() *dirWork { return &dirWork{} },
	resetDirWork,
)

// resetDirWork clears d for reuse and keeps its path buffers. A reported that
// aliases current is dropped, so a later fill with different paths cannot write
// reported over current.
func resetDirWork(d *dirWork) {
	if cap(d.current) > 0 && cap(d.reported) > 0 && &d.current[:1][0] == &d.reported[:1][0] {
		d.reported = nil
	}
	if d.current != nil {
		d.current = d.current[:0]
	}
	if d.reported != nil {
		d.reported = d.reported[:0]
	}
	d.visitKey = PathHash{}
	d.root = false
}

// Option configures a Walker.
type Option func(*Walker)

type readDirBuffers struct {
	entries     []osza.Entry
	nameBuf     []byte
	scratch     []byte // Unix getdents scratch; unused on Windows
	pathBuf     []byte
	reportedBuf []byte
	joinBuf     []byte // Entry.Info and LstatJoin scratch
	evalPathBuf []byte // EvalSymlinks input; must not alias pathBuf (EvalSymlinksAt may rewrite)
	evalDest    []byte // EvalSymlinks canonical output
	meta        osza.FileMeta
}

// ReportedFile is one file sent on the ParallelWalk results channel.
type ReportedFile struct {
	Path        []byte // Arena subslice from the walk's WalkPathStore; valid until store Reset or discard.
	ModTimeUnix int64  // From the FileInfo used for filtering
	SizeBytes   int64
}

// Walker holds state for a parallel directory traversal: a fixed pool of worker
// goroutines reading from dirCh, an unbounded schedule queue and feeder that is
// the sole writer to dirCh, outstanding directory accounting, visited-path
// loop detection, and result/error channels.
type Walker struct {
	results chan ReportedFile // Buffered channel of matching files with walk metadata.
	errs    chan error        // Buffered channel of walk errors.
	mu      sync.Mutex        // Protects visited.
	// visited keys are PathHash of canonical directory bytes. FNV-128 collision may
	// silently skip a directory (no stored path bytes for collision checks).
	visited map[PathHash]struct{}
	ctx     context.Context // Cancellation; defaults to context.Background().

	// maxWorkers is the directory worker count when > 0. Tests in this package set
	// it directly; production leaves it zero and workers() uses GOMAXPROCS(0).
	maxWorkers int

	// dirChCapacity is the dirCh buffer size when > 0. Tests in this package set it
	// directly; production leaves it zero and the buffer is workers()*1024.
	dirChCapacity int

	basenameInclude basenameIncludeFunc
	sizeNotZero     bool

	getDirEntMap        GetDirEntMapFunc
	checkIfFileModified CheckIfFileModifiedFunc
	hasDirEntCatalog    bool

	pathStore *WalkPathStore // Bump arena for reported paths; default created in ParallelWalk when nil.
}

// ParallelWalk traverses rootPath. The returned channels are read-only; both close
// when the walk finishes. The visited set is not cleared between calls, so create
// a new Walker for each walk.
func (w *Walker) ParallelWalk(rootPath string) (<-chan ReportedFile, <-chan error) {
	if w.pathStore == nil {
		w.pathStore = NewWalkPathStore()
	}
	w.pathStore.Reset()

	n := w.workers()
	dirChCap := n * 1024
	if w.dirChCapacity > 0 {
		dirChCap = w.dirChCapacity
	}
	dirCh := make(chan *dirWork, dirChCap)

	var workerWg sync.WaitGroup
	var feederWg sync.WaitGroup

	dirQueue := queue.NewQueue[*dirWork](64)
	feederCtx, stopFeeder := context.WithCancel(w.ctx)

	var idleMu sync.Mutex
	idleCond := sync.NewCond(&idleMu)
	var outstanding int
	var stopping bool
	finish := func(work *dirWork) {
		dirWorkPool.Put(work)
		idleMu.Lock()
		outstanding--
		if outstanding == 0 {
			idleCond.Broadcast()
		}
		idleMu.Unlock()
	}

	schedule := func(work *dirWork) {
		idleMu.Lock()
		defer idleMu.Unlock()
		if stopping || w.ctx.Err() != nil {
			dirWorkPool.Put(work)
			return
		}
		outstanding++
		if err := dirQueue.Enqueue(work); err != nil {
			outstanding--
			dirWorkPool.Put(work)
			if outstanding == 0 {
				idleCond.Broadcast()
			}
		}
	}

	for range n {
		workerWg.Go(func() {
			dirBufs := newReadDirBuffers()
			for work := range dirCh {
				w.processDir(work, schedule, dirBufs)
				dirBufs.trim()
				finish(work)
			}
		})
	}

	feederWg.Go(func() {
		for {
			work, err := dirQueue.DequeueWait(feederCtx)
			if err != nil {
				if errors.Is(err, queue.ErrEmptyQueue) {
					continue
				}
				return
			}
			if feederCtx.Err() != nil {
				finish(work)
				return
			}
			select {
			case dirCh <- work:
			case <-feederCtx.Done():
				finish(work)
				return
			}
		}
	})

	go func() {
		root := cloneStringPath(rootPath)
		dw := dirWorkPool.Get()
		dw.current = root
		dw.reported = root
		dw.root = true
		schedule(dw)

		stopWake := context.AfterFunc(w.ctx, func() {
			idleMu.Lock()
			idleCond.Broadcast()
			idleMu.Unlock()
		})
		idleMu.Lock()
		for outstanding != 0 && w.ctx.Err() == nil {
			idleCond.Wait()
		}
		stopping = true
		idleMu.Unlock()
		stopWake()

		stopFeeder()
		feederWg.Wait()
		for {
			work, err := dirQueue.Dequeue()
			if err != nil {
				break
			}
			finish(work)
		}
		close(dirCh)
		workerWg.Wait()
		close(w.results)
		close(w.errs)
	}()

	return w.results, w.errs
}

// sendErr sends err on errs unless the walk is cancelled. It reports whether
// err was sent; an error that arrives after cancellation is dropped.
func (w *Walker) sendErr(err error) bool {
	if w.ctx.Err() != nil {
		return false
	}
	select {
	case w.errs <- err:
		return true
	case <-w.ctx.Done():
		return false
	}
}

func (w *Walker) filterAndReportFile(reportedBuf []byte, reportedLen int, info fs.FileInfo, includeMatched bool, entMap map[PathHash]DirEntState, catalogBasename []byte) {
	if !includeMatched && w.basenameInclude != nil && !w.basenameInclude(basenameInBuf(reportedBuf, reportedLen)) {
		return
	}

	if info == nil {
		return
	}

	if w.sizeNotZero && info.Size() == 0 {
		return
	}

	if w.hasDirEntCatalog {
		h := HashPathBytes(catalogBasename)
		state, inMap := entMap[h]
		modified, err := w.checkIfFileModified(catalogBasename, info, state, inMap)
		if err != nil {
			w.sendErr(err)
			return
		}
		if !modified {
			return
		}
	}

	w.sendReported(reportedBuf, reportedLen, info)
}

func (w *Walker) processDir(work *dirWork, schedule func(*dirWork), dirBufs *readDirBuffers) {
	if w.ctx.Err() != nil {
		return
	}
	if cap(dirBufs.pathBuf) > len(dirBufs.pathBuf) {
		dirBufs.pathBuf = dirBufs.pathBuf[:cap(dirBufs.pathBuf)]
	}
	if cap(dirBufs.reportedBuf) > len(dirBufs.reportedBuf) {
		dirBufs.reportedBuf = dirBufs.reportedBuf[:cap(dirBufs.reportedBuf)]
	}

	dirLen, err := writePathBytes(dirBufs.pathBuf, work.current)
	if err != nil {
		w.sendErr(&fs.PathError{Op: "ReadDir", Path: string(work.reported), Err: err})
		return
	}
	reportedLen, err := writePathBytes(dirBufs.reportedBuf, work.reported)
	if err != nil {
		w.sendErr(&fs.PathError{Op: "ReadDir", Path: string(work.reported), Err: err})
		return
	}

	var visitHash PathHash
	var loopGuard bool
	if work.root {
		dirLen, visitHash, err = resolveRoot(dirBufs, dirLen)
	} else {
		visitHash, loopGuard, err = evalDirRealPath(work.visitKey, dirBufs, dirLen)
	}
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Error("EvalSymlinks error", "currentPath", string(work.current), "err", err)
		}
		w.sendErr(&fs.PathError{Op: "EvalSymlinks", Path: string(work.reported), Err: err})
		return
	}

	w.mu.Lock()
	if loopGuard {
		if _, ok := w.visited[visitHash]; ok {
			slog.Debug("Detected loop, already visited",
				"visitHash", visitHash,
				"currentPath", string(work.current),
				"reportedPath", string(work.reported))
			w.mu.Unlock()
			return
		}
	}
	w.visited[visitHash] = struct{}{}
	w.mu.Unlock()

	var entMap map[PathHash]DirEntState
	if w.hasDirEntCatalog {
		entMap, err = w.getDirEntMap(w.ctx, work.reported)
		if err != nil {
			w.sendErr(err)
			return
		}
	}

	n, dirLen, err := readDirAllFn(dirBufs, dirLen)
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Error("ReadDir error", "currentPath", string(work.current), "err", err)
		}
		info, statErr := walkLstatPathLen(dirBufs, dirLen)
		if statErr == nil && !info.IsDir() {
			fileBasename := basenameInBuf(dirBufs.reportedBuf, reportedLen)
			w.filterAndReportFile(dirBufs.reportedBuf, reportedLen, info, false, entMap, fileBasename)
			return
		}
		w.sendErr(&fs.PathError{Op: "ReadDir", Path: materializeString(dirBufs.reportedBuf, reportedLen), Err: err})
		return
	}

	if w.ctx.Err() != nil {
		return
	}

	splitReported := !bytes.Equal(dirBufs.pathBuf[:dirLen], dirBufs.reportedBuf[:reportedLen])

	for i := range n {
		if w.ctx.Err() != nil {
			return
		}

		entry := &dirBufs.entries[i]
		var pathEnd int
		var segErr error
		dirBufs.pathBuf, pathEnd, segErr = appendPathSegment(dirBufs.pathBuf, dirLen, entry.Name)
		if segErr != nil {
			if !w.sendErr(&fs.PathError{Op: "Stat", Path: materializeString(dirBufs.pathBuf, pathEnd), Err: segErr}) {
				return
			}
			continue
		}
		reportedEnd := pathEnd
		if splitReported {
			dirBufs.reportedBuf, reportedEnd, segErr = appendPathSegment(dirBufs.reportedBuf, reportedLen, entry.Name)
			if segErr != nil {
				if !w.sendErr(&fs.PathError{Op: "Stat", Path: materializeString(dirBufs.pathBuf, pathEnd), Err: segErr}) {
					return
				}
				continue
			}
		} else {
			copy(dirBufs.reportedBuf[dirLen:pathEnd], dirBufs.pathBuf[dirLen:pathEnd])
		}

		if entry.Type()&fs.ModeSymlink != 0 {
			info, statErr := walkStatPathLen(dirBufs, pathEnd)
			if statErr == nil && info.IsDir() {
				schedule(dirWorkFromBufs(dirBufs.pathBuf, dirBufs.reportedBuf, pathEnd, reportedEnd))
				continue
			}
			w.filterAndReportFile(dirBufs.reportedBuf, reportedEnd, info, false, entMap, entry.Name)
			continue
		}

		if entry.IsDir() {
			schedule(dirWorkFromBufs(dirBufs.pathBuf, dirBufs.reportedBuf, pathEnd, reportedEnd))
			continue
		}

		if w.basenameInclude != nil && !w.basenameInclude(entry.Name) {
			continue
		}

		info, statErr := fileInfoForDirEntry(dirBufs, entry)
		if statErr != nil {
			if !w.sendErr(&fs.PathError{Op: "Stat", Path: materializeString(dirBufs.reportedBuf, reportedEnd), Err: statErr}) {
				return
			}
			continue
		}
		w.filterAndReportFile(dirBufs.reportedBuf, reportedEnd, info, true, entMap, entry.Name)
	}
}

func (w *Walker) sendReported(reportedBuf []byte, reportedLen int, info fs.FileInfo) {
	if w.pathStore == nil {
		w.pathStore = NewWalkPathStore()
	}
	rf := ReportedFile{
		Path:        w.pathStore.AppendPath(reportedBuf, reportedLen),
		ModTimeUnix: info.ModTime().Unix(),
		SizeBytes:   info.Size(),
	}
	select {
	case w.results <- rf:
	case <-w.ctx.Done():
		return
	}
}

func (w *Walker) workers() int {
	if w.maxWorkers > 0 {
		return w.maxWorkers
	}
	return runtime.GOMAXPROCS(0)
}

// WithBasenameInclude matches file basenames with suffix checks derived from r.
// Patterns must be an anchored alternation of literal suffix fragments (see
// basenameSuffixesFromPattern); derivation is verified against r at build time.
func WithBasenameInclude(r *regexp.Regexp) Option {
	return func(w *Walker) {
		w.basenameInclude = basenameIncludeForRegexp(r)
	}
}

// NewWalker creates a Walker and applies options. The worker count defaults to
// runtime.GOMAXPROCS(0).
//
// Panics when WithDirEntCatalog is incomplete or lacks WithBasenameInclude or
// WithSizeNotZero.
func NewWalker(opts ...Option) *Walker {
	w := &Walker{
		results: make(chan ReportedFile, 100),
		errs:    make(chan error, 100),
		visited: make(map[PathHash]struct{}),
	}

	for _, opt := range opts {
		opt(w)
	}

	if w.ctx == nil {
		w.ctx = context.Background()
	}

	hasGet := w.getDirEntMap != nil
	hasCheck := w.checkIfFileModified != nil
	if hasGet != hasCheck {
		panic("parallelwalkdir: WithDirEntCatalog requires both GetDirEntMapFunc and CheckIfFileModifiedFunc, or neither")
	}
	if hasGet {
		w.hasDirEntCatalog = true
		if w.basenameInclude == nil {
			panic("parallelwalkdir: WithDirEntCatalog requires WithBasenameInclude")
		}
		if !w.sizeNotZero {
			panic("parallelwalkdir: WithDirEntCatalog requires WithSizeNotZero")
		}
	}

	return w
}

// WithWalkPathStore sets the bump arena for reported paths. When unset,
// ParallelWalk allocates a default store for the Walker.
func WithWalkPathStore(store *WalkPathStore) Option {
	return func(w *Walker) {
		w.pathStore = store
	}
}

// WithContext sets cancellation for the walk. When cancelled, the feeder stops
// without feeding queued work that was not yet sent on dirCh; workers and
// schedule submit paths exit as soon as practical; results and errs close after
// workers finish.
func WithContext(ctx context.Context) Option {
	return func(w *Walker) {
		w.ctx = ctx
	}
}

// WithSizeNotZero excludes zero-length files.
func WithSizeNotZero() Option {
	return func(w *Walker) {
		w.sizeNotZero = true
	}
}

func alternationBeforeEndAnchor(re *syntax.Regexp) *syntax.Regexp {
	re = unwrapSyntaxGroup(re)
	if re.Op != syntax.OpConcat || len(re.Sub) < 2 {
		return nil
	}
	last := re.Sub[len(re.Sub)-1]
	if last.Op != syntax.OpEndText {
		return nil
	}
	return unwrapSyntaxGroup(re.Sub[len(re.Sub)-2])
}

func appendPathSegment(buf []byte, baseLen int, name []byte) ([]byte, int, error) {
	nameLen := len(name)
	if baseLen < 0 {
		return buf, 0, syscall.ENAMETOOLONG
	}
	if nameLen == 0 {
		return buf, baseLen, nil
	}
	need := baseLen + 1 + nameLen
	if need > cap(buf) {
		return buf, 0, syscall.ENAMETOOLONG
	}
	if len(buf) < need {
		buf = buf[:need]
	}
	buf[baseLen] = '/'
	copy(buf[baseLen+1:need], name)
	return buf, need, nil
}

func basenameIncludeForRegexp(re *regexp.Regexp) basenameIncludeFunc {
	pattern := re.String()
	if fn, ok := basenameIncludeByPattern.Load(pattern); ok {
		return fn.(basenameIncludeFunc)
	}
	fn := buildBasenameIncludeForRegexp(pattern, re)
	actual, _ := basenameIncludeByPattern.LoadOrStore(pattern, fn)
	return actual.(basenameIncludeFunc)
}

// basenameSuffixesFromPattern parses pattern and returns dotted lowercase suffix
// bytes (e.g. ".jpg") for alternation-before-$ patterns used by WithBasenameInclude.
func basenameSuffixesFromPattern(pattern string) ([][]byte, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	parsed = parsed.Simplify()
	alt := alternationBeforeEndAnchor(parsed)
	if alt == nil {
		return nil, errors.New("pattern is not an anchored alternation suffix")
	}
	raw := expandSuffixStrings(alt)
	if len(raw) == 0 {
		return nil, errors.New("pattern alternation expands to no suffixes")
	}
	seen := make(map[string]struct{})
	var out [][]byte
	for _, s := range raw {
		if s == "" {
			continue
		}
		key := "." + strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, []byte(key))
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out, nil
}

func buildBasenameIncludeForRegexp(pattern string, re *regexp.Regexp) basenameIncludeFunc {
	suffixes := mustBasenameSuffixesFromPattern(pattern, re)
	return func(name []byte) bool {
		return matchBasenameWithSuffixes(name, suffixes)
	}
}

func bytesToUpperASCII(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

func concatSuffixVariants(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]string, 0, len(a)*len(b))
	for _, x := range a {
		for _, y := range b {
			out = append(out, x+y)
		}
	}
	return out
}

func defaultFileInfoForDirEntry(bufs *readDirBuffers, entry *osza.Entry) (fs.FileInfo, error) {
	return entry.Info(bufs.joinBuf)
}

func defaultWalkLstatPathLen(bufs *readDirBuffers, pathLen int) (fs.FileInfo, error) {
	if err := osza.LstatAt(bufs.pathBuf, pathLen, &bufs.meta); err != nil {
		return nil, err
	}
	return &bufs.meta, nil
}

func defaultWalkStatPathLen(bufs *readDirBuffers, pathLen int) (fs.FileInfo, error) {
	if err := osza.StatAt(bufs.pathBuf, pathLen, &bufs.meta); err != nil {
		return nil, err
	}
	return &bufs.meta, nil
}

func expandSuffixStrings(re *syntax.Regexp) []string {
	switch re.Op {
	case syntax.OpAlternate:
		var out []string
		for _, sub := range re.Sub {
			out = append(out, expandSuffixStrings(sub)...)
		}
		return out
	case syntax.OpEmptyMatch:
		return []string{""}
	case syntax.OpLiteral:
		var b strings.Builder
		for _, r := range re.Rune {
			if re.Flags&syntax.FoldCase != 0 && r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			b.WriteRune(r)
		}
		return []string{b.String()}
	case syntax.OpConcat:
		if len(re.Sub) == 0 {
			return []string{""}
		}
		acc := expandSuffixStrings(re.Sub[0])
		for i := 1; i < len(re.Sub); i++ {
			next := expandSuffixStrings(re.Sub[i])
			acc = concatSuffixVariants(acc, next)
		}
		return acc
	case syntax.OpQuest:
		if len(re.Sub) != 1 {
			return nil
		}
		opt := expandSuffixStrings(re.Sub[0])
		out := []string{""}
		for _, s := range opt {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func defaultEvalSymlinksAt(pathBuf []byte, pathLen int, dest []byte) (int, error) {
	return osza.EvalSymlinksAt(pathBuf, pathLen, dest)
}

// evalSymlinksCanonical resolves pathBuf[:pathLen] into bufs.evalDest, growing
// evalPathBuf/evalDest on osza.ErrPathBuffer until success or maxReadDirNameBytes.
// pathBuf is copied into evalPathBuf first because EvalSymlinksAt may rewrite its input.
func evalSymlinksCanonical(bufs *readDirBuffers, pathLen int) (canonicalLen int, err error) {
	if cap(bufs.evalPathBuf) < pathLen+1 {
		need := max(pathLen+1, initialReadDirNameBytes)
		bufs.evalPathBuf = make([]byte, need)
	} else if cap(bufs.evalPathBuf) > len(bufs.evalPathBuf) {
		bufs.evalPathBuf = bufs.evalPathBuf[:cap(bufs.evalPathBuf)]
	}
	if cap(bufs.evalDest) == 0 {
		bufs.evalDest = make([]byte, initialReadDirNameBytes)
	} else if cap(bufs.evalDest) > len(bufs.evalDest) {
		bufs.evalDest = bufs.evalDest[:cap(bufs.evalDest)]
	}
	copy(bufs.evalPathBuf[:pathLen], bufs.pathBuf[:pathLen])
	pathBuf := bufs.evalPathBuf
	dest := bufs.evalDest

	for {
		n, err := evalSymlinksAt(pathBuf, pathLen, dest)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, osza.ErrPathBuffer) {
			return 0, err
		}
		if len(dest) < maxReadDirNameBytes {
			next := len(dest) * 2
			if next < len(dest) {
				next = maxReadDirNameBytes
			}
			if next > maxReadDirNameBytes {
				next = maxReadDirNameBytes
			}
			bufs.evalDest = make([]byte, next)
			dest = bufs.evalDest
			continue
		}
		if cap(pathBuf) < maxReadDirNameBytes {
			next := cap(pathBuf) * 2
			if next < cap(pathBuf) {
				next = maxReadDirNameBytes
			}
			if next > maxReadDirNameBytes {
				next = maxReadDirNameBytes
			}
			nb := make([]byte, next)
			copy(nb[:pathLen], bufs.pathBuf[:pathLen])
			bufs.evalPathBuf = nb
			pathBuf = nb
			continue
		}
		return 0, err
	}
}

// evalDirRealPath returns the visit map key for the directory at
// bufs.pathBuf[:dirLen] and whether to check visited before descent. A directory
// that is not a symlink is keyed by visitKey without EvalSymlinks. A symlinked
// directory is keyed by its canonical path; loopGuard reports whether that
// differs from the path read.
func evalDirRealPath(visitKey PathHash, bufs *readDirBuffers, dirLen int) (PathHash, bool, error) {
	info, err := walkLstatPathLen(bufs, dirLen)
	if err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return visitKey, false, nil
	}
	canonicalLen, err := evalSymlinksCanonical(bufs, dirLen)
	if err != nil {
		return PathHash{}, false, err
	}
	canonical := bufs.evalDest[:canonicalLen]
	return HashPathBytes(canonical), !bytes.Equal(bufs.pathBuf[:dirLen], canonical), nil
}

// resolveRoot replaces the root path in bufs.pathBuf[:dirLen] with its canonical
// form and returns its length and visit key. Directories below the root are then
// keyed by canonical paths that match the target of a symlink back into the tree,
// even when an ancestor of the root is a symlink.
func resolveRoot(bufs *readDirBuffers, dirLen int) (int, PathHash, error) {
	canonicalLen, err := evalSymlinksCanonical(bufs, dirLen)
	if err != nil {
		return 0, PathHash{}, err
	}
	n, err := writePathBytes(bufs.pathBuf, bufs.evalDest[:canonicalLen])
	if err != nil {
		return 0, PathHash{}, err
	}
	return n, HashPathBytes(bufs.pathBuf[:n]), nil
}

func basenameInBuf(buf []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	start := 0
	for i := range n {
		if buf[i] == '/' {
			start = i + 1
		}
	}
	return buf[start:n]
}

func fileInfoForDirEntry(bufs *readDirBuffers, entry *osza.Entry) (fs.FileInfo, error) {
	return fileInfoForDirEntryFn(bufs, entry)
}

func hasASCIISuffixFoldBytes(name, suffix []byte) bool {
	ns, nl := len(name), len(suffix)
	if nl == 0 || ns < nl {
		return false
	}
	start := ns - nl
	for i := range nl {
		a := name[start+i]
		b := suffix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

func matchBasenameWithSuffixes(name []byte, suffixes [][]byte) bool {
	for _, suf := range suffixes {
		if hasASCIISuffixFoldBytes(name, suf) {
			return true
		}
	}
	return false
}

func materializeString(buf []byte, n int) string {
	return string(buf[:n])
}

func mustBasenameSuffixesFromPattern(pattern string, re *regexp.Regexp) [][]byte {
	suffixes, err := basenameSuffixesFromPattern(pattern)
	if err != nil {
		panic("parallelwalkdir: basename include pattern: " + err.Error())
	}
	for _, probe := range suffixAgreementProbes(suffixes) {
		gotBytes := matchBasenameWithSuffixes(probe, suffixes)
		gotRE := re.Match(probe)
		if gotBytes != gotRE {
			panic("parallelwalkdir: suffix derivation disagrees with regexp on " + string(probe))
		}
	}
	return suffixes
}

func newReadDirBuffers() *readDirBuffers {
	b := &readDirBuffers{
		entries:     make([]osza.Entry, initialReadDirEntries),
		nameBuf:     make([]byte, initialReadDirNameBytes),
		pathBuf:     make([]byte, initialReadDirNameBytes),
		reportedBuf: make([]byte, initialReadDirNameBytes),
		joinBuf:     make([]byte, initialReadDirNameBytes),
		evalPathBuf: make([]byte, initialReadDirNameBytes),
		evalDest:    make([]byte, initialReadDirNameBytes),
	}
	if runtime.GOOS != "windows" {
		b.scratch = make([]byte, readDirScratchBytes)
	}
	return b
}

// trim replaces entries and nameBuf with initial-sized buffers when either has
// grown past the retain thresholds. Other buffers are left unchanged.
func (b *readDirBuffers) trim() {
	if len(b.entries) <= retainReadDirEntries && len(b.nameBuf) <= retainReadDirNameBytes {
		return
	}
	b.entries = make([]osza.Entry, initialReadDirEntries)
	b.nameBuf = make([]byte, initialReadDirNameBytes)
}

func readDirAllFromPathBuf(b *readDirBuffers, dirLen int) (n int, outLen int, err error) {
	if cap(b.pathBuf) > len(b.pathBuf) {
		b.pathBuf = b.pathBuf[:cap(b.pathBuf)]
	}
	for {
		n, err = oszaReadDir(b.pathBuf, dirLen, b.entries, b.nameBuf, b.scratch, b.joinBuf)
		if err == nil {
			return n, dirLen, nil
		}
		if errors.Is(err, osza.ErrOverflow) {
			if len(b.entries)*2 > maxReadDirEntries {
				return n, dirLen, err
			}
			if len(b.nameBuf)*2 > maxReadDirNameBytes {
				return n, dirLen, err
			}
			b.entries = make([]osza.Entry, len(b.entries)*2)
			b.nameBuf = make([]byte, len(b.nameBuf)*2)
			continue
		}
		if errors.Is(err, osza.ErrPathBuffer) {
			if len(b.joinBuf)*2 > maxReadDirNameBytes {
				return n, dirLen, err
			}
			b.joinBuf = make([]byte, len(b.joinBuf)*2)
			continue
		}
		return n, dirLen, err
	}
}

func cloneStringPath(path string) []byte {
	return append([]byte(nil), path...)
}

func copyPathInto(dst *[]byte, src []byte, n int) {
	if n <= 0 {
		*dst = nil
		return
	}
	if cap(*dst) < n {
		*dst = make([]byte, n)
	} else {
		*dst = (*dst)[:n]
	}
	copy(*dst, src[:n])
}

func dirWorkFromBufs(pathBuf, reportedBuf []byte, pathEnd, reportedEnd int) *dirWork {
	dw := dirWorkPool.Get()
	copyPathInto(&dw.current, pathBuf, pathEnd)
	if pathEnd == reportedEnd && bytes.Equal(pathBuf[:pathEnd], reportedBuf[:reportedEnd]) {
		dw.reported = dw.current
	} else {
		copyPathInto(&dw.reported, reportedBuf, reportedEnd)
	}
	dw.visitKey = HashPathBytes(dw.current)
	return dw
}

func writePathBytes(buf []byte, path []byte) (int, error) {
	n := len(path)
	if n == 0 || n >= len(buf) {
		return 0, syscall.EINVAL
	}
	copy(buf[:n], path)
	return n, nil
}

func suffixAgreementProbes(suffixes [][]byte) [][]byte {
	probes := [][]byte{
		[]byte("notes.txt"),
		[]byte("noextension"),
		[]byte(".jpg"),
	}
	for _, suf := range suffixes {
		base := []byte("file")
		probes = append(probes, append(base, suf...))
		probes = append(probes, append([]byte("FILE"), bytesToUpperASCII(suf)...))
	}
	return probes
}

func unwrapSyntaxGroup(re *syntax.Regexp) *syntax.Regexp {
	for {
		switch re.Op {
		case syntax.OpCapture:
			if len(re.Sub) != 1 {
				return re
			}
			re = re.Sub[0]
		case syntax.OpConcat:
			if len(re.Sub) != 1 {
				return re
			}
			re = re.Sub[0]
		default:
			return re
		}
	}
}
