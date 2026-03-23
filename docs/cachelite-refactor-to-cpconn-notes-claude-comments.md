# Cachelite Refactor: Analysis and Recommendations

_Analysis of the cachelite refactor to writebatcher-only design, based on docs/cachelite-refactor-to-cpconn-notes.md_

---

## Current State Summary

**What's already writebatcher-backed:**

- Primary HTTP middleware cache fill path (via `submitCacheWrite` → unified batcher → `flushBatchedWrites` → `StoreCacheEntryInTx`)
- Cache size tracking via atomic counter
- Cache eviction check runs AFTER successful flush in `OnSuccess` callback

**What still uses direct writes:**

- `StoreCacheEntry` (middleware fallback when submit is nil)
- `StoreCacheEntryBatch` (preload path)
- `DeleteCacheEntry`, `ClearCache`, `EvictLRU`, `CleanupExpired` (maintenance/invalidation)
- `invalidateHTTPCache()` in app.go (ETag version change)
- `maybeEvictCacheEntries()` in `flushBatchedWrites`

---

## Is It Doable? **Yes, with caveats**

The refactor is technically possible, but **writebatcher needs significant enhancement** to support the use cases. This is not a simple "route through the batcher" change.

---

## Key Gaps in Current WriteBatcher

### 1. **No Result/Return Values**

Current `Submit()` is fire-and-forget:

```go
func (wb *WriteBatcher[T]) Submit(item T) error
```

Problem: Some cache operations need results:

- `EvictLRU` → returns freed bytes (needed for size counter update)
- `CleanupExpired` → returns count/signal
- `ClearCache` → may need confirmation for ETag invalidation flows

**Implication**: You can't blindly pipe everything through async batcher when callers need the result.

### 2. **No Synchronous/blocking Submit**

Current `Submit()` is non-blocking (select on channel):

```go
select {
case wb.ch <- item:
    return nil
default:
    return ErrFull
}
```

Problem: Operations like `ClearCache` on ETag invalidation need deterministic completion:

```go
// Current: invalidateHTTPCache
func (app *App) invalidateHTTPCache() {
    if err := cachelite.ClearCache(ctx, app.dbRwPool); err != nil {
        // error handling
    }
    // Function returns BEFORE clear completes if batched!
    // ETag invalidation logic expects cache to be actually cleared
}
```

**Implication**: Either add `SubmitAndWait` or change ETag invalidation to be async with barriers.

### 3. **Single Command Type**

Current writebatcher is monomorphic:

```go
type WriteBatcher[T any] struct {
    ch chan T  // only one type allowed
}
```

Problem: You need different cache operations:

- `CacheUpsert` (current)
- `CacheDeleteByKey`
- `CacheClear`
- `CacheEvictLRU`
- `CacheCleanupExpired`

**Implication**: Either use `interface{}` and type-switch, or create separate batchers per operation type (defeats the "unified" goal).

### 4. **Eviction Timing**

Current eviction logic is clever:

```go
OnSuccess: func(batch []BatchedWrite) {
    app.maybeEvictCacheEntries(batch)  // runs AFTER commit
}
```

Problem: This reads cache size with `GetCacheSizeBytes()` (direct DB read) then calls `EvictLRU()` (direct DB write). Both are direct operations outside the batcher.

**Implication**: Eviction itself needs to be a batched command to satisfy "no direct writes".

### 5. **Cache Size Synchronization**

The atomic counter approach works for writes:

```go
app.cacheSizeBytes.Add(entry.ContentLength.Int64)
```

But eviction reads from DB:

```go
currentSize, err := cachelite.GetCacheSizeBytes(ctx, app.dbRwPool)
```

If everything goes through batcher, the counter may drift from reality (race condition between in-memory counter and batched writes).

---

## What You're Not Thinking About

### 1. **Cache Invalidation Semantics During ETag Version Increment**

When ETag version increments:

```go
func (app *App) invalidateHTTPCache() {
    cachelite.ClearCache(ctx, app.dbRwPool)
}
```

If this becomes batched async:

- The function returns before cache is actually cleared
- Subsequent requests might see stale cached responses
- You'd need to either:
  - Add a `FlushAndWait()` call
  - Change the invalidation flow to be "eventually consistent"
  - Add a barrier that waits for specific batch ID to flush

### 2. **Test Coverage Regression**

The notes mention: "Remove middleware fallback and update tests to construct a batcher-backed middleware harness."

Many tests explicitly use `nil` submit function to test synchronous behavior. These tests would break or need significant rewriting.

### 3. **Error Backpressure Propagation**

Current writebatcher:

```go
if err := wb.Submit(item); err != nil {
    // ErrFull or ErrClosed
    slog.Warn("batcher full, dropping")
}
```

For cache fills (best-effort), dropping is fine. For invalidation operations, dropping is catastrophic:

- `ClearCache` fails silently → cache never invalidates
- `EvictLRU` fails silently → cache grows unbounded

You'd need separate handling:

```go
// Pseudocode
func SubmitBestEffort(item) error {
    return wb.Submit(item)  // drop on full, log warning
}

func SubmitCritical(item) error {
    for i := 0; i < maxRetries; i++ {
        if err := wb.Submit(item); errors.Is(err, ErrFull) {
            time.Sleep(backoff(i))
            continue
        }
        return err
    }
    return fmt.Errorf("failed after %d retries", maxRetries)
}
```

### 4. **Race Conditions in Size Tracking**

Current approach: atomic counter for writes, direct DB read for eviction.

If all writes go through batcher:

- Counter increments on submit (optimistic)
- Batch flush may fail
- Counter would be wrong (too high)

Fix: Update counter in `OnSuccess` only (as you already do for some paths), not at submit time.

### 5. **Connection Pool Exhaustion During Eviction**

Current `EvictLRU` does:

```go
func EvictLRU(ctx context.Context, db *dbconnpool.DbSQLConnPool, targetFreeBytes int64) (int64, error) {
    cpc, err := db.Get()  // takes connection
    defer db.Put(cpc)       // returns it
    // ... delete loop ...
}
```

If called from `OnSuccess` callback (which is already using the batcher's connection for the flush):

- You're holding a RW connection in the batcher
- You get another RW connection for eviction
- Pool may exhaust if many concurrent batches with large evictions

The notes suggest using RW pool for EvictLRU, but this could deadlock under high load.

---

## Writebatcher Modifications Needed

To enable this refactor, writebatcher would need:

```go
// 1. Command types (not just values)
type CacheCommand struct {
    Type      CommandType
    Upsert    *HTTPCacheEntry
    DeleteKey  string
    Clear      struct{}
    EvictLRU   struct {
        TargetBytes int64
        Result     chan int64  // for return value
    }
    CleanupExpired struct {
        Result chan int64
    }
}

// 2. Sync submission
func (wb *WriteBatcher[T]) SubmitAndWait(item T, timeout time.Duration) (result any, err error) {
    // Submit with a result channel, wait for it
}

// 3. Prioritized submission (optional)
func (wb *WriteBatcher[T]) SubmitCritical(item T) error {
    // Separate channel or priority queue
}

// 4. Batch ID/sequence (optional, for barriers)
func (wb *WriteBatcher[T]) FlushAndWait() (BatchID, error) {
    // Returns when current batch is committed
}
```

---

## Recommended Approach

Given the complexity, I'd recommend a **phased approach** rather than big-bang:

### Phase 1: Hardening the Batch Path

- Make the fallback path panic in production (only allow in tests)
- Add `SubmitAndWait` to writebatcher for sync operations
- Introduce `CacheCommand` type

### Phase 2: Convert Invalidation Paths

- Convert `invalidateHTTPCache()` to use batcher with `SubmitAndWait`
- Convert `CleanupExpired` to use batcher command
- Keep direct `EvictLRU` for now (see Phase 3)

### Phase 3: Unified Cache Commands

- Add all cache commands to `CacheCommand` enum
- Convert `EvictLRU` to batched command with result channel
- Remove direct write functions

### Phase 4: Cleanup

- Remove `SQLiteCacheStore` or make it read-only
- Update restricted pool interfaces
- Remove fallback paths

---

## Risk Assessment

| Risk                    | Severity   | Mitigation                                                                 |
| ----------------------- | ---------- | -------------------------------------------------------------------------- |
| ETag invalidation races | High       | Add `FlushAndWait` for critical clears                                     |
| Size counter drift      | Medium     | Update counter only in `OnSuccess`                                         |
| Test breakage           | Medium     | Incremental migration, keep test doubles                                   |
| Pool exhaustion         | Low-Medium | Tune batch sizes, use separate connection for eviction in same transaction |

---

**Bottom line**: The refactor is **doable** but requires significant writebatcher enhancements and careful handling of synchronous vs asynchronous semantics. The notes document is correct about the API gaps—this is not a trivial change.
