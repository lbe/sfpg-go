# Changelog

## v2.1.0

### Breaking changes

- **Generics:** `DQue` is now `DQue[T any]`. `qSegment` is `qSegment[T any]`
  internally. Constructors `New`, `Open`, and `NewOrOpen` accept a type
  parameter instead of a builder function. Remove the `builder func()
interface{}` argument from all constructor calls.
- **Builder removal:** The `builder` / `objectBuilder` concept is gone.
  `new(T)` is used internally when loading segments from disk.
- **Type-safe returns:** `Dequeue`, `Peek`, `DequeueBlock`, and `PeekBlock`
  return `(T, error)` instead of `(interface{}, error)`. Remove all manual
  type assertions on return values.
- **Enqueue signature:** `Enqueue(obj interface{})` → `Enqueue(obj T)`.

## v2.0.0

### Breaking changes

- **Go version:** The module now requires Go 1.26.0.
- **`Config` type:** The previously exported `Config` struct is now unexported
  (`config`).
- **Empty-queue error:** `DQUE_EMPTY` has been replaced by the exported sentinel
  error `ErrEmpty`.
- **Error formatting:** Wrapped errors now capture and render a stack trace in
  `Error()`. Code that performs exact-string equality on `err.Error()` for
  wrapped errors may break; use `errors.Is` and `errors.As` instead.
- **Dependencies removed:** The external dependencies `github.com/pkg/errors`
  and `github.com/gofrs/flock` have been removed in favor of internal
  `internal/errors` and `internal/flock` packages.

### Other changes

- Added path-traversal validation for queue names.
- Added validation that `itemsPerSegment` must be greater than zero.
- Improved portability by using `filepath.Join` for all filesystem paths.
- Fixed a file-descriptor leak when lock acquisition fails.
- Fixed `Flock.Close` to always close the underlying file even if unlocking
  fails.
- Fixed data races on closed-state checks in `Size`, `SizeApprox`,
  `SegmentNumbers`, and `Turbo`.
- Added an unsupported-platform stub for `internal/flock` so the package
  compiles on `plan9`, `js/wasm`, etc. (lock acquisition fails at runtime).
