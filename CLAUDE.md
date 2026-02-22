# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SFPG (Simple Fast Photo Gallery) is a high-performance, self-hosted photo gallery application built with Go. It serves images from a local directory, generates thumbnails on-the-fly, and provides a responsive, password-protected web interface built with HTMX, Hyperscript, and daisyUI/Tailwind CSS.

**Key Design Principles:**

- **Performance**: Asynchronous processing, intelligent caching, connection pooling
- **Idempotency**: Safe to re-run file processing without duplicates
- **Security**: Multiple layers (auth, CSRF, path validation, session security)
- **Simplicity**: Single binary, SQLite database, minimal external dependencies

## Common Development Commands

### Running the Application

```bash
# Development mode with live reload (requires air)
export SEPG_SESSION_SECRET="a-strong-secret-for-development"
air

# Windows development mode
$env:SEPG_SESSION_SECRET="a-strong-secret-for-development"
air -c .air.windows.toml

# Build and run production binary
go build -o sfpg-go .
export SEPG_SESSION_SECRET="REPLACE_WITH_A_VERY_STRONG_RANDOM_SECRET"
./sfpg-go

# Run on custom port
./sfpg-go -port 8082
```

### Testing

```bash
# Run tests (redirect to file for efficiency, then grep)
mkdir -p tmp
go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|PASS|ERROR" ./tmp/test_output.txt

# Run specific package tests
make test PKG=./internal/server

# Run tests with race detector
make test-race

# Generate coverage report
make cover

# Run benchmarks
make bench
make bench5
```

**Important:** Always use `-tags "integration e2e"` to run all tests including integration and e2e tests. Always redirect test output to a file first, then grep the file. Never run `go test` with grep pipes sequentially.

### Code Quality

```bash
# Format code (Go + Prettier for templates, CSS, JS, YAML)
make format

# Check formatting without writing
make format-check

# Validate Hyperscript syntax
make validate-hyperscript
# Or validate specific file:
go run ./scripts/validate-hyperscript.go web/templates/gallery.html
```

### Build

```bash
# Build the binary
make build

# Build and run
make run
```

## High-Level Architecture

### Core Application Structure

```
internal/
├── server/              # HTTP server, routing, orchestration
│   ├── app.go          # App struct - central shared state
│   ├── server.go       # HTTP server and middleware chain
│   ├── router.go       # Route registration
│   ├── handlers/       # HTTP request handlers
│   ├── config/         # Configuration management
│   ├── session/        # Session & CSRF management
│   ├── auth/           # Authentication services
│   └── database/       # Database initialization
├── cachelite/          # SQLite-backed HTTP response cache
├── workerpool/         # Concurrent task processing
├── scheduler/          # Cron-like task scheduling
├── queue/              # Thread-safe deque
├── writebatcher/       # Batch database operations
├── dbconnpool/         # SQLite connection pooling (RO/RW pools)
├── gallerydb/          # Type-safe SQL queries (sqlc generated)
├── imagemeta/          # EXIF/IPTC/XMP extraction (forked)
├── files/              # File processing pipeline
└── log/                # Structured logging
```

### Key Architectural Patterns

**1. Separate Read/Write Connection Pools**

- Read-Only Pool: 10 connections (WAL mode enables concurrent reads)
- Read-Write Pool: 2 connections (serialized writes)
- Prevents writer starvation and maximizes SQLite concurrency

**2. Async Cache Write Pipeline**

- Request thread queues cache write and returns immediately
- Background worker processes writes with async eviction
- Atomic size tracking avoids expensive `SELECT SUM()` queries

**3. Handler Dependencies Pattern**

- Handlers use interfaces (`HandlerQueries`) for testability
- Handlers are constructed via dependency injection in `buildHandlers()`
- Use `hqOverride` in tests to inject mock query behavior

**4. Configuration Precedence**
Defaults → Database → Environment Variables → CLI flags (highest)

### Request Flow

```
Request → Logging Middleware → Auth Middleware → CSRF Middleware → Cache Middleware → Handler → Response
```

**Middleware Order (Critical):**

1. Logging - Log all requests
2. CORS - Check Origin header
3. Authentication - Verify session cookie
4. CSRF - Validate CSRF token for unsafe methods
5. Cache - Check HTTP cache before handler (return cached or forward)
6. Handler - Process request
7. Cache (on return) - Queue async cache write

## Database Schema

**Key Tables:**

- `files` - Image metadata (id, folder_id, filename, mime_type, dimensions, exif_json)
- `folders` - Directory structure (id, path, parent_id, name)
- `thumbnails` - Generated thumbnails (id, file_id, size, width, height, data)
- `config` - Key-value configuration (key, value, type)
- `http_cache` - HTTP response cache (id, path, etag, content_length, body, headers)
- `admin` - Admin credentials (id, username, password_hash, failed_attempts, locked_until)
- `login_attempts` - Failed login tracking

**SQL Queries:** All queries generated via [sqlc](https://sqlc.dev/) from `sqlc/queries/*.sql`

## Important Development Rules

### Test-Driven Development

1. **Write tests first** - Red, Green, Refactor cycle
2. **Never assume** - Always verify claims with evidence
3. **Use HTML parsing** in tests (`golang.org/x/net/html`) - never use `strings.Contains` on response bodies
4. **Each test must be independent** - use `CreateApp()` or `CreateAppWithOpt()` for setup
5. **Always cleanup** - call `defer app.Shutdown()` in tests

### Testing Workflow

```bash
# DO NOT do this (slow, runs Go compiler multiple times):
go test ./... | grep FAIL
go test ./... | grep PASS

# DO THIS (runs Go compiler once):
go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|PASS|ERROR" ./tmp/test_output.txt
```

### Frontend Rules

**NO JavaScript** - This is a Hyperscript project. Use HTMX for HTML-over-the-wire functionality and Hyperscript for client-side scripting.

**Before integrating Hyperscript:**

```bash
go run ./scripts/validate-hyperscript.go web/templates
```

### Concurrency Rules

- Protect shared state with mutexes (`sync.RWMutex` or `sync.Mutex`)
- Protect context access (`app.ctx`) when modified by goroutines
- Always test with `-race` flag
- Ensure background goroutines handle context cancellation

### Database Connection Pattern

```go
# Always get from pool:
conn, err := app.dbRwPool.Get(ctx)
if err != nil { /* handle error */ }
defer app.dbRwPool.Put(conn)
```

### HTMX Pattern Recognition

- Many routes are **partials** - won't work via direct URL access
- Check for `Hx-Request` header to determine request type
- Full page renders have `<body>` tag; partials don't
- Use `hx-swap-oob="outerHTML"` for out-of-band updates
- Return 200 for validation errors (not 400) so HTMX processes response

### Forbidden Practices

- **NEVER use Python** - Use bash or Perl for scripts
- **NEVER use `strings.Contains` on httptest responses** - Parse HTML first
- **NEVER skip tests** - All tests must pass before commits
- **NEVER make assumptions without verification**

## Environment Variables

### Required

- `SEPG_SESSION_SECRET` - Strong random string (>= 32 bytes) for session encryption

### Optional (Session Security)

- `SEPG_SESSION_HTTPONLY` - Default: `true` (prevents XSS access)
- `SEPG_SESSION_SECURE` - Default: `true` (HTTPS only - use with reverse proxy)
- `SEPG_SESSION_MAX_AGE` - Default: `604800` (7 days)
- `SEPG_SESSION_SAMESITE` - Default: `Lax` (CSRF protection)

### Optional (Application)

- `SFG_PORT` - Default: `8081`
- `SFG_DISCOVER` - Run file discovery on startup. Default: `false`
- `SFG_CACHE_PRELOAD` - Enable cache preloading. Default: `false`
- `SFG_HTTP_CACHE` - Enable HTTP response cache. Default: `true`
- `SFG_COMPRESSION` - Enable gzip/brotli. Default: `true`
- `SFG_UNLOCK_ACCOUNT` - Unlock locked account by username
- `SFG_RESTORE_LAST_KNOWN_GOOD` - Restore last known good config on startup
- `SFG_INCREMENT_ETAG` - Increment application-wide ETag version on startup

## Key Files to Understand

- `internal/server/app.go` - App struct with all shared state, lifecycle management
- `internal/server/router.go` - Route registration and middleware wiring
- `internal/server/handlers/` - All HTTP request handlers
- `internal/cachelite/cache.go` - HTTP response caching with async eviction
- `internal/dbconnpool/pool.go` - SQLite connection pooling
- `docs/ARCHITECTURE.md` - Comprehensive architecture documentation
- `.cursorrules` - Additional development rules and patterns

## Configuration Management

Configuration is managed through:

1. Web UI (Configuration menu)
2. Database (`config` table)
3. Environment variables
4. CLI flags

Some changes require restart (port, directories), others apply immediately (cache settings, session options, credentials).
