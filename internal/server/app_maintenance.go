package server

import (
	"context"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// startStartupPragmaOptimize schedules a one-shot PRAGMA optimize=0x10002
// that runs after the server is listening and the system is quiet.
func (app *App) startStartupPragmaOptimize() {
	app.SchedulePragmaOptimize(
		app.RuntimeManager.ctx,
		dbconnpool.PragmaOptimizeFreshConnection,
		"startup",
		app.cacheSizeQuietCheck,
		app.RuntimeManager.wg.Go,
	)
}

// scheduleDiscoveryCompletePragmaOptimize schedules a plain PRAGMA optimize
// after file discovery completes. Non-blocking; uses cacheSizeQuietCheck to
// wait until the system is idle.
func (app *App) scheduleDiscoveryCompletePragmaOptimize() {
	app.SchedulePragmaOptimize(
		app.RuntimeManager.ctx,
		dbconnpool.PragmaOptimizeDefault,
		"discovery-complete",
		app.cacheSizeQuietCheck,
		app.RuntimeManager.wg.Go,
	)
}

func (app *App) startCacheSizeCalibration() {
	app.ConfigManager.ConfigMu.RLock()
	cacheEnabled := app.ConfigManager.Config != nil && app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()
	if !cacheEnabled {
		app.cacheSizeCalibrated.Store(true)
		return
	}

	quiet := app.cacheSizeQuietCheck
	app.StartCacheSizeCalibration(app.RuntimeManager.ctx, quiet, func(fn func()) {
		app.RuntimeManager.wg.Go(fn)
	})
}

// fileProcessingQuiet reports whether discovery walk, queue, and workers are idle.
func (app *App) fileProcessingQuiet() bool {
	if app.discoveryRunning.Load() {
		return false
	}
	sm := app.SubsystemManager
	if sm == nil {
		return true
	}
	if sm.qSendersActive.Load() > 0 {
		return false
	}
	if sm.q != nil && sm.q.Len() > 0 {
		return false
	}
	if sm.processingStats != nil && sm.processingStats.InFlight.Load() > 0 {
		return false
	}
	if sm.fileProcessor != nil && sm.fileProcessor.PendingWriteCount() > 0 {
		return false
	}
	return true
}

func (app *App) cacheSizeQuietCheck(ctx context.Context) bool {
	if !app.fileProcessingQuiet() {
		return false
	}
	if app.testSeams.ModuleStateActive != nil {
		active, err := app.testSeams.ModuleStateActive()
		if err != nil || active {
			return false
		}
	} else if app.SubsystemManager.moduleStateService != nil {
		active, err := app.SubsystemManager.moduleStateService.IsActive(ctx, "discovery")
		if err != nil || active {
			return false
		}
	}
	if app.writeBatcher != nil && app.writeBatcher.PendingCount() >= cacheCalibrationQuietPendingMax {
		return false
	}
	return true
}

func (app *App) onServerListening() {
	app.SetCacheCalibrationListening(true)
	app.SetPragmaOptimizeListening(true)
	app.StartDQueDrain()
}
