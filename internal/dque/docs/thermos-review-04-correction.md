# Thermos Review #4 — Correction Plan

**Date:** 2026-06-16
**Based on:** `thermos-review-04.md` (findings verified against source)
**Excluded:** #10 (intentional two-level turbo guards), #12 (not a real FD leak; Go's Close() releases the fd regardless of error)

---

## Execution Phases (dependency-ordered)

```
Phase 1 (parallel):   #1, #2, #6     — Critical bug fixes
Phase 2:              #3             — SizeApprox() fix (blocks #4)
Phase 3:              #4             — Remove seg.mutex (needs #3 first)
Phase 4:              #5             — Delete internal/errors package
Phase 5:              #7             — Test cleanup
Phase 6 (ordered):    #13, #8, #9, #11 — #13 first (changes dirExists/fileExists sigs used by #8)
Phase 7 (parallel):   #14..#17       — Minor cleanups
```

### Dependency rationale

- **#3 → #4:** `seg.mutex` currently protects `size()` calls from `SizeApprox()`'s unlocked access. Once #3 fixes `SizeApprox()` to hold `q.mutex`, `seg.mutex` has no remaining purpose and can be deleted.
- **#5 → #7:** The test file `segment_test.go` imports `dqueerrors`. Phase 4 removes that package, so the import must be cleaned up in Phase 5.
- **#13 → #8:** Fix #8 refactors `New`/`Open` using `dirExists()`. Fix #13 changes `dirExists()`'s signature from `bool` to `(bool, error)`. Apply #13 first to avoid rework.
- **Phase 4 standalone:** The errors package removal is mechanical and doesn't depend on the bug fixes. It's placed after Phase 3 so bug-fix changes don't need to be rebased across the rename, but could also be done earlier.

---

## Phase 1 — Critical Bug Fixes

### Fix #1: `remove()` returns `nil` on `_sync()` failure — silent data loss

**File:** `segment.go`  
**Severity:** HIGH  
**Lines:** ~205

**Problem:** When `_sync()` fails, `remove()` returns `(nil, err)` despite having already removed the item from `seg.objects` and written a deletion marker to disk. The local `object` variable holds the removed item but is discarded in the error return.

**Current code (line ~205):**

```go
if err := seg._sync(); err != nil {
    return nil, err
}
```

**Fix:**

```go
if err := seg._sync(); err != nil {
    return object, err
}
```

**Test:** No new test needed. The fix is a one-token change (`nil` → `object`) — trivially correct by inspection. Triggering a real `_sync()` failure requires a filesystem-level fsync error, which is impractical in unit tests (tmpfs never fails fsync). Adding test-only fault injection (package-level var hook) would add complexity disproportionate to the value. The existing integration tests cover the normal path; code review covers the fix.

---

### Fix #2: `add()` appends to in-memory slice before `_sync()` — inconsistency on error

**File:** `segment.go`  
**Severity:** HIGH  
**Lines:** ~243

**Problem:** `seg.objects = append(seg.objects, object)` executes before `_sync()`. If `_sync()` fails, the caller gets an error but the item is already in the queue's in-memory state. Retrying causes duplicates both in memory and on disk.

**Current code (lines ~241-246):**

```go
seg.objects = append(seg.objects, object)

// Possibly force writes to disk
return seg._sync()
```

**Fix:** Move the append after `_sync()`, or roll back on error. The cleanest approach: roll back.

```go
seg.objects = append(seg.objects, object)

// Possibly force writes to disk
if err := seg._sync(); err != nil {
    // Roll back in-memory state on sync failure
    seg.objects = seg.objects[:len(seg.objects)-1]
    return err
}
return nil
```

Alternatively, move append after sync:

```go
// Possibly force writes to disk
if err := seg._sync(); err != nil {
    return err
}
seg.objects = append(seg.objects, object)
return nil
```

The rollback approach is preferred because the disk writes have already succeeded at this point — only `fsync` failed. The data is on disk but not durably flushed. Keeping the in-memory state consistent with the (already-written) disk state is correct.

**Test:** No new test needed — same reasoning as #1. The fix is mechanically correct (append + rollback on error). Triggering a real fsync failure in unit tests is impractical. The existing `TestSegment` test verifies the normal add path.

---

### Fix #6: File descriptor leak in `load()` error path

**File:** `queue.go`  
**Severity:** MAJOR  
**Lines:** ~620-630 (`initQueue`)

**Problem:** `initQueue()` calls `q.load()`. If `load()` sets `q.firstSegment` successfully but then fails opening the last segment (in the `maxNum > minNum` branch), `initQueue()` only closes `q.fileLock`. `q.firstSegment`'s file handle is never closed.

**Current code:**

```go
func (q *DQue) initQueue(fullPath string, itemsPerSegment int, builder func() interface{}) error {
    q.fullPath = fullPath
    q.config.ItemsPerSegment = itemsPerSegment
    q.builder = builder
    q.emptyCond = sync.NewCond(&q.mutex)
    if err := q.lock(); err != nil {
        return err
    }
    if err := q.load(); err != nil {
        if releaseErr := q.fileLock.Close(); releaseErr != nil {
            return errors.Join(err, releaseErr)
        }
        return err
    }
    return nil
}
```

**Fix:** Close `q.firstSegment` (and `q.lastSegment` if set) in the error path.

```go
func (q *DQue) initQueue(fullPath string, itemsPerSegment int, builder func() interface{}) error {
    q.fullPath = fullPath
    q.config.ItemsPerSegment = itemsPerSegment
    q.builder = builder
    q.emptyCond = sync.NewCond(&q.mutex)
    if err := q.lock(); err != nil {
        return err
    }
    if err := q.load(); err != nil {
        // Close any segments that were opened before the failure.
        if q.firstSegment != nil {
            _ = q.firstSegment.close()
        }
        if q.lastSegment != nil && q.lastSegment != q.firstSegment {
            _ = q.lastSegment.close()
        }
        if releaseErr := q.fileLock.Close(); releaseErr != nil {
            return errors.Join(err, releaseErr)
        }
        return err
    }
    return nil
}
```

**Test:** No new test needed. The error path requires a corrupted/missing last segment file while the first is valid — difficult to reproduce reliably. The fix is straightforward (close opened segments before returning error) and is verifiable by code review. The existing `TestQueue_LoadSkipsEmptySegments` and `TestQueue_LoadAllEmptyCompleteSegments` tests cover the normal `load()` path.

---

## Phase 2 — SizeApprox() Fix (blocks Phase 3)

### Fix #3: `SizeApprox()` ABA data race — can produce nonsensical results

**File:** `queue.go`  
**Severity:** MAJOR (was HIGH in review; downgraded — no crash, just wrong results)  
**Lines:** ~404-420

**Problem:** `SizeApprox()` copies segment pointers under `q.mutex`, drops the lock, then calls `size()` on the copied pointers. Between unlock and use, `Dequeue()` can replace and `delete()` those segments. `size()` on a deleted segment returns 0 (its `objects` was zeroed in `delete()`). The result can be wildly wrong.

**Fix:** Replace the body of `SizeApprox()` with a call to `sizeUnsafeLocked()`, which already holds the lock. Delete the `SizeApprox()` method entirely, or make it a thin wrapper that holds the lock.

**Option A — Delete SizeApprox() and inline:**

```go
func (q *DQue) SizeApprox() int {
    q.mutex.Lock()
    defer q.mutex.Unlock()
    if q.closed {
        return 0
    }
    return q.sizeUnsafeLocked()
}
```

**Option B — Remove SizeApprox() entirely and rename sizeUnsafeLocked():**
If no external callers rely on the "approximate" semantics, just keep `Size()` as the only API and remove `SizeApprox()`. But since this is a public API, option A preserves backward compatibility.

**Recommendation:** Option A (thin wrapper). Update the doc comment to note that it now takes the lock and is equivalent to `Size()`.

**Test:** Existing `Size()` tests cover correctness. The existing concurrent tests (`TestQueue_BlockingAggressive`, `TestQueue_BlockingBehaviour`) already exercise `Enqueue`/`Dequeue` concurrently — running them with `-race` verifies no new races after the fix. No new test needed.

---

## Phase 3 — Remove `seg.mutex`

### Fix #4: Redundant `seg.mutex` adds overhead with zero concurrency benefit

**File:** `segment.go`  
**Severity:** MAJOR  
**Affected methods:** `load()`, `peek()`, `remove()`, `add()`, `size()`, `sizeOnDisk()`, `delete()`

**Prerequisite:** Fix #3 must be applied first. After #3, `SizeApprox()` holds `q.mutex` for the entire operation, so `seg.mutex` is no longer needed by any caller.

**Fix:**

1. Delete the `mutex sync.Mutex` field from the `qSegment` struct.
2. Remove all `seg.mutex.Lock()` / `defer seg.mutex.Unlock()` pairs from these methods:
   - `load()` (line ~87-88)
   - `peek()` (line ~157-158)
   - `remove()` (line ~177-178)
   - `add()` (line ~216-217)
   - `size()` (line ~253-254)
   - `sizeOnDisk()` (line ~266-267)
   - `delete()` (line ~276-277)
3. Remove the `"sync"` import from `segment.go`. After removing `seg.mutex`, no sync primitives remain in this file.

**Safety note on `load()`:** `load()` is only called during `openQueueSegment()` → `initQueue()`, before any goroutine can access the queue. Removing `seg.mutex` from `load()` is safe because no concurrent access is possible during initialization.

**Test:** Run the full test suite with `-race`. No new races should appear since `q.mutex` protects all segment access through the public API.

---

## Phase 4 — Delete Custom Errors Package

### Fix #5: `internal/errors/` package adds complexity for no production value

**Files:** `queue.go`, `segment.go`, `segment_test.go`, `internal/errors/errors.go`, `internal/errors/errors_test.go`  
**Severity:** MAJOR  
**Blast radius:** ~31 `dqueerrors.New` calls, ~31 `dqueerrors.Wrap`/`Wrapf` calls across two files

**Fix:**

#### Step 1: Replace calls in `queue.go`

Replace all `dqueerrors.New("msg")` with `errors.New("msg")` (stdlib).  
Replace all `dqueerrors.Wrap(err, "msg")` with `fmt.Errorf("msg: %w", err)`.  
Replace all `dqueerrors.Wrapf(err, "fmt", args...)` with `fmt.Errorf("fmt: %w", args..., err)`.

Remove the import alias:

```go
// Remove:
dqueerrors "github.com/lbe/sfpg-go/internal/errors"
// Add (or keep existing):
"errors"
"fmt"
```

**Concrete mapping for every call site in `queue.go`:**

| Current                                                             | Replacement                                                          |
| ------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `dqueerrors.New("queue is closed")`                                 | `errors.New("queue is closed")`                                      |
| `dqueerrors.New("dque is empty")`                                   | `errors.New("dque is empty")`                                        |
| `dqueerrors.New("the queue name requires a value")`                 | `errors.New(...)` (×11 `New` calls total)                            |
| `dqueerrors.Wrap(err, "error creating queue directory "+fullPath)`  | `fmt.Errorf("error creating queue directory %s: %w", fullPath, err)` |
| `dqueerrors.Wrapf(err, "error creating new queue segment: %d.", n)` | `fmt.Errorf("error creating new queue segment: %d: %w", n, err)`     |
| All other `Wrap`/`Wrapf` calls                                      | Same pattern: message + `": %w"` + err                               |

#### Step 2: Replace calls in `segment.go`

Same mechanical replacements. The `ErrCorruptedSegment` and `ErrUnableToDecode` custom error types use `dqueerrors.Wrap`/`Wrapf` internally — replace those too.

Replace the import alias with stdlib `"errors"`:

```go
// Replace:
dqueerrors "github.com/lbe/sfpg-go/internal/errors"
// With:
"errors"
```

(`segment.go` already imports `"fmt"`, needed for the new `fmt.Errorf` calls.)

#### Step 3: Update `segment_test.go`

`segment_test.go` already imports stdlib `"errors"` (used for `errors.As`). Remove only the `dqueerrors` import line:

```go
// Remove this line:
dqueerrors "github.com/lbe/sfpg-go/internal/errors"
```

Change the one usage at line ~118 from `dqueerrors.New("root cause")` to `errors.New("root cause")`.

Note: `errEmptySegment` is accessed directly (same package `dque`) — no import needed.

#### Step 4: Delete the package

```bash
rm -rf internal/errors/
```

#### Step 5: Verify

```bash
go build ./...
go vet ./...
go test ./... -race
```

**Caution:** The custom `Wrap`/`Wrapf` functions use `": "` as separator between message and cause. `fmt.Errorf("msg: %w", err)` produces `msg: cause` which is equivalent. However, the custom `wrappedError.Error()` also appends a stack trace. After migration, `err.Error()` no longer contains stack traces — this is intentional and matches standard Go practice. Any code that parses stack frames from `err.Error()` will break (but no production code should be doing that; only the errors_test.go does, and it's being deleted).

---

## Phase 5 — Test Cleanup

### Fix #7: Triplicated helpers, identical types, custom assert

**Files:** `queue_test.go`, `segment_test.go`, `internal/testutil/assert.go`  
**Severity:** MAJOR (test code)

**Fix:**

#### Step 1: Collapse test helpers

Replace `newQ`, `openQ`, `newOrOpenQ` with a single parameterized helper:

```go
func newQ(t *testing.T, dir, qName string, turbo bool) *dque.DQue { ... }
func openQ(t *testing.T, dir, qName string, turbo bool) *dque.DQue { ... }
func newOrOpenQ(t *testing.T, dir, qName string, turbo bool) *dque.DQue { ... }
```

All three only differ in the constructor call (`dque.New`, `dque.Open`, `dque.NewOrOpen`). Extract:

```go
type openFunc func(name, dirPath string, itemsPerSegment int, builder func() any) (*dque.DQue, error)

func mustOpenQ(t *testing.T, fn openFunc, dir, qName string, turbo bool) *dque.DQue {
    t.Helper()
    q, err := fn(qName, dir, 3, item2Builder)
    if err != nil {
        t.Fatalf("Error creating/opening dque: %v", err)
    }
    if turbo {
        if err := q.TurboOn(); err != nil {
            t.Fatalf("TurboOn failed: %v", err)
        }
    }
    return q
}
```

Callers become:

```go
q := mustOpenQ(t, dque.New, dir, qName, turbo)
q := mustOpenQ(t, dque.Open, dir, qName, turbo)
q := mustOpenQ(t, dque.NewOrOpen, dir, qName, turbo)
```

#### Step 2: Unify test types

`item1` (in `segment_test.go`, package `dque`) and `item2` (in `queue_test.go`, package `dque_test`) are identical in purpose but have different field names:

- `item1` has `Name string`
- `item2` has `Id int`

Keep them separate since they're in different packages. The duplication is low-impact. If desired, export a test helper type from the main package, but that pollutes the public API. **Decision: leave as-is, not worth the API surface cost.**

#### Step 3: Delete `testutil.Assert`

There are **73 call sites** across `queue_test.go` (51) and `segment_test.go` (22). Replace each `testutil.Assert(t, cond, msg, args...)` with:

```go
if !cond {
    t.Fatalf(msg, args...)
}
```

**Mechanical approach:** A sed one-liner won't work reliably due to multi-line calls. Use a Go refactoring tool or manual find-and-replace. The pattern is consistent:

- `testutil.Assert(t, <condition>, "<format>", <args...>)` → `if !<condition> { t.Fatalf("<format>", <args...>) }`
- Single-arg: `testutil.Assert(t, cond, "msg")` → `if !cond { t.Fatalf("msg") }`

Then delete `internal/testutil/assert.go` and the `internal/testutil/` directory (if empty — `flock_test.go` does not import testutil). Remove the `"github.com/lbe/sfpg-go/internal/testutil"` import from both test files.

---

## Phase 6 — Moderate Code Quality

**Ordering:** Apply #13 before #8 because #8 uses `dirExists()`, whose signature changes in #13.

---

### Fix #13: `dirExists`/`fileExists` silently swallow OS errors

**File:** `util.go`  
**Severity:** LOW  
**Lines:** 8-23

**Fix:** Change both functions to return `(bool, error)` and use `os.IsNotExist` to distinguish "not found" from real errors:

```go
func dirExists(path string) (bool, error) {
    info, err := os.Stat(path)
    if err != nil {
        if os.IsNotExist(err) {
            return false, nil
        }
        return false, err
    }
    return info.IsDir(), nil
}

func fileExists(path string) (bool, error) {
    info, err := os.Stat(path)
    if err != nil {
        if os.IsNotExist(err) {
            return false, nil
        }
        return false, err
    }
    return !info.IsDir(), nil
}
```

Update all **10 call sites** (8 `dirExists`, 2 `fileExists`) to handle the error. At each site, replace:

```go
if !dirExists(path) { return error }
```

with:

```go
exists, err := dirExists(path)
if err != nil {
    return fmt.Errorf("checking directory %s: %w", path, err)
}
if !exists { return error }
```

**Call sites in `segment.go`:**

- Line 383: `newQueueSegment` — `dirExists` check (return error on OS error)
- Line 387: `newQueueSegment` — `fileExists` check
- Line 407: `openQueueSegment` — `dirExists` check
- Line 411: `openQueueSegment` — `fileExists` check

**Call sites in `queue.go`:**

- Line 99: `validateQueueInputs` — `dirExists` (propagate error, currently returns sentinel error)
- Line 115: `New` — `dirExists` check
- Line 137: `Open` — `dirExists` check
- Line 155: `NewOrOpen` — `dirExists` check

**Test:** Existing tests exercise all call sites. No new tests needed — the behavior change is that OS errors (permission denied, etc.) are now surfaced instead of silently treated as "doesn't exist."

---

### Fix #8: `New`/`Open` constructors ~90% identical

**File:** `queue.go`  
**Severity:** MODERATE  
**Lines:** ~110-145

**Fix:** Extract a shared `createQueueDir` helper for the existence check + optional Mkdir. Note: since #13 runs first, `dirExists` already returns `(bool, error)` — use that signature.

```go
// createQueueDir ensures the queue directory exists (or does not exist),
// creating it when mustNotExist is true. It returns a descriptive error
// when the precondition is violated or when the filesystem operation fails.
func createQueueDir(fullPath string, mustNotExist bool) error {
    exists, err := dirExists(fullPath)
    if err != nil {
        return err
    }
    if mustNotExist && exists {
        return errors.New("the given queue directory already exists: " + fullPath + ". Use Open instead")
    }
    if !mustNotExist && !exists {
        return errors.New("the given queue does not exist (" + fullPath + ")")
    }
    if mustNotExist {
        return os.Mkdir(fullPath, 0755)
    }
    return nil
}
```

Then simplify `New` and `Open`:

```go
func New(name string, dirPath string, itemsPerSegment int, builder func() interface{}) (*DQue, error) {
    fullPath, err := validateQueueInputs(name, dirPath, itemsPerSegment)
    if err != nil {
        return nil, err
    }
    if err := createQueueDir(fullPath, true); err != nil {
        return nil, err
    }
    q := &DQue{Name: name, DirPath: dirPath}
    if err := q.initQueue(fullPath, itemsPerSegment, builder); err != nil {
        return nil, err
    }
    return q, nil
}

func Open(name string, dirPath string, itemsPerSegment int, builder func() interface{}) (*DQue, error) {
    fullPath, err := validateQueueInputs(name, dirPath, itemsPerSegment)
    if err != nil {
        return nil, err
    }
    if err := createQueueDir(fullPath, false); err != nil {
        return nil, err
    }
    q := &DQue{Name: name, DirPath: dirPath}
    if err := q.initQueue(fullPath, itemsPerSegment, builder); err != nil {
        return nil, err
    }
    return q, nil
}
```

Note: the first approach in the original review (extract a single `newOrOpen` function with a `create bool` flag) would collide with the exported `NewOrOpen` combinator. The `createQueueDir` approach is simpler and avoids the naming conflict.

**Risk:** Low. Pure refactor, semantics unchanged. The `dirExists` error path that was previously swallowed is now properly propagated (courtesy of #13).

---

### Fix #9: `validateQueueInputs` has redundant dead-code loops

**File:** `queue.go`  
**Severity:** MODERATE  
**Lines:** ~87-95

**Fix:** Delete the two dead `for` loops:

```go
// Delete these lines:
// Defense in depth: reject '..' segments when split by either separator.
for _, part := range strings.Split(name, "/") {
    if part == ".." {
        return "", dqueerrors.New("the queue name cannot contain '..'")
    }
}
for _, part := range strings.Split(name, "\\") {
    if part == ".." {
        return "", dqueerrors.New("the queue name cannot contain '..'")
    }
}
```

They are unreachable because:

1. `strings.ContainsAny(name, "/\\")` already catches any name with `/` or `\`
2. A bare `".."` is caught by `name == ".."` above

**Test:** The existing `TestQueue_ValidationErrors` already covers `..` rejection. No new tests needed.

**Note:** By this phase the errors package has been deleted, so the actual lines to delete will contain `errors.New(...)` rather than `dqueerrors.New(...)`. Match against the current file contents at implementation time.

---

### Fix #11: `Dequeue()` returns `(obj, error)` with inconsistent-state semantics

**File:** `queue.go`  
**Severity:** MODERATE  
**Lines:** ~275-305 (`dequeueLocked`)

**Problem:** Three error paths return `(obj, error)` where `obj` is the successfully dequeued item but cleanup failed (segment delete, new segment creation). The caller can't distinguish "item + cleanup error" from "total failure."

**Fix Option A (document):** Add a doc comment clarifying that on non-nil error, the returned object may still be valid. Callers should always process the object if non-nil.

**Fix Option B (log):** Log the cleanup error and return `(obj, nil)` since the item was successfully dequeued. The queue is in an inconsistent state (orphaned segment file, etc.) but the item is safely out.

**Fix Option C (panic):** If these errors indicate truly unrecoverable corruption, panic. Not recommended for a library.

**Recommendation:** Option A (document). The errors indicate real filesystem problems that should not be silently swallowed. Update `Dequeue()` and `dequeueLocked()` docs.

```go
// Dequeue removes and returns the first item in the queue.
// When the queue is empty, nil and dque.ErrEmpty are returned.
//
// On error, the returned object may still be non-nil and valid — it was
// successfully dequeued but subsequent cleanup (segment deletion or creation)
// failed. Callers should process the returned object even when err != nil.
func (q *DQue) Dequeue() (interface{}, error) {
```

---

## Phase 7 — Minor Cleanups

---

### Fix #14: Unbounded allocation from untrusted `gobLen` in `load()`

**File:** `segment.go`  
**Severity:** LOW  
**Lines:** ~125

**Fix:** Add a sanity cap on `gobLen` before allocating:

```go
gobLen := binary.LittleEndian.Uint32(lenBytes)
if gobLen == 0 {
    // existing deletion-marker handling...
}
// Sanity cap: reject objects larger than 64MB
const maxObjectSize = 64 << 20
if gobLen > maxObjectSize {
    return ErrCorruptedSegment{
        Path: seg.filePath(),
        Err:  fmt.Errorf("object length %d exceeds maximum %d", gobLen, maxObjectSize),
    }
}
data := make([]byte, int(gobLen))
```

**Test:** Add the following case to `TestSegment_ErrCorruptedSegment` in `segment_test.go`:

```go
func TestSegment_ErrCorruptedSegment_gobLenCap(t *testing.T) {
	testDir := t.TempDir()
	expectedPath := (&qSegment{dirPath: testDir}).filePath()

	f, err := os.Create(expectedPath)
	if err != nil {
		t.Fatal(err)
	}

	// Write a 4-byte length of 0xFFFFFFFF (max uint32) to trigger the cap.
	if _, err := f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	_, err = openQueueSegment(testDir, 0, false, func() interface{} { return make([]byte, 4) })
	if err == nil {
		t.Fatal("expected ErrCorruptedSegment but got nil")
	}
	var corruptedError ErrCorruptedSegment
	if !errors.As(err, &corruptedError) {
		t.Fatalf("expected ErrCorruptedSegment but got %T: %s", err, err)
	}
	if corruptedError.Path != expectedPath {
		t.Fatalf("unexpected file path: %s", corruptedError.Path)
	}
	if !strings.Contains(corruptedError.Error(), "exceeds maximum") {
		t.Fatalf("expected 'exceeds maximum' in error, got: %s", corruptedError.Error())
	}
}
```

---

### Fix #15: Lock file (`lock.lock`) never cleaned up

**File:** `queue.go`  
**Severity:** LOW

**Fix:** Optionally delete the lock file in `Close()` after releasing the flock. Not required — standard flock practice leaves the lock file. If implementing, add after `fileLock.Close()`:

```go
// Optional: clean up lock file
_ = os.Remove(filepath.Join(q.fullPath, lockFile))
```

**Decision:** Skip this fix. Standard flock practice is to leave the lock file. Deleting it introduces a TOCTOU race where another process could create its own lock file between our close and delete.

---

### Fix #16: Comment references nonexistent `mutexEmptyCond`

**File:** `queue.go`  
**Severity:** LOW  
**Lines:** ~355 (`DequeueBlock` comment) and ~372 (`PeekBlock` comment)

**Fix:** Replace `mutexEmptyCond` with `q.mutex` in both comments:

```go
// Wait() atomically unlocks q.mutex and suspends execution of the calling goroutine.
```

---

### Fix #17: `Close()` nils segments before broadcasting

**File:** `queue.go`  
**Severity:** LOW  
**Lines:** ~194-197

**Fix:** Reorder to set `q.closed = true` before nil-ing segments:

```go
// Mark the instance as closed first, so any unsynchronized readers
// see the closed state before the segments are nil-ed.
q.closed = true

// Safe-guard ourself from accidentally using segments after closing the queue
q.firstSegment = nil
q.lastSegment = nil
q.fileLock = nil
q.emptyCond.Broadcast()
```

**Rationale:** The struct comment on `closed` says it's "used by unsynchronized readers to avoid data races on the segment and lock fields." Setting `closed = true` first respects that contract, even though all current callers hold `q.mutex`.

---

## Summary of Changes by File

| File                             | Fixes                                  | Type                         |
| -------------------------------- | -------------------------------------- | ---------------------------- |
| `segment.go`                     | #1, #2, #4, #5, #13, #14               | Bug fix + refactor + cleanup |
| `queue.go`                       | #3, #5, #6, #8, #9, #11, #13, #16, #17 | Bug fix + refactor + doc     |
| `util.go`                        | #13                                    | Refactor (signature change)  |
| `segment_test.go`                | #5, #7                                 | Import update + collapse     |
| `queue_test.go`                  | #7                                     | Collapse helpers             |
| `internal/testutil/assert.go`    | #7                                     | Delete                       |
| `internal/errors/errors.go`      | #5                                     | Delete                       |
| `internal/errors/errors_test.go` | #5                                     | Delete                       |

## Test Needs Assessment

| Fix | New tests needed? | Rationale                                                                                        |
| --- | ----------------- | ------------------------------------------------------------------------------------------------ |
| #1  | **No**            | One-token fix (`nil`→`object`). fsync failure untriggerable in unit tests. Code review suffices. |
| #2  | **No**            | Append+rollback pattern is mechanically correct. Same fsync limitation as #1.                    |
| #3  | **No**            | Existing concurrent tests (`TestQueue_BlockingAggressive`) + `-race` cover this.                 |
| #4  | **No**            | All access goes through `q.mutex`. Existing tests + `-race` confirm no regressions.              |
| #5  | **No**            | Pure mechanical rename. Existing error-path tests cover all code paths.                          |
| #6  | **No**            | Error path hard to trigger reliably. Fix is straightforward close-on-error.                      |
| #7  | **No**            | Behavior-preserving refactor of test code. Existing tests verify unchanged.                      |
| #8  | **No**            | Pure refactor, semantics unchanged.                                                              |
| #9  | **No**            | Dead code deletion. Existing `TestQueue_ValidationErrors` covers `..` rejection.                 |
| #11 | **No**            | Doc-only change.                                                                                 |
| #13 | **No**            | Existing tests exercise all call sites. OS-error propagation is additive (previously swallowed). |
| #14 | **Yes**           | New behavior: gobLen cap. Add `TestSegment_ErrCorruptedSegment_gobLenCap` (included above).      |
| #15 | N/A               | Skipped (standard flock practice).                                                               |
| #16 | **No**            | Comment-only fix.                                                                                |
| #17 | **No**            | Trivial reorder. All access is under `q.mutex` so ordering is already safe.                      |

## Test Plan

After each phase, run:

```bash
go build ./...
go vet ./...
go test ./... -race -count=1
```

After Phase 4 (error package removal), also verify:

```bash
# Ensure no remaining references to internal/errors
grep -r "internal/errors" --include="*.go" | grep -v vendor | grep -v .git
# Should produce no output
```
