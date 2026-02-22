package cachepreload

import (
	"context"
	"log/slog"
	"time"

	"go.local/sfpg/internal/scheduler"
)

// PreloadTask makes an internal HTTP request to warm the cache for a single endpoint.
// When HXTarget is non-empty, the request uses HTMX variant headers so the stored
// cache entry matches real browser requests (e.g. info box, lightbox).
type PreloadTask struct {
	CacheKey      string
	Path          string
	HXTarget      string // optional; when set, request uses HX-Request/HX-Target and Encoding
	Encoding      string // optional; used with HXTarget (e.g. "gzip")
	TaskTracker   *TaskTracker
	RequestConfig InternalRequestConfig
	Metrics       *PreloadMetrics
}

// Run implements scheduler.Task.
func (t *PreloadTask) Run(ctx context.Context) error {
	defer func() {
		if t.TaskTracker != nil {
			t.TaskTracker.UnregisterTask(t.CacheKey)
		}
		PutPreloadTask(t) // Return to pool after all fields done being used
	}()

	start := time.Now()
	var err error
	if t.HXTarget != "" {
		enc := t.Encoding
		if enc == "" {
			enc = "gzip"
		}
		err = MakeInternalRequestWithVariant(ctx, t.RequestConfig, t.Path, t.HXTarget, enc)
	} else {
		err = MakeInternalRequest(ctx, t.RequestConfig, t.Path)
	}
	duration := time.Since(start)

	if err != nil {
		if t.Metrics != nil {
			t.Metrics.RecordFailure(t.Path, err, duration)
		}
		slog.Warn("preload task failed", "path", t.Path, "error", err, "duration", duration)
		return err
	}

	if t.Metrics != nil {
		t.Metrics.RecordSuccess(t.Path, duration)
	}
	slog.Debug("preload task completed", "path", t.Path, "duration", duration)
	return nil
}

// Ensure PreloadTask implements scheduler.Task.
var _ scheduler.Task = (*PreloadTask)(nil)
