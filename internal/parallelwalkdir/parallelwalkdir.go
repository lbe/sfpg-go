// Package parallelwalkdir walks directory trees with a bounded pool of directory
// workers and streams matching files on channels.
//
// # Concurrency
//
// Workers read dirWork only from a buffered dirCh. Subdirectories are enqueued on
// an unbounded schedule FIFO (internal/queue.Queue[*dirWork]). One feeder goroutine
// dequeues with DequeueWait, is the sole dirCh writer, and is the only caller of
// pending.Add before each send. The coordinator waits on idleMu/idleCond until the
// schedule queue is empty and processing and feeding atomics are zero, then
// pending.Wait (serialized with feeder Add via pendingMu), stops the feeder,
// closes dirCh, waits for workers, and closes results and errs.
//
// Visit tracking uses FNV-1a 128-bit hashes of canonical directory path bytes
// (EvalSymlinks when needed) to avoid symlink loops. Hash collisions may silently
// skip a directory (accepted policy).
//
// # Filtering (pick one include mode)
//
//   - WithBasenameInclude: match entry basenames with zero-alloc suffix checks
//     derived from the compiled regexp pattern (cached by pattern string). Use for
//     production discovery walks (see files.WalkImageDir). Unsupported patterns
//     panic when the option is applied.
//   - WithRegexpInclude: match entry basenames with regexp.Regexp.Match (may
//     allocate under load). Tests and patterns not supported by suffix derivation.
//   - WithValidationFunc: custom filter on reported path bytes and fs.FileInfo.
//
// WithSizeNotZero drops zero-length files. WithRegexpExclude skips paths whose
// reported path matches. Include options are mutually exclusive with each other
// and with WithValidationFunc.
//
// WithDirEntCatalog: GetDirEntMapFunc runs before readDirAllFn per directory;
// CheckIfFileModifiedFunc gates sendReported on PathHash keys from
// HashPathBytes(entry.Name). Requires WithBasenameInclude and WithSizeNotZero.
// Production discovery passes files.DiscoveryDirEntModifiedWithStats from WalkImageDir;
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
// walkLstatAt, walkLstatPathLen, walkStatAt, walkStatPathLen, and walkLstatJoin default
// to production implementations and may be overridden in tests.
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
	"sync/atomic"
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
)

var (
	basenameIncludeByPattern sync.Map // pattern string -> basenameIncludeFunc
	fileInfoForDirEntryFn    = defaultFileInfoForDirEntry
	evalSymlinksAt           = defaultEvalSymlinksAt // hook: osza.EvalSymlinksAt
	oszaReadDir              = osza.ReadDir          // hook: osza.ReadDir
	readDirAllFn             = readDirAllFromPathBuf // hook: readDirAllFromPathBuf
	walkLstatAt              = defaultWalkLstatAt
	walkLstatPathLen         = defaultWalkLstatPathLen
	walkStatAt               = defaultWalkStatAt
	walkStatPathLen          = defaultWalkStatPathLen
	walkLstatJoin            = defaultWalkLstatJoin
)

type basenameIncludeFunc func(name []byte) bool

type dirWork struct {
	current  []byte
	reported []byte
	// visitKey is HashPathBytes(work.current) at schedule time; used when EvalSymlinks
	// is skipped so eval does not re-hash or canonicalize.
	visitKey PathHash
}

var dirWorkPool = gensyncpool.New(
	func() *dirWork { return &dirWork{} },
	func(d *dirWork) {
		if d.current != nil {
			d.current = d.current[:0]
		}
		if d.reported != nil {
			d.reported = d.reported[:0]
		}
		d.visitKey = PathHash{}
	},
)

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

// WalkDirFunc decides inclusion from the reported path and FileInfo.
// path is the reported path slice; valid only for the call; do not retain.
// Mutually exclusive with WithRegexpInclude, WithBasenameInclude, and WithSizeNotZero.
type WalkDirFunc func(path []byte, info fs.FileInfo) bool

// Walker holds state for a parallel directory traversal: a fixed pool of worker
// goroutines reading from dirCh, an unbounded schedule queue and feeder that is
// the sole writer to dirCh, in-flight work accounting (pending), visited-path
// loop detection, and result/error channels.
type Walker struct {
	results chan ReportedFile // Buffered channel of matching files with walk metadata.
	errs    chan error        // Buffered channel of walk errors.
	mu      sync.Mutex        // Protects visited.
	// visited keys are PathHash of canonical directory bytes. FNV-128 collision may
	// silently skip a directory (no stored path bytes for collision checks).
	visited map[PathHash]struct{}
	ctx     context.Context // Cancellation; defaults to context.Background().

	maxReportedFiles int64         // When > 0, stop the walk after this many results (benchmarks).
	reportedSent     atomic.Uint64 // Reports emitted toward maxReportedFiles for the current walk.
	stopWalk         context.CancelFunc
	stopWalkOnce     sync.Once

	maxWorkers int // Directory worker count when > 0; else workers() uses GOMAXPROCS(0).

	dirChCapacity int // dirCh buffer size when > 0; else workers()*1024 (tests only).

	includeRegex    *regexp.Regexp
	basenameInclude basenameIncludeFunc
	excludeRegex    *regexp.Regexp
	sizeNotZero     bool
	validationFunc  WalkDirFunc

	hasIncludeRegex    bool
	hasBasenameInclude bool
	hasSizeNotZero     bool
	hasValidationFunc  bool

	getDirEntMap        GetDirEntMapFunc
	checkIfFileModified CheckIfFileModifiedFunc
	hasDirEntCatalog    bool

	pathStore *WalkPathStore // Bump arena for reported paths; default created in ParallelWalk when nil.
}

// ParallelWalk traverses rootPath. The returned channels are read-only; both close
// when the walk finishes. The same Walker may be reused for another walk.
//
// WithMaxReportedFiles wraps the walk context in a cancel func; reaching the limit
// cancels further scheduling.
func (w *Walker) ParallelWalk(rootPath string) (<-chan ReportedFile, <-chan error) {
	parentCtx := w.ctx
	walkCtx := parentCtx
	if w.maxReportedFiles > 0 {
		walkCtx, w.stopWalk = context.WithCancel(parentCtx)
	}
	w.ctx = walkCtx
	w.reportedSent.Store(0)

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

	var pending sync.WaitGroup
	var pendingMu sync.Mutex // Serializes feeder Add with coordinator Wait (race-free at zero).
	var workerWg sync.WaitGroup
	var feederWg sync.WaitGroup

	dirQueue := queue.NewQueue[*dirWork](64)
	var processing atomic.Int32
	var feeding atomic.Int32
	var stopFeeder atomic.Bool

	var idleMu sync.Mutex
	idleCond := sync.NewCond(&idleMu)
	idleBroadcast := func() {
		idleMu.Lock()
		idleCond.Broadcast()
		idleMu.Unlock()
	}

	push := func(work *dirWork) bool {
		if stopFeeder.Load() {
			dirWorkPool.Put(work)
			return false
		}
		if err := dirQueue.Enqueue(work); err != nil {
			dirWorkPool.Put(work)
			return false
		}
		idleBroadcast()
		return true
	}

	schedule := func(work *dirWork) {
		if w.ctx.Err() != nil {
			dirWorkPool.Put(work)
			return
		}
		push(work)
	}

	endProcessing := func() {
		processing.Add(-1)
		idleBroadcast()
	}

	endFeeding := func() {
		feeding.Add(-1)
		idleBroadcast()
	}

	stopFeederNow := func() {
		stopFeeder.Store(true)
		dirQueue.BroadcastWaiters()
		idleBroadcast()
	}

	dropQueued := func() {
		for {
			work, err := dirQueue.Dequeue()
			if errors.Is(err, queue.ErrEmptyQueue) {
				break
			}
			if err != nil {
				break
			}
			dirWorkPool.Put(work)
		}
		idleBroadcast()
	}

	scheduleQuiescent := func() bool {
		return dirQueue.IsEmpty() && processing.Load() == 0 && feeding.Load() == 0
	}

	for range n {
		workerWg.Go(func() {
			dirBufs := newReadDirBuffers()
			for work := range dirCh {
				processing.Add(1)
				w.processDir(work, schedule, dirBufs)
				dirWorkPool.Put(work)
				endProcessing()
				pending.Done()
			}
		})
	}

	feederWg.Go(func() {
		for {
			if stopFeeder.Load() {
				return
			}
			work, err := dirQueue.DequeueWait(w.ctx)
			if err != nil {
				if w.ctx.Err() != nil {
					return
				}
				if errors.Is(err, queue.ErrClosedQueue) {
					return
				}
				if errors.Is(err, queue.ErrEmptyQueue) {
					if stopFeeder.Load() {
						return
					}
					continue
				}
				return
			}
			if stopFeeder.Load() {
				dirWorkPool.Put(work)
				return
			}
			feeding.Add(1)
			idleBroadcast()
			pendingMu.Lock()
			pending.Add(1)
			pendingMu.Unlock()
			select {
			case dirCh <- work:
				endFeeding()
			case <-w.ctx.Done():
				endFeeding()
				pending.Done()
				dirWorkPool.Put(work)
				return
			}
		}
	})

	go func() {
		defer func() {
			w.ctx = parentCtx
			w.stopWalk = nil
			w.stopWalkOnce = sync.Once{}
		}()

		root := cloneStringPath(rootPath)
		dw := dirWorkPool.Get()
		dw.current = root
		dw.reported = root
		dw.visitKey = HashPathBytes([]byte(rootPath))
		push(dw)

		for w.ctx.Err() == nil {
			idleMu.Lock()
			for (!dirQueue.IsEmpty() || processing.Load() != 0 || feeding.Load() != 0) && w.ctx.Err() == nil {
				idleCond.Wait()
			}
			idleMu.Unlock()

			if w.ctx.Err() != nil {
				break
			}

			pendingMu.Lock()
			pending.Wait()
			pendingMu.Unlock()

			if scheduleQuiescent() {
				break
			}
		}

		if w.ctx.Err() != nil {
			dropQueued()
		}

		stopFeederNow()
		feederWg.Wait()

		pendingMu.Lock()
		pending.Wait()
		pendingMu.Unlock()
		close(dirCh)
		workerWg.Wait()
		close(w.results)
		close(w.errs)
	}()

	return w.results, w.errs
}

func (w *Walker) acquireReportSlot() bool {
	for {
		n := w.reportedSent.Load()
		if n >= uint64(w.maxReportedFiles) {
			return false
		}
		if w.reportedSent.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (w *Walker) filterAndReportFile(reportedBuf []byte, reportedLen int, info fs.FileInfo, includeMatched bool, entMap map[PathHash]DirEntState, catalogBasename []byte) {
	if w.validationFunc != nil {
		if w.validationFunc(reportedBuf[:reportedLen], info) {
			w.sendReported(reportedBuf, reportedLen, info)
		}
		return
	}

	if !includeMatched && w.usesIncludeFilter() && !w.includeBasenameMatchesBytes(basenameInBuf(reportedBuf, reportedLen)) {
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
			select {
			case w.errs <- err:
			case <-w.ctx.Done():
			}
			return
		}
		if !modified {
			return
		}
	}

	w.sendReported(reportedBuf, reportedLen, info)
}

func (w *Walker) includeBasenameMatchesBytes(name []byte) bool {
	if w.hasBasenameInclude {
		return w.basenameInclude(name)
	}
	if !w.hasIncludeRegex {
		return true
	}
	return w.includeRegex.Match(name)
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
		select {
		case w.errs <- &fs.PathError{Op: "ReadDir", Path: string(work.current), Err: err}:
		case <-w.ctx.Done():
		}
		return
	}
	reportedLen, err := writePathBytes(dirBufs.reportedBuf, work.reported)
	if err != nil {
		select {
		case w.errs <- &fs.PathError{Op: "ReadDir", Path: string(work.current), Err: err}:
		case <-w.ctx.Done():
		}
		return
	}

	visitHash, loopGuard, err := evalDirRealPath(work, dirBufs, dirLen)
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Error("EvalSymlinks error", "currentPath", string(work.current), "err", err)
		}
		select {
		case w.errs <- &fs.PathError{Op: "EvalSymlinks", Path: string(work.current), Err: err}:
		case <-w.ctx.Done():
		}
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

	if w.excludeRegex != nil && w.excludeRegex.Match(work.reported) {
		return
	}

	var entMap map[PathHash]DirEntState
	if w.hasDirEntCatalog {
		entMap, err = w.getDirEntMap(w.ctx, work.reported)
		if err != nil {
			select {
			case w.errs <- err:
			case <-w.ctx.Done():
			}
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
		select {
		case w.errs <- &fs.PathError{Op: "ReadDir", Path: materializeString(dirBufs.pathBuf, dirLen), Err: err}:
		case <-w.ctx.Done():
		}
		return
	}

	if w.ctx.Err() != nil {
		return
	}

	splitReported := !bytes.Equal(work.current, work.reported)

	for i := range n {
		if w.ctx.Err() != nil {
			return
		}

		entry := &dirBufs.entries[i]
		var pathEnd int
		var segErr error
		dirBufs.pathBuf, pathEnd, segErr = appendPathSegment(dirBufs.pathBuf, dirLen, entry.Name)
		if segErr != nil {
			select {
			case w.errs <- &fs.PathError{Op: "Stat", Path: materializeString(dirBufs.pathBuf, pathEnd), Err: segErr}:
			case <-w.ctx.Done():
				return
			}
			continue
		}
		reportedEnd := pathEnd
		if splitReported {
			dirBufs.reportedBuf, reportedEnd, segErr = appendPathSegment(dirBufs.reportedBuf, reportedLen, entry.Name)
			if segErr != nil {
				select {
				case w.errs <- &fs.PathError{Op: "Stat", Path: materializeString(dirBufs.pathBuf, pathEnd), Err: segErr}:
				case <-w.ctx.Done():
					return
				}
				continue
			}
		} else {
			copy(dirBufs.reportedBuf[dirLen:pathEnd], dirBufs.pathBuf[dirLen:pathEnd])
		}

		if w.excludeRegex != nil && w.excludeRegex.Match(dirBufs.reportedBuf[:reportedEnd]) {
			continue
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

		if w.usesIncludeFilter() && !w.includeBasenameMatchesBytes(entry.Name) {
			continue
		}

		info, statErr := fileInfoForDirEntry(dirBufs, entry)
		if statErr != nil {
			select {
			case w.errs <- &fs.PathError{Op: "Stat", Path: materializeString(dirBufs.pathBuf, pathEnd), Err: statErr}:
			case <-w.ctx.Done():
				return
			}
			continue
		}
		w.filterAndReportFile(dirBufs.reportedBuf, reportedEnd, info, true, entMap, entry.Name)
	}
}

func (w *Walker) sendReported(reportedBuf []byte, reportedLen int, info fs.FileInfo) {
	if w.maxReportedFiles > 0 && !w.acquireReportSlot() {
		w.requestWalkStop()
		return
	}

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

	if w.maxReportedFiles > 0 && int64(w.reportedSent.Load()) >= w.maxReportedFiles {
		w.requestWalkStop()
	}
}

func (w *Walker) usesIncludeFilter() bool {
	return w.hasIncludeRegex || w.hasBasenameInclude
}

func (w *Walker) requestWalkStop() {
	if w.stopWalk == nil {
		return
	}
	w.stopWalkOnce.Do(func() {
		w.stopWalk()
	})
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
// Mutually exclusive with WithRegexpInclude and WithValidationFunc.
func WithBasenameInclude(r *regexp.Regexp) Option {
	return func(w *Walker) {
		w.basenameInclude = basenameIncludeForRegexp(r)
		w.hasBasenameInclude = true
	}
}

// NewWalker creates a Walker and applies options. Default worker count is
// runtime.GOMAXPROCS(0) when WithMaxWorkers is unset or <= 0.
//
// Panics if include options conflict: WithValidationFunc with any other filter
// option, or both WithRegexpInclude and WithBasenameInclude.
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

	if w.hasValidationFunc && (w.hasIncludeRegex || w.hasBasenameInclude || w.hasSizeNotZero) {
		panic("WithValidationFunc is mutually exclusive with WithRegexpInclude, WithBasenameInclude, and WithSizeNotZero")
	}
	if w.hasIncludeRegex && w.hasBasenameInclude {
		panic("WithRegexpInclude is mutually exclusive with WithBasenameInclude")
	}

	hasGet := w.getDirEntMap != nil
	hasCheck := w.checkIfFileModified != nil
	if hasGet != hasCheck {
		panic("parallelwalkdir: WithDirEntCatalog requires both GetDirEntMapFunc and CheckIfFileModifiedFunc, or neither")
	}
	if hasGet {
		w.hasDirEntCatalog = true
		if w.hasIncludeRegex {
			panic("parallelwalkdir: WithDirEntCatalog is mutually exclusive with WithRegexpInclude")
		}
		if w.hasValidationFunc {
			panic("parallelwalkdir: WithDirEntCatalog is mutually exclusive with WithValidationFunc")
		}
		if !w.hasBasenameInclude {
			panic("parallelwalkdir: WithDirEntCatalog requires WithBasenameInclude")
		}
		if !w.hasSizeNotZero {
			panic("parallelwalkdir: WithDirEntCatalog requires WithSizeNotZero")
		}
	}

	return w
}

// WithContext sets cancellation for the walk. When cancelled, the feeder stops
// without feeding queued work that was not yet sent on dirCh; workers and
// schedule submit paths exit as soon as practical; results and errs close after
// workers finish.
// WithWalkPathStore sets the bump arena for reported paths. When unset,
// ParallelWalk allocates a default store for the Walker.
func WithWalkPathStore(store *WalkPathStore) Option {
	return func(w *Walker) {
		w.pathStore = store
	}
}

func WithContext(ctx context.Context) Option {
	return func(w *Walker) {
		w.ctx = ctx
	}
}

// WithDirChCapacity sets the buffered dirCh capacity. Zero uses workers()*1024.
// Values > 0 set an exact buffer size (tests only; production does not set this).
func WithDirChCapacity(n int) Option {
	return func(w *Walker) {
		w.dirChCapacity = n
	}
}

// WithMaxWorkers sets the number of directory worker goroutines. Values <= 0 use
// runtime.GOMAXPROCS(0). Intended for tests; production discovery does not set this.
func WithMaxWorkers(n int) Option {
	return func(w *Walker) {
		w.maxWorkers = n
	}
}

// WithRegexpExclude skips directory entries whose reported path matches r.
// Applied to both files and directories before descent.
func WithRegexpExclude(r *regexp.Regexp) Option {
	return func(w *Walker) {
		w.excludeRegex = r
	}
}

// WithRegexpInclude limits files to those whose basename matches r via Match.
// Mutually exclusive with WithBasenameInclude and WithValidationFunc.
func WithRegexpInclude(r *regexp.Regexp) Option {
	return func(w *Walker) {
		w.includeRegex = r
		w.hasIncludeRegex = true
	}
}

// WithMaxReportedFiles stops the walk after n matching files are sent on results.
// Intended for benchmarks and sampling; production discovery does not set this.
func WithMaxReportedFiles(n int64) Option {
	return func(w *Walker) {
		w.maxReportedFiles = n
	}
}

// WithSizeNotZero excludes zero-length files. Mutually exclusive with WithValidationFunc.
func WithSizeNotZero() Option {
	return func(w *Walker) {
		w.sizeNotZero = true
		w.hasSizeNotZero = true
	}
}

// WithValidationFunc supplies custom file filtering on reported path bytes.
// Mutually exclusive with WithRegexpInclude, WithBasenameInclude, and WithSizeNotZero.
func WithValidationFunc(vf WalkDirFunc) Option {
	return func(w *Walker) {
		w.validationFunc = vf
		w.hasValidationFunc = true
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

func defaultWalkLstatAt(bufs *readDirBuffers, path string) (fs.FileInfo, error) {
	pathLen, err := writePathBuf(bufs.pathBuf, path)
	if err != nil {
		return nil, err
	}
	if err := osza.LstatAt(bufs.pathBuf, pathLen, &bufs.meta); err != nil {
		return nil, err
	}
	return &bufs.meta, nil
}

func defaultWalkStatAt(bufs *readDirBuffers, path string) (fs.FileInfo, error) {
	pathLen, err := writePathBuf(bufs.pathBuf, path)
	if err != nil {
		return nil, err
	}
	if err := osza.StatAt(bufs.pathBuf, pathLen, &bufs.meta); err != nil {
		return nil, err
	}
	return &bufs.meta, nil
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

func defaultWalkLstatJoin(bufs *readDirBuffers, parentPath string, name []byte) (fs.FileInfo, error) {
	parentLen := len(parentPath)
	nameLen := len(name)
	need := parentLen + 1 + nameLen + 1
	if len(bufs.joinBuf) < need {
		return nil, syscall.ENAMETOOLONG
	}
	copy(bufs.joinBuf[:parentLen], parentPath)
	copy(bufs.joinBuf[parentLen:parentLen+nameLen], name)
	if err := osza.LstatJoin(bufs.joinBuf, parentLen, nameLen, &bufs.meta); err != nil {
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

// evalDirRealPath returns the visit map key and whether to check visited before
// descent (symlink alias or canonical path differs from work.current). When current
// and reported paths match and current is not a symlink, EvalSymlinks is skipped.
func evalDirRealPath(work *dirWork, bufs *readDirBuffers, dirLen int) (PathHash, bool, error) {
	if !bytes.Equal(work.current, work.reported) {
		canonicalLen, err := evalSymlinksCanonical(bufs, dirLen)
		if err != nil {
			return PathHash{}, false, err
		}
		canonical := bufs.evalDest[:canonicalLen]
		return HashPathBytes(canonical), !bytes.Equal(work.current, canonical), nil
	}
	info, err := walkLstatPathLen(bufs, dirLen)
	if err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return work.visitKey, false, nil
	}
	canonicalLen, err := evalSymlinksCanonical(bufs, dirLen)
	if err != nil {
		return PathHash{}, false, err
	}
	canonical := bufs.evalDest[:canonicalLen]
	return HashPathBytes(canonical), !bytes.Equal(work.current, canonical), nil
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

func writePathBuf(buf []byte, path string) (int, error) {
	n := len(path)
	if n == 0 || n >= len(buf) {
		return 0, syscall.EINVAL
	}
	copy(buf[:n], path)
	return n, nil
}
