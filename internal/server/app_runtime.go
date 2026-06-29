package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/lbe/sfpg-go/internal/server/metrics"
)

// appRuntimeState groups runtime lifecycle and operational state into one
// sub-struct, reducing field sprawl on the App god-object.
// Embedded into App to promote its fields.
type appRuntimeState struct {
	cancel context.CancelFunc
	ctx    context.Context // INVARIANT: set exactly once in New(); never reassigned after Run() starts
	wg     sync.WaitGroup

	stopProfiler     func()
	metricsCollector *metrics.Collector // Centralized metrics collector for dashboard
	version          string             // Application version for display in UI and logs

	restartRequired bool          // Flag indicating if restart is needed
	restartMu       sync.RWMutex  // protects httpServer and restartCh from concurrent access (ORDER: 3)
	httpServer      *http.Server  // HTTP server instance for restart support
	restartCh       chan struct{} // Channel to signal server restart

	poolDone chan struct{} // closed when StartWorkerPool goroutine exits; nil if pool never started

	// Gallery stats cache for about modal (invalidated when discovery runs)
	galleryStatsMu    sync.RWMutex
	galleryStatsCache *GalleryStats
	galleryStatsAt    int64 // LastStartedAt when cache was computed; 0 = invalid

	staleCacheDropInFlight atomic.Bool
}
