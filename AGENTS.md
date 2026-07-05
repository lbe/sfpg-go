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
- Before committing, **Read** the current value from `version.go`, include `version.go` in the commit if it has changed, and include that exact value in the commit message.

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
  - **Configuration:** Uses `internal/getopt` to parse configuration with precedence: CLI flags > Environment variables > YAML config files > Defaults.
  - **Initialization:** Sets up structured logging (`slog`) with `internal/multihandler` (console + file), database pools, and starts the server.
  - **Profiling:** `internal/profiler` allows enabling CPU/Mem/Block profiling via config.

- **Web Server (`internal/server`):**
  - **Routing:** Standard `net/http` `ServeMux`. Routes are ID-based (e.g., `/gallery/{id}`, `/image/{id}`) for stability and performance.
  - **Middleware Chain:**
    1.  **Cross-Origin Protection:** Enforces strict origin checks for unsafe methods.
    2.  **Compression:** Gzip/Brotli compression (if enabled).
    3.  **HTTP Cache (`internal/cachelite`):** A custom SQLite-backed HTTP cache that stores full responses for high performance. It handles `ETag` generation and validation.
    4.  **Conditional Request:** Handles `304 Not Modified` responses.
    5.  **Logging:** Structured request/response logging.
  - **Handlers:**
    - `galleryByIDHandler`, `imageByIDHandler`: Serve main content.
    - `lightboxByIDHandler`: Interactive lightbox with loop-around navigation logic.
    - `infoBoxFolderHandler`, `infoBoxImageHandler`: HTMX-loaded details.
    - `thumbnailByIDHandler`, `folderThumbnailByIDHandler`: Serve binary image data.

- **Database Layer (`internal/gallerydb`, `internal/dbconnpool`):**
  - **Schema:** Comprehensive SQLite schema with foreign keys. Migrations are embedded (`migrations/*.sql`).
  - **Access:** `sqlc` generates type-safe Go code in `internal/gallerydb`.
  - **Connection Pooling:** `internal/dbconnpool` manages generic `*sql.DB` pools with specific SQLite pragmas (WAL mode, synchronous=NORMAL) for performance.

- **Data Processing & Infrastructure (`internal/`):**
  - **`gallerylib/importer`:** Logic to ingest file paths and populate the database (folders, files, paths).
  - **`parallelwalkdir`:** High-performance concurrent directory scanner.
  - **`workerpool`:** Dynamic worker pool that scales based on queue depth to process background tasks (importing, thumbnail generation).
  - **`cachelite`:** Database-backed HTTP response cache. Features asynchronous writes via the unified WriteBatcher and post-flush LRU eviction to avoid blocking request processing.
  - **`writebatcher`:** High-throughput batched database writer for efficiently queuing and executing SQL insert/update operations, with a persistent on-disk overflow queue (`dque`) for burst absorption and crash recovery.
  - **`dque`/`flock`/`errors`:** Segment-backed persistent FIFO overflow queue, cross-platform file locking, and error sentinels used by `writebatcher`'s overflow path.
  - **`gensyncpool`:** Generic, type-safe wrappers around `sync.Pool` that enforce object resetting to prevent state leakage.
  - **`coords`:** Utility for parsing geographic coordinates (likely for EXIF data).
  - **`gen-test-files`:** Development utility for generating synthetic test files and directory structures.
  - **`testutil`:** Shared testing helpers and mocks.

- **Frontend Architecture:**
  - **Hypermedia-Driven:** Uses `htmx` to swap parts of the page (e.g., opening a folder info box) without full reloads.
  - **Templates:** `ui.go` manages parsing. `layout.html.tmpl` defines the base shell.

## Critical Development Patterns

### Handler Testing Pattern

When testing handlers, use `hqOverride` to inject mock query behavior:

- Handlers use `HandlerQueries` interface for testability
- Constructed via dependency injection in `buildHandlers()`
- Never call handlers directly - use the server's router

### Database Access Pattern

Always get connections from the pool:

```go
conn, err := app.dbRwPool.Get(ctx)
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

Defaults → Database → Environment Variables → CLI flags (highest)

### Hyperscript Integration

Before integrating Hyperscript changes, ALWAYS validate:

```bash
make validate-hyperscript
# Or specific file:
go run ./scripts/validate-hyperscript.go web/templates/gallery.html
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
- ❌ DON'T: Spawn separate test servers (causes OOM)
- ❌ DON'T: Use Python scripts (use bash or Perl)
- ❌ DON'T: Make assumptions without verification
- ❌ DON'T: Use `strings.Contains` on HTTP responses (parse HTML first)

## Development Directives

- **HTML content checks:** When checking HTML content (tests or reviews), use structural HTML assertions (no string/bytes contains checks) per [internal/server/methodology-html-content-test-writing.md](internal/server/methodology-html-content-test-writing.md).

- **Run `gofmt` and `goimports` and `go build -o /dev/null .`** on go files and `prettier` on html.tmpl files immediately after edits.

- **Commit Message Workflow:** Use file-based commits: `git commit -F tmp/commit_message.txt`.

## Documentation Reference

This AGENTS.md file is a concise guide for AI agents. For detailed information, see:

### Core Documentation

- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** - Comprehensive architecture documentation with diagrams, data flows, and design patterns
- **[docs/SERVER_DEEP_DIVE.md](docs/SERVER_DEEP_DIVE.md)** - In-depth server architecture and component analysis
- **[CLAUDE.md](CLAUDE.md)** - Project instructions for Claude Code (includes common commands, high-level architecture, forbidden practices)
- **[README.md](README.md)** - Project overview, features, and setup instructions
- **[DEPLOYMENT.md](DEPLOYMENT.md)** - Deployment and production configuration guide
- **[ENV_CONFIGURATION.md](ENV_CONFIGURATION.md)** - Environment variable reference

### References

- **[references/htmx-referencd.md](references/htmx-referencd.md)** - HTMX patterns and usage in this project
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
