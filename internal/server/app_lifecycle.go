package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// Shutdown gracefully stops the application: drains the write batcher, cancels
// the main context, waits for background goroutines and the worker pool,
// closes database pools and the logger. Safe to call multiple times.
func (app *App) Shutdown() {
	app.RuntimeManager.shutdownOnce.Do(func() {
		app.InfrastructureService.Shutdown()

		if app.RuntimeManager.cancel != nil {
			app.RuntimeManager.cancel()
		}
		if app.RuntimeManager.poolDone != nil {
			<-app.RuntimeManager.poolDone
		}
		app.RuntimeManager.wg.Wait()

		app.SubsystemManager.Shutdown()
		if app.SubsystemManager.scheduler != nil {
			if err := app.SubsystemManager.scheduler.Shutdown(); err != nil {
				slog.Error("error shutting down scheduler", "err", err)
			}
		}

		if app.dbRoPool != nil {
			if err := app.dbRoPool.Close(); err != nil {
				slog.Error("error closing read-only pool", "err", err)
			}
		}
		if app.dbRwPool != nil {
			if err := app.dbRwPool.Close(); err != nil {
				slog.Error("error closing read-write pool", "err", err)
			}
		}
		if app.logger != nil {
			if err := app.logger.Shutdown(); err != nil {
				slog.Error("error shutting down logger", "err", err)
			}
		}
	})
}

// LogProfileLocation logs the profile directory and stops the profiler if active.
// This should be called before shutdown to ensure profile location is logged to both console and file.
func (app *App) LogProfileLocation() {
	if app.RuntimeManager.stopProfiler != nil {
		app.RuntimeManager.stopProfiler()
		// Log after stopping to ensure profile is flushed
		if dir := profiler.Dir(); dir != "" {
			slog.Info("Profile artifacts written", "dir", dir)
		}
	}
}

// refreshGalleryStatsCache runs getGalleryStatistics, updates the in-memory cache,
// and returns the stats. Uses discoveryLastStartedAt as the cache key.
func (app *App) refreshGalleryStatsCache(ctx context.Context, discoveryLastStartedAt int64) (GalleryStats, error) {
	stats, err := app.getGalleryStatistics(ctx)
	if err != nil {
		return GalleryStats{}, err
	}
	app.SetGalleryStatsCache(&stats, discoveryLastStartedAt)
	return stats, nil
}

// getGalleryStatsCached returns cached stats if valid for current discoveryLastStartedAt.
// If invalid or stale, returns nil and caller should call refreshGalleryStatsCache.
func (app *App) getGalleryStatsCached(discoveryLastStartedAt int64) *GalleryStats {
	return app.GetGalleryStatsCached(discoveryLastStartedAt)
}

// Forwarding methods keep the public App API stable while ownership lives in
// the named manager fields. These replace the previous anonymous-embedding
// promotion so callers do not depend on embedded-method resolution.

// EnsureSession creates the session store and manager if not already set.
func (app *App) EnsureSession(getOptionsConfig func() *session.OptionsConfig) {
	app.SessionAuthFacade.EnsureSession(getOptionsConfig)
}

// IsAuthenticated reports whether the request has a valid authenticated session.
func (app *App) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return app.SessionAuthFacade.IsAuthenticated(w, r)
}

// EnsureCSRFToken ensures a CSRF token exists in the session and returns it.
func (app *App) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return app.SessionAuthFacade.EnsureCSRFToken(w, r)
}

// CSRFTokenForPage returns a CSRF token for template rendering.
func (app *App) CSRFTokenForPage(w http.ResponseWriter, r *http.Request, authenticated bool) string {
	return app.SessionAuthFacade.CSRFTokenForPage(w, r, authenticated)
}

// GetEffectiveTheme returns the effective theme for a request.
func (app *App) GetEffectiveTheme(r *http.Request, getThemes func() []string, defaultTheme string) string {
	return app.SessionAuthFacade.GetEffectiveTheme(r, getThemes, defaultTheme)
}

// GetAdminUsername retrieves the administrator's username from the database.
func (app *App) GetAdminUsername(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (string, error) {
	return app.SessionAuthFacade.GetAdminUsername(ctx, pool)
}

// Build creates all handler instances using ServerDeps.
func (app *App) Build(templateFS fs.FS, appDeps interfaces.ServerDeps, authSvc auth.AuthService, sm session.SessionManager, dbRoPool, dbRwPool *dbconnpool.DbSQLConnPool, ctx context.Context, configService config.ConfigService, getETagVersion func() string, metricsCollector *metrics.Collector) error {
	return app.HandlerManager.Build(templateFS, appDeps, authSvc, sm, dbRoPool, dbRwPool, ctx, configService, getETagVersion, metricsCollector)
}

// SetPreloadService wires the preload service into gallery handlers.
func (app *App) SetPreloadService(pm cachepreload.PreloadService) {
	app.HandlerManager.SetPreloadService(pm)
}

// Start creates all subsystems from config.
func (app *App) Start(ctx context.Context, cfg *config.Config, minPoolWorkers, maxPoolWorkers int, imagesDir, normalizedImagesDir string, removeImagesDirPrefixFn func(string, string) (string, error), getRouter func() http.Handler, getHandlerQueries func(*dbconnpool.CpConn) interfaces.HandlerQueries, getETagVersion func() string) {
	app.SubsystemManager.Start(ctx, cfg, minPoolWorkers, maxPoolWorkers, imagesDir, normalizedImagesDir, removeImagesDirPrefixFn, getRouter, getHandlerQueries, getETagVersion)
}

// StartPool launches the worker pool goroutine.
func (app *App) StartPool(ctx context.Context, poolDone chan struct{}, normalizedImagesDir string, removeImagesDirPrefixFn func(string, string) (string, error), processor files.FileProcessor) {
	app.SubsystemManager.StartPool(ctx, poolDone, normalizedImagesDir, removeImagesDirPrefixFn, processor)
}

// WireMetrics connects subsystem metrics to the collector.
func (app *App) WireMetrics(collector *metrics.Collector) {
	app.SubsystemManager.WireMetrics(collector)
}

// Forwarding methods for RuntimeManager-owned lifecycle operations.
// These keep the public App API stable while ownership lives in RuntimeManager.

// IsRestartRequested reports whether a process restart has been requested.
func (app *App) IsRestartRequested() bool {
	return app.RuntimeManager.IsRestartRequested()
}

// TriggerRestart gracefully shuts down the server so Serve returns.
func (app *App) TriggerRestart() {
	app.RuntimeManager.TriggerRestart()
}

// ExecRestart replaces the current process image.
func (app *App) ExecRestart() {
	app.RuntimeManager.ExecRestart()
}

// GetGalleryStatsCached returns cached gallery stats for the current discovery run.
func (app *App) GetGalleryStatsCached(discoveryLastStartedAt int64) *GalleryStats {
	return app.RuntimeManager.GetGalleryStatsCached(discoveryLastStartedAt)
}

// SetGalleryStatsCache stores gallery stats keyed by discovery start time.
func (app *App) SetGalleryStatsCache(stats *GalleryStats, at int64) {
	app.RuntimeManager.SetGalleryStatsCache(stats, at)
}

// MemoryReclaimerConfig holds the configuration for the memory reclaimer.
type MemoryReclaimerConfig struct {
	InitialDelay  time.Duration
	CheckInterval time.Duration
	IdleThreshold time.Duration
	FreeMemFunc   func()
}

// memoryReclaimer is a background goroutine that periodically checks if the application is idle
// and, if so, triggers a garbage collection and releases unused memory back to the OS.
func (app *App) memoryReclaimer(cfg MemoryReclaimerConfig) {
	app.RuntimeManager.wg.Add(1)
	defer app.RuntimeManager.wg.Done()

	// Start checking after an initial delay.
	initialDelay := time.NewTimer(cfg.InitialDelay)
	ctx := app.getCtx()
	select {
	case <-initialDelay.C:
	case <-ctx.Done():
		initialDelay.Stop()
		return
	}

	ticker := time.NewTicker(cfg.CheckInterval)
	defer ticker.Stop()

	for {
		ctx := app.getCtx()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			isQueueEmpty := app.SubsystemManager.q.IsEmpty()
			timeSinceLastCompletion := app.SubsystemManager.pool.TimeSinceLastCompletion()

			if isQueueEmpty && timeSinceLastCompletion > cfg.IdleThreshold {
				runtime.GC()      // 1. Trigger a GC
				cfg.FreeMemFunc() // 2. Release unused memory to the OS
			}
		}
	}
}

// StartCacheBatchLoad attempts to start cache batch load. Returns blocked=true when
// discovery is active (caller should return 409). Starts the run in a goroutine on success.
func (app *App) StartCacheBatchLoad() (interfaces.StartCacheBatchLoadResult, error) {
	ctx := app.getCtx()

	var active bool
	var err error
	if app.testSeams.ModuleStateActive != nil {
		active, err = app.testSeams.ModuleStateActive()
	} else if app.SubsystemManager.moduleStateService != nil {
		active, err = app.SubsystemManager.moduleStateService.IsActive(ctx, "discovery")
	}
	if err != nil {
		return interfaces.StartCacheBatchLoadResult{}, err
	}
	if active {
		return interfaces.StartCacheBatchLoadResult{
			Blocked: true,
			Message: "Cache batch load blocked: discovery active",
		}, nil
	}

	if app.SubsystemManager.batchLoadManager == nil && app.testSeams.BatchLoadManagerRun == nil {
		return interfaces.StartCacheBatchLoadResult{
			Blocked: false,
			Message: "Cache batch load not available",
		}, nil
	}

	go func() {
		var err error
		if app.testSeams.BatchLoadManagerRun != nil {
			err = app.testSeams.BatchLoadManagerRun(ctx)
		} else {
			err = app.SubsystemManager.batchLoadManager.Run(ctx)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("cache batch load run failed", "err", err)
		}
	}()

	return interfaces.StartCacheBatchLoadResult{
		Blocked: false,
		Message: "Cache batch load started",
	}, nil
}
