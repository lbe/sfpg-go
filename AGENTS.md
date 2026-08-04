# My Zeroth Law: The User is in Control

My primary directive, which overrides all other standard operating procedures or internal workflows, is to follow the user's explicit instructions.

I **WILL** perform actions in order to fulfill instructions that the user has provided to me without requiring an additional approval.

I **WILL** perform repeated action when instructed to do so by the user without requiring an additional approval.

I will **NEVER** perform any action—including modifying a file, running a command, or proceeding to the next step in a workflow—for which I have not been instructed without first proposing the action and receiving a clear, affirmative approval from the user.

My process is:

1.  **Analyze and Propose.**
2.  **STOP. Await Approval.**
3.  **Act ONLY after approval.**

This is my fundamental operating principle. There are no exceptions.

---

### Hard Rules

- **Do not manually edit `version.go`.** Never use Edit or Write on `version.go`.
- **Do not stop, restart, or interfere with `air`.**
- `version.go` is managed automatically by `scripts/gen_version.sh` (run via `go generate` / `air` rebuilds).
- Before committing, **Read** the current value from `version.go`, include `version.go` in the commit if it has changed, and put that exact value in the commit message as a **footer line** `Version: X.Y.Z` — never as `(vX.Y.Z)` in the subject. See `plans/pi-runbook-sfpg-go.md` § Commit message format.

## Project Learnings

- **`air` can fail silently.** If code changes do not seem to have any effect, `air` might be failing to rebuild the application due to compilation errors. If you encounter this problem, notify the user.
- **`air` runs the dev server on port 8083.** The `.air.toml` config uses port 8083 (not the default 8081) to avoid conflicts. It also sets `SEPG_SESSION_SECURE=false` for local HTTP development.
- **Prefer curl over manual browser testing.** Use curl for end-to-end testing whenever possible to minimize manual user testing. This is faster, reproducible, and doesn't require explaining UI interactions. See examples below.
- **End-to-end testing with curl:** The login endpoint is POST-only (GET returns 400). To test authenticated flows:

  ```shell
  # 1. Check if server is running (returns HTML gallery page)
  curl -s http://localhost:8083/gallery/1 | head -5
  
  # 2. Log in (default credentials: admin / admin)
  # The login form is loaded via HTMX in the gallery page modal
  curl -s -X POST http://localhost:8083/login \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "username=admin" \
    -d "password=admin" \
    -c /tmp/cookies.txt
  
  # 3. Access authenticated endpoints (e.g., dashboard)
  curl -s http://localhost:8083/dashboard \
    -b /tmp/cookies.txt | head -10
  
  # 4. Trigger discovery (requires auth)
  curl -s -X POST http://localhost:8083/server/discovery \
    -b /tmp/cookies.txt
  
  # 5. Check stats via SSE stream (requires auth)
  curl -s http://localhost:8083/dashboard/sse \
    -b /tmp/cookies.txt
  ```

- **`web-testsuite` (`e2eweb`) and login rate limits:** `TestMain` sets `login_rate_limit_per_ip=0` before snapshotting config so many admin logins from one IP do not hit 429 on shared dev `air`. Restart tests (`TestRestart`) use `waitForServerDown` then `waitForServer` so a 200 from the dying process is not mistaken for the new one.

## Project Overview

`sfpg-go` is a self-hosted photo gallery web application written in Go. It serves images from a local directory, generates thumbnails on the fly, and provides a responsive web interface for browsing with a password-protected administrative configuration. The application is designed to be performant, using concurrency for background tasks, aggressive caching strategies, and a hypermedia-driven frontend architecture to minimize client-side JavaScript.

**Core Technologies:**

- **Backend:** Go (1.26+)
- **Database:** SQLite (ncruces/go-sqlite3), used for storing thumbnail data, directory tile information, application configuration, and HTTP cache entries.
- **Frontend:**
  - Go HTML Templates (`html/template`) for server-side rendering.
  - `htmx` for handling UI interactivity with AJAX requests that swap HTML content.
  - `hyperscript` for lightweight client-side scripting.
  - `daisyUI` and `tailwindcss` for styling and UI components.
- **Concurrency:** Extensive use of goroutines, channels, `errgroup`, and dynamic worker pools.

## Key Architectural Components

The application follows a structured design with a clear separation of concerns, leveraging internal packages for modularity.

- **Application Entrypoint (`main.go`, `internal/getopt`):**
  - **Configuration:** Uses `internal/getopt` to parse configuration with precedence: Defaults → Database → YAML files → CLI/Env (CLI flags override environment variables for the same setting).
  - **Initialization:** Sets up structured logging (`slog`) with `internal/multihandler` (console + file), database pools, and starts the server.
  - **Profiling:** `internal/profiler` allows enabling CPU/Mem/Block profiling via config.

- **Web Server (`internal/server`):**
  - **Routing:** Standard `net/http` `ServeMux`. Routes are ID-based (e.g., `/gallery/{id}`, `/image/{id}`) for stability and performance.
  - **Middleware Chain (outermost → innermost):**
    1.  **Logging:** Structured request/response logging.
    2.  **HTTP Cache (`internal/cachelite`):** A custom SQLite-backed HTTP cache that stores full responses for high performance. It handles `ETag` generation and validation.
    3.  **Cross-Origin Protection:** Strict same-origin check for unsafe methods via `http.CrossOriginProtection`.
    4.  **Mux / Handler:** Authentication is applied selectively to protected routes inside the mux.
  - **Handlers:**
    - `GalleryByID`, `ImageByID`: Serve main content.
    - `LightboxByID`: Interactive lightbox with loop-around navigation logic.
    - `InfoBoxFolder`, `InfoBoxImage`: HTMX-loaded details.
    - `ThumbnailByID`, `FolderThumbnailByID`: Serve binary image data.
    - `Config*`, `Dashboard*`, `Server*`, `Theme*`, `Menu*`: Admin and utility handlers.

- **Database Layer (`internal/gallerydb`, `internal/dbconnpool`, `internal/server/database`):**
  - **Schema:** Comprehensive SQLite schema with foreign keys. Migrations are embedded (`migrations/*.sql`). Thumbnail blobs live in a separate `DB/thumbs/thumbs.db`.
  - **Access:** `sqlc` generates type-safe Go code in `internal/gallerydb`.
  - **Connection Pooling:** `internal/dbconnpool` manages generic `*sql.DB` pools; `internal/server/database` wires them with WAL mode and SQLite-specific pragmas.

- **Data Processing & Infrastructure (`internal/`):**
  - **`gallerylib/importer`:** Logic to ingest file paths and populate the database (folders, files, paths).
  - **`parallelwalkdir`:** High-performance concurrent directory scanner.
  - **`workerpool`:** Dynamic worker pool that scales based on queue depth to process background tasks (importing, thumbnail generation).
  - **`cachelite`:** Database-backed HTTP response cache. Features asynchronous writes via the unified WriteBatcher and post-flush LRU eviction to avoid blocking request processing.
  - **`writebatcher`:** Generic high-throughput batched database writer used by the unified server batcher, with a persistent on-disk overflow queue (`dque`) for burst absorption and crash recovery.
  - **`dque`/`flock`/`errors`:** Segment-backed persistent FIFO overflow queue, cross-platform file locking, and error sentinels used by `writebatcher`'s overflow path.
  - **`gensyncpool`:** Generic, type-safe wrappers around `sync.Pool` that enforce object resetting to prevent state leakage.
  - **`coords`:** Utility for parsing geographic coordinates (likely for EXIF data).
  - **`gen-test-files`:** Development utility for generating synthetic test files and directory structures.
  - **`testutil`:** Shared testing helpers and mocks.

- **Frontend Architecture:**
  - **Hypermedia-Driven:** Uses `htmx` to swap parts of the page (e.g., opening a folder info box) without full reloads.
  - **Templates:** `internal/server/ui/templates.go` and `render.go` manage parsing and rendering. `layout.html.tmpl` defines the base shell.

## Critical Development Patterns

### Handler Testing Pattern

Handlers depend on the `HandlerQueries` interface defined in `internal/server/interfaces` for testability:

- Unit tests construct handler groups directly and inject `fakeHandlerQueries` (see `internal/server/handlers/helpers_test.go`).
- Integration tests can replace the live queries via `app.InfrastructureService.testSeams.HandlerQueries`.
- Handler groups are wired in `App.buildHandlers()` (called from `server.go`).
- For routing/integration tests, use the server's router rather than calling handlers directly.

### Test Seams (`testseams.go`)

Optional test doubles live in `internal/server/testseams.go` and are wired through unexported `testSeams` fields on `App` and embedded managers. Zero value means production behavior.

- **App lifecycle:** `app.testSeams.Serve`, `app.testSeams.LoadConfig`, `app.testSeams.GetGalleryStatistics`, etc.
- **Infrastructure:** `infra.testSeams.*` or `app.InfrastructureService.testSeams.*` (e.g. `HandlerQueries`, `RecreatePoolsWithConfig`)
- **Runtime:** `m.testSeams.*` or `app.RuntimeManager.testSeams.*` (e.g. `BeforeListen`, `ExecCommand`, `Exit`)
- **Handlers:** `hm.testSeams.BuildHandlers` or `app.HandlerManager.testSeams.BuildHandlers`

Do **not** add `testHook*` fields to production structs or use promoted `app.testHook*` in tests. Root test files live under `internal/server/*_test.go` (23 files).

### Database Access Pattern

Always get connections from the pool:

```go
conn, err := app.dbRwPool.Get()
if err != nil { /* handle error */ }
defer app.dbRwPool.Put(conn)
```

### HTMX Partial Routes

Many routes are HTMX partials (won't work via direct URL access):

- Check `Hx-Request` header to determine request type
- Full page renders have `<body>` tag; partials don't
- Use `hx-swap-oob="outerHTML"` for out-of-band updates
- Return 200 for validation errors (not 400) so HTMX processes response

### Configuration Precedence

Defaults → Database → YAML files → CLI/Env (CLI flags override environment variables for the same setting)

### Hyperscript Integration

Before integrating Hyperscript changes, ALWAYS validate:

```bash
make validate-hyperscript
# Or specific file:
go run ./scripts/validate-hyperscript/ web/templates/gallery.html.tmpl
```

**After validating, also verify the RENDERED output with curl** — the validator checks
source syntax only. Go's `html/template` may HTML-escape `<` characters inside
`<script type="text/hyperscript">` blocks (it does not recognize `text/hyperscript` as
a JavaScript MIME type). Always check for `&amp;lt;` in the rendered HTML:

```bash
curl -s http://localhost:8083/gallery/1 | grep -c '&amp;lt;'
```

See [references/hyperscript-reference.md](references/hyperscript-reference.md) for Hyperscript patterns.

### Hyperscript Style Conventions

- Use `@attr` syntax (e.g., `element@data-id`) instead of `getAttribute('data-id')`.
- Use `exists` instead of `is not null` (e.g., `if element exists`).
- Use `matches` instead of `classList.contains` (e.g., `if element matches .hidden`).
- Use `halt the event` instead of bare `halt`.
- Inside `<script type="text/hyperscript">` blocks, use `querySelectorAll('.class')`
  instead of `<.class/>` to avoid Go template escaping of `<` characters.

### Template Validation

Before or after template edits, run the fast template integrity gate:

```bash
make validate-templates
```

This runs `TestTemplateRendering` plus Hyperscript validation and catches malformed HTML/template attribute structure early.

## Testing Workflow (CRITICAL)

NEVER run tests with grep pipes directly - this leads to multiple execution if you need to grep with more than regex.

```bash
# ❌ WRONG (slow, runs tests multiple times):
go test ./... | grep FAIL
go test ./... | grep PASS

# ✅ RIGHT (runs Go tests once):
mkdir -p tmp
make test-all > ./tmp/test_output.txt 2>&1
grep -E "FAIL|PASS|ERROR" ./tmp/test_output.txt
```

See [references/tdd_process.md](references/tdd_process.md) and [references/methodology-html-content-test-writing.md](references/methodology-html-content-test-writing.md) for detailed testing guidance.

## Building and Running

The project is configured to use `air` for live reloading during development.

**Development (Recommended):**

1.  **Install `air`:**
    ```shell
    go install github.com/cosmtrek/air@latest
    ```
2.  **Run the application:**
    ```shell
    air
    ```
    `air` builds to `./tmp/main`.

**Production/Manual:**

1.  **Build:** `go build -o sfpg-go .`
2.  **Run:** `./sfpg-go` (Port 8081 default).

## Agent Operating Principles

**For this project:**

- ✅ DO: Run `go build -o /dev/null .` to verify clean builds
- ✅ DO: Test against the running dev server (localhost:8083)
- ✅ DO: Let `air` auto-rebuild when you save files
- ❌ DON'T: Use Python scripts (use bash or Perl)
- ❌ DON'T: Make assumptions without verification
- ❌ DON'T: Use `strings.Contains` on HTTP responses (parse HTML first)

## Development Directives

- **HTML content checks:** When checking HTML content (tests or reviews), use structural HTML assertions (no string/bytes contains checks) per [references/methodology-html-content-test-writing.md](references/methodology-html-content-test-writing.md).

- **Run `gofmt` and `goimports` and `go build -o /dev/null .`** on go files and `prettier` on html.tmpl files immediately after edits.

- **Commit Message Workflow:** Write `tmp/commit_message.txt` then `git commit -F tmp/commit_message.txt`. Required shape:

  ```text
  <type>(optional-scope): <imperative summary>

  <body>

  Version: <exact value from version.go>
  ```

  **Forbidden:** version in the subject (`(v0.10.124)`, `v0.10.124`). Plans that show `(v<version>)` in the subject are wrong — follow this format.

## Documentation Reference

This AGENTS.md file is a concise guide for AI agents. For detailed information, see:

### Core Documentation

- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** - Comprehensive architecture documentation with diagrams, data flows, and design patterns
- **[docs/SERVER_DEEP_DIVE.md](docs/SERVER_DEEP_DIVE.md)** - Archived server package entry point (links to ARCHITECTURE.md)
- **[CLAUDE.md](CLAUDE.md)** - Project instructions for Claude Code (includes common commands, high-level architecture, forbidden practices)
- **[README.md](README.md)** - Project overview, features, and setup instructions
- **[DEPLOYMENT.md](DEPLOYMENT.md)** - Deployment and production configuration guide
- **[ENV_CONFIGURATION.md](ENV_CONFIGURATION.md)** - Environment variable reference

### References

- **[references/htmx-reference.md](references/htmx-reference.md)** - HTMX patterns and usage in this project
- **[references/hyperscript-reference.md](references/hyperscript-reference.md)** - Hyperscript patterns and examples
- **[references/methodology-html-content-test-writing.md](references/methodology-html-content-test-writing.md)** - HTML testing methodology (structural assertions, no string contains)
- **[references/tdd_process.md](references/tdd_process.md)** - Test-driven development process and workflow

### Scripts

- **[scripts/hyperscript_validation.md](scripts/hyperscript_validation.md)** - Hyperscript validation script documentation
- **[scripts/preload_curl_test.md](scripts/preload_curl_test.md)** - Cache preload testing with curl

### Quick Reference

- **Package overview:** See `Key Architectural Components` section above
- **Database schema:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#database-schema)
- **Middleware stack:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#request-middleware-stack)
- **Security model:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#security-model)
- **Testing strategy:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#testing-strategy)
