# refactorv3 review rules

Every review of refactorv3 work uses these rules: my own, any subagent's, and Sol's.
Each finding cites one rule ID, a `file:line`, the evidence, and the smallest fix.

## Authority and precedence

1. User instructions and the approval rules below.
2. These rules.
3. The cxuu Go skills (`go-*`). The skill named in each rule is the reference.
4. `thermo-nuclear-code-quality-review`, for structure and simplification.

When the thermo-nuclear skill suggests adding a structure (a policy object, state
machine, dispatcher, new module or helper layer), that suggestion counts only if it
deletes more code and concepts than it adds. A remedy that needs a new package, a
rename, an exported API change or a `go.mod` change is reported as "needs approval",
not as a required fix.

## Severity

- **MUST.** Blocks the task. It is a correctness bug, a broken approval rule, or a rule
  marked MUST below. It needs evidence and either a concrete failure scenario or a
  quoted rule violation. A MUST without evidence is rejected.
- **SHOULD.** A clear improvement with a stated benefit. The user or orchestrator
  decides whether to act.
- Not reported: anything gofmt or golangci-lint already enforces, speculation without a
  scenario, and preference.

"No MUST findings" is a valid and expected result.

## Rules

`Tool` names the check that catches the rule mechanically, when one exists. Reviewers
still apply the rule where the tool cannot see the problem. `scripts/v3-check.sh` runs
every mechanical check and reports only findings that `dev` does not already have. Lint
rules are in `.golangci-v3.yml`.

### Approval and scope

| ID  | Rule                                                                                                                                                             | Severity | Tool                      |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | ------------------------- |
| R1  | No package added, removed or renamed without user approval. v2's packages count as additions.                                                                    | MUST     | `v3-check.sh` (packages)  |
| R2  | No exported API change in a package that exists in `dev` (addition, removal, rename, signature) without user approval.                                           | MUST     | `v3-check.sh` (`apidiff`) |
| R3  | No `go.mod` dependency change without user approval.                                                                                                             | MUST     | `v3-check.sh` (`go.mod`)  |
| R4  | Observable behavior matches the v2 spec, except for the approved changes: new pHash, Wuffs decode, ExifTool metadata and IPTC, malformed JPEG failing in decode. | MUST     | `v3-parity.sh`            |

### Dead and duplicated code

| ID  | Rule                                                                                                                                                                                                                                                                                                                     | Severity | Tool                                                 |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------- | ---------------------------------------------------- |
| R5  | All production code is reachable from `.` or `./cmd/sfpg-go-dashboard`. A test caller is not a caller. No compatibility wrappers, aliases, fallbacks or test-only exports. Superseded code and its tests are deleted in the same workstream. No comments recording what was removed or replaced; git history holds that. | MUST     | `deadcode`, `staticcheck -tests=false -checks U1000` |
| R6  | Change in place. No new function, type or SQL query beside an existing one that does the same job (for example `InsertIPTCKeywordForFile` beside `InsertIPTCKeyword`).                                                                                                                                                   | MUST     | review                                               |
| R7  | No reimplementation of the standard library, `golang.org/x/` or an existing project helper: `os.ReadFile`, `io.ReadAll`, `slices`, `maps`, `sync.OnceValue`, `x/sync/semaphore`, `x/sync/errgroup`.                                                                                                                      | MUST     | review                                               |

### Abstraction (`go-interfaces`, `go-packages`, `go-functional-options`)

| ID  | Rule                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       | Severity                                | Tool                        |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------- | --------------------------- |
| R8  | An interface exists only when it has a real need: more than one production implementation, or a consumer that must not depend on a concrete external resource. It is defined in the consumer package and kept small. Functions return concrete types.                                                                                                                                                                                                                                                                                                                                                                                                                                      | MUST                                    | `ireturn`, `interfacebloat` |
| R9  | Production code does not change shape for tests: no package-level variables that tests reassign, no hook or seam fields, no `production*` globals, no options or exported constructors used only by tests. Allowed: real dependencies passed in as constructor arguments or unexported fields, even when tests pass a different value (a clock where timing is behavior, a directory, an `io.Reader`, a limit); `testing/synctest` for timers and goroutines; unexported identifiers in the package's `_test.go` files; the existing `*.testSeams.*` pattern in the server handlers, with no new fields. The test: would the parameter exist if there were no tests? If not, it is a seam. | MUST                                    | `gochecknoglobals`, review  |
| R10 | No pass-through wrappers, identity types, constructor chains (`New` → `newWithX` → `newWithY`), builders or factories. Use one constructor with plain arguments, or a config struct. Use functional options only when there are several optional settings with real callers.                                                                                                                                                                                                                                                                                                                                                                                                               | MUST for pass-through; otherwise SHOULD | review                      |
| R11 | No package whose only reason to exist is breaking an import cycle, and no `util`, `common` or `helpers` packages. Resolve a cycle by, in order: (1) moving the type to its owner; (2) a consumer-defined interface (R8); (3) merging packages that are one concept. If none works, a shared package is allowed only with the user's approval; it must be named for and own one coherent domain concept, and its design must state why each remedy fails.                                                                                                                                                                                                                                   | MUST (fix needs approval, R1)           | review                      |

### Errors (`go-error-handling`)

| ID  | Rule                                                                                                                                                                                                                                                       | Severity | Tool                            |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | ------------------------------- |
| R12 | No swallowed errors. No `_ =` on an error without a comment saying why. No logging an error and also returning it. Use `%w` when callers may inspect the cause and `%v` otherwise; never compare error strings. No `panic` for errors a caller can expect. | MUST     | `errcheck`, `errorlint`, review |
| R13 | Error strings are lowercase, have no trailing punctuation and no "failed to" prefix, and don't repeat context the caller adds.                                                                                                                             | SHOULD   | review                          |

### State and construction (`go-declarations`, `go-defensive`)

| ID  | Rule                                                                                                                                                                                                                                                                                        | Severity                                                | Tool               |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------- | ------------------ |
| R14 | No mutable package-level state. Sentinel errors, compiled regexps and constant tables are fine. Make the zero value useful when that is cheap.                                                                                                                                              | MUST                                                    | `gochecknoglobals` |
| R15 | No sets of booleans tracking ownership or lifecycle, and no pointer or `sql.Null*` fields meaning "unset". Model the state directly or restructure so the flags disappear.                                                                                                                  | SHOULD; MUST if it causes a bug                         | review             |
| R16 | In code v3 adds or rewrites, `database/sql` (including `sql.Null*`), `gallerydb` and `dbconnpool` types stay inside the database packages. Database row types are not domain payloads. `depguard` enforces this for `discovery`, `memlimit`, `metadata`, `parallelwalkdir` and `thumbnail`. | MUST                                                    | `depguard`, review |
| R17 | Logic lives in the package that owns the concept. No feature-specific checks in shared code.                                                                                                                                                                                                | SHOULD; MUST if it creates an import cycle or duplicate | review             |

### Concurrency (`go-concurrency`, `go-context`)

| ID  | Rule                                                                                                                                                                                                               | Severity                                    | Tool                            |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------- | ------------------------------- |
| R18 | Every goroutine has an owner, a way to stop and a wait. Pass `context.Context` as the first argument and never store it in a struct.                                                                               | MUST                                        | `containedctx`, `-race`, review |
| R19 | Use a mutex or a plain loop where a channel adds nothing. No hand-rolled semaphores, pools, once-guards or wait groups where `sync` or `x/sync` has one.                                                           | MUST                                        | review                          |
| R20 | No repeated `ctx.Err()` checks between calls that already honor the context. No checks for conditions that cannot happen, and no nil checks on values that are never nil. Check at blocking points and loop heads. | SHOULD; MUST when it hides the control flow | review                          |

### Product hygiene

| ID  | Rule                                                                                                                                                                                                                                                 | Severity                           | Tool                         |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------- | ---------------------------- |
| R21 | No evidence, measurement or debug code in the product: `Stats`/`snapshot` types, counters nothing reads, timing fields kept for a report. Production metrics that the dashboard or logs actually use are fine.                                       | MUST                               | review                       |
| R22 | Names follow `go-naming`: no stutter (`thumbnail.ThumbnailGenerator`), no `Get` prefix, no getters or setters without logic, no `Manager`, `Impl` or `Helper` suffixes, short names in short scopes.                                                 | SHOULD                             | review                       |
| R23 | Comments say why, not what. No narration of the code, no restating the name, no references to tasks, plans, phases or "v2". Comments about removed or replaced code fall under R5. Every exported identifier has a doc comment (`go-documentation`). | SHOULD                             | review                       |
| R24 | Keep control flow flat: early return, no `else` after `return`, no deep nesting. A file that grows past 1000 lines needs a reason.                                                                                                                   | SHOULD                             | `nestif`, `gocognit`         |
| R25 | No per-item allocation in hot loops, and no regression against the v2 benchmarks or the run24/run26 measurements (`go-performance`).                                                                                                                 | MUST if measured; otherwise SHOULD | `benchstat`, acceptance runs |

### Tests (`go-testing`)

| ID  | Rule                                                                                                                                                                                                                                                                                                                                                                                                                                      | Severity                                   | Tool                      |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------ | ------------------------- |
| R26 | Tests assert behavior through the package API. No tests that only check a seam, a mock's call sequence or an unexported helper the API already covers. No duplicate tests.                                                                                                                                                                                                                                                                | MUST for seam-only tests; otherwise SHOULD | review                    |
| R27 | Tests do not sleep or depend on wall-clock time. Use events, channels or `sync` to wait, `testing/synctest` for timers and timeouts, `t.TempDir`, and `t.Context`. Real time is allowed only where `synctest` cannot reach: code that waits on another process, real network I/O, or a runtime outside the bubble. There, wait by polling a condition with a deadline, never a fixed sleep. Tests must pass under `-race` and `-count=3`. | MUST                                       | `-race`, `-count`, review |
| R28 | Use table-driven tests for repeated cases, `t.Helper` in helpers, and failure messages that show got and want. `go-cmp` is not a dependency; adding it needs approval (R3). Keep the unit, integration and e2e split from AGENTS.md.                                                                                                                                                                                                      | SHOULD                                     | review                    |

## Mapping to the user's starting list

- Over-abstraction and premature interfaces: R8, R10, R11.
- Non-idiomatic error handling: R12, R13.
- Heavy constructors and hidden state: R9, R10, R14, R15.
- Concurrency mismatches: R18, R19, R20.
