# Sign-off: osza zero-allocation stat

**Plan:** `plans/osza-stat-plan.md` (P8)  
**Design:** `plans/osza-stat-design.md` (read-only spec)  
**Branch:** `dev`  
**Host:** Linux (`linux/amd64`, Intel Xeon E5-2680 v3 @ 2.50GHz)  
**Signed:** P8 worker gate run recorded below.

## Success criteria (design)

| Criterion                                                                                                                                                                      | Status       | Evidence                                       |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------ | ---------------------------------------------- |
| Facade exports match design facade table (`FileMeta`, `LstatAt`, `StatAt`, `LstatJoin`, `StatJoin`, `ErrPathBuffer`, `ErrNilFileMeta`; no `Lstat`/`Stat` string-path wrappers) | Pass         | `internal/osza/stat_export.go`                 |
| `-benchmem`: **0 allocs/op** on all osza stat benchmarks (reused buffers)                                                                                                      | Pass (Linux) | § Gate 3 and § Gate 5 below; osza names only   |
| Parity vs `os` on file, directory, symlink (Unix) in tests (mode/size/mtime/`IsDir`; not `Name()`)                                                                             | Pass         | `go test ./internal/osza/stat/...` (gate 1)    |
| `FileMeta.Name()` returns `""`                                                                                                                                                 | Pass         | `TestFileMetaNameEmpty`, `TestStatFacadeSmoke` |
| Gates green on Linux                                                                                                                                                           | Pass         | § Standard gates below                         |

### Path naming (design)

- Hot-path `*At`/`*Join` fill metadata only; paths live in caller `pathBuf`/`joinBuf`.
- `FileMeta.Name()` is always `""`; discovery/walk path labels are **out of scope** for `FileMeta` (future callers use their own buffers).

### Documentation gap (intentional, P8 scope)

- `docs/ARCHITECTURE.md` §4.1 and §11.1.11 updated for **stat** only.
- `internal/osza/doc.go` and `internal/osza/README.md` were **not** changed in P8; they may still emphasize `ReadDir` only. Treat this sign-off and `plans/osza-stat-design.md` as authoritative for stat until those files are updated separately.

### Windows benchmark waiver

Per user directive and plan P7/P8: **no Windows host is required** to mark this work complete on Linux Pi/dev.

- **Cross-compile:** `GOOS=windows GOARCH=amd64 go test -c ./internal/osza/stat/...` passes on Linux (gate 7).
- **0 allocs/op on Windows stat benches:** not executed on a Windows host for this sign-off; waiver recorded here. Re-verify on Windows before relying on Windows hot-path alloc guarantees.

## Standard gates (plan §2.5)

Commands run from repo root; full log: `tmp/p8_gates_full.txt`.

| #   | Command                                                                      | Result                                              |
| --- | ---------------------------------------------------------------------------- | --------------------------------------------------- |
| 1   | `go test ./internal/osza/stat/...`                                           | **PASS** (`ok`, 0.016s)                             |
| 2   | `go test -race ./internal/osza/stat/...`                                     | **PASS** (`ok`, 1.053s)                             |
| 3   | `go test ./internal/osza/stat/... -bench=. -benchmem -count=1`               | **PASS** — osza benches **0 allocs/op** (see table) |
| 4   | `go test ./internal/osza/ -run TestStatFacadeSmoke`                          | **PASS** (`ok`, 0.007s)                             |
| 5   | `go test ./internal/osza/ -bench=BenchmarkFacade -benchmem -count=1`         | **PASS** — `BenchmarkFacadeLstatAt` **0 allocs/op** |
| 6   | `go build -o /dev/null ./internal/osza/stat/...`                             | **PASS**                                            |
| 7   | `GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/osza/stat/...` | **PASS**                                            |
| 8   | `go build -o /dev/null .`                                                    | **PASS**                                            |

### Gate 3 — osza benchmark allocs (Linux)

| Benchmark                | allocs/op |
| ------------------------ | --------- |
| `BenchmarkLstatAt`       | 0         |
| `BenchmarkStatAt`        | 0         |
| `BenchmarkStatAtSymlink` | 0         |
| `BenchmarkLstatJoin`     | 0         |
| `BenchmarkStatJoin`      | 0         |

Comparison benches (`BenchmarkOS*`) are excluded from the 0-alloc requirement per plan §2.

### Gate 5 — facade benchmark allocs (Linux)

| Benchmark                | allocs/op |
| ------------------------ | --------- |
| `BenchmarkFacadeLstatAt` | 0         |

`BenchmarkFacadeOSLstat` excluded (comparison only).

## Worker verify (plan §1.2)

| Step                      | Result                                                         |
| ------------------------- | -------------------------------------------------------------- |
| Prettier on touched `.md` | **PASS** (`docs/osza-stat-signoff.md`, `docs/ARCHITECTURE.md`) |
| `make format-check`       | **PASS**                                                       |
| `make lint`               | N/A (no `.go` in P8)                                           |
