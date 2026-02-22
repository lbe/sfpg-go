package cachepreload

import "github.com/lbe/sfpg-go/internal/gensyncpool"

// preloadTaskPool reuses PreloadTask instances when scheduling preloads to reduce allocations.
var preloadTaskPool = gensyncpool.New(
	func() *PreloadTask {
		return &PreloadTask{}
	},
	func(t *PreloadTask) {
		// Reset all fields to zero values
		t.CacheKey = ""
		t.Path = ""
		t.HXTarget = ""
		t.Encoding = ""
		t.TaskTracker = nil
		t.RequestConfig = InternalRequestConfig{} // zero value
		t.Metrics = nil
	},
)

// GetPreloadTask retrieves a PreloadTask from the pool.
func GetPreloadTask() *PreloadTask {
	return preloadTaskPool.Get()
}

// PutPreloadTask returns a PreloadTask to the pool.
func PutPreloadTask(task *PreloadTask) {
	if task != nil {
		preloadTaskPool.Put(task)
	}
}
