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
  - **SQLite-backed HTTP Response Cache:** Persistently caches fully-rendered, compressed HTTP responses in the database, dramatically speeding up subsequent page loads.
  - **Unified Write Batching (Feb 2026):** All database writes (file metadata, thumbnails, cache entries) are consolidated through a single batched writer, eliminating SQLite lock contention and improving throughput by 2-10x.
  - **Client-Side Caching:** Uses `ETag` and `Last-Modified` headers to allow browsers to serve content from their local cache, avoiding unnecessary requests.
- **Advanced Interactive Lightbox:**
  - View images in an interactive, full-screen modal.
  - Full keyboard navigation, including circular (looping) navigation.
  - "Actual Size" mode to view the image at its native resolution.
- **Keyboard Shortcuts:** Navigate through the gallery pages using Vim-style (`h`, `j`, `k`, `l`) or arrow keys.
- **Secure:** Uses session-based authentication with secure cookie settings to protect access to the gallery.
- **Web-Based Configuration:** Easily update administrator credentials through the web UI.
- **Self-Contained Deployment:** The compiled binary includes all necessary assets and migrations, requiring no external file dependencies to run.
- **Live-Reload for Development:** Includes an `air` configuration for a smooth development workflow.
- **Robust Testing:** Comprehensive test suite with unit and integration tests, separated by build tags for fast TDD cycles.

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

- All tests must pass
- Code formatting must be correct
- Linter must pass

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

    The application will be available at `http://localhost:8081`.

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
make test      # Run tests for default package (./internal/server)
make test-all  # Run tests across all packages
make test-race # Run tests with race detector

# Code quality
make lint               # Run golangci-lint (required before commits)
make validate-templates # Validate Go template rendering + Hyperscript
make format             # Format Go code and run Prettier
make format-check       # Check formatting without writing changes

# Coverage and benchmarks
make cover  # Generate coverage report (coverage.html)
make bench  # Run benchmarks (single iteration)
make bench5 # Run benchmarks (5 iterations)

# Build and run
make build # Build the binary
make run   # Build and run the server
make clean # Remove build artifacts
```

**Before committing:** The pre-commit hooks will automatically run `make validate-templates`, `make test-all`, `make format-check`, and `make lint`. If any check fails, the commit will be aborted.

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

The application supports the following command-line flags. All flags can also be set via environment variables.

| Flag                       | Environment Variable          | Description                                                     | Default |
| -------------------------- | ----------------------------- | --------------------------------------------------------------- | ------- |
| `-port`                    | `SFG_PORT`                    | TCP port for the HTTP server.                                   | `8081`  |
| `-discover`                | `SFG_DISCOVER`                | Run file discovery on startup.                                  | `false` |
| `-debug-delay-ms`          | `SFG_DEBUG_DELAY_MS`          | Artificial delay in milliseconds for debugging.                 | `0`     |
| `-profile`                 | `SFG_PROFILE`                 | Profiling mode: 'cpu', 'mem', 'block', etc.                     | `''`    |
| `-compression`             | `SFG_COMPRESSION`             | Enable gzip/brotli response compression.                        | `true`  |
| `-http-cache`              | `SFG_HTTP_CACHE`              | Enable SQLite HTTP response cache.                              | `true`  |
| `-cache-preload`           | `SFG_CACHE_PRELOAD`           | Enable cache preloading when folders are opened.                | `false` |
| `-unlock-account`          | `SFG_UNLOCK_ACCOUNT`          | Unlock a locked account by username.                            | `''`    |
| `-restore-last-known-good` | `SFG_RESTORE_LAST_KNOWN_GOOD` | Restore last known good configuration from database on startup. | `false` |
| `-increment-etag`          | `SFG_INCREMENT_ETAG`          | Increment application-wide ETag version on startup.             | `false` |

Precedence order: **Defaults** < **Database** < **Environment variables** < **CLI flags**

Configuration is loaded in stages: application defaults are applied first, then values from the database override them, then environment variables take precedence, and finally command-line flags override everything. This allows flexibility in deployment while ensuring secure defaults.

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

#### Automatic CSRF Token Validation

All state-changing requests (POST, PUT, DELETE) to protected endpoints are validated using CSRF tokens:

- **Token Generation:** A cryptographically secure 32-byte token is generated and stored in the user's session.
- **Token Validation:** The token is validated on every state-changing request using constant-time comparison to prevent timing attacks.
- **Form Integration:** All forms include a hidden CSRF token field that is validated server-side before processing requests.

#### Session Cookie Security Settings

CSRF protection is enhanced through session cookie configuration. These settings are managed via the **Configuration** modal in the web interface and can be overridden with environment variables:

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
- Requires explicit CSRF token validation to provide any CSRF protection.

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

- **Explicit CSRF Token Validation:** Every state-changing request includes a cryptographically secure token.
- **SameSite Cookie Attribute:** Browser-enforced protection against cross-site request inclusion.
- **Secure & HttpOnly Flags:** Protect the session cookie from interception and XSS attacks.
- **Session Timeouts:** Limit exposure window for session hijacking.

This multi-layer approach ensures protection against CSRF attacks even if one layer is bypassed.

### HTTP Caching & Compression

The application includes built-in HTTP response caching and compression to improve performance and reduce bandwidth usage.

#### Compression

Responses are automatically compressed using gzip or Brotli (when supported by the client). The application negotiates the best encoding based on the `Accept-Encoding` header.

- **Negotiation Order:** Brotli (`br`) > gzip > identity (no compression)
- **Skipped For:** Already-compressed media (images, videos, archives), very small responses (< 512 bytes), and responses with explicit `Cache-Control: no-store` directives.
- **Header:** Responses include `Vary: Accept-Encoding` to ensure proxies and browsers cache separate versions per encoding.

#### SQLite Response Cache

HTTP responses for gallery endpoints are cached in SQLite with compression, keyed by path and encoding type. This provides:

- **Single Compression Pass:** Responses are compressed once and stored in the database.
- **Encoding-Aware Caching:** Separate cache entries for gzip, Brotli, and identity (uncompressed) encodings.
- **Efficient 304 Revalidation:** When clients send `If-None-Match` (ETag) or `If-Modified-Since` headers, the server returns a 304 Not Modified response with minimal overhead.
- **Size Limits:** Individual cache entries are limited to 10MB; total cache size is limited to 500MB with automatic LRU eviction.
- **TTL Support:** Responses respect `Cache-Control` headers and can expire automatically.

#### Configuration

Both features are **enabled by default**. The HTTP cache respects the full configuration precedence chain: **Default** (true) → **Database** → **Environment Variables** (`SFG_HTTP_CACHE`) → **Command Line** (`-http-cache`). This means you can disable caching at any level:

**Example: Disable caching but keep compression**

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
- After logging in, click **Configuration** in the menu to open the configuration modal and update settings.

## Project Architecture

The application is organized with a clear separation of concerns, with most of the core logic encapsulated in the `internal/server` package.

- **`main.go`**: The application entry point. It initializes and runs the main server application from `internal/server`.
- **`internal/server`**: The core application package.
  - `app.go`: The central `App` struct, holding shared state like database pools and configuration.
  - `server.go`: The HTTP server, router, and middleware chain.
  - `batched_write.go`, `batched_write_flush.go`: Unified WriteBatcher for consolidating all database writes (Feb 2026).
  - `batcher_adapter.go`: Adapter pattern to break circular dependencies between packages.
  - `handlers/`: Domain-specific HTTP handlers (auth, gallery, config, health).
  - `config/`: Configuration service (load, save, validate, export, import).
  - `files/`: File processing service (discovery, MIME detection, EXIF, thumbnails).
  - `middleware/`: Reusable middleware (auth, compress, conditional, CSRF, logging).
  - `session/`: Session management and CSRF helpers.
  - `ui/`: Template parsing and rendering logic.
- **`internal/`**: Other supporting packages providing reusable components.
  - `cachelite`: The SQLite-backed HTTP response cache middleware.
  - `writebatcher`: Generic batched database writer for high-throughput operations (used by unified batcher).
  - `dbconnpool`: A robust connection pool for SQLite with separate read-only and read-write pools.
  - `gallerydb`: Type-safe database access code generated by `sqlc`.
  - `parallelwalkdir`: A utility for high-performance concurrent directory scanning.
  - `workerpool`: The worker pool implementation for background tasks.
  - `getopt`: Parses and manages configuration from flags, environment variables, and config files.
  - `thumbnail`: Thumbnail generation with object pooling for memory efficiency.
  - `imagemeta`: EXIF/IPTC/XMP metadata extraction.
- **`web/`**: Contains embedded static assets and Go HTML templates.
- **`DB/`**: The default directory where the `sfpg.db` SQLite database file is stored.
- **`docs/`**: Comprehensive architecture documentation and design diagrams.
- **`Images/`**: The default directory where you should place your photos.
