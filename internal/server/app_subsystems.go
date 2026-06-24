package server

import (
	"sync/atomic"

	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// appSubsystems groups all background processing subsystems into one
// sub-struct, reducing field sprawl on the App god-object.
// Embedded into App to promote its fields.
type appSubsystems struct {
	pool               *workerpool.Pool
	q                  *queue.Queue[string]
	qSendersActive     atomic.Int64
	fileProcessor      files.FileProcessor
	processingStats    *files.ProcessingStats
	scheduler          *scheduler.Scheduler
	preloadManager     *cachepreload.PreloadManager
	batchLoadManager   *cachebatch.Manager
	moduleStateService *modulestate.Service // DB-backed module state (discovery active, etc.)
}
