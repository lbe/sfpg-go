# Cachelite Refactor: Restricted Pool Interfaces and CacheliteStore

Design notes for refactoring cachelite to use restricted connection pool interfaces, preventing misuse and aligning with app patterns. Implement **after** the symptom fix (Get/Put instead of db.DB()) is committed.

## Background

### Root Cause of the Panic

- Cachelite functions called `db.DB()` directly, bypassing the pool and using `database/sql`'s connection logic.
- With `MaxOpenConns` default (0 = unlimited), cache preload created unbounded connections.
- Each ncruces connection instantiates SQLite WASM; under memory pressure, `unix.Mprotect` in the allocator fails and returns `nil`, causing wazero to panic: `slice bounds out of range [:327680] with capacity 0`.

### Design Flaw

The `ConnectionPool` interface exposes `DB() *sql.DB`. That method allows callers to bypass the pool. Cachelite received the full pool and used `db.DB()` because it was the path of least resistance—`gallerydb.New()` accepts `DBTX` (`*sql.DB` or `*sql.Tx`). The design invited misuse.

## Two-Step Approach

1. **Step 1 (already done)**: Fix the symptom—refactor all cache functions to use `db.Get()` / `db.Put()` instead of `db.DB()`. No interface changes. Minimal risk. Commit.

2. **Step 2 (this refactor)**: Introduce restricted pool interfaces and rename the store. Implement on a new branch.

## Restricted Pool Interfaces

Define narrow interfaces that expose only `Get` and `Put`—no `DB()`, so misuse is impossible:

```go
// CacheReadPool is the minimal interface for read-only cache operations.
// Does not expose DB() so callers cannot bypass the pool.
type CacheReadPool interface {
    Get() (*dbconnpool.CpConn, error)
    Put(cpc *dbconnpool.CpConn)
}

// CacheWritePool is the minimal interface for write cache operations.
type CacheWritePool interface {
    Get() (*dbconnpool.CpConn, error)
    Put(cpc *dbconnpool.CpConn)
}
```

These interfaces live in cachelite. `DbSQLConnPool` implements both (it has Get/Put). The full `ConnectionPool` with `DB()` stays in dbconnpool for migrations and other legitimate uses; cachelite never receives it.

## Read vs Write Operations

**Read-only** (use RO pool):

- `GetCacheEntry`
- `GetCacheSizeBytes`
- `CountCacheEntries`

**Writes** (use RW pool):

- `StoreCacheEntry`
- `StoreCacheEntryBatch`
- `DeleteCacheEntry`
- `ClearCache`
- `EvictLRU` (read + delete; use RW for the deletes)
- `CleanupExpired`

RO/RW separation matches the app architecture: `config.Service`, `files.NewFileProcessor`, handlers all take `(dbRoPool, dbRwPool)` as separate parameters.

## CacheliteStore (Rename)

- Rename `SQLiteCacheStore` to **CacheliteStore**.
- "Cachelite" = SQLite cache (the "lite" refers to SQLite). A generic HTTP cache would not differentiate RO/RW; that is an implementation detail of the SQLite backend.
- Constructor signature: `NewCacheliteStore(readPool CacheReadPool, writePool CacheWritePool) CacheStore`.

## HTTPCacheMiddleware

- Update to accept read and write pools instead of a single `*DbSQLConnPool`.
- Its internal cache lookups use the read pool; cache eviction, submit path, and maintenance use the write pool as appropriate.

## Call Site Changes

Current:

```go
app.cacheStore = cachelite.NewSQLiteCacheStore(app.dbRwPool)
app.cacheMW = cachelite.NewHTTPCacheMiddleware(app.dbRwPool, cfg, ...)
```

Refactored:

```go
app.cacheStore = cachelite.NewCacheliteStore(app.dbRoPool, app.dbRwPool)
app.cacheMW = cachelite.NewHTTPCacheMiddleware(app.dbRoPool, app.dbRwPool, cfg, ...)
```

On pool reconfigure, `UpdatePool` would receive both pools.

## Implementation Checklist

- [ ] Define `CacheReadPool` and `CacheWritePool` in cachelite (or a shared place cachelite imports).
- [ ] Rename `SQLiteCacheStore` → `CacheliteStore`; change constructor to `NewCacheliteStore(read, write)`.
- [ ] Refactor internal cache functions to accept the appropriate pool interface; use `read.Get()`/Put for reads, `write.Get()`/Put for writes.
- [ ] Update `HTTPCacheMiddleware` to take `(readPool, writePool)`; adjust `UpdatePool` signature.
- [ ] Update app wiring, config, and tests.
- [ ] Ensure no cachelite code ever receives or calls `DB()`.

## Investigation Findings (March 2026)

### What is currently writebatcher-backed

- The primary HTTP middleware cache-insert path is writebatcher-backed when a submit callback is provided.
- In that path, cache writes are persisted via `StoreCacheEntryInTx` during unified batch flush.

### What still performs direct writes

- Direct write paths still exist and are active:
  - `StoreCacheEntry` (used as middleware fallback when submit callback is nil)
  - `StoreCacheEntryBatch`
  - `DeleteCacheEntry`
  - `ClearCache`
  - `EvictLRU`
  - `CleanupExpired`
- Runtime call sites currently invoke direct writes for invalidation and maintenance (`ClearCache`, `CleanupExpired`, `EvictLRU`).

### Is strict writebatcher-only technically possible?

- Yes. There is no hard technical blocker that prevents a writebatcher-only design.
- However, this is not a search-and-replace change. Several API/semantic gaps must be closed first.

## Design Constraints To Solve For Writebatcher-Only

1. Submission failures are first-class (`ErrFull`, `ErrClosed`).

- The current batcher submit API is non-blocking and can reject items.
- For best-effort cache fill this can be acceptable, but invalidation and maintenance paths need deterministic guarantees.

2. Some operations need synchronous completion semantics.

- `ClearCache` on ETag invalidation should be complete before returning in some flows.
- An async enqueue-only API introduces a temporary stale window unless a barrier or flush-and-wait mechanism exists.

3. Some cache APIs return values.

- `EvictLRU` returns freed bytes.
- `CleanupExpired` returns a delete signal/count.
- Current submit API has no response channel/future for callers that need results.

4. Ordering rules must be explicit.

- In mixed batches, command ordering (for example Clear before/after Upsert) must be deterministic.
- Without explicit rules, subtle races are possible.

## Recommended Refactor Direction

### 1) Expand unified batch operation model

Add explicit cache operation variants, rather than only cache upsert entries:

- `CacheUpsert`
- `CacheDeleteByKey`
- `CacheClear`
- `CacheEvictLRU`
- `CacheCleanupExpired`

Represent these as typed command payloads in the unified batched write type.

### 2) Introduce two submission modes

- Async best-effort mode for request-path cache fills.
- Sync barrier mode for operations that must complete before returning (clear/invalidate, selected maintenance operations).

Implementation options:

- Add a `SubmitAndWait` API on the adapter layer (internally use command response channels).
- Or add command structs with `Result chan` fields and wait at call sites that require completion.

### 3) Define ordering and conflict policy

At minimum, define and enforce:

- `CacheClear` ordering relative to upserts.
- Whether maintenance operations can run in the same batch as request-path upserts.
- Whether some command classes should flush in dedicated batches.

### 4) Remove middleware direct-write fallback

After test and wiring migration, eliminate nil-submit fallback paths in middleware for production behavior consistency.

### 5) Keep direct SQL only if explicitly intentional

If any direct write path is retained, mark it as intentionally out-of-band (for example bootstrap/tooling-only), document why, and test it separately.

## Suggested Migration Sequence

1. Implement restricted RO/RW pool interfaces and `CacheliteStore` rename.
2. Convert middleware and app wiring to always provide batcher submit in production bootstrap paths.
3. Add explicit cache command variants and response semantics for sync-required commands.
4. Route `ClearCache`, `DeleteCacheEntry`, `EvictLRU`, and `CleanupExpired` through the command path.
5. Remove middleware fallback and update tests to construct a batcher-backed middleware harness.
6. Validate with:
   - full test suite,
   - cache middleware integration tests,
   - ETag invalidation tests,
   - preload and cleanup behavior checks.

## Acceptance Criteria For This Refactor

- No cachelite production write path executes direct SQL outside the unified writebatcher flow.
- Cache invalidation paths that require completion have deterministic synchronous semantics.
- Backpressure behavior is explicit and tested for each write class (drop/retry/block).
- Ordering guarantees for clear/upsert/maintenance are documented and covered by tests.
