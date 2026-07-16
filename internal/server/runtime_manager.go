package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lbe/sfpg-go/internal/server/metrics"
)

var (
	// osExit is a testable hook for os.Exit used by RuntimeManager.exit.
	osExit = os.Exit
)

// RuntimeManager owns application lifecycle, HTTP server, and restart state.
type RuntimeManager struct {
	cancel           context.CancelFunc
	ctx              context.Context
	wg               sync.WaitGroup
	stopProfiler     func()
	metricsCollector *metrics.Collector
	version          string

	restartRequired  atomic.Bool
	restartRequested atomic.Bool
	httpServerMu     sync.Mutex
	httpServer       *http.Server
	execCommand      func(path string, args []string, env []string) error
	shutdownOnce     sync.Once
	poolDone         chan struct{}

	galleryStatsMu         sync.RWMutex
	galleryStatsCache      *GalleryStats
	galleryStatsAt         int64
	staleCacheDropInFlight atomic.Bool

	testSeams RuntimeManagerTestSeams
}

// NewRuntimeManager constructs a runtime manager with a cancellable child context.
func NewRuntimeManager(parent context.Context) *RuntimeManager {
	ctx, cancel := context.WithCancel(parent)
	return &RuntimeManager{
		ctx:         ctx,
		cancel:      cancel,
		execCommand: syscall.Exec,
	}
}

// ─── Serve ──────────────────────────────────────────────────────────

// Serve starts the HTTP server and blocks until shutdown, error, or context cancellation.
func (m *RuntimeManager) Serve(handler http.Handler, addr string) error {
	if m.testSeams.BeforeListen != nil {
		m.testSeams.BeforeListen()
	}

	m.httpServerMu.Lock()
	m.httpServer = &http.Server{Addr: addr, Handler: handler}
	httpServer := m.httpServer
	m.httpServerMu.Unlock()

	slog.Info("starting web server", "addr", addr)

	serverErr := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	case <-m.ctx.Done():
		slog.Info("context cancelled, shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m.httpServerMu.Lock()
		shutdownServer := m.httpServer
		m.httpServerMu.Unlock()
		if shutdownServer != nil {
			if err := m.shutdownServer(shutdownCtx, shutdownServer); err != nil {
				slog.Error("error during server shutdown", "err", err)
			}
		}
		return nil
	}
}

func (m *RuntimeManager) shutdownServer(ctx context.Context, srv *http.Server) error {
	if m.testSeams.Shutdown != nil {
		return m.testSeams.Shutdown(ctx)
	}
	return srv.Shutdown(ctx)
}

// ─── Restart ────────────────────────────────────────────────────────

// SetRestartRequired records whether a configuration change requires process restart.
func (m *RuntimeManager) SetRestartRequired(b bool) { m.restartRequired.Store(b) }

// IsRestartRequested reports whether a process restart has been requested.
func (m *RuntimeManager) IsRestartRequested() bool { return m.restartRequested.Load() }

// RestartRequired reports whether configuration changes require a restart.
func (m *RuntimeManager) RestartRequired() bool { return m.restartRequired.Load() }

// TriggerRestart gracefully shuts down the HTTP server to prepare for exec restart.
func (m *RuntimeManager) TriggerRestart() {
	slog.Info("process restart requested")
	m.restartRequested.Store(true)
	m.httpServerMu.Lock()
	srv := m.httpServer
	m.httpServerMu.Unlock()
	if srv == nil {
		slog.Warn("no HTTP server running, cannot gracefully shut down before restart")
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.shutdownServer(shutdownCtx, srv); err != nil {
		slog.Error("error during graceful shutdown before restart", "err", err)
	}
}

// ExecRestart replaces the current process image with a fresh instance.
func (m *RuntimeManager) ExecRestart() {
	exe, err := os.Executable()
	if m.testSeams.Executable != nil {
		exe, err = m.testSeams.Executable()
	}
	if err != nil {
		slog.Error("failed to get executable path", "err", err)
		m.exit(1)
		return
	}
	slog.Info("re-executing process", "exe", exe, "args", os.Args)
	execCmd := m.execCommand
	if m.testSeams.ExecCommand != nil {
		execCmd = m.testSeams.ExecCommand
	}
	if execCmd == nil {
		execCmd = syscall.Exec
	}
	if err := execCmd(exe, os.Args, os.Environ()); err != nil {
		slog.Error("failed to exec new process image", "err", err)
		m.exit(1)
		return
	}
}

func (m *RuntimeManager) exit(code int) {
	if m.testSeams.Exit != nil {
		m.testSeams.Exit(code)
		return
	}
	osExit(code)
}

// ─── Gallery stats ──────────────────────────────────────────────────

// GetGalleryStatsCached returns cached gallery stats when still valid for the given discovery timestamp.
func (m *RuntimeManager) GetGalleryStatsCached(discoveryLastStartedAt int64) *GalleryStats {
	m.galleryStatsMu.RLock()
	defer m.galleryStatsMu.RUnlock()
	if m.galleryStatsCache == nil || m.galleryStatsAt != discoveryLastStartedAt {
		return nil
	}
	c := *m.galleryStatsCache
	return &c
}

// SetGalleryStatsCache stores gallery stats with the discovery timestamp used for invalidation.
func (m *RuntimeManager) SetGalleryStatsCache(stats *GalleryStats, at int64) {
	m.galleryStatsMu.Lock()
	m.galleryStatsCache = stats
	m.galleryStatsAt = at
	m.galleryStatsMu.Unlock()
}
