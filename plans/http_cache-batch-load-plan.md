# HTTP Cache Batch Load Plan

Date: 2026-03-11

## Notes

- Sub-agents are not available in this environment. I will proceed single-agent unless you provide a different mechanism. I will keep changes tightly scoped to reduce context rot.
- Execution will follow this plan exactly. If anything unplanned arises, I will stop and notify you.
- TDD: each feature slice will be validated with tests before implementation when practical.
- Use `make format` (covers gofmt, goimports, prettier).
- Also run `make lint` and `make test` as requested.
- Test workflow: do not pipe `go test` directly; capture output to file per AGENTS.md if direct `go test` is needed.

## Plan

### Pre-flight: Architecture and Practices Check

**Dependencies:** none  
**Goals:** confirm alignment with existing architecture and operational practices.

1. Re-skim `docs/ARCHITECTURE.md` and cachepreload packages to ensure the design aligns with current patterns.
2. Confirm operational constraints: do not spawn extra servers; use existing dev server on port 8083 and `curl` if manual verification is needed.
3. If any conflicts are found, stop and notify you before proceeding.

### Step 1: Add DB module_state support (migration + data access)

**Dependencies:** none  
**Goals:** DB state for discovery active/inactive, available to server and CLI.

1. Add migration to create `module_state` table.
2. Add queries for `SetModuleActive`, `GetModuleActive`.
3. Add small service or helper layer to read/write module state (prefer in config/service or new `module_state` service).
4. Add unit tests for module state access.
5. Run format/lint/test.
6. Commit.

### Step 2: Wire discovery lifecycle to DB state

**Dependencies:** Step 1  
**Goals:** discovery sets `module_state` active/inactive on start/finish.

1. Add `moduleStateService *modulestate.Service` to `App`.
2. Construct `app.moduleStateService = modulestate.NewService(app.dbRwPool)` during app init (in `database.Init` or where DB pools are wired). Ensure it exists before discovery can run.
3. Wrap `app.walkImageDir()`: call `SetActive(ctx, "discovery", true)` at start, `defer SetActive(ctx, "discovery", false)` for finish. Do **not** modify `files.WalkDeps` or `files.WalkImageDir`.
4. Write tests for discovery lifecycle updating module state (e.g. start discovery, assert `IsActive("discovery")` true; wait for finish, assert false).
5. Run format/lint/test.
6. Commit.

### Step 3: Add batch load targets query (folders first ordering)

**Dependencies:** Step 1  
**Goals:** single query returning HTMX-only targets (gallery/info folder/info image/lightbox), folders first.

1. Add tests for query output structure and ordering.
2. Update `custom.sql` with `GetBatchLoadTargets` (ordered by route type then path).
3. Use `sqlc` for generated code updates **except** the custom query implementation, which will be added to `custom.sql.go`.
4. Ensure `custom.sql.go` updates include:
   - Prepared statement initialization and teardown
   - Interface exposure (HandlerQueries / relevant interfaces)
   - Method implementation
5. Run `make format`, `make lint`, `make test`.
6. Commit.

### Step 4: Implement Batch Load Manager (core engine)

**Dependencies:** Steps 1, 3  
**Goals:** bounded worker pool, skip cached, internal requests, metrics, running guard.

1. Add tests for:
   - skip when discovery active (via DB state)
   - skip cached entries
   - 404 counted as failure
   - totals vs scheduled vs completed
2. Implement `internal/server/cachebatch` with:
   - bounded concurrency `maxWorkers = min(8, NumCPU)`
   - queue size default
   - metrics (total enumerated, scheduled, completed, failed, skipped, in-flight)
3. Run format/lint/test.
4. Commit.

### Step 5: Metrics collector integration + dashboard card

**Dependencies:** Step 4  
**Goals:** dashboard shows batch load metrics and status.

1. Add tests for metrics collector snapshot with batch load source.
2. Add `CacheBatchLoadMetrics` + source interface to collector.
3. Add adapter in `metrics_adapters.go`.
4. Update dashboard template with new card and progress bar.
5. Run `prettier` on template, then format/lint/test.
6. Commit.

### Step 6: Server endpoint + hamburger menu trigger

**Dependencies:** Steps 1, 4, 5  
**Goals:** authenticated POST endpoint triggers batch load; toast response; discovery guard.

1. Add handler tests: auth required, blocked when discovery active, starts run when idle.
2. Add server handler + route `POST /server/cache-batch-load`.
3. Add toast template for success/blocked.
4. Add hamburger menu item.
5. Run `prettier` (templates), format/lint/test.
6. Commit.

### Step 7: CLI support

**Dependencies:** Steps 1, 3, 4  
**Goals:** `--cache-batch-load` flag, exit codes, summary output.

1. Add CLI tests for:
   - success (exit 0)
   - blocked (exit 2, discovery active)
   - error (exit 1)
2. Add CLI flag parsing in `getopt`.
3. Add `InitForBatchLoad` in server app.
4. Wire main to execute batch load and exit.
5. Run format/lint/test.
6. Commit.

### Step 8: Documentation update in ARCHITECTURE.md

**Dependencies:** Step 5  
**Goals:** document default-theme-only cache warm limitation.

1. Update `docs/ARCHITECTURE.md` with limitation note.
2. Commit.

### Step 9: Final verification

**Dependencies:** Steps 1-8  
**Goals:** ensure repository clean and all tests pass.

1. Run `make format`, `make lint`, `make test`.
2. If failures occur, stop and notify you.
3. Commit only if changes are required to fix failing tests (otherwise no commit).
