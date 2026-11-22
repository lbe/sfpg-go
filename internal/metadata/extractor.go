// Package metadata reads image metadata with ExifTool.
package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	exiftool "github.com/lbe/go-exiftool-wasm"
	"golang.org/x/sync/semaphore"
)

// ErrClosed is returned by Extract after Close.
var ErrClosed = errors.New("metadata: extractor closed")

const (
	// idleTimeout is how long a server may sit idle before it is shut down to
	// release its memory.
	idleTimeout = 30 * time.Second

	// ExifTool's WASM memory grows to fit the largest file or result it has
	// handled and never shrinks, so a server is shut down after a file or a
	// result at least this large instead of being kept.
	retireFileBytes   = 16 << 20
	retireOutputBytes = 1 << 20
)

// Extractor runs a small pool of ExifTool servers. Servers start on first use
// and shut down after idleTimeout. It is safe for concurrent use.
type Extractor struct {
	// sem bounds running extractions, and so the number of servers: each
	// holder of a slot holds at most one server.
	sem   *semaphore.Weighted
	slots int64

	mu     sync.Mutex
	idle   []idleServer
	closed bool

	stop      chan struct{}
	done      chan struct{}
	closeOnce func() error
}

type idleServer struct {
	server *exiftool.Server
	since  time.Time
}

// PoolSize returns the most ExifTool servers an Extractor runs at once: one
// per three CPUs, from 2 to 4.
func PoolSize() int { return min(4, max(2, runtime.NumCPU()/3)) }

// New returns an Extractor. Close must be called to stop it.
func New() *Extractor {
	slots := int64(PoolSize())
	e := &Extractor{
		sem:   semaphore.NewWeighted(slots),
		slots: slots,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	e.closeOnce = sync.OnceValue(e.shutdown)
	go e.reap()
	return e
}

// Extract reads the metadata of the image at path. An error reported by
// ExifTool for the file is returned as an error.
func (e *Extractor) Extract(ctx context.Context, path string) (Info, error) {
	if path == "" || strings.ContainsAny(path, "\r\n\x00") {
		return Info{}, fmt.Errorf("metadata: path %q cannot be passed to ExifTool", path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Info{}, fmt.Errorf("metadata: %w", err)
	}
	if err = e.sem.Acquire(ctx, 1); err != nil {
		return Info{}, err
	}
	defer e.sem.Release(1)

	server, err := e.take()
	if err != nil {
		return Info{}, err
	}
	var size int64
	if fi, statErr := os.Stat(path); statErr == nil {
		size = fi.Size()
	}
	// A command cannot be interrupted once it starts, so check just before.
	if err = ctx.Err(); err != nil {
		e.put(server, false)
		return Info{}, err
	}
	out, err := server.Command(append(args(), path)...)
	// A server whose command failed may be broken; the next file gets a new one.
	e.put(server, err != nil || size >= retireFileBytes || len(out) >= retireOutputBytes)
	if err != nil {
		return Info{}, fmt.Errorf("metadata: exiftool: %w", err)
	}
	// A caller canceled while the command ran does not want its result.
	if err = ctx.Err(); err != nil {
		return Info{}, err
	}
	return parse(out, path)
}

// args returns ExifTool's arguments, less the path: JSON with group names and
// numeric values, every tag, the raw XMP packet as XMP:XMP, and no embedded
// images.
func args() []string {
	return []string{"-json", "-G1", "-n", "-struct", "-b", "-all", "-XMP:XMP",
		"--ThumbnailImage", "--PreviewImage", "--PhotoshopThumbnail", "--JpgFromRaw", "--OtherImage"}
}

// take returns an idle server or starts one. The caller holds a slot.
func (e *Extractor) take() (*exiftool.Server, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, ErrClosed
	}
	if n := len(e.idle); n > 0 {
		s := e.idle[n-1].server
		e.idle[n-1] = idleServer{}
		e.idle = e.idle[:n-1]
		e.mu.Unlock()
		return s, nil
	}
	e.mu.Unlock()

	s, err := exiftool.NewServer()
	if err != nil {
		return nil, fmt.Errorf("metadata: start exiftool: %w", err)
	}
	if _, err = s.Command("-ver"); err != nil {
		shutdown(s)
		return nil, fmt.Errorf("metadata: start exiftool: %w", err)
	}
	return s, nil
}

// put returns a server to the idle list, or shuts it down if retire is set.
func (e *Extractor) put(s *exiftool.Server, retire bool) {
	if retire {
		shutdown(s)
		return
	}
	e.mu.Lock()
	e.idle = append(e.idle, idleServer{server: s, since: time.Now()})
	e.mu.Unlock()
}

// reap shuts down idle servers until Close. A server is shut down at the
// first tick after it has been idle for idleTimeout, so between 30 and 60
// seconds after its last use.
func (e *Extractor) reap() {
	defer close(e.done)
	t := time.NewTicker(idleTimeout)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case now := <-t.C:
			e.shutdownIdle(now)
		}
	}
}

// shutdownIdle shuts down the servers that have been idle for idleTimeout at
// now. It holds a slot while a server shuts down, so a replacement cannot start
// until the old server is gone and the pool never exceeds its size. It stops
// when every slot is in use; the next tick tries again.
func (e *Extractor) shutdownIdle(now time.Time) {
	for e.sem.TryAcquire(1) {
		e.mu.Lock()
		i := slices.IndexFunc(e.idle, func(s idleServer) bool { return now.Sub(s.since) >= idleTimeout })
		if i < 0 {
			e.mu.Unlock()
			e.sem.Release(1)
			return
		}
		s := e.idle[i].server
		e.idle = slices.Delete(e.idle, i, i+1)
		e.mu.Unlock()
		shutdown(s)
		e.sem.Release(1)
	}
}

// shutdown stops a server that is no longer needed. Its result, if any, has
// already been read, so a failure to stop cleanly changes nothing.
func shutdown(s *exiftool.Server) {
	_ = s.Shutdown() //nolint:errcheck // see above
}

// Close waits for running extractions and shuts the servers down. Extract
// returns ErrClosed afterwards. Calling Close again returns the first result.
func (e *Extractor) Close() error {
	return e.closeOnce()
}

func (e *Extractor) shutdown() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	close(e.stop)
	<-e.done

	// Holding every slot means no extraction is running, so every server is
	// idle. Release them afterwards so callers blocked in Acquire can return
	// ErrClosed.
	_ = e.sem.Acquire(context.Background(), e.slots) //nolint:errcheck // cannot fail without a deadline
	defer e.sem.Release(e.slots)
	e.mu.Lock()
	idle := e.idle
	e.idle = nil
	e.mu.Unlock()
	var errs []error
	for _, s := range idle {
		errs = append(errs, s.server.Shutdown())
	}
	return errors.Join(errs...)
}
