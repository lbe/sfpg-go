# sfpg-go - Simple Fast Photo Gallery or is it Single File Photo Gallery

[![Go Reference](https://pkg.go.dev/badge/github.com/lbe/sfpg-go.svg)](https://pkg.go.dev/github.com/lbe/sfpg-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26.0-blue.svg)](https://go.dev/dl/)
[![Go Report Card](https://goreportcard.com/badge/github.com/lbe/sfpg-go)](https://goreportcard.com/report/github.com/lbe/sfpg-go)
[![Release](https://github.com/lbe/sfpg-go/actions/workflows/releases.yml/badge.svg)](https://github.com/lbe/sfpg-go/actions/workflows/releases.yml)
[![CI](https://github.com/lbe/sfpg-go/actions/workflows/ci.yml/badge.svg)](https://github.com/lbe/sfpg-go/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/lbe/sfpg-go/branch/main/graph/badge.svg)](https://codecov.io/gh/lbe/sfpg-go)

A self-hosted photo gallery web application written in Go. It serves images from a local directory, generates thumbnails on the fly, and provides a responsive, password-protected web interface for browsing.

The application is designed to be performant and simple to deploy, using concurrency for background tasks and a hypermedia-driven frontend architecture to minimize client-side JavaScript.

<p align="center">
  <img src="sfpg-go-demo.gif" alt="SFPG Go Demo" width="700">
  <br>
  <em>Demonstration of the SFPG Go interface with 2-second transitions.</em>
</p>

## Motivation

This project was inspired by [Single File PHP Gallery](http://sye.dk/sfpg/). I have been a long time user of it and think it is a great project!
Many thanks to its author for providing it!!
My only complaint about it is that you had to install a web server and php and configure the web server to serve the project.
The project takes advantage of Go's statically linked binaries and its standard lib web server to provide a one file solution that
serves a similar photo gallery. While similar, I have added some functionality documented below. I have tried to stick to my inspirations
good defaults.

## Quickstart

Get your photo gallery running in under 2 minutes:

### macOS & Linux

```bash
# 1. Clone and navigate to the project
git clone https://github.com/lbe/sfpg-go.git
cd sfpg-go

# 2. Create an image directory and add your photos
mkdir Images
# Copy your photos into Images/ (e.g., cp -r ~/Pictures/Vacation/* Images/)

# 3. Set the session secret and run
export SEPG_SESSION_SECRET="your-secret-here-change-this"
go run main.go
```

### Windows (PowerShell)

```powershell
# 1. Clone and navigate to the project
git clone https://github.com/lbe/sfpg-go.git
cd sfpg-go

# 2. Create an image directory and add your photos
mkdir Images
# Copy your photos into Images/ (e.g., Copy-Item -Path "C:\Users\YourName\Pictures\Vacation\*" -Destination ".\Images\" -Recurse)

# 3. Set the session secret and run
$env:SEPG_SESSION_SECRET="your-secret-here-change-this"
go run main.go
```

That's it! Open `http://localhost:8081` in your browser and log in with:

- **Username:** `admin`
- **Password:** `admin`

**First steps after login:**

1. Click **Configuration** and change your admin password
2. Click **Discover Files** to scan your photo directory
3. Start browsing your gallery!

**For production deployment:** Build a static binary and use a reverse proxy (see [Deployment](#deployment) and `DEPLOYMENT.md`).

## Features

- **Directory-Based Galleries:** Organizes photos based on your filesystem's directory structure.
- **Responsive UI:** A clean and modern interface that works on desktop and mobile, built with daisyUI and Tailwind CSS.
- **Contextual Info Box:** Hover over any folder or image to see a pop-up box with detailed information, including file metadata, image dimensions, and EXIF/IPTC tags.
- **Performant Thumbnailing:** Generates and caches thumbnails in the background for a fast user experience, utilizing efficient object pooling to minimize memory allocations.
- **Advanced Caching:** A sophisticated, multi-layer caching system:
  - **SQLite-backed HTTP Response Cache:** Persistently caches fully-rendered HTTP responses in the database, dramatically speeding up subsequent page loads.
  - **Unified Write Batching (Feb 2026):** All database writes (file metadata, thumbnails, cache entries) are consolidated through a single batched writer, eliminating SQLite lock contention and improving throughput by 2-10x.
  - **Persistent Write Overflow (Jun 2026):** The batcher spills excess writes to a segment-backed on-disk queue (`dque`) when the in-memory channel fills, so bursts during preload/discovery are absorbed instead of dropped, and pending writes survive process restarts (crash recovery).
  - **Client-Side Caching:** Uses `ETag` and `Last-Modified` headers to allow browsers to serve content from their local cache, avoiding unnecessary requests.
- **Advanced Interactive Lightbox:**
  - View images in an interactive, full-screen modal.
  - Full keyboard navigation, including circular (looping) navigation.
  - "Actual Size" mode to view the image at its native resolution.
- **Keyboard Shortcuts:** Navigate through the gallery pages using Vim-style (`h`, `j`, `k`, `l`) or arrow keys.
- **Secure:** Session-based authentication with configurable per-IP login rate limiting and per-account lockout (Session tab → **Login security** in the config modal).
- **Web-Based Configuration:** Update administrator credentials, session settings, and login security through the web UI.
- **Self-Contained Deployment:** The compiled binary includes all necessary assets and migrations, requiring no external file dependencies to run.
- **Live-Reload for Development:** Includes an `air` configuration for a smooth development workflow.
- **Robust Testing:** Comprehensive test suite with unit, integration, e2e, and browser tests; **23** root `internal/server/*_test.go` files; optional test doubles in `internal/server/testseams.go`.

## Technology Stack

- **Backend:** Go 1.26 or later
- **Database:** SQLite (for thumbnail data, configuration, response caching, etc.)
- **Frontend:**
  - Go HTML Templates (`html/template`) for server-side rendering.
  - **htmx** for UI interactivity and AJAX requests.
  - **hyperscript** for lightweight client-side scripting.
  - **daisyUI** & **Tailwind CSS** for styling and UI components.
- **Concurrency:** Makes extensive use of goroutines, channels, and a custom worker pool for background processing.

## Getting Started

### Prerequisites

- Go 1.26 or later.
- (For development) `air` for live reloading.
- (For development) `golangci-lint` for code linting.
- (For development) Node.js & npm for Prettier (code formatting).

### Development Tools Setup

Install the required development tools:

```shell
# Install air for live reloading
go install github.com/cosmtrek/air@latest

# Install golangci-lint for code quality checks
# macOS via Homebrew
brew install golangci-lint

# Linux via script
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Or via Go install
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Verify installation
golangci-lint --version
```

**Pre-commit hooks:** The project includes pre-commit hooks that run automatically before each commit to ensure code quality:

- Code formatting must be correct (`make format-check`)
- Linter must pass (`make lint`)
- Template and Hyperscript validation must pass (`make validate-templates`)
- HTML test assertions must pass (`make validate-html-test-assertions`)
- All tests must pass (`make test-all`)

The hooks are automatically enabled if you've cloned the repository. To manually enable them:

```shell
git config core.hooksPath .githooks
```

### Installation & Running

1.  **Clone the repository:**

    ```shell
    git clone https://github.com/lbe/sfpg-go.git
    cd sfpg-go
    ```

2.  **Create the image directory:**
    Create a directory named `Images` in the project root and place your photo directories inside it.

    ```shell
    mkdir Images
    # Example:
    # mkdir -p Images/Vacation/
    # mv ~/Pictures/vacation_photo.jpg Images/Vacation/
    ```

3.  **Run in Development Mode (Recommended):**
    This mode uses `air` for live reloading when code or template files change.

    a. **Install `air`:**

    ```shell
    go install github.com/cosmtrek/air@latest
    ```

    b. **Run the application:**
    Set the session secret for development.

    ```shell
    export SEPG_SESSION_SECRET="a-strong-secret-for-development"
    air
    ```

    The application will be available at `http://localhost:8083` (`.air.toml` uses port 8083 to avoid conflicts).

    > Windows users: run Air with the Windows config file to produce an .exe binary:

    ```powershell
    $env:SEPG_SESSION_SECRET="a-strong-secret-for-development"
    air -c .air.windows.toml
    ```

4.  **Run in Production Mode:**

    a. **Build the binary:**

    ```shell
    go build -o sfpg-go .
    ```

    b. **Set the session secret and run the binary:**

    ```shell
    export SEPG_SESSION_SECRET="REPLACE_WITH_A_VERY_STRONG_RANDOM_SECRET"
    ./sfpg-go
    ```

    The application will be available at `http://localhost:8081` by default.

### Development Workflow

The project includes a Makefile with common development tasks:

```shell
# Run tests
make test         # Run tests for all packages (PKG=./... by default)
make test-all     # Run unit, integration, e2e, and e2eweb tests across all packages
make test-race    # Run tests with race detector
make test-browser # Run Playwright browser tests

# Code quality
make lint                          # Run golangci-lint (required before commits)
make validate-templates            # Validate Go template rendering + Hyperscript
make validate-hyperscript          # Validate Hyperscript syntax in templates
make validate-html-test-assertions # Forbid strings.Contains HTML assertions in tests
make format                        # Format Go code and run Prettier
make format-check                  # Check formatting without writing changes

# Coverage and benchmarks
make cover  # Generate coverage report (coverage.html)
make bench  # Run benchmarks (single iteration)
make bench5 # Run benchmarks (5 iterations)

# Build and run
make build        # Build the binary
make build-assets # Build embedded CSS/JS assets
make run          # Build and run the server
make clean        # Remove build artifacts

# Performance testing
make perf-test-setup         # Set up performance test fixtures
make perf-test               # Run performance tests
make perf-test-compare-cache # Compare cached vs uncached performance
make perf-test-clean         # Clean up performance test artifacts
make perf-test-help          # Show performance test options
```

**Before committing:** The pre-commit hooks run `make format-check`, `make lint`, `make validate-templates`, `make validate-html-test-assertions`, and `make test-all`. If any check fails, the commit will be aborted. You can run the same sequence manually with `./scripts/pre-commit-check.sh` (also runs `make test-browser`). For a full Hyperscript pass outside the hook, use `make validate-hyperscript`.

### Code Linting

The project uses [golangci-lint](https://golangci-lint.run/) for static code analysis. The configuration is defined in `.golangci.yml` and includes:

**Enabled Linters:**

- **bodyclose** - Checks whether HTTP response bodies are closed
- **gocritic** - Provides many diagnostics from code style to performance
- **govet** - Reports suspicious constructs (veterinarian)
- **ineffassign** - Detects ineffectual assignments
- **staticcheck** - Go static analysis
- **testifylint** - Checks for common test anti-patterns with testify
- **unused** - Checks for unused constants, variables, functions, and types

**Enabled Formatters:**

- **goimports** - Fixes imports, formats code

Run linters manually:

```shell
# Run all linters
make lint

# Run with specific options
golangci-lint run --max-same-issues 0 ./...
```

### Command-Line Options

Most settings can be provided via command-line flags, environment variables, or a `config.yaml` file. The following flags have corresponding environment variables; flags listed as **CLI-only** have no environment-variable equivalent.

| Flag                       | Environment Variable          | Description                                                     | Effective Default |
| -------------------------- | ----------------------------- | --------------------------------------------------------------- | ----------------- |
| `-port`                    | `SFG_PORT`                    | TCP port for the HTTP server.                                   | `8081`            |
| `-discover`                | `SFG_DISCOVER`                | Run file discovery on startup.                                  | `true`            |
| `-debug-delay-ms`          | `SFG_DEBUG_DELAY_MS`          | Artificial delay in milliseconds for debugging.                 | `0`               |
| `-profile`                 | `SFG_PROFILE`                 | Profiling mode: 'cpu', 'mem', 'block', etc.                     | `''`              |
| `-http-cache`              | `SFG_HTTP_CACHE`              | Enable SQLite HTTP response cache.                              | `true`            |
| `-cache-preload`           | `SFG_CACHE_PRELOAD`           | Enable cache preloading when folders are opened.                | `true`            |
| `-unlock-account`          | `SFG_UNLOCK_ACCOUNT`          | Unlock a locked account by username.                            | `''`              |
| `-restore-last-known-good` | `SFG_RESTORE_LAST_KNOWN_GOOD` | Restore last known good configuration from database on startup. | `false`           |
| `-increment-etag`          | _(CLI-only)_                  | Increment application-wide ETag version on startup.             | `false`           |
| `-cache-batch-load`        | _(CLI-only)_                  | Warm the HTTP cache and exit.                                   | `false`           |

**Note on defaults:** Flag zero-values shown by `./sfpg-go -help` may differ from the effective defaults above. The effective defaults come from `config.DefaultConfig()` and are used unless overridden by the database, YAML, environment variables, or CLI flags.

Precedence order (lowest to highest): **Defaults** → **Database** → **YAML files** → **CLI/Env**

Configuration is loaded in stages:

1. Hard-coded defaults are applied.
2. Values persisted in the database (via the Configuration UI) override defaults.
3. `config.yaml` files override database values.
4. Environment variables and CLI flags are merged into a single `getopt.Opt` tier; CLI flags override environment variables for the same setting.

This allows flexibility in deployment while ensuring secure defaults.

#### YAML Configuration Files

In addition to flags and environment variables, the application reads `config.yaml` from:

- The directory containing the executable.
- The platform-specific user config directory (`~/.config/sfpg/config.yaml` on Linux/macOS, `%APPDATA%\sfpg\config.yaml` on Windows).

Example `config.yaml` keys:

```yaml
listener-port: 8081
http-cache: true
http-cache-body-codec: zstd-1
enable-cache-preload: true
discover: true
session-secret: "change-me-in-production"
```

See `internal/server/config/fields.go` for the full set of supported YAML keys.

### Configuration Sequencing Guarantee

To enforce precedence for database-dependent runtime settings (especially DB pool sizing), startup includes a reconciliation step after config load:

- Bootstrap may initialize DB pools before full config is loaded.
- `loadConfig()` then applies precedence (`Default -> DB -> YAML -> CLI/Env`).
- `reconfigurePoolsFromConfig()` reconciles configured values with effective RW/RO pool values.

This specifically prevents the historical mismatch where `DBMaxPoolSize=500` was stored in the DB but effective pools remained at default `100`.

Troubleshooting tip:

- Check logs for `pool config applied`, `configured/effective DB pool mismatch`, and `startup config summary`.
- These diagnostics provide configured versus effective values so precedence or sequencing drift is visible immediately.

Example:

```shell
export SFG_PORT=8082
./sfpg-go -http-cache=false -profile cpu
```

Restore last known good configuration:

```shell
./sfpg-go -restore-last-known-good
# or via environment variable
export SFG_RESTORE_LAST_KNOWN_GOOD=true
./sfpg-go
```

### Session Environment Variables

These variables are critical for securing session cookies, especially in production.

| Variable                | Required | Default  | Purpose                                                                        |
| ----------------------- | -------- | -------- | ------------------------------------------------------------------------------ |
| `SEPG_SESSION_SECRET`   | **Yes**  | -        | A strong random string (>= 32 bytes) for session encryption.                   |
| `SEPG_SESSION_HTTPONLY` | No       | `true`   | If true, prevents JavaScript access to the cookie (XSS protection).            |
| `SEPG_SESSION_SECURE`   | No       | `true`   | If true, requires HTTPS to send the cookie.                                    |
| `SEPG_SESSION_MAX_AGE`  | No       | `604800` | Session lifetime in seconds (default: 7 days). Users re-auth after expiration. |
| `SEPG_SESSION_SAMESITE` | No       | `Lax`    | SameSite attribute for CSRF protection: `Strict`, `Lax`, or `None`.            |

### CSRF Protection Configuration

CSRF (Cross-Site Request Forgery) protection is built into the application through multiple complementary mechanisms:

#### Cross-Origin Protection (COP)

All state-changing requests (POST, PUT, DELETE, PATCH) pass through Go's
`http.CrossOriginProtection` middleware before reaching route handlers:

- **Same-origin check:** For unsafe methods, the middleware validates `Sec-Fetch-Site`
  (preferred) or `Origin` against the request `Host`.
- **No session tokens:** Forms do not include hidden CSRF fields; cached HTML stays
  auth-agnostic.
- **Non-browser clients:** Requests without `Sec-Fetch-Site` or `Origin` (e.g. `curl`)
  are permitted by the standard library.

Behind a reverse proxy, preserve the original `Host` header so same-origin checks work.
See `DEPLOYMENT.md` for Caddy/nginx examples.

#### Session Cookie Security Settings

Session cookie configuration complements COP and is managed via the **Configuration** modal
in the web interface (overridable with environment variables):

| Setting             | Environment Variable    | Default | Security Impact                                                                                                 |
| ------------------- | ----------------------- | ------- | --------------------------------------------------------------------------------------------------------------- |
| **Session Max Age** | (configurable via UI)   | 7 days  | Defines session lifetime. Shorter sessions reduce exposure window for session hijacking. Recommended: 1-7 days. |
| **SessionHttpOnly** | `SEPG_SESSION_HTTPONLY` | `true`  | Prevents JavaScript access to session cookies. Essential XSS protection. Must remain `true` in production.      |
| **SessionSecure**   | `SEPG_SESSION_SECURE`   | `true`  | Restricts cookies to HTTPS only. Prevents MITM attacks. Must remain `true` in production with HTTPS.            |
| **SessionSameSite** | (configurable via UI)   | `Lax`   | Controls cross-site cookie behavior for CSRF defense.                                                           |

#### SessionSameSite Attribute

The SameSite attribute is the primary browser-level CSRF defense mechanism:

**Lax (default, recommended)**

- Cookies are sent with same-site requests and top-level navigations.
- Provides strong CSRF protection while maintaining good user experience.
- Recommended for most applications.
- Example: Following an external link to your site will include the session cookie.

**Strict**

- Cookies are sent **only** with same-site requests.
- Maximum CSRF protection; even top-level navigations from external sites don't include the cookie.
- Best for highly sensitive applications (banking, health records).
- Trade-off: Users following external links to your site will not be logged in, reducing convenience.

**None**

- Cookies are sent with all requests, including cross-site requests.
- Essentially disables SameSite CSRF protection.
- Only use if cross-site requests require authentication, and only with `SessionSecure=true`.
- SameSite protection is weakened; rely on COP and HTTPS (HSTS recommended).

#### Production Security Recommendations

1. **Always use HTTPS in production:** Set `SessionSecure=true` (default).
2. **Keep SessionHttpOnly enabled:** Protect against XSS attacks (default).
3. **Use SameSite=Lax:** Provides excellent CSRF defense without sacrificing usability (default).
4. **Set a strong session secret:** Use at least 32 random bytes for `SEPG_SESSION_SECRET`.
5. **Customize SessionMaxAge** if needed:
   - **7 days (604800 seconds)** – Default, suitable for most applications.
   - **24 hours (86400 seconds)** – Higher security, more frequent re-authentication.
   - **1 hour (3600 seconds)** – Very high security for sensitive operations.

#### Configuring CSRF Settings at Runtime

Session security settings can be modified via the web interface:

1. Log in as an administrator.
2. Click **Configuration** in the menu.
3. Navigate to the **Session** tab.
4. Adjust:
   - **Session Max Age** (in seconds)
   - **Prevent JavaScript Access** (SessionHttpOnly toggle)
   - **Only Send Over HTTPS** (SessionSecure toggle)
   - **CSRF Protection Level** (SessionSameSite dropdown)
5. Click **Save**.

**Note:** Changes to session settings require the server to restart for them to take effect. The configuration modal displays a "Restart Required" indicator for these settings.

#### Defense-in-Depth

The application implements CSRF protection at multiple layers:

- **Cross-Origin Protection:** Same-origin validation on every unsafe HTTP method.
- **SameSite Cookie Attribute:** Browser-enforced protection against cross-site request inclusion.
- **Secure & HttpOnly Flags:** Protect the session cookie from interception and XSS attacks.
- **Session Timeouts:** Limit exposure window for session hijacking.

### HTTP Caching

The application includes built-in HTTP response caching to improve performance and reduce bandwidth usage.

#### SQLite Response Cache

HTTP responses for gallery pages are cached in SQLite. Bodies may be **compressed at rest**
(zstd-1 by default) to reduce disk use; clients still receive plaintext HTML. The cache is
keyed by HTTP method, request path, and HTMX variant (full page vs. partial). This provides:

- **Storage compression:** Large HTML pages are compressed in SQLite; wire compression is
  handled by Caddy `encode` in production.
- **Simple Cache Key:** One cache entry per method/path/HTMX variant — no per-encoding rows.
- **Efficient 304 Revalidation:** When clients send `If-None-Match` (ETag) or `If-Modified-Since`
  headers, the server returns a 304 Not Modified response with minimal overhead.
- **Size Limits:** Individual cache entries are limited to 10MB; total cache size is limited to
  500MB with automatic LRU eviction.
- **TTL Support:** Responses respect `Cache-Control` headers and can expire automatically.

#### Configuration

The HTTP cache is enabled by default. It respects the full configuration precedence chain: **Default** → **Database** → **Environment Variables** → **Command Line**. This means you can adjust settings at any level:

**Example: Disable caching**

```shell
./sfpg-go -http-cache=false
```

**Or via environment variable:**

```shell
export SFG_HTTP_CACHE=false
./sfpg-go
```

#### Cache Invalidation

The cache is automatically cleaned of expired entries every 5 minutes. For immediate invalidation (e.g., after uploading new images), restart the application or use a reverse proxy to clear the cache.

## Deployment

For a secure production deployment behind a reverse proxy with correct session cookie settings, see `DEPLOYMENT.md`.

## Configuration

Configuration is managed via the web interface.

- Navigate to `http://localhost:8081` and log in.
- On the first run, the default credentials are **username:** `admin` / **password:** `admin`.
- After logging in, click **Configuration** in the menu to open the configuration modal and update settings (including **Login security** on the Session tab: IP rate limit, lockout threshold, and lockout duration).
- IP rate limit startup override: `SEPG_LOGIN_RATE_LIMIT_PER_IP` (see `ENV_CONFIGURATION.md`). **`0` disables** IP rate limiting.

## Project Architecture

The application is organized with a clear separation of concerns, with most of the core logic encapsulated in the `internal/server` package.

- **`main.go`**: The application entry point. It initializes and runs the main server application from `internal/server`.
- **`internal/server`**: The core application package, organized into domain-driven subpackages.
  - `app.go`: The central `App` struct; embeds `InfrastructureService`, `RuntimeManager`, `HandlerManager`, and `SubsystemManager`.
  - `server.go`: HTTP server lifecycle.
  - `router.go`: Route registration and middleware chain.
  - `infrastructure_service.go`, `runtime_manager.go`, `handler_manager.go`, `subsystem_manager.go`: Orchestration managers embedded on `App`.
  - `testseams.go`: Optional test doubles (`AppTestSeams`, `InfrastructureTestSeams`, `RuntimeManagerTestSeams`, `HandlerManagerTestSeams`); zero value means production behavior.
  - `auth/`: Authentication service (bcrypt credential verification, account lockout).
  - `batched_write.go`, `batched_write_flush.go`: Unified `BatchedWrite` union and flush logic for file metadata and HTTP cache entries.
  - `batcher_wiring.go`: Thin `fileBatcher` wiring implementing `files.UnifiedBatcher` so the `files` package submits writes without importing `writebatcher`.
  - `cachebatch/`: One-shot HTTP cache batch-load manager.
  - `cachepreload/`: Cache preloading when folders are opened.
  - `conditional/`: Pure helper package for ETag/304 handling.
  - `config/`: Configuration service (load, save, validate, export, import, restore).
  - `database/`: Database setup, migrations, and connection-pool configuration.
  - `files/`: File processing service (discovery, MIME detection, EXIF, thumbnail generation).
  - `handlers/`: Domain-specific HTTP handlers (auth, gallery, config, dashboard, server, theme, menu, health).
  - `interfaces/`: Shared interfaces such as `HandlerQueries` to avoid circular imports.
  - `logging/`: Bootstrap logging setup.
  - `menu/`: Session-aware hamburger-menu rendering.
  - `metrics/`: Runtime metrics collection.
  - `middleware/`: Reusable middleware (auth, conditional, logging); COP is wired in `router.go`.
  - `modulestate/`: Tracks active background modules (discovery, cache batch load).
  - `pathutil/`: Path-manipulation utilities with path-traversal checks.
  - `security/`: Per-IP login rate limiting (`IPRateLimiter`), lockout thresholds, unlock tasks, and security helpers.
  - `session/`: Session management and CSRF helpers.
  - `subsystem/`: Background subsystem coordination helpers.
  - `template/`: Template data helpers.
  - `theme/`: Theme selection handlers.
  - `ui/`: Template parsing and rendering logic.
  - `validation/`: Input validation rules.
- **`internal/`**: Reusable infrastructure packages.
  - `cachelite`: The SQLite-backed HTTP response cache middleware.
  - `writebatcher`: Generic batched database writer (used by the unified server batcher), with optional persistent on-disk overflow queue.
  - `dque`: Generic, segment-backed persistent on-disk FIFO queue used by `writebatcher` for overflow and crash recovery.
  - `flock`: Minimal cross-platform file locking (flock on Unix, `LockFileEx` on Windows) used by `dque`.
  - `errors`: Focused error sentinels/wrappers used by `dque`.
  - `dbconnpool`: A robust connection pool for SQLite with separate read-only and read-write pools.
  - `gallerydb`: Type-safe database access code generated by `sqlc`.
  - `gallerylib`: File import logic — path-chain upserts with per-batch folder and tiled-directory memoization.
  - `parallelwalkdir`: A utility for high-performance concurrent directory scanning.
  - `workerpool`: The worker pool implementation for background tasks.
  - `scheduler`: Cron-like task scheduling for periodic maintenance tasks.
  - `queue`: Thread-safe, dynamically-resizing deque.
  - `gensyncpool`: Generic, type-safe `sync.Pool` wrappers with reset enforcement.
  - `getopt`: Parses and manages configuration from flags, environment variables, and config files.
  - `thumbnail`: Thumbnail generation with object pooling for memory efficiency.
  - `imagemeta`: EXIF/IPTC/XMP metadata extraction.
  - `multihandler`: Multi-handler structured logging (console + file).
  - `profiler`: Optional CPU/memory/block profiling via config.
  - `coords`: Parsing of geographic coordinates (for EXIF GPS data).
  - `humanize`: Human-readable number/byte formatting for the UI.
  - `log`: Structured logging wrappers.
  - `testutil`: Shared testing helpers and mocks.
  - `gen-test-files`: Utility for generating synthetic test files and directory structures.
- **`web/`**: Contains embedded static assets and Go HTML templates.
- **`DB/`**: The default directory for application data.
  - `sfpg.db` — main SQLite database (folders, files, config, HTTP cache, etc.).
  - `thumbs/thumbs.db` — separate SQLite database for thumbnail JPEG blobs.
  - `sfpg.db-dque/` — persistent write-overflow queue used by `writebatcher`.
- **`docs/`**: Architecture documentation and diagrams.
  - `ARCHITECTURE.md` — authoritative system reference.
  - `SERVER_DEEP_DIVE.md` — server package entry point (links to ARCHITECTURE.md).
  - `diagrams/` — Mermaid architecture diagrams.
- **`Images/`**: The default directory where you should place your photos.
