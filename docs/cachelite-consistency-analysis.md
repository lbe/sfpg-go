# Cachelite, Pre-load, and Batch Load: Consistency Analysis & Recommendations

_Analysis of cache-related components for consistency and recommended path forward._

---

## Executive Summary

**The three components are NOT consistent.** There are multiple race conditions, cache key mismatches, and architectural tensions between them. The biggest issue is that **preloaded cache entries won't match real browser requests**, making preload ineffective for most users.

---

## Component Overview

### 1. Cachelite (cache storage)

- **Purpose**: SQLite-backed HTTP response cache
- **Read path**: Package-level functions (`GetCacheEntry`, `GetCacheSizeBytes`, `CountCacheEntries`)
- **Write paths**:
  - **Hot path (middleware)**: Uses unified writebatcher via `submitCacheWrite`
  - **Fallback (middleware)**: Uses `StoreCacheEntry` directly (only when submitFunc is nil)
  - **Batch/preload**: Goes through middleware, inherits write path
  - **Direct functions**: `StoreCacheEntry`, `StoreCacheEntryBatch`, `DeleteCacheEntry`, `ClearCache`, `EvictLRU`, `CleanupExpired`
- **Pool usage**: Uses `db.Get()/Put()` for both reads and writes

### 2. Cache Pre-load (cachepreload)

- **Purpose**: Proactively warm cache when user views a gallery
- **Trigger**: Cache HIT on gallery pages triggers `ScheduleFolderPreload()`
- **Process**:
  1. Query DB for folder's direct children (`GetPreloadRoutesByFolderID`)
  2. For each route, check if cached (`HttpCacheExistsByKey`)
  3. If not cached and not already pending, schedule preload task
  4. Preload task makes internal HTTP request through full handler chain
  5. Request goes through cache middleware → cache entry is stored
- **Pool usage**: Uses `dbRoPool.Get()/Put()` for deduplication checks
- **Scheduler**: Uses `scheduler.Scheduler` (4 workers × NumCPU)
- **Deduplication**: Tracks pending tasks to avoid duplicate preloads

### 3. Cache Batch Load (cachebatch)

- **Purpose**: Bulk load cache entries for all gallery routes
- **Trigger**: CLI command or API endpoint (`RunCacheBatchLoad`)
- **Process**:
  1. Get batch load targets from DB (`GetBatchLoadTargets`)
  2. For each target, check if cached (`HttpCacheExistsByKey`)
  3. If not cached, schedule job
  4. Job makes internal HTTP request with variant (HX headers)
  5. Request goes through cache middleware → cache entry is stored
- **Pool usage**: Uses `dbRoPool.Get()/Put()` for existence checks
- **Workers**: 8 workers (or NumCPU if smaller)
- **Concurrency**: Uses bounded channel (1000 jobs)

---

## Critical Issues Found

### Issue 1: Cache Key Mismatch (CRITICAL)

**Problem**: Preloaded cache entries don't match real browser requests.

#### Root Cause

**Real request cache key** (middleware.go line 186-201):

```go
encoding := NormalizeAcceptEncoding(r.Header.Get("Accept-Encoding"))
htmx := r.Header.Get("HX-Request")
hxTarget := r.Header.Get("HX-Target")
query := r.URL.RawQuery
theme := "dark"
if cookie, err := r.Cookie("theme"); err == nil && cookie.Value != "" {
    theme = cookie.Value
}
cacheKey := NewCacheKey(r.Method, r.URL.Path,
    query+"|HX="+htmx+"|HXTarget="+hxTarget+"|Theme="+theme,
    encoding)
```

\*\*Preload cache key (folder_preload_task.go line 119-123):

```go
cacheKey := GenerateCacheKey("GET", path, query, encoding)
// cache_key.go:
func GenerateCacheKey(method, path, query, encoding string) string {
    return fmt.Sprintf("%s:%s?%s|%s", method, path, query, encoding)
}
```

#### Impact

| Component                 | Cache Key Components |
| ------------------------- | -------------------- | --------- | ------------ | --------- | --------- |
| Real request              | `METHOD:/path?query  | HX=...    | HXTarget=... | Theme=... | encoding` |
| Preload (no variant)      | `METHOD:/path?query  | encoding` |
| Batch load (with variant) | `METHOD:/path?query  | HX=...    | HXTarget=... | encoding` |

**Preload entries WILL MISS for real browser requests** because:

- `HX=...` component missing
- `HXTarget=...` component missing
- `Theme=...` component missing

#### Why This Matters

Preload is triggered by cache HITS on gallery pages. The user:

1. Navigates to `/gallery/123`
2. Server responds from cache (HIT)
3. Preload schedules preload for `/gallery/124`
4. Preload loads and caches `/gallery/124` with **key WITHOUT HX/Theme**
5. User navigates to `/gallery/124`
6. Cache MISS (key mismatch) → user waits for full page generation
7. **Preload was wasted**

For gallery routes specifically, preload DOES use variant (line 37-39), but still misses theme.

### Issue 2: Deduplication Race Condition (HIGH)

**Problem**: "Check then schedule" pattern is not atomic.

#### Root Cause

Preload deduplication (folder_preload_task.go line 126-146):

```go
exists, err := queries.HttpCacheExistsByKey(ctx, cacheKey)
if err == nil && exists != 0 {
    return false  // Skip scheduling
}

if t.TaskTracker.IsTaskPending(cacheKey) {
    return false  // Skip scheduling
}

// Register task and schedule
t.TaskTracker.RegisterTask(cacheKey, ...)
_, err = t.Scheduler.AddTask(...)
```

**Race window**: Between checking cache existence and scheduling the task:

1. A real request could fill the cache (write to DB)
2. OR another preload process could register the task first
3. Result: Wasted preload work or redundant scheduling

#### Impact

Under high concurrency:

- Preload tasks are scheduled for entries that are already cached
- OR multiple preloads are scheduled for the same key
- Both waste CPU, memory, and DB connections

### Issue 3: Connection Pool Exhaustion Risk (MEDIUM)

**Problem**: Both preload and batch load use `dbRoPool`, but with different concurrency characteristics.

#### Current Usage

| Component               | Concurrency Model             | Pool                         | Connection Behavior |
| ----------------------- | ----------------------------- | ---------------------------- | ------------------- |
| Preload                 | 4 workers × NumCPU (e.g., 32) | dbRoPool, acquired per check |
| Batch load              | 8 workers                     | dbRoPool, acquired per job   |
| Normal requests         | app worker pool               | dbRoPool, acquired per read  |
| Cache middleware writes | Unified batcher               | dbRoPool, acquired per flush |

#### Impact

Under high load:

- Preload + Batch Load + Normal Requests can all compete for RO pool connections
- With 128+ workers trying to get connections, pool can be exhausted
- Preload and batch load jobs block on `db.Get()`, delaying cache warming

### Issue 4: Write Path Inconsistency (LOW)

**Problem**: Preload and batch load go through middleware, but write path depends on configuration.

#### Current Path

Both preload and batch load jobs make internal requests:

```go
// cachepreload/internal_request.go
rec := httptest.NewRecorder()
cfg.Handler.ServeHTTP(rec, req)  // Goes through full middleware chain
```

Middleware (http_cache_middleware.go line 317-332):

```go
if hcm.submitFunc != nil {
    go hcm.submitFunc(newEntry)  // Unified batcher
} else {
    // Fallback: direct write
    if err := StoreCacheEntry(r.Context(), hcm.db, newEntry); err != nil {
        slog.Warn("Synchronous cache store failed", "key", cacheKey, "err", err)
    }
}
```

#### Impact

If `submitFunc` is not configured (only for tests):

- Preload and batch load writes use direct `StoreCacheEntry`
- NOT unified batched → slower, but still works

In normal production:

- Both use unified batcher (correct)

### Issue 5: Low-Volume Write Paths Keep Direct Functions (LOW)

**Problem**: Forcing low-volume operations through writebatcher adds complexity without meaningful benefit.

#### Current Usage

Direct write functions are used for:

- `ClearCache` - ETag version invalidation (needs sync completion)
- `EvictLRU` - Cache maintenance (returns freed bytes)
- `DeleteCacheEntry` - Single entry delete (rare)
- `CleanupExpired` - Scheduled maintenance (rare)
- `StoreCacheEntry` - Middleware fallback (should not happen in production)

These operations differ from hot-path cache fills:

- **Low volume**: Seconds/minutes between operations
- **Different semantics**: Need synchronous completion or return values
- **Not latency-sensitive**: Invalidation happens once per ETag change

#### Impact Analysis

| Operation                 | Frequency            | Semantics                   | Should Use Batcher?         |
| ------------------------- | -------------------- | --------------------------- | --------------------------- |
| Cache fill (request-time) | Every request        | Fire-and-forget             | **YES** - high volume       |
| ClearCache                | Per ETag version     | Must complete before return | **NO** - sync required      |
| EvictLRU                  | When budget exceeded | Return freed bytes          | **NO** - needs return value |
| DeleteCacheEntry          | Rare                 | Immediate                   | **NO** - no benefit         |
| CleanupExpired            | Scheduled            | Return count                | **NO** - rare               |

**Conclusion**: Direct write functions are appropriate for their use cases. The middleware fallback to `StoreCacheEntry` is the real problem, not the direct functions themselves.

### Issue 6: Batch Load Backpressure Not Handled (MEDIUM)

**Problem**: If commits are slow (1-3 seconds) and queue fills, jobs keep getting scheduled, causing more contention.

#### Current Code

cachebatch/manager.go line 149-158:

```go
func (m *Manager) runJob(...) {
    err := cachepreload.MakeInternalRequestWithVariant(...)
    if err != nil {
        metrics.RecordFailed()
        slog.Debug("batch load request failed", "path", j.target.Path, "err", err)
        return
    }
    metrics.RecordCompleted()
}
```

Middleware write path (http_cache_middleware.go line 317-320):

```go
if hcm.submitFunc != nil {
    go hcm.submitFunc(newEntry)
    // submitCacheWrite:
    if err := adapter.SubmitCache(entry); err != nil {
        if errors.Is(err, writebatcher.ErrFull) {
            slog.Warn("unified batcher full, dropping cache write", "path", entry.Path, "pending", ba.wb.PendingCount())
        }
        cachelite.PutHTTPCacheEntry(entry)  // Return to pool
        return
    }
}
```

#### Impact

If batcher channel is full AND commits are slow:

- Queue keeps growing (more jobs waiting than flushing)
- Preload entries may wait in queue for seconds
- Batch load jobs block on full channel
- Cache is not warmed efficiently
- No indication to user or operator
- System thrashes: submitting to full queue, slow commits

**Root Cause**: No backpressure awareness at scheduling time.

---

## Architectural Inconsistencies

### A. Three Different Cache Key Generation Functions

```go
// cachelite/cache.go
func NewCacheKey(method, path, query, encoding string) string

// cachepreload/cache_key.go
func GenerateCacheKey(method, path, query, encoding string) string
func GenerateCacheKeyWithHX(method, path, query, hxTarget, encoding string) string
```

These should be ONE function with a consistent interface.

### B. Two Different Internal Request Functions

```go
// cachepreload/internal_request.go
func MakeInternalRequest(ctx context.Context, cfg InternalRequestConfig, path string) error
func MakeInternalRequestWithVariant(ctx context.Context, cfg InternalRequestConfig, path string, hxTarget string, encoding string) error
```

The variant handling should be unified.

### C. Duplicate Deduplication Logic

Both preload and batch load check if cache entry exists:

- Preload: `queries.HttpCacheExistsByKey(ctx, cacheKey)` (folder_preload_task.go line 127)
- Batch load: `queries.HttpCacheExistsByKey(ctx, cacheKey)` (cachebatch/manager.go line 118)

This logic is duplicated and suffers from race condition.

### D. Connection Pool Management Without Coordination

Three components all use `dbRoPool.Get()/Put()` independently:

- Preload workers
- Batch load workers
- Normal request handlers

No coordination or priority mechanism, leading to potential starvation under high load.

#### Action: Add Backpressure Monitoring and Throttling

Monitor batcher queue utilization and throttle job scheduling:

```go
// internal/server/cachebatch/manager.go

const (
    defaultQueueSize    = 1000
    highWatermark      = 800  // 80% - throttle zone
    mediumWatermark    = 600  // 60% - warning zone
    throttleDelay      = 50 * time.Millisecond
)

type Manager struct {
    // ... existing fields ...
    throttleThrottled bool
    throttleCounter   atomic.Int64
}

func (m *Manager) Run(ctx context.Context) error {
    // ... existing setup ...

    for _, t := range targets {
        // Check backpressure BEFORE scheduling each job
        pending := m.writeBatcher.PendingCount()
        utilization := float64(pending) / float64(defaultQueueSize)

        if utilization > 0.8 {
            // High backpressure: throttle
            if m.throttleCounter.Load()%10 == 0 {
                slog.Warn("cache batch load: throttling due to high backpressure",
                    "pending", pending,
                    "utilization", utilization)
            }
            m.throttleThrottled = true
            time.Sleep(throttleDelay)
        } else if m.throttleThrottled && utilization < 0.6 {
            // Recovering from throttle
            slog.Info("cache batch load: recovered from throttle",
                "pending", pending,
                "utilization", utilization)
            m.throttleThrottled = false
        }

        // Schedule job (or skip if severely overloaded)
        if utilization > 0.95 {
            m.metrics.RecordSkipped("backpressure")
            continue
        }

        // ... rest of job scheduling ...
    }
}
```

**Key Principle:**

- Slow down ADMISSION rate when backpressure is high
- Don't retry jobs that are already waiting (they're in the queue)
- Give the batcher time to catch up
- Log throttling events for tuning

#### Action: Monitor Throughput for Capacity Planning

Add throughput tracking to understand baseline and detect degradation:

```go
type Manager struct {
    // ... existing fields ...
    writesPerSecond   atomic.Float64
    lastTime         atomic.Int64
    lastWrites       atomic.Int64
}

func (m *Manager) Run(ctx context.Context) error {
    // ... existing setup ...

    // Background throughput monitor
    go func() {
        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        for range ticker.C {
            now := time.Now()
            writes := m.writesPerSecond.Load()

            // Estimate commits per second from batcher
            // Assuming ~100ms flush interval = 10 commits/sec
            commitsPerSec := writes / 10

            m.lastWrites.Store(writes)
            m.lastTime.Store(now)

            slog.Debug("Cache write throughput",
                "writes/sec", writes,
                "commits/sec", commitsPerSec)
        }
    }()

    // Schedule jobs with backpressure awareness
    for _, t := range targets {
        // ... existing logic with throttling ...
    }
}
```

---

## Recommended Path Forward

### Phase 1: Fix Cache Key Generation (CRITICAL - Do First)

**Goal**: Ensure preloaded cache entries match real browser requests.

#### Action 1: Create Unified Cache Key Generator

```go
// internal/cachelite/key.go (new file)
package cachelite

import (
    "fmt"
    "net/http"
)

type CacheKeyParams struct {
    Method     string
    Path       string
    Query      string
    HTMX       HTMXParams
    Theme      string
    Encoding   string
}

type HTMXParams struct {
    Request   string  // "true" or "false"
    Target    string  // e.g., "gallery-content", "box_info"
    IsVariant bool    // true when using variant (not full page)
}

// NewCacheKey generates a consistent cache key for all components.
// Used by middleware, preload, and batch load.
func NewCacheKey(params CacheKeyParams) string {
    parts := []string{params.Method, ":", params.Path, "?"}

    // Add query with cache-busting version
    if params.Query != "" {
        parts = append(parts, params.Query)
    }

    // Add HTMX parameters consistently
    parts = append(parts, "|HX=", params.HTMX.Request)
    if params.HTMX.Target != "" {
        parts = append(parts, "|HXTarget=", params.HTMX.Target)
    }
    if params.HTMX.IsVariant {
        parts = append(parts, "|IsVariant=true")
    }

    // Add theme (default to "dark" if not specified)
    theme := params.Theme
    if theme == "" {
        theme = "dark"
    }
    parts = append(parts, "|Theme=", theme)

    // Add encoding
    parts = append(parts, "|", params.Encoding)

    return fmt.Sprintf("%s", parts)
}

// NewCacheKeyForRequest builds CacheKeyParams from an http.Request.
// Used by middleware.
func NewCacheKeyForRequest(r *http.Request, theme string) CacheKeyParams {
    htmx := r.Header.Get("HX-Request")
    if htmx == "" {
        htmx = "false"
    }

    return CacheKeyParams{
        Method:   r.Method,
        Path:     r.URL.Path,
        Query:    r.URL.RawQuery,
        HTMX: HTMXParams{
            Request:   htmx,
            Target:    r.Header.Get("HX-Target"),
            IsVariant: htmx != "false",
        },
        Theme:    theme,
        Encoding: NormalizeAcceptEncoding(r.Header.Get("Accept-Encoding")),
    }
}

// NewCacheKeyForPreload builds CacheKeyParams for preload.
// Used by preload and batch load.
func NewCacheKeyForPreload(path, query, encoding string, theme string, useVariant bool) CacheKeyParams {
    params := CacheKeyParams{
        Method:   "GET",
        Path:     path,
        Query:    query,
        HTMX:     HTMXParams{},
        Theme:     theme,
        Encoding:  encoding,
    }

    if useVariant {
        variant := variantForPath(path)
        params.HTMX = HTMXParams{
            Request:   "true",
            Target:    variant.hxTarget,
            IsVariant: true,
        }
    }

    return params
}
```

#### Action 2: Update Middleware to Use New Key Generator

```go
// internal/cachelite/http_cache_middleware.go

func (hcm *HTTPCacheMiddleware) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // ... existing checks ...

        theme := "dark"
        if cookie, err := r.Cookie("theme"); err == nil && cookie.Value != "" {
            theme = cookie.Value
        }

        params := NewCacheKeyForRequest(r, theme)
        cacheKey := NewCacheKey(params)

        // ... rest of middleware ...
    })
}
```

#### Action 3: Update Preload to Use New Key Generator

```go
// internal/server/cachepreload/folder_preload_task.go

func (t *FolderPreloadTask) schedulePreload(...) bool {
    // Get theme from somewhere (context or default)
    // For now, use "dark" as default

    params := NewCacheKeyForPreload(path, query, encoding, "dark", useVariant)
    cacheKey := NewCacheKey(params)

    // ... rest of logic ...
}
```

#### Action 4: Update Batch Load to Use New Key Generator

```go
// internal/server/cachebatch/manager.go

func (m *Manager) Run(ctx context.Context) error {
    // ... existing code ...

    for _, t := range targets {
        useVariant := (t.HxTarget != "")  // Has variant
        params := NewCacheKeyForPreload(t.Path, queryStr, t.Encoding, "dark", useVariant)
        cacheKey := NewCacheKey(params)

        exists, err := queries.HttpCacheExistsByKey(ctx, cacheKey)
        // ... rest of logic ...
    }
}
```

---

### Phase 2: Fix Deduplication with Atomic Operations

**Goal**: Eliminate race condition between check and schedule.

#### Action: Add "Upsert" to Preload Task Tracking

Modify TaskTracker to support atomic "check and claim" operation:

```go
// internal/server/cachepreload/task_tracker.go

func (tt *TaskTracker) TryClaimTask(cacheKey string) bool {
    tt.mu.Lock()
    defer tt.mu.Unlock()

    // Check if already cached
    if tt.isCached[cacheKey] {
        return false
    }

    // Check if already pending
    if _, exists := tt.pending[cacheKey]; exists {
        return false
    }

    // Claim it
    taskID := fmt.Sprintf("claim-%s-%d", cacheKey, time.Now().UnixNano())
    tt.pending[cacheKey] = taskID
    return true
}

func (tt *TaskTracker) MarkCached(cacheKey string) {
    tt.mu.Lock()
    defer tt.mu.Unlock()
    tt.isCached[cacheKey] = true
}
```

Update preload to use atomic claim:

```go
// internal/server/cachepreload/folder_preload_task.go

func (t *FolderPreloadTask) schedulePreload(...) bool {
    // ... build cacheKey ...

    // Atomic check-and-claim
    if !t.TaskTracker.TryClaimTask(cacheKey) {
        if t.Metrics != nil {
            t.Metrics.RecordSkipped("already_claimed")
        }
        return false
    }

    // Schedule task (now safe from race)
    // ...
}
```

Update middleware to mark cache as soon as write is queued:

```go
// internal/server/cachelite/http_cache_middleware.go

if hcm.submitFunc != nil {
    go hcm.submitFunc(newEntry)
    // Mark as claimed so preload knows it's being handled
    // (Need to expose TaskTracker to middleware or use shared cache)
} else {
    // Direct path: mark as cached immediately
    if err := StoreCacheEntry(r.Context(), hcm.db, newEntry); err == nil {
        // Success: mark as cached
        // (Requires cache tracker access)
    }
}
```

---

### Phase 3: Keep Direct Write Functions For Low-Volume Operations

**Goal**: Use direct write functions where appropriate (low volume, different semantics).

#### Analysis

Direct write functions serve different needs than hot-path cache fills:

- `ClearCache` - ETag invalidation, needs **synchronous completion**
- `EvictLRU` - Maintenance, needs **return value** (freed bytes)
- `DeleteCacheEntry` - Single delete, rare
- `CleanupExpired` - Scheduled maintenance, rare
- `StoreCacheEntry` - Middleware fallback (shouldn't happen in production)

These operations are fundamentally different from request-time cache fills:

- **Frequency**: Seconds/minutes between operations vs. every request
- **Semantics**: Need sync completion or return values vs. fire-and-forget
- **Volume**: 1-10 operations per ETag change vs. thousands per second
- **Latency sensitivity**: Invalidation blocks until complete vs. can queue and respond

#### Action 1: Keep Direct Write Functions

Keep these functions in `cachelite` package for their appropriate use cases:

```go
// ClearCache clears all cache entries synchronously.
// Used by: ETag version invalidation, manual cache clear.
// Uses direct DB write because:
//   - Called from admin/CLI where synchronous completion is required
//   - Low volume, no benefit from batching
//   - Caller needs confirmation of completion before proceeding
func ClearCache(ctx context.Context, db *dbconnpool.DbSQLConnPool) error { ... }

// EvictLRU removes oldest cache entries to free targetBytes.
// Used by: Maintenance when cache budget exceeded.
// Returns: Actual number of bytes freed.
// Uses direct DB write because:
//   - Low volume, only when budget exceeded
//   - Needs return value for size counter update
func EvictLRU(ctx context.Context, db *dbconnpool.DbSQLConnPool, targetFreeBytes int64) (int64, error) { ... }

// DeleteCacheEntry removes a single cache entry by key.
// Used by: Admin commands, selective invalidation (rare).
// Uses direct DB write because:
//   - Low volume, no benefit from batching
//   - Immediate feedback required
func DeleteCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, key string) error { ... }

// CleanupExpired removes expired cache entries from database.
// Used by: Scheduled maintenance (rare).
// Uses direct DB write because:
//   - Low volume, periodic only
//   - Return value for metrics
func CleanupExpired(ctx context.Context, db *dbconnpool.DbSQLConnPool) (int64, error) { ... }

// StoreCacheEntryBatch may be deprecated (see Issue 5).
// Used by: Benchmarks and tests only.
// NOT used in production.
func StoreCacheEntryBatch(ctx context.Context, db *dbconnpool.DbSQLConnPool, entries []*HTTPCacheEntry) error { ... }
```

Add clear documentation comments to each function explaining when it's appropriate to use.

#### Action 2: Remove Middleware Fallback Path

The middleware fallback to `StoreCacheEntry` (lines 320-332 in http_cache_middleware.go) should panic in production:

```go
// internal/cachelite/http_cache_middleware.go

func NewHTTPCacheMiddleware(
    db *dbconnpool.DbSQLConnPool,
    cfg CacheConfig,
    sizeCounter *atomic.Int64,
    submit func(*HTTPCacheEntry),
) *HTTPCacheMiddleware {

    // In production, submitFunc must be provided
    if isProduction() && submit == nil {
        panic("HTTPCacheMiddleware: submitFunc is required in production - use batcher")
    }

    return &HTTPCacheMiddleware{
        db:          db,
        config:      cfg,
        sizeCounter: sizeCounter,
        submitFunc:  submit,
    }
}

func isProduction() bool {
    // Check if running in production mode
    // Can be set via build tag or environment variable
    return true  // For now, always enforce
}
```

Remove the `else` branch that uses `StoreCacheEntry` directly. The fallback should only exist for tests.

#### Action 3: Direct Write Functions Remain Appropriate

Direct writes remain available for:

- ETag invalidation via `app.invalidateHTTPCache()`
- Manual cache clear commands (future feature)
- Cache maintenance operations

Hot path (request-time cache fills) uses writebatcher exclusively.

---

### Phase 4: Add Backpressure Throttling to Batch Load

**Goal**: Prevent silent failures when batcher is full.

#### Action: Implement Retry with Backoff

```go
// internal/server/cachebatch/manager.go

const (
    maxRetries = 3
    retryDelay = 50 * time.Millisecond
)

func (m *Manager) runJob(ctx context.Context, j job, ...) {
    var err error
    for attempt := 0; attempt < maxRetries; attempt++ {
        err = cachepreload.MakeInternalRequestWithVariant(...)
        if err == nil {
            break
        }

        // Check if error is batcher full
        // (Need to expose or detect this error type)
        if isBatcherFullError(err) {
            if attempt < maxRetries-1 {
                time.Sleep(retryDelay * time.Duration(attempt+1))
                continue
            }
            break
        }

        // Other errors: don't retry
        break
    }

    if err != nil {
        metrics.RecordFailed()
        return
    }

    metrics.RecordCompleted()
}
```

---

### Phase 5: Connection Pool Coordination (OPTIONAL - Consider Later)

**Goal**: Prevent pool exhaustion under high concurrent preloading.

#### Action: Add Connection Limiter

```go
// internal/server/cachepreload/manager.go

type PreloadManager struct {
    // ... existing fields ...
    connLimiter chan struct{}  // Bounded semaphore
}

func NewPreloadManager(cacheableRoutes []string, initialEnabled bool) *PreloadManager {
    pm := &PreloadManager{
        // ... existing init ...
        connLimiter: make(chan struct{}, 10),  // Max 10 concurrent DB checks
    }
    // ... rest of init ...
}

func (pm *PreloadManager) ScheduleFolderPreload(...) {
    // ... existing checks ...

    fpt := &FolderPreloadTask{
        // ... existing fields ...
        ConnLimiter: pm.connLimiter,  // Pass limiter to task
        // ... other fields ...
    }
    // ... rest of method ...
}
```

Update task to use limiter:

```go
// internal/server/cachepreload/folder_preload_task.go

func (t *FolderPreloadTask) Run(ctx context.Context) error {
    // Acquire connection semaphore
    if t.ConnLimiter != nil {
        <-t.ConnLimiter
        defer func() { t.ConnLimiter <- struct{}{} }()
    }

    cpc, err := t.DBRoPool.Get()
    if err != nil {
        return fmt.Errorf("get db connection: %w", err)
    }
    defer t.DBRoPool.Put(cpc)

    // ... rest of logic ...
}
```

Apply same limiter to batch load manager.

---

## Migration Priority

### Critical (Do First)

1. **Fix cache key generation** - Phase 1
   - Blocker for preload effectiveness
   - High impact, low complexity

### High

2. **Fix deduplication race condition** - Phase 2
   - Prevents wasted preloads
   - Medium complexity

### Medium

3. **Keep direct write functions for low-volume ops** - Phase 3
   - Direct writes remain for maintenance/invalidation
   - Remove middleware fallback path only
   - Enforces hot-path batcher usage

### Low

4. **Add backpressure throttling** - Phase 4
   - Monitors queue utilization and slows job scheduling
   - Optional if current sizing is adequate

### Defer

5. **Connection pool coordination** - Phase 5
   - Only needed under extreme load
   - Adds complexity, measure first

---

## Testing Recommendations

### Cache Key Consistency Tests

```go
// internal/cachelite/key_test.go (new file)

func TestCacheKey_Consistency(t *testing.T) {
    middlewareKey := NewCacheKey(CacheKeyParams{
        Method: "GET",
        Path: "/gallery/123",
        Query: "v=1",
        HTMX: HTMXParams{
            Request:   "true",
            Target:    "gallery-content",
            IsVariant: true,
        },
        Theme: "dark",
        Encoding: "gzip",
    })

    preloadKey := NewCacheKeyForPreload("/gallery/123", "v=1", "gzip", "dark", true)

    if middlewareKey != NewCacheKey(preloadKey) {
        t.Errorf("Cache keys don't match:\nMiddleware: %s\nPreload: %s", middlewareKey, preloadKey)
    }
}
```

### Deduplication Race Tests

```go
// internal/server/cachepreload/task_tracker_test.go

func TestTaskTracker_ConcurrentClaim(t *testing.T) {
    tt := NewTaskTracker()
    cacheKey := "test-key"

    var wg sync.WaitGroup
    successes := atomic.Int64{}

    // Spawn 10 concurrent claims
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if tt.TryClaimTask(cacheKey) {
                successes.Add(1)
            }
        }()
    }

    wg.Wait()

    // Only one should succeed
    if successes.Load() != 1 {
        t.Errorf("Expected 1 successful claim, got %d", successes.Load())
    }
}
```

### End-to-End Preload Tests

```go
// internal/server/cachepreload/preload_e2e_test.go (new file)

func TestPreload_FullIntegration(t *testing.T) {
    // Setup app with cache middleware
    app := setupTestApp(t)
    defer app.Shutdown()

    // Make a real request to cache an entry
    req := makeTestRequest(t, "GET", "/gallery/1")
    rec := httptest.NewRecorder()
    app.router.ServeHTTP(rec, req)
    assertCacheHit(t, rec)  // Verify first request is MISS, entry created

    // Trigger preload (simulating cache hit on gallery 1)
    // Wait for preload to complete
    // Verify preloaded entry matches real request cache key
}
```

---

## Summary

| Issue                      | Severity | Priority | Fix Complexity |
| -------------------------- | -------- | -------- | -------------- |
| Cache key mismatch         | CRITICAL | P1       | Low            |
| Deduplication race         | HIGH     | P2       | Medium         |
| Middleware fallback path   | MEDIUM   | P3       | Low            |
| Backpressure/throttling    | MEDIUM   | P4       | Medium         |
| Connection pool exhaustion | LOW      | P5       | Medium         |

**Recommended start order:**

1. Phase 1 (cache key) → Fix immediate blocker
2. Phase 2 (deduplication) → Prevent wasted work
3. Phase 3 (middleware fallback) → Keep direct writes for low-volume, remove fallback
4. Phase 4 (backpressure throttling) → Monitor and throttle job scheduling
5. Phase 5 (connection pool) → Only if needed after profiling

---

## Post-Migration State

### Cachelite

- Only read functions at package level
- `StoreCacheEntryInTx` for hot-path batcher use
- Direct write functions kept for low-volume operations (`ClearCache`, `EvictLRU`, etc.)
- Middleware fallback path removed (hot path only uses batcher)
- Direct writes used where appropriate (invalidation, maintenance, return values needed)

### Pre-load

- Uses unified cache key generator
- Atomic task claiming (no races)
- Connection-limited to prevent exhaustion
- Preloaded entries match real browser requests

### Batch Load

- Uses unified cache key generator
- Backpressure-throttled job scheduling (monitors queue, slows down admission)
- Connection-limited to prevent exhaustion (deferred improvement)
- Clear error reporting when batcher full
- Throughput monitoring for capacity planning

### All Components

- Consistent cache keys
- Hot path uses writebatcher, cold paths use direct writes
- No race conditions
- Backpressure-throttled job scheduling
