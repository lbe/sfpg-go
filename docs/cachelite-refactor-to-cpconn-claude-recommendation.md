# Cachelite Refactor: Recommendations

_Recommendations for the cachelite refactor goals, based on analysis of current codebase._

---

## Revised Requirements

### Original → Recommended

| Original Requirement                 | My Assessment       | Recommended Alternative                                             |
| ------------------------------------ | ------------------- | ------------------------------------------------------------------- |
| All writes through writebatcher      | Too absolute        | **Hot path only** (cache fills during request handling)             |
| cachelite instantiation only Get/Put | Good idea           | **Remove dead CacheStore interface**, expose reads at package level |
| Lean and mean, damn fast             | Already there       | **Don't over-optimize**, remove dead code                           |
| No fallbacks whatsoever              | Dangerous for tests | **Panic in prod, keep for tests**                                   |

---

## Requirement 1: "All writes through writebatcher" → **"Hot path through writebatcher"**

### Problem with absolutism

Writebatcher is designed for **high-volume, fire-and-forget operations** where:

- Latency can be traded for throughput
- Backpressure is acceptable (drop on full)
- Ordering is within-batch only

Some cache operations don't fit this model:

**Operations needing sync semantics:**

- `ClearCache` on ETag invalidation - must complete before next request
- `EvictLRU` when called directly from maintenance - caller needs return value

**Operations needing critical delivery:**

- Invalidation commands - dropping them is unacceptable

### Recommended: Hybrid approach

```
Request path → writebatcher (high volume, best-effort)
Maintenance   → direct writes (low volume, critical, needs results)
Invalidation  → direct writes OR writebatcher with SubmitAndWait
```

Benefits:

- Hot path optimized (where 99% of writes happen)
- Cold paths remain simple and correct
- No need to over-engineer writebatcher for sync operations

---

## Requirement 2: "cachelite only Get/Put" → **"Remove dead CacheStore interface"**

### Finding: SQLiteCacheStore is already read-only in practice

Current usage in app.go:

```go
app.cacheStore = cachelite.NewSQLiteCacheStore(app.dbRwPool)
```

But where is `cacheStore` actually used?

```go
// Only used for:
app.cacheStore.SizeBytes(app.ctx)  // read
```

The middleware doesn't use `cacheStore`:

- Reads use `GetCacheEntry()` directly (not via CacheStore)
- Writes use the submit callback path

**The `Store()` method on SQLiteCacheStore is DEAD CODE.** Nothing calls it.

### Recommended: Remove CacheStore interface entirely

The interface adds unnecessary complexity:

1. Interface allocation overhead
2. Virtual dispatch cost (tiny but real)
3. Dead methods that confuse readers

For "lean and mean," expose only read functions as package-level:

```go
// Keep these as package-level (already are):
func GetCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, key string) (*HTTPCacheEntry, error)
func GetCacheSizeBytes(ctx context.Context, db *dbconnpool.DbSQLConnPool) (int64, error)
func CountCacheEntries(ctx context.Context, db *dbconnpool.DbSQLConnPool) (int64, error)

// Remove:
// - CacheStore interface
// - SQLiteCacheStore struct
// - All write methods (they're dead code anyway)
```

The writes are already going through `StoreCacheEntryInTx()` for batcher. Package-level functions are already "lean and fast."

---

## Requirement 3: "Lean and mean, damn fast" → **"Don't over-optimize, remove dead code"**

### Current optimizations (already good)

The current code already has strong optimizations:

- `GetHTTPCacheEntry()` from pool (8KB pre-allocated)
- `PutHTTPCacheEntry()` with capacity capping at 16KB
- Atomic size counter (no DB queries on write path)

### Potential improvements (profile first)

**1. Connection reuse in reads**

```go
// Current: GetCacheEntry
func GetCacheEntry(...) (*HTTPCacheEntry, error) {
    cpc, err := db.Get()  // acquire
    defer db.Put(cpc)       // release
    // ...
}
```

If the same caller does multiple reads (e.g., preload), this thrashes the pool. But the pool is the abstraction—optimizing this at the call site may be premature.

**2. Batch reads for preload**

```go
func GetCacheEntriesByPrefix(ctx context.Context, db *dbconnpool.DbSQLConnPool, prefix string) ([]*HTTPCacheEntry, error) {
    // Single query instead of N queries
    // Useful for preload verification
}
```

But this adds query complexity and may not move the needle (cache fills are the bottleneck, not reads).

**3. Result pooling**
Already doing this with `HTTPCacheEntry`. Could also pool query result objects.

### Warning: Over-optimization risk

Adding layers (unified command types, sync submission, barriers) will make cachelite **slower**, not faster, due to:

- Additional allocations (command structs, result channels)
- Channel operations (one more select per command)
- Type assertions (command enum handling)

**"Lean and fast" often means "simple," not "everything abstracted."**

---

## Requirement 4: "No fallbacks whatsoever" → **"No fallback in production, keep for tests"**

### Current fallback is critical for tests

```go
// middleware.go line 317-331
if hcm.submitFunc != nil {
    go hcm.submitFunc(newEntry)  // batcher path
} else {
    // Synchronous path (usually for tests)
    if _, err := hcm.evictIfNeeded(r.Context(), bodySize); err != nil {
        slog.Warn("Cache eviction failed (sync)", "err", err)
    }
    if err := StoreCacheEntry(r.Context(), hcm.db, newEntry); err != nil {
        slog.Warn("Synchronous cache store failed", "key", cacheKey, "err", err)
    }
    // ...
}
```

Making tests require a fully-initialized writebatcher adds:

- Test setup complexity (need mock batcher or real DB)
- Test fragility (batcher timing issues)
- Slower test execution

### Recommended: Production guard, keep test path

```go
// In production build
func (hcm *HTTPCacheMiddleware) Middleware(next http.Handler) http.Handler {
    if buildMode == "production" && hcm.submitFunc == nil {
        panic("cache middleware: submitFunc is required in production")
    }
    // ...
}

// Tests use nil submitFunc for synchronous behavior
```

Or create a test-only variant:

```go
func NewHTTPCacheMiddlewareForTest(db *dbconnpool.DbSQLConnPool, cfg CacheConfig) *HTTPCacheMiddleware {
    // Always uses synchronous path, no batcher required
    // Simpler test setup
}
```

---

## What This Gives You

| Benefit          | How                                                                     |
| ---------------- | ----------------------------------------------------------------------- |
| **Simplicity**   | Don't force square operations into round holes                          |
| **Performance**  | Hot path optimized (batcher), cold paths remain simple                  |
| **Correctness**  | ETag invalidation completes synchronously                               |
| **Testability**  | Tests can use sync path without full batcher setup                      |
| **Future-proof** | If you later need Redis, add another package, don't make SQLite generic |

---

## The "Over-Engineering" Risk

Current (simple):

```
Middleware → submitFunc → batcher → StoreCacheEntryInTx → DB
Eviction → EvictLRU → DB (direct, simple)
```

Proposed (complex):

```
Middleware → CacheCommand → batcher → command processor → type switch → DB
Eviction → EvictLRUCommand → batcher → wait for result → DB
```

Each layer adds:

- **Complexity** (more code paths)
- **Debugging difficulty** (where did my command go?)
- **Performance overhead** (allocations, channels)

For "lean and mean," **removing dead code** is often better than **adding abstraction layers.**

---

## Concrete Next Steps

### Step 1: Remove dead code (immediate, low risk)

```bash
# Remove these from cachelite:
- CacheStore interface
- SQLiteCacheStore struct and methods
- StoreCacheEntry (direct write fallback)
- StoreCacheEntryBatch (use writebatcher for preload instead)
- DeleteCacheEntry, ClearCache, EvictLRU, CleanupExpired from interfaces

# Update app.go:
- Remove app.cacheStore field
- Use GetCacheSizeBytes() directly (or add cachelite.Size())
```

### Step 2: Harden batcher path for production

```go
// middleware.go
func NewHTTPCacheMiddleware(...) *HTTPCacheMiddleware {
    // Add production guard
    if isProduction && submit == nil {
        panic("cache middleware: submitFunc required in production")
    }
    // ...
}
```

### Step 3: Keep direct writes for critical operations

Explicitly document which operations use direct writes and why:

```go
// ClearCache clears all cache entries synchronously.
// Uses direct DB write because:
// 1. Called on ETag invalidation, which must complete before next request
// 2. Low volume, no benefit from batching
// 3. Caller needs confirmation of completion
func ClearCache(ctx context.Context, db *dbconnpool.DbSQLConnPool) error { ... }
```

### Step 4: Profile before optimizing

Add benchmarks for actual hot paths:

```go
func BenchmarkCacheWriteBatch(b *testing.B) {
    // Benchmark the actual: request → middleware → batcher → DB
}

func BenchmarkCacheRead(b *testing.B) {
    // Benchmark: request → middleware → GetCacheEntry → DB
}
```

Only add complexity if profiling shows it's needed.

---

## TL;DR Summary

| Area            | Recommendation                                                                                 |
| --------------- | ---------------------------------------------------------------------------------------------- |
| **Write path**  | Use batcher for hot path (cache fills), direct writes for cold path (maintenance/invalidation) |
| **Read path**   | Keep package-level functions, remove dead CacheStore interface                                 |
| **Performance** | Remove dead code first, profile before adding optimizations                                    |
| **Tests**       | Keep synchronous fallback, panic if missing in production                                      |
| **Overall**     | Pragmatic over pure: 90% of benefits, 10% of complexity                                        |

The refactor described in the notes is technically sound, but **the complexity cost may not be worth the purity gain**. A pragmatic hybrid approach gives you most benefits with minimal risk.
