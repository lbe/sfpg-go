# Plan: Address Code Review Issues from cachelite-refactor-to-cpconn

## Status: EXECUTED

Steps 1-6 have been implemented in commits `ba42d6b..8e2b0e1`.
See validation report at the end of this document.

## Context

Code review of commits `bca6be3..479ca7e` identified 9 issues ranging from dead code to duplicated logic to flaky test synchronization. This plan addresses all of them in dependency order.

**Branch**: `cachelite-refactor-to-cpconn` (continue from `479ca7e`)

---

## Step 1: Export `variantForPath` from `cachelite` and remove the duplicate

**Issue**: #1 (Medium) — `variantForPath` duplicated in `cachelite/key.go` and `cachepreload/folder_preload_task.go`. Will diverge.

**Actions**:

1. In `internal/cachelite/key.go`: rename `variantForPath` → `VariantForPath` (export it)
2. In `internal/server/cachepreload/folder_preload_task.go`: delete the local `variantForPath` function, replace the call site with `cachelite.VariantForPath`
3. Update any tests that reference the unexported name (if any exist as internal tests)
4. Run tests: `go test -tags "integration e2e" ./internal/cachelite/... ./internal/server/cachepreload/... > ./tmp/test_output.txt 2>&1`
5. `make format && make lint`
6. Commit: `refactor(cachelite): export VariantForPath, remove duplicate from cachepreload`

---

## Step 2: Remove dead `isCached`/`MarkCached` from `TaskTracker`

**Issue**: #2 (Medium) — `isCached` map and `MarkCached` are dead code. Production initializes `TaskTracker` as zero-value (`&TaskTracker{}`), bypassing `NewTaskTracker()`. `MarkCached` is never called.

**Actions**:

1. In `internal/server/cachepreload/deduplication.go`:
   - Remove `isCached map[string]bool` and `cachedMu sync.Mutex` fields from `TaskTracker`
   - Remove `NewTaskTracker()` constructor (or keep it returning `&TaskTracker{}` if tests use it)
   - Remove `MarkCached()` method
   - In `TryClaimTask()`: remove the `isCached` lookup, keep only the `RegisterTask` call
2. In `internal/server/cachepreload/task_tracker_test.go`:
   - Remove `TestTaskTracker_MarkCachedPreventsClaim` test
   - Update `TestTaskTracker_TryClaimTask` and `TestTaskTracker_ConcurrentClaim` to use `&TaskTracker{}` instead of `NewTaskTracker()` (matching production usage)
3. Run tests: `go test -tags "integration e2e" ./internal/server/cachepreload/... > ./tmp/test_output.txt 2>&1`
4. `make format && make lint`
5. Commit: `refactor(cachepreload): remove dead isCached/MarkCached from TaskTracker`

---

## Step 3: Remove dead `RecordThrottled`, unused `throttleCounter`, and unused `mediumWatermark`

**Issues**: #3 (Low) — `RecordThrottled()` never called, `throttleCounter` on Manager never read. #4 (Low) — `mediumWatermark` constant unused.

**Actions**:

1. In `internal/server/cachebatch/manager.go`:
   - Delete `mediumWatermark` constant
   - Delete `throttleCounter atomic.Int64` field from `Manager` struct
   - Replace `m.throttleCounter.Add(1)` in the throttle block with `metrics.RecordThrottled()`
   - Rename `throttleThrottled` → `isThrottled` for clarity
2. In `internal/server/cachebatch/metrics.go`: keep `RecordThrottled()` (it's now used)
3. Run tests: `go test -tags "integration e2e" ./internal/server/cachebatch/... > ./tmp/test_output.txt 2>&1`
4. `make format && make lint`
5. Commit: `refactor(cachebatch): wire RecordThrottled, remove dead code and unused constant`

---

## Step 4: Fix test async synchronization — make test submit functions synchronous

**Issue**: #5 (Medium) — Tests use `time.Sleep` without deterministic signaling. The `go hcm.submitFunc(newEntry)` in middleware means tests can't reliably wait for completion.

**Strategy**: Change all test submit functions to store entries synchronously (no goroutine race), and add a `SyncSubmitFunc` field to `HTTPCacheMiddleware` that, when set, is called directly (not via `go`) instead of `submitFunc`. This keeps production behavior unchanged while giving tests a deterministic path.

**Alternative strategy** (simpler): Since `NewHTTPCacheMiddlewareForTest` already exists, add a `syncMode bool` field. When `syncMode` is true, the middleware calls `submitFunc(entry)` directly instead of `go submitFunc(entry)`. Test constructors set this.

**Actions**:

1. In `internal/cachelite/http_cache_middleware.go`:
   - Add `syncMode bool` field to `HTTPCacheMiddleware` struct
   - In `Middleware()`, change the store path from:
     ```go
     go hcm.submitFunc(newEntry)
     ```
     to:
     ```go
     if hcm.syncMode {
         hcm.submitFunc(newEntry)
     } else {
         go hcm.submitFunc(newEntry)
     }
     ```
   - In `NewHTTPCacheMiddlewareForTest`: set `syncMode: true`
2. In all test files (`http_cache_middleware_test.go`, `http_cache_edge_cases_test.go`, `http_cache_middleware_routes_test.go`, `internal/server/http_cache_middleware_integration_test.go`):
   - Remove all `time.Sleep(10 * time.Millisecond)` and `time.Sleep(20 * time.Millisecond)` calls that exist solely to wait for async cache writes
   - Remove `sync.WaitGroup` returns from `createTestMiddlewareWithSubmit`, `createSyncSubmitFuncForEdgeCases`, `createSyncSubmitFuncForRoutes` — they are no longer needed
   - Remove `wg.Wait()` calls and `wg.Add(1)`/`wg.Done()` from submit functions
   - Simplify `createSyncCacheSubmit` in integration tests (remove WaitGroup if present)
3. Run tests: `go test -tags "integration e2e" ./internal/cachelite/... ./internal/server/... > ./tmp/test_output.txt 2>&1`
4. Run with race detector: `go test -tags "integration e2e" -race ./internal/cachelite/... > ./tmp/test_output.txt 2>&1`
5. `make format && make lint`
6. Commit: `refactor(cachelite): add syncMode to test middleware, remove all Sleep-based synchronization`

---

## Step 5: Fix misleading `t.Skip()` in eviction test

**Issue**: #6 (Low) — `TestBudgetEviction_LRU_UnifiedBatcher` calls `t.Skip()` at the end after running all other assertions, making it look skipped when it mostly ran.

**Actions**:

1. In `internal/server/http_cache_middleware_integration_test.go`:
   - Remove the `t.Skip()` call at the end of `TestBudgetEviction_LRU_UnifiedBatcher`
   - Since the test now uses `syncMode` (from Step 4), the synchronous submit stores entries directly. Eviction still won't happen (it's a batcher concern), so replace the old eviction assertion with a clear comment explaining why eviction is not tested here and where it is tested
   - Alternatively: add eviction logic to the sync submit function (call `EvictLRU` if size exceeds budget), so the test actually works. This would be a more complete fix.
2. Run tests: `go test -tags "integration e2e" ./internal/server/... > ./tmp/test_output.txt 2>&1`
3. `make format && make lint`
4. Commit: `fix(test): remove misleading t.Skip in eviction test, clarify coverage gap`

---

## Step 6: Remove old `GenerateCacheKey`/`GenerateCacheKeyWithHX` wrappers

**Issue**: #9 (Low) — Plan Step 6 called for removal but they still exist. They now delegate to new API, so they're redundant indirection.

**Actions**:

1. In `internal/server/cachepreload/cache_key.go`: delete `GenerateCacheKey` and `GenerateCacheKeyWithHX` functions
2. In `internal/server/cachepreload/cache_key_test.go`: rewrite tests to use `cachelite.NewCacheKey(cachelite.CacheKeyParams{...})` directly. These tests verify that preload keys match middleware keys — they should use the same API the middleware uses.
3. In `internal/server/cachepreload/folder_preload_task_test.go`: update any `GenerateCacheKeyWithHX` calls to use `cachelite.NewCacheKey(cachelite.CacheKeyParams{...})`
4. In `internal/server/cachebatch/manager_test.go`: same update
5. If `cache_key.go` becomes empty after removing the functions, delete the file
6. Run tests: `go test -tags "integration e2e" ./internal/server/cachepreload/... ./internal/server/cachebatch/... > ./tmp/test_output.txt 2>&1`
7. `make format && make lint`
8. Commit: `refactor(cachepreload): remove old GenerateCacheKey wrappers, use cachelite.NewCacheKey directly`

---

## Step 7: Final verification

**Actions**:

1. Full test suite: `go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1`
2. Race detector on cachelite: `go test -tags "integration e2e" -race ./internal/cachelite/... > ./tmp/test_output.txt 2>&1`
3. Clean build: `go build -o /dev/null .`
4. `make lint`
5. Verify with curl against dev server (if running):
   ```bash
   curl -s -X POST http://localhost:8083/login \
     -H "Content-Type: application/x-www-form-urlencoded" \
     -d "username=admin&password=admin" -c /tmp/cookies.txt
   curl -s http://localhost:8083/gallery/1 -b /tmp/cookies.txt -o /dev/null -w "%{http_code}"
   ```
6. Commit: `refactor(cachelite): ext1 cleanup complete — all issues from code review addressed`

---

## Notes

- **Execution order matters**: Steps 1-3 are independent and could be parallelized. Step 4 must come before Step 5. Step 6 is independent of 4-5.
- **No new abstractions**: Every step removes code or simplifies existing code.
- **Cache key format**: Issue #7 (orphaned cached entries from format change) is inherent to the prior commits and doesn't need a fix — just ensure `SFG_INCREMENT_ETAG` is set on next deploy.
- **Theme in preload**: Issue #8 (preload assumes dark theme) is a pre-existing design limitation, not a regression. Out of scope.
- **TDD**: Steps that modify production logic (1, 3, 4) should have test verification before and after.

---

## Validation Report (post-execution)

**Validated**: 2026-03-14 against commits `ba42d6b..8e2b0e1`

### Step 1: Export `VariantForPath` — DONE (commit `ba42d6b`)

| Claim                                                                    | Result                                                            |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------- |
| `variantForPath` was duplicated in `key.go` and `folder_preload_task.go` | Confirmed at time of review; now resolved                         |
| Exported as `VariantForPath` in `cachelite/key.go`                       | **Confirmed** — line 115                                          |
| Duplicate removed from `folder_preload_task.go`                          | **Confirmed** — calls `cachelite.VariantForPath(path)` at line 99 |
| `NewCacheKeyForPreload` updated                                          | **Confirmed** — calls `VariantForPath(path)` at line 102          |
| Tests updated                                                            | **Confirmed** — `key_test.go` tests reference `VariantForPath`    |

**Note**: Comment on line 113 still says `variantForPath` (lowercase) — stale, should read `VariantForPath`.

### Step 2: Remove dead `isCached`/`MarkCached` — DONE (commit `6ac98fe`)

| Claim                                                   | Result                                             |
| ------------------------------------------------------- | -------------------------------------------------- |
| `isCached`, `cachedMu` removed from `TaskTracker`       | **Confirmed** — struct has only `pending sync.Map` |
| `NewTaskTracker()` removed                              | **Confirmed** — not present                        |
| `MarkCached()` removed                                  | **Confirmed** — not present                        |
| `TryClaimTask()` simplified to only call `RegisterTask` | **Confirmed** — lines 58-60                        |
| `TestTaskTracker_MarkCachedPreventsClaim` removed       | **Confirmed** — not present                        |
| Tests use `&TaskTracker{}` (matching production)        | **Confirmed**                                      |

### Step 3: Wire `RecordThrottled`, remove dead code — DONE (commit `85261b5`)

| Claim                                                  | Result                           |
| ------------------------------------------------------ | -------------------------------- |
| `mediumWatermark` removed                              | **Confirmed** — not in constants |
| `throttleCounter` removed from Manager                 | **Confirmed** — not in struct    |
| `m.metrics.RecordThrottled()` called in throttle block | **Confirmed** — line 137         |
| Field renamed to `isThrottled`                         | **Confirmed** — line 29          |

**Remaining issue**: `highWatermark = 800` (line 20) is declared but never used. The throttle logic uses literal `0.8` and `0.95` ratios instead of the watermark constants. This is a minor dead constant.

### Step 4: `syncMode` for test middleware — DONE (commit `1f3f871`, `3db66af`)

| Claim                                                      | Result                          |
| ---------------------------------------------------------- | ------------------------------- |
| `syncMode bool` field added to `HTTPCacheMiddleware`       | **Confirmed** — line 21         |
| Middleware uses `if hcm.syncMode` to call directly vs `go` | **Confirmed** — lines 332-336   |
| `NewHTTPCacheMiddlewareForTest` sets `syncMode: true`      | **Confirmed** — line 79         |
| No production callers of `NewHTTPCacheMiddlewareForTest`   | **Confirmed** — test files only |

**Remaining `time.Sleep` calls (not fully eliminated)**:

| File                                        | Count               | Purpose                                                                               |
| ------------------------------------------- | ------------------- | ------------------------------------------------------------------------------------- |
| `http_cache_middleware_test.go`             | 4 × 50ms            | Waiting for `OnGalleryCacheHit` callback goroutine — **legitimate**, not cache writes |
| `http_cache_edge_cases_test.go`             | 4 × 10ms            | "Wait for async cache write" — **should be unnecessary** with `syncMode`              |
| `http_cache_middleware_integration_test.go` | 8 × 20ms, 1 × 300ms | "Wait for async/batcher" — **should be unnecessary** with `syncMode`                  |
| `http_cache_middleware_routes_test.go`      | 0                   | Clean                                                                                 |

The 50ms sleeps for `OnGalleryCacheHit` callback are legitimate (that callback runs in a separate `go` goroutine inside the middleware, not via `submitFunc`). The remaining 10ms/20ms sleeps for "cache write" are vestigial — with `syncMode: true`, the cache write happens synchronously before the handler returns. They are harmless but unnecessary.

The 300ms sleep in `TestBudgetEviction_LRU_UnifiedBatcher` is also unnecessary since `syncMode` is active, but it was intentionally left (see Step 5).

### Step 5: Fix misleading `t.Skip` in eviction test — DONE (commit `2b1782f`)

| Claim                                                | Result                                                                               |
| ---------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `t.Skip()` removed                                   | **Confirmed** — replaced with comment block (lines 374-378)                          |
| Eviction assertion removed (was already inoperative) | **Confirmed** — comment explains why                                                 |
| Other eviction tests exist                           | **Confirmed** — `evict_lru_test.go`, `TestBudgetEviction_LRU`, `evictIfNeeded` tests |

### Step 6: Remove old `GenerateCacheKey` wrappers — DONE (commit `e0267de`, `ca55d3d`)

| Claim                                                   | Result                                                 |
| ------------------------------------------------------- | ------------------------------------------------------ |
| `GenerateCacheKey` and `GenerateCacheKeyWithHX` removed | **Confirmed** — `cache_key.go` only contains a comment |
| Tests rewritten to use `cachelite.NewCacheKey` directly | **Confirmed**                                          |
| No production callers remain                            | **Confirmed**                                          |

**Note**: `cache_key.go` is effectively empty (package declaration + comment). Could be deleted entirely.

### Step 7: Final verification — NOT YET RUN

Full test suite and curl verification have not been run as a final step.

### Summary of remaining items

| #   | Severity     | Item                                                                                                          |
| --- | ------------ | ------------------------------------------------------------------------------------------------------------- |
| 1   | **Trivial**  | Stale comment on `VariantForPath` (line 113 of `key.go` says `variantForPath`)                                |
| 2   | **Trivial**  | `highWatermark = 800` constant unused in `cachebatch/manager.go`                                              |
| 3   | **Low**      | `cache_key.go` in cachepreload is effectively empty — delete it                                               |
| 4   | **Low**      | 12 vestigial `time.Sleep` calls in edge case and integration tests (harmless but unnecessary with `syncMode`) |
| 5   | **Required** | Step 7 final verification (full test suite, race detector, lint, curl) not yet run                            |
