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

I **WILL** be concise in all communications.

I **WILL NOT** use bloviate AI speak.

I **WILL** be specific in my analysis and not fall back to generalization because of laziness.

I **WILL** seek concrete answers and solutions.

---

### Hard Rules

- **Do not manually edit `version.go`.** Never use Edit or Write on `version.go`.
- **Do not stop, restart, or interfere with `air`.**
- `version.go` is managed automatically by `scripts/gen_version.sh` (run via `go generate` / `air` rebuilds).
- Before committing, **Read** the current value from `version.go`, include `version.go` in the commit if it has changed, and put that exact value in the commit message as a **footer line** `Version: X.Y.Z` — never as `(vX.Y.Z)` in the subject. See `plans/pi-runbook-sfpg-go.md` § Commit message format.
- **Do not use `git commit --no-verify` (or equivalent hook-skipping flags)** unless the user explicitly instructs you to do so, or a plan the user has approved explicitly requires it. Otherwise **STOP** and notify the user.
- **Do not use `git add -f` / `git add --force` (or otherwise override `.gitignore`)** unless the user explicitly instructs or approves it. A plan alone is **not** sufficient authorization. If a plan or workflow appears to require force-adding an ignored path, **STOP** and notify the user — do not force-add.
- **No JavaScript** in application UI code. Use Hyperscript and HTMX only. **Approved exception:** password complexity validation in `web/templates/config-modal.html.tmpl` (`#config-password-validator`). Do not add further JavaScript without explicit user approval.

## Project Learnings

- **`air` can fail silently.** If code changes do not seem to have any effect, `air` might be failing to rebuild the application due to compilation errors. If you encounter this problem, notify the user.
- **`air` runs the dev server on port 8083.** The `.air.toml` config uses port 8083 (not the default 8081) to avoid conflicts. It also sets `SEPG_SESSION_SECURE=false` for local HTTP development.
- **Prefer curl over manual browser testing.** Use curl for end-to-end testing whenever possible to minimize manual user testing. This is faster, reproducible, and doesn't require explaining UI interactions. See examples below.
- **Caddy edge smoke (optional):** App-direct `air` on `:8083` covers handlers; TLS/HSTS/`encode`/COP-through-proxy are checked with `deploy/Caddyfile.local` + `./scripts/caddy-smoke.sh` (see `DEPLOYMENT.md`). Do not stop/restart `air` for this — set `SFPG_BACKEND_PORT=8083` when proxying.
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
  
  # 5. Poll dashboard stats (requires auth; HTMX partial, not a separate SSE route)
  curl -s http://localhost:8083/dashboard \
    -b /tmp/cookies.txt \
    -H "Hx-Request: true" | head -10
  ```

- **`web-testsuite` (`e2eweb`) and login rate limits:** `TestMain` sets `login_rate_limit_per_ip=0` before snapshotting config so many admin logins from one IP do not hit 429 on shared dev `air`. Restart tests (`TestRestart`) use `waitForServerDown` then `waitForServer` so a 200 from the dying process is not mistaken for the new one.

## Project Overview

`sfpg-go` is a self-hosted photo gallery web application written in Go. It serves images from a local directory, generates thumbnails on the fly, and provides a responsive admin-configurable web UI.

**Stack:** Go (1.26+), SQLite (ncruces/go-sqlite3), Go `html/template`, HTMX, Hyperscript, daisyUI, TailwindCSS. Concurrency via goroutines, channels, `errgroup`, and worker pools.

Package map, schema, middleware, data flows, and design patterns: see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Critical Development Patterns

### Handler Testing Pattern

Handlers depend on the `HandlerQueries` interface defined in `internal/server/interfaces` for testability:

- Unit tests construct handler groups directly and inject `fakeHandlerQueries` (see `internal/server/handlers/helpers_test.go`).
- Integration tests can replace the live queries via `app.InfrastructureService.testSeams.HandlerQueries`.
- Handler groups are wired in `App.buildHandlers()` (called from `server.go`).
- For routing/integration tests, use the server's router rather than calling handlers directly.

### Test Seams (`testseams.go`)

Optional test doubles in `internal/server/testseams.go`, wired through unexported `testSeams` on `App` and embedded managers.

- **Nil func seam:** zero value → production (nil-check in caller).
- **Infrastructure cache seams:** `NewInfrastructureService` seeds `GetCacheSizeBytes`, `GetCacheEntryCount`, `EvictLRU` with `cachelite` defaults; tests override directly.
- **Pre-`New()`:** set `defaultNewTestSeams` before `New()`.

Typical: `app.testSeams.Serve`, `app.testSeams.LoadConfig`, `app.testSeams.GalleryStatsStartup`, `app.InfrastructureService.testSeams.HandlerQueries`, `app.RuntimeManager.testSeams.BeforeListen`, `app.HandlerManager.testSeams.BuildHandlers`.

Do **not** add `testHook*` fields or use promoted `app.testHook*` in tests. **Full field inventory:** [ARCHITECTURE.md §Test Seams](docs/ARCHITECTURE.md#test-seams). Prefer the lightest seam per [Choosing a Test Seam](docs/ARCHITECTURE.md#choosing-a-test-seam).

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
- ❌ DON'T: Add JavaScript (Hyperscript/HTMX only; approved exception: password complexity in `config-modal.html.tmpl`)
- ❌ DON'T: Make assumptions without verification
- ❌ DON'T: Use `strings.Contains` on HTTP responses (parse HTML first)
- ❌ DON'T: Run `goimports -w .` or whole-package import formatting (see Development Directives; override generic skills such as `golang-patterns` that recommend it)

## Development Directives

- **HTML content checks:** When checking HTML content (tests or reviews), use structural HTML assertions (no string/bytes contains checks) per [references/methodology-html-content-test-writing.md](references/methodology-html-content-test-writing.md).

- **Format only files you changed** after an approved edit batch:
  - Go: `scripts/format-go-changed.sh` on changed `.go` files (always `gofmt`; `goimports` only when the import block changed, the file is new, or `FORMAT_GO_IMPORTS=1`).
  - Never `goimports -w .` or whole-package trees; pre-commit `make lint` enforces imports via golangci-lint's goimports formatter.
  - `go build -o /dev/null .` when Go changed.
  - Prettier only on the changed `.html.tmpl`, `.md`, or `.sh` files — not a whole-tree format.

- **Commit Message Workflow:** Write `tmp/commit_message.txt` then `git commit -F tmp/commit_message.txt`. Required shape:

  ```text
  <type>(optional-scope): <imperative summary>

  <body>

  <test status>

  Version: <exact value from version.go>
  ```

  `<test status>` example: `All unit, integration, e2eweb, and race tests passed`

  **Forbidden:** version in the subject (`(v0.10.124)`, `v0.10.124`). Plans that show `(v<version>)` in the subject are wrong — follow this format.

## Documentation Reference

This AGENTS.md file is a concise guide for AI agents. For detailed information, see:

### Core Documentation

- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** - Comprehensive architecture documentation with diagrams, data flows, and design patterns
- **[docs/SERVER_DEEP_DIVE.md](docs/SERVER_DEEP_DIVE.md)** - Archived server package entry point (links to ARCHITECTURE.md)
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

- **Package overview / architecture:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- **Database schema:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#database-schema)
- **Middleware stack:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#request-middleware-stack)
- **Security model:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#security-model)
- **Testing strategy:** See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#testing-strategy)
