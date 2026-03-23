# Plan: Fix Cachelite Consistency Issues

## Context

**Problem**: Three cache-related components (cachelite, pre-load, batch load) are inconsistent, leading to:

1. **Preloaded cache entries won't match real browser requests** (CRITICAL) - Cache key components missing in preload/batch load
2. **Deduplication race condition** (HIGH) - "Check then schedule" pattern allows concurrent preloads for same cache key
3. **Middleware fallback in hot path** (MEDIUM) - Direct `StoreCacheEntry` used when submitFunc is nil

**Analysis completed**: See `docs/cachelite-consistency-analysis.md` for detailed root cause analysis.

---

## Execution Steps

### Step 6: Remove old cache key functions

**Goal**: Delete old `GenerateCacheKey` and `GenerateCacheKeyWithHX` functions that are incompatible with the new unified approach.

**Subagent**: `feature-dev:code-explorer` - Analyzes existing code to understand where old functions are used

**Actions**:

Use `Agent` tool with subagent_type `feature-dev:code-explorer` and model `claude-sonnet-4-6` to:

1. **Analyze** the codebase to find all usages of:
   - `GenerateCacheKey()` in `internal/server/cachepreload/cache_key.go`
   - `GenerateCacheKeyWithHX()` in `internal/server/cachepreload/cache_key.go`
   - `cachelite.GenerateCacheKey()` in `internal/cachelite/cache.go` (if it still exists)
   - Any other references

2. **Report** findings on which files use these functions and the call sites

3. **Remove** old cache key functions:
   - Delete `GenerateCacheKey()` and `GenerateCacheKeyWithHX()` from `internal/server/cachepreload/cache_key.go`
   - Delete entire file if it becomes empty
   - Remove any remaining references found by analysis

4. **Update imports** in files that referenced these functions if needed

5. **Run tests**: `go test -tags "integration e2e" ./internal/cachelite/... ./internal/server/cachepreload/... ./internal/server/cachebatch/...`
   - Ensure all pass

6. **Commit**: `git add -A && git commit -m "refactor(cachelite): remove old GenerateCacheKey functions"`

**Subagent instruction**: "Analyze the codebase to find all usages of GenerateCacheKey, GenerateCacheKeyWithHX, and cachelite.NewCacheKey functions. Report where each is used. Delete the old functions from internal/server/cachepreload/cache_key.go. Remove the file if empty. Update any imports. Run tests. Commit changes with: 'git add -A && git commit -m refactor(cachelite): remove old cache key functions'. Use model claude-sonnet-4-6 for code analysis work."

---

### Step 7: Add TryClaimTask to TaskTracker with tests (TDD)

**Goal**: Implement atomic `TryClaimTask` method to prevent deduplication race condition.

**Subagent**: `general-purpose` - model `claude-sonnet-4-6`

**Actions**:

Use `Agent` tool with subagent_type `general-purpose` and model `claude-sonnet-4-6` to:

1. **Write TDD Red Test** - Add to `internal/server/cachepreload/task_tracker_test.go`:

   ```go
   func TestTaskTracker_TryClaimTask(t *testing.T) {
       tt := NewTaskTracker()
       cacheKey := "test-key"

       // First claim should succeed
       if !tt.TryClaimTask(cacheKey) {
           t.Fatal("First claim should succeed")
       }

       // Second claim should fail (already claimed)
       if tt.TryClaimTask(cacheKey) {
           t.Error("Second claim should fail - key already claimed")
       }

       // Different key should succeed
       if !tt.TryClaimTask("different-key") {
           t.Error("Claim on different key should succeed")
       }
   }

   func TestTaskTracker_ConcurrentClaim(t *testing.T) {
       tt := NewTaskTracker()
       cacheKey := "test-key"

       var wg sync.WaitGroup
       successes := atomic.Int64{}

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

       if successes.Load() != 1 {
           t.Errorf("Expected 1 successful claim, got %d", successes.Load())
       }
   }

   func TestTaskTracker_MarkCachedPreventsClaim(t *testing.T) {
       tt := NewTaskTracker()
       cacheKey := "test-key"

       if !tt.TryClaimTask(cacheKey) {
           t.Fatal("First claim should succeed")
       }

       tt.MarkCached(cacheKey)

       if tt.TryClaimTask(cacheKey) {
           t.Error("Should not claim cached key")
       }
   }
   ```

2. **Implement** - Update `internal/server/cachepreload/task_tracker.go`:
   - Add `isCached map[string]bool` field to TaskTracker struct
   - Initialize in `NewTaskTracker()` constructor
   - Implement `TryClaimTask(cacheKey string) bool` method with atomic check-and-claim
   - Implement `MarkCached(cacheKey string)` method

3. **Run tests**: `go test -tags "integration e2e" ./internal/server/cachepreload/...`
   - Iterate until all tests pass

4. **Format and Lint**: `make format`, `make lint`, fix issues

5. **Commit**: `git add -A && git commit -m "refactor(cachepreload): implement atomic TryClaimTask for cache deduplication"`

**Subagent instruction**: "Write TDD red tests to internal/server/cachepreload/task_tracker_test.go for TryClaimTask. Then implement TryClaimTask and MarkCached methods in internal/server/cachepreload/task_tracker.go. Add isCached map to TaskTracker struct. Run tests, iterate until green, then make format and make lint. Commit with: 'git add -A && git commit -m refactor(cachepreload): implement atomic TryClaimTask'. Use model claude-sonnet-4-6."

---

### Step 8: Update preload to use atomic claim

**Goal**: Replace deduplication check with atomic `TryClaimTask` to prevent race condition.

**Subagent**: `general-purpose` - model `claude-sonnet-4-6`

**Actions**:

Use `Agent` tool with subagent_type `general-purpose` and model `claude-sonnet-4-6` to:

1. **Read** `internal/server/cachepreload/folder_preload_task.go` to locate deduplication check around line 126-140

2. **Replace** check with atomic claim:

   ```go
   if !t.TaskTracker.TryClaimTask(cacheKey) {
       if t.Metrics != nil {
           t.Metrics.RecordSkipped("already_claimed")
       }
       return false
   }
   ```

3. **Update imports**:
   - Add: `"sfpg-go/internal/cachelite"` (should already be there from Step 6)
   - Ensure `"sfpg-go/internal/server/cachepreload"` package is imported

4. **Run tests**: `go test -tags "integration e2e" ./internal/server/cachepreload/...`
   - Ensure all pass

5. **Format and Lint**: `make format`, `make lint`, fix issues

6. **Commit**: `git add -A && git commit -m "refactor(cachepreload): update preload to use atomic TryClaimTask"`

**Subagent instruction**: "Update internal/server/cachepreload/folder_preload_task.go to use TryClaimTask instead of IsTaskPending for deduplication check. Find the check around line 126-140 and replace it. Update imports if needed. Run tests, make format, make lint. Commit with: 'git add -A && git commit -m refactor(cachepreload): update preload to use atomic TryClaimTask'. Use model claude-sonnet-4-6."

---

### Step 9: Add test-friendly HTTPCacheMiddleware constructor

**Goal**: Create separate constructor for tests that allows nil submitFunc without panicking.

**Subagent**: `general-purpose` - model `claude-sonnet-4-6`

**Actions**:

Use `Agent` tool with subagent_type `general-purpose` and model `claude-sonnet-4-6` to:

1. **Read** `internal/cachelite/http_cache_middleware.go` to locate `NewHTTPCacheMiddleware` function

2. **Add** test-only constructor at the end of file:

   ```go
   // NewHTTPCacheMiddlewareForTest creates middleware without production guard for testing.
   // Tests can use this to bypass the panic on nil submitFunc.
   func NewHTTPCacheMiddlewareForTest(
       db *dbconnpool.DbSQLConnPool,
       cfg CacheConfig,
       sizeCounter *atomic.Int64,
       submitFunc func(*HTTPCacheEntry), // test-only: accepts nil submitFunc
   ) *HTTPCacheMiddleware {
       return &HTTPCacheMiddleware{
           db:          db,
           config:      cfg,
           sizeCounter: sizeCounter,
           submitFunc:  submitFunc,
       }
   }
   ```

3. **Add production guard** to main constructor:

   ```go
   // In production, submitFunc is mandatory
   if submit == nil {
       panic("HTTPCacheMiddleware: submitFunc is required in production - use unified batcher")
   }
   ```

4. **Update tests** that need nil submitFunc to use `NewHTTPCacheMiddlewareForTest` instead of `NewHTTPCacheMiddleware`

5. **Run tests**: `go test -tags "integration e2e" ./internal/cachelite/...`

6. **Commit**: `git add -A && git commit -m "refactor(cachelite): add test-friendly constructor and production guard"`

**Subagent instruction**: "Add a test-friendly constructor NewHTTPCacheMiddlewareForTest at the end of internal/cachelite/http_cache_middleware.go. This constructor accepts nil submitFunc for testing, bypassing the panic. Add a production guard to the main NewHTTPCacheMiddleware constructor that panics if submitFunc is nil. Find and update any tests that create middleware with nil submitFunc to use NewHTTPCacheMiddlewareForTest instead. Run tests, make format, make lint. Commit with: 'git add -A && git commit -m refactor(cachelite): add test-friendly constructor'. Use model claude-sonnet-4-6."

---

### Step 10: Remove middleware fallback path

**Goal**: Remove synchronous write path that was only for tests, now that test-friendly constructor exists.

**Subagent**: `general-purpose` - model `claude-sonnet-4-6`

**Actions**:

Use `Agent` tool with subagent_type `general-purpose` and model `claude-sonnet-4-6` to:

1. **Read** `internal/cachelite/http_cache_middleware.go` to locate fallback else branch around line 320-332

2. **Remove** fallback:
   - Find: `if hcm.submitFunc != nil { ... } else { ... }`
   - Remove entire `else` branch (synchronous write path)
   - Keep only `if hcm.submitFunc != nil` check

3. **Update tests** if any test was using the else branch (unlikely)

4. **Run tests**: `go test -tags "integration e2e" ./internal/cachelite/...`

5. **Format and Lint**: `make format`, `make lint`, fix issues

6. **Commit**: `git add -A && git commit -m "refactor(cachelite): remove middleware fallback path, enforce batcher usage"`

**Subagent instruction**: "Remove the middleware fallback else branch from internal/cachelite/http_cache_middleware.go around line 320-332. Keep only the if hcm.submitFunc != nil check. Remove the synchronous write path that was only for tests. Update any tests that were using the fallback. Run tests, make format, make lint. Commit with: 'git add -A && git commit -m refactor(cachelite): remove middleware fallback and enforce batcher'. Use model claude-sonnet-4-6."

---

### Step 11: Add backpressure throttling to batch load

**Goal**: Add queue utilization monitoring and throttling to prevent queue overflow during slow commits.

**Subagent**: `general-purpose` - model `claude-sonnet-4-6`

**Actions**:

Use `Agent` tool with subagent_type `general-purpose` and model `claude-sonnet-4-6` to:

1. **Read** `internal/server/cachebatch/manager.go` to understand the Run method structure

2. **Add throttling constants**:

   ```go
   const (
       highWatermark  = 800
       mediumWatermark = 600
       throttleDelay   = 50 * time.Millisecond
       defaultQueueSize = 1000
   )
   ```

3. **Add throttling fields** to Manager struct:

   ```go
   throttleThrottled bool
   throttleCounter   atomic.Int64
   ```

4. **Implement throttling logic** in Run method before scheduling each job:
   - Check backpressure using PendingCount()
   - Throttle when utilization > 0.8
   - Skip when utilization > 0.95
   - Log throttling status

5. **Update Metrics struct** in `internal/server/cachebatch/metrics.go`:
   - Add ThrottlesSkipped atomic.Int64
   - Add BackpressureSkipped atomic.Int64
   - Add RecordThrottled() method
   - Add RecordBackpressureSkipped() method

6. **Update throttling logic** to use new metrics methods

7. **Run tests**: `go test -tags "integration e2e" ./internal/server/cachebatch/...`
   - Fix any failing tests, iterate until green

8. **Format and Lint**: `make format`, `make lint`, fix issues

9. **Commit**: `git add -A && git commit -m "refactor(cachebatch): add backpressure throttling to batch load"`

**Subagent instruction**: "Add backpressure throttling to internal/server/cachebatch/manager.go. Add constants, add throttling fields to Manager struct, implement throttling logic in Run method that checks PendingCount() and throttles or skips jobs. Update Metrics struct with ThrottlesSkipped, BackpressureSkipped counters and recording methods. Run tests, make format, make lint. Commit with: 'git add -A && git commit -m refactor(cachebatch): add backpressure throttling'. Use model claude-sonnet-4-6."

---

### Step 12: Final verification

**Goal**: Run all tests and verify cache functionality works correctly.

**Subagent**: `general-purpose` - model `claude-sonnet-4-6`

**Actions**:

Use `Agent` tool with subagent_type `general-purpose` and model `claude-sonnet-4-6` to:

1. **Run all tests**: `go test -tags "integration e2e" ./...`
   - Ensure all pass

2. **Verify with curl**:
   - Login: `curl -s -X POST http://localhost:8083/login -H "Content-Type: application/x-www-form-urlencoded" -d "username=admin&password=admin" -c /tmp/cookies.txt`
   - Request gallery: `curl -s http://localhost:8083/gallery/1 -b /tmp/cookies.txt`
   - Check cache is working (should get cache hit after first request)

3. **Report results**: Document any issues found

4. **Commit**:
   ```bash
   git add -A
   git commit -m "refactor(cachelite): final verification complete - all tests passing, cache working correctly"
   ```

**Subagent instruction**: "Run full test suite and verify with curl. Execute 'go test -tags integration e2e ./...'. Ensure all tests pass. Test cache functionality by logging into localhost:8083, requesting gallery, verifying cache hit behavior. Document any issues. Commit final results with: 'git add -A && git commit -m refactor(cachelite): final verification complete'. Use model claude-sonnet-4-6."

---

## Notes

- **TDD REQUIRED**: Each step follows Red → Implement → Green pattern
- **Code removal**: Old cache key functions are deleted, not kept for backwards compatibility
- **Simplify**: No new abstractions added; use straightforward replacements
- **Sequential**: Execute steps 6-12 in order
- **Sub-agents**: Each step uses Agent tool with specified subagent type and model
- **Model specifications**:
  - Code analysis: claude-sonnet-4-6
  - Implementation: claude-sonnet-4-6 (most steps)
  - Testing: claude-sonnet-4-6 (or claude-haiku-4-5-20251001 for simple test updates)
- **Skip problematic steps**: Steps 2-5 are skipped (caused circular dependency issue)
- **Starting point**: Begin at Step 6 after old functions removed by Step 6 subagent
