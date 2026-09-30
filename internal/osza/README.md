# internal/osza

Zero-allocation helpers that mirror selected `os` operations. Callers own all buffers; implementations avoid heap allocations on hot paths where the platform allows it.

## Directory layout

```text
internal/osza/
├── doc.go              # package osza — godoc entry point
├── readdir_export.go   # osza.ReadDir, Entry, ErrOverflow, ErrShortScratch
├── stat_export.go      # FileMeta, LstatAt/StatAt, LstatJoin/StatJoin, sentinels
├── osza_test.go        # smoke tests through the public import path
├── README.md           # this file (maintainers)
├── example/
│   └── read_dir_retry/ # grow-and-retry on ErrOverflow
├── readdir/
│   ├── readdir.go          # portable API
│   ├── entry.go            # IsDir, Type
│   ├── entry_unix.go       # Info (linux, darwin)
│   ├── entry_windows.go    # Info
│   ├── readdir_unix.go     # linux, darwin
│   ├── readdir_windows.go
│   └── readdir_test.go     # exhaustive behavior tests
└── stat/
    ├── join.go             # joinBuf layout for *Join
    ├── filemeta_unix.go / filemeta_windows.go
    └── stat_*_unix.go / stat_*_windows.go
```

## Usage (application code)

Import only the facade:

```go
import "github.com/lbe/sfpg-go/internal/osza"
```

```go
dirPath := make([]byte, len(dir)+1)
dirLen, err := osza.CopyDirPath(dirPath, dir)
entries := make([]osza.Entry, 256)
nameBuf := make([]byte, 64*1024)
scratch := make([]byte, 8192)
joinBuf := make([]byte, 64*1024)

n, err := osza.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
if errors.Is(err, osza.ErrOverflow) {
    // partial listing in entries[:n]; enlarge entries and nameBuf, ReadDir again from scratch
}
```

- **Entry.Name** (`[]byte`) aliases **nameBuf**; do not use Name after reusing nameBuf. Use `string(entries[i].Name)` when you need a string (allocates).
- **Entry.IsDir**, **Type**, and **Info** match **fs.DirEntry** on the current GOOS. Call **`entries[i].Info(joinBuf)`** so Unix stat results are cached on the entry.
- Compare sentinels with **`errors.Is`**, not `==` (`ErrOverflow`, `ErrShortScratch`, `ErrPathBuffer`, `ErrNilFileMeta`).
- **Windows:** `scratch` may be `nil`. **Unix:** `len(scratch)` must be ≥ 8192 or `ReadDir` returns `ErrShortScratch`.
- **joinBuf** must not overlap **dirPathBuf**, **nameBuf**, or **scratch**; use one buffer set per goroutine (NUL bytes are written temporarily during syscalls).
- Do not call **`Info` on a copied `Entry`** (`e := entries[i]`); use **`entries[i].Info(joinBuf)`**. Do not retain **`fs.FileInfo`** across the next `ReadDir` that reuses the same `entries` slot.
- **ReadDir** may return **`ErrPathBuffer`** mid-list (partial `entries[:n]`) when `joinBuf` is too small for unknown dirent types on Unix; size `joinBuf` like `dirLen+1+maxNameLen+1`.

### Stat (zero-alloc metadata)

```go
var meta osza.FileMeta
pathBuf := make([]byte, len(path)+1)
copy(pathBuf, path)
if err := osza.LstatAt(pathBuf, len(path), &meta); err != nil { ... }
```

`FileMeta.Name()` always returns `""`; paths live in caller buffers. See `stat/join.go` for `LstatJoin` / `StatJoin` layout.

### Full listing with grow-and-retry

`osza` does not grow buffers internally. `ReadDir` fills **`entries[0:len(entries)]`** ( **`len(entries)`**, not `cap(entries)`). Use `make([]osza.Entry, N)` with **len = N**, or `entries = entries[:cap(entries)]` before the first call if you rely on capacity alone.

On **`ErrOverflow`**, double **`len(entries)`** and **`len(nameBuf)`**, then call `ReadDir` again from the start. On **`ErrPathBuffer`**, double **`len(joinBuf)`** and re-read from the start (partial `entries[:n]` is not resumable). Repeat until `err == nil`. Integrators choose their own safety caps.

Reference implementation: [`example/read_dir_retry/main.go`](example/read_dir_retry/main.go).

## Godoc

Run `go doc github.com/lbe/sfpg-go/internal/osza` (or `-u` for readdir). Package comments on `osza` and `ReadDir` on the facade are the primary API docs; `readdir` documents implementation and platform split.

## Tests

| Location                                 | Role                                             |
| ---------------------------------------- | ------------------------------------------------ |
| `osza_test.go`                           | Facade smoke (ReadDir, stat, sentinels)          |
| `readdir/readdir_test.go`                | Overflow, unicode, os.ReadDir parity, retry      |
| `readdir/readdir_checkptr_test.go`       | Unix checkptr / concurrent grow-retry canary     |
| `readdir/readdir_symlinks_linux_test.go` | Linux symlink ReadDir parity                     |
| `stat/stat_test.go`                      | `*At`/`*Join`, joinPacked, parity (linux/darwin) |
| `stat/stat_windows_test.go`              | Same parity + alloc tests on Windows             |
| `*_benchmark_test.go`                    | `ReportAllocs` / throughput (not CI gates)       |
| `example/read_dir_retry/`                | Grow-and-retry integrator reference              |

```bash
go test ./internal/osza/...
```

## Adding a new submodule

1. Create `internal/osza/<name>/` with `package <name>`, build tags as needed.
2. Add tests in that directory (`<name>_test.go` or external test package).
3. Add `internal/osza/<name>_export.go`: type aliases, vars, and forwarding funcs.
4. Extend `doc.go` package comment (one line + link to this README).
5. Add a subsection under **§11.1** in `docs/ARCHITECTURE.md` and a row in the **§4.1** package table.

Conventions: caller-provided buffers, sentinel errors suitable for `errors.Is`, no silent truncation without a documented sentinel.
