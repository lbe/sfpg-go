# Server Package Deep Dive

**Package**: `go.local/sfpg/internal/server`

> **Note:** This document provides a detailed, technical deep-dive into the server package internals.
> For high-level application architecture covering all packages, see [ARCHITECTURE.md](ARCHITECTURE.md).

This document explains the server package's key components, request flow, data processing pipeline, and design decisions in detail.

---

## Table of Contents

1. [Overview](#overview)
2. [Package Structure](#package-structure)
3. [Core Components](#core-components)
4. [Service Interfaces](#service-interfaces)
5. [Request Flow & Middleware](#request-flow--middleware)
6. [File Processing Pipeline](#file-processing-pipeline)
7. [Database Architecture](#database-architecture)
8. [Session Management](#session-management)
9. [Security Model](#security-model)
10. [Concurrency & Performance](#concurrency--performance)
11. [Configuration](#configuration)
12. [Testing Strategy](#testing-strategy)

---

## Overview

The `internal/server` package implements a photo gallery web application with the following key features:

- **Web server** with HTMX-powered UI
- **Authentication** using bcrypt-hashed passwords and session cookies
- **Concurrent file processing** with worker pool architecture
- **Thumbnail generation** and image metadata extraction
- **SQLite database** with separate read-only and read-write connection pools
- **Persistent HTTP response caching** with compression awareness (`cachelite`)
- **Security hardening** including CSRF protection, path traversal prevention, and configurable session security

### Design Philosophy

- **Separation of Concerns**: Database logic in `gallerydb`, UI templates in `web`, caching in `cachelite`, server logic here.
- **Idempotency**: File processing is idempotent - re-running produces same database state.
- **Memory Efficiency**: Stream large files from disk; buffer only small cachable responses.
- **Security First**: Multiple layers of protection (auth, CSRF, path validation, session security).

---

## Package Structure

The `internal/server` package is organized into domain-driven sub-packages. The root `server` package owns the `App` orchestrator, routing, and wiring; domain logic lives in sub-packages.

| Sub-package          | Purpose                                                                                                                                                                                   |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **`config`**         | Configuration management: loader, saver, validator, exporter, and `ConfigService` implementation. Loads/saves config from the database, validates, exports/imports YAML.                  |
| **`files`**          | File processing: discovery, MIME detection, EXIF extraction, thumbnail generation. Exposes `FileProcessor` interface and worker-pool integration.                                         |
| **`handlers`**       | HTTP handlers (auth, gallery, config, health). Split into focused handler groups (`AuthHandlers`, `GalleryHandlers`, `ConfigHandlers`, `HealthHandlers`), each with minimal dependencies. |
| **`middleware`**     | Reusable middleware: auth, compress, conditional (ETag/304), CSRF, logging.                                                                                                               |
| **`session`**        | Session store, CSRF helpers, cookie options. `SessionManager` interface and `Manager` implementation.                                                                                     |
| **`ui`**             | Template parsing and rendering. Used by handlers for HTML output.                                                                                                                         |
| **`validation`**     | Input validation (e.g. username, password rules). Used by config and admin handlers.                                                                                                      |
| **`interfaces`**     | Shared interfaces such as `HandlerQueries` (DB queries used by handlers).                                                                                                                 |
| **`writebatcher`**   | Batch processing for efficient database writes.                                                                                                                                           |
| **`gen-test-files`** | Utility for generating synthetic test files and directory structures.                                                                                                                     |
| **`compress`**       | Pure functions for compression content negotiation: encoding selection, content type checking, path extension checking.                                                                   |
| **`conditional`**    | Pure functions for HTTP conditional requests: ETag matching, Last-Modified comparison.                                                                                                    |
| **`pathutil`**       | Path manipulation utilities: directory prefix removal with path traversal security checks.                                                                                                |
| **`restart`**        | Pure functions for restart determination: HTTP-only vs full restart based on config changes.                                                                                              |
| **`template`**       | Pure functions for building template data maps: authentication state, CSRF token addition.                                                                                                |
| **`security`**       | Pure functions for security calculations: account lockout thresholds, lockout duration formatting, failed attempt tracking.                                                               |

**Root `server`** retains: `App`, `server.go` (lifecycle, middleware wiring), `router.go` (route registration), `config.go` (runtime `Config` wrapper), `config_handlers.go` (legacy redirects into `handlers`), `restart.go`, and test helpers.

---

## Core Components

### App Struct (Minimal Orchestrator)

The `App` struct (`app.go`) is the central application context. It acts as a **minimal orchestrator**: it owns lifecycle and infrastructure, creates and wires **injected services**, and delegates behavior to them. It holds only what cannot live inside a single service.

**Injected services:**

| Field             | Type                        | Purpose                                                       |
| ----------------- | --------------------------- | ------------------------------------------------------------- |
| `configService`   | `config.ConfigService`      | Load, save, validate, export, import, restore config.         |
| `fileProcessor`   | `files.FileProcessor`       | ProcessFile, CheckIfModified, GenerateThumbnail.              |
| `sessionManager`  | `*session.Manager`          | GetOptions, EnsureCSRFToken, ValidateCSRFToken, ClearSession. |
| `authHandlers`    | `*handlers.AuthHandlers`    | Authentication handlers (login, logout, status).              |
| `configHandlers`  | `*handlers.ConfigHandlers`  | Configuration handlers (get, post, export, import, restore).  |
| `galleryHandlers` | `*handlers.GalleryHandlers` | Gallery handlers (images, folders, thumbnails, metadata).     |
| `healthHandlers`  | `*handlers.HealthHandlers`  | Health check and version handlers.                            |

**Infrastructure and orchestration state** (subset): `ctx`, `cancel`, `ctxMu`, `wg`; `dbRoPool`, `dbRwPool`; `rootDir`, `dbDir`, `dbPath`, `opt`; `config`, `configMu`; `store`, `sessionSecret`; `logger`, `scheduler`; `imagesDir`, `normalizedImagesDir`; `pool`, `q`, `qSendersActive`; `writeBatcher`, `cacheMW`; `restartRequired`, `restartMu`, `restartCh`, `httpServer`; `ImporterFactory`, `hqOverride` (test-only).

**Note on Unified WriteBatcher (Feb 2026):** The application now uses a single `writeBatcher` instance to handle all high-volume database writes (file metadata, invalid files, HTTP cache entries). This eliminates SQLite lock contention by consolidating three previously independent write paths into one batched, transactional writer.

### Key Files

- **`app.go`**: Application initialization, configuration, database setup, service wiring.
- **`server.go`**: HTTP server lifecycle, middleware helpers, auth middleware.
- **`router.go`**: Route registration; wires the four split handler groups and middleware.
- **`handlers/`**: HTTP handlers (auth, gallery, config, health). Individual handlers live in `auth_handlers.go`, `gallery_handlers.go`, `config_handlers.go`, and `health_handlers.go`.
- **`config/`**: Config domain (service, loader, saver, validator, exporter).
- **`files/`**: File processing (processor, service, walker, thumbnail, metadata).
- **`ui/`**: Template rendering.
- **`middleware/`**: Auth, compress, conditional, CSRF, logging.
- **`session/`**: Session manager and options.
- **`validation/`**: Username/password validation.
- **`test_helpers.go`**: Shared helpers for tests (`CreateApp`, `MakeAuthCookie`, etc.).

---

## Service Interfaces

Domain logic is accessed through interfaces. App creates concrete implementations and injects them into `Handlers` and other consumers.

### ConfigService (`config`)

```go
type ConfigService interface {
    Load(ctx context.Context) (*Config, error)
    Save(ctx context.Context, cfg *Config) error
    Validate(cfg *Config) error
    Export() (string, error)
    Import(yamlContent string, ctx context.Context) error
    RestoreLastKnownGood(ctx context.Context) (*Config, error)
    EnsureDefaults(ctx context.Context, rootDir string) error
    GetConfigValue(ctx context.Context, key string) (string, error)
}
```

**Implementation**: `config.NewService(dbRwPool, dbRoPool)`. Uses loader, saver, validator, and exporter under the hood.

### FileProcessor (`files`)

```go
type FileProcessor interface {
    ProcessFile(ctx context.Context, path string) (*File, error)
    CheckIfModified(ctx context.Context, path string) (bool, error)
    GenerateThumbnail(ctx context.Context, file *File) error
}
```

**Implementation**: `files.NewFileProcessor` (or equivalent), built with `dbRoPool`, `ImporterFactory`, `imagesDir`. Used by the worker pool and by handlers that need thumbnails.

### SessionManager (`session`)

```go
type SessionManager interface {
    GetOptions() *sessions.Options
    EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string
    ValidateCSRFToken(r *http.Request) bool
    ClearSession(w http.ResponseWriter, r *http.Request)
}
```

**Implementation**: `session.NewManager(store, configGetter)`. Wraps the session store and provides CSRF helpers.

### HandlerQueries (`interfaces`)

```go
type HandlerQueries interface {
    GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error)
    GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error)
    GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error)
    GetFileViewByID(ctx context.Context, id int64) (gallerydb.FileView, error)
    GetFolderByID(ctx context.Context, id int64) (gallerydb.Folder, error)
    GetThumbnailsByFileID(ctx context.Context, fileID int64) (gallerydb.Thumbnail, error)
    GetThumbnailBlobDataByID(ctx context.Context, id int64) ([]byte, error)
}
```

Abstracts the subset of DB queries used by gallery/image/thumbnail handlers. Implemented by `*gallerydb.CustomQueries`; tests can inject alternatives via `hqOverride`.

### Handlers (`handlers`)

The `handlers` package contains discrete handler groups for different app domains: `AuthHandlers`, `GalleryHandlers`, `ConfigHandlers`, and `HealthHandlers`. Each group is initialized with only the dependencies it needs (Dependency Injection).

**Key Handler Groups:**

- **`AuthHandlers`**: Manages login, logout, and session status.
- **`ConfigHandlers`**: Manages application settings, export/import, and admin credential updates. Depends on `AuthService` and `SessionManager`.
- **`GalleryHandlers`**: Manages image viewing, folder navigation, and thumbnail retrieval. Depends on `HandlerQueries` and read-only DB pools.
- **`HealthHandlers`**: Provides system health checks and version information.

**Dependency Management:** Handlers are initialized with specific interfaces (e.g., `AuthService`, `SessionManager`, `HandlerQueries`) rather than large orchestrator objects. This facilitates testing and ensures clear boundaries.

---

## Request Flow & Middleware

### Middleware Chain

The server uses a middleware chain to process requests. Middleware is applied in `getRouter()` and wraps the main `mux` handler.

**Global Middleware Chain (applied to all requests):**

```
Request
    ↓
[loggingMiddleware] - Logs request and response details.
    ↓
[cachelite.Middleware] (if enabled) - Checks for cached response in SQLite. Serves if HIT.
    ↓
[CompressMiddleware] (if enabled) - Negotiates `Accept-Encoding` and compresses response.
    ↓
[crossOriginProtection] - Enforces same-origin policy for unsafe methods.
    ↓
[authMiddleware] - Checks session cookie and handles auth redirects.
    ↓
[Mux] -> Dispatches to `app.handlers` (e.g. `h.GalleryByID`, `h.ConfigGet`).
```

**Selective Middleware:**

- **`ConditionalMiddleware`**: This middleware, which handles `304 Not Modified` responses by buffering the response, is **selectively applied** only to lightweight page handlers (like `/gallery/{id}`, `/info/...`, etc.). It is explicitly **not** applied to large file handlers (`/raw-image/{id}`) to prevent buffering large files in memory.

### Authenticated Request Example

```
Client Request (with session cookie and "If-None-Match" ETag)
    ↓
HTTP Router
    ↓
[loggingMiddleware]
    ↓
[cachelite.Middleware] - MISS: No entry in SQLite cache.
    ↓
[CompressMiddleware] - Wraps response writer for gzip/brotli.
    ↓
[crossOriginProtection] - Allows GET request.
    ↓
[authMiddleware] - Authenticated, proceeds.
    ↓
[Mux] -> to gallery handler (`h.GalleryByID`)
    ↓
[ConditionalMiddleware] - Wraps response writer to buffer response for 304 check.
    ↓
[gallery handler] - Fetches data, sets ETag header, renders template into buffer.
    ↓
[ConditionalMiddleware] - Sees ETag matches "If-None-Match", sends `304 Not Modified`, discards buffer.
    ↓
(Response sent, cachelite middleware does nothing on the way back for a 304)
```

---

## File Processing Pipeline

### Overview

The file processing pipeline discovers image files, extracts metadata, generates thumbnails, and stores everything in the database. It uses a concurrent worker pool for parallel processing.

### Pipeline Stages

```
1. File Discovery (walkImageDir)
    ↓
2. Queue Population (app.q.Enqueue)
    ↓
3. Worker Pool Processing (poolFunc)
    ├─→ Check if file needs processing
    ├─→ Detect MIME type
    ├─→ Extract EXIF data
    ├─→ Generate thumbnail
    ├─→ Calculate perceptual hash (pHash)
    ├─→ Upsert to database
    └─→ Update folder tile
```

### 1. File Discovery

**Function**: `walkImageDir()` is an `App` method in `server.go`; it delegates to `files.WalkImageDir` for traversal.

- Uses `parallelwalkdir.WalkFunc` for concurrent directory traversal
- Filters for image files (`.jpg`, `.png`, `.webp`, etc.)
- Enqueues absolute file paths to work queue
- Runs in background goroutine

### 2. Worker Pool Architecture

**Component**: `workerpool.Pool` (from `internal/workerpool`)

- **Configurable workers**: Default `2 * NumCPU` workers
- **Queue-based**: Workers pull from shared queue
- **Database connection sharing**: Each worker gets connection from pool
- **Graceful shutdown**: Workers drain queue before exiting

### 3. File Processing

The worker pool uses `files.NewPoolFuncWithProcessor(fileProcessor, ...)`. Processing logic lives in the `files` package (`ProcessFile`, etc.).

For each file:

1. **Path Normalization**: Convert absolute path to relative (remove `imagesDir` prefix)
2. **Modification Check**: Query database for existing file, compare mtime/size
3. **Skip if Unchanged**: If file unchanged, skip processing
4. **MIME Detection**: Read first 512 bytes, detect image type
5. **EXIF Extraction**: Parse EXIF data (orientation, datetime, etc.)
6. **Thumbnail Generation**:
   - Decode image
   - Apply EXIF orientation
   - Clear pixel data (zero out) for privacy if needed
   - Resize to thumbnail dimensions
   - Encode as WebP
   - Store in `Thumbnails/` directory
7. **Perceptual Hash**: Calculate pHash for duplicate detection
8. **Unified Batcher Submission**: Submit file metadata and thumbnails to the unified WriteBatcher
9. **Folder Tile Update**: Set folder's representative image

### 4. Unified WriteBatcher (Feb 2026 Refactoring)

All file writes are now routed through a **single unified WriteBatcher** at the App level:

**Implementation Components:**

- **`internal/server/batched_write.go`**: Defines the `BatchedWrite` union type with three variants:
  - `File`: Complete file metadata including EXIF and thumbnail data
  - `InvalidFile`: Tracks files that failed processing
  - `CacheEntry`: HTTP response cache entries
- **`internal/server/batched_write_flush.go`**: Unified flush function that processes all write types in a single transaction
- **`internal/server/batcher_adapter.go`**: Adapter pattern that breaks circular dependency between `server` and `files` packages
- **`files.UnifiedBatcher` interface**: Allows `files` package to submit writes without depending on `server`

**Benefits:**

- **Eliminated Lock Contention**: One writer instead of three competing for SQLite's exclusive lock
- **Improved Throughput**: Batching reduces transaction overhead (from many small transactions to fewer large ones)
- **Resource Cleanup**: Automatic return of pooled resources (thumbnail buffers, cache entries) after flush

**Flow:**

```
File Processor → UnifiedBatcher.SubmitFile() → WriteBatcher queue
Cache Middleware → UnifiedBatcher.SubmitCache() → WriteBatcher queue
                                    ↓
                          Background worker periodically flushes
                                    ↓
                    flushBatchedWrites() in single transaction
                                    ↓
                   Cleanup pooled resources (thumbnails, cache entries)
```

### 5. Idempotency

The pipeline is **idempotent**:

- Same input file → same database state
- Re-running discovery/processing is safe
- Skips unchanged files (mtime/size check)
- MD5 hash verifies file integrity

### 6. Testing (Feb 2026 Update)

The file processing pipeline has comprehensive test coverage:

- **Unit Tests**: `files/service_test.go` - fast, no database (uses mocks)
- **Integration Tests**: `files/files_integration_test.go` - full pipeline with real database
- **Build Tags**: Integration tests require `-tags integration` flag for separation

**Test Organization Benefits:**

- Fast TDD cycle with unit tests (<5s for full suite)
- Comprehensive E2E validation with integration tests
- Clear separation prevents slow tests from blocking development

---

## Database Architecture

### Connection Pools

**Two separate pools** for different access patterns:

#### Read-Only Pool (`dbRoPool`)

- **Purpose**: Serve read-heavy web requests (gallery, image, search)
- **Configuration**:
  - Max open connections: `20 * NumCPU`
  - `PRAGMA query_only = true`
  - `PRAGMA journal_mode = WAL`
- **WAL Mode**: Allows concurrent reads without blocking

#### Read-Write Pool (`dbRwPool`)

- **Purpose**: File processing, configuration updates, login
- **Configuration**:
  - Max open connections: `2 * NumCPU`
  - `PRAGMA journal_mode = WAL`
- **Single writer**: SQLite serializes writes, but WAL allows concurrent reads

### Why Separate Pools?

1. **Isolation**: Web requests don't compete with background processing
2. **Performance**: Read-only pool can be larger (more concurrent web requests)
3. **Safety**: Read-only pool can't accidentally modify data
4. **Clarity**: Code makes access intent explicit

### Database Schema

**Key Tables**:

- **`files`**: Image metadata (path, size, mtime, md5, width, height, pHash)
- **`folders`**: Directory hierarchy, tile image references
- **`thumbnails`**: Thumbnail paths and metadata
- **`config`**: Application configuration (username, password hash)

**Views** (for convenience):

- **`file_view`**: Joins files + thumbnails
- **`folder_view`**: Joins folders + tile thumbnails

### Migrations

- **Tool**: `golang-migrate`
- **Storage**: Embedded filesystem (`migrations` package)
- **Execution**: Automatic on startup (`app.setDB()`)
- **Safety**: Migrations are transactional

---

## Session Management

### Session Store

**Library**: `gorilla/sessions`

- **Storage**: Cookie-based (encrypted session data stored in cookie)
- **Secret**: Loaded from `SEPG_SESSION_SECRET` environment variable (required)
- **Expiration**: 7 days (`MaxAge: 86400 * 7`)

### Session Cookie Configuration

**Function**: `getSessionOptions()` in `server.go`

Configurable via environment variables for different deployment contexts:

| Environment Variable    | Default | Purpose                                    |
| ----------------------- | ------- | ------------------------------------------ |
| `SEPG_SESSION_HTTPONLY` | `true`  | Prevent JavaScript access (XSS protection) |
| `SEPG_SESSION_SECURE`   | `true`  | Require HTTPS (prevent interception)       |
| (SameSite)              | `Lax`   | CSRF protection                            |

**Development Mode**:

```bash
export SEPG_SESSION_HTTPONLY=false
export SEPG_SESSION_SECURE=false
```

**Production Mode** (default):

```bash
# No env vars needed - defaults to secure
```

### Authentication Flow

The authentication system uses event-driven communication between the frontend and backend to ensure reliable session management.

#### Login Flow

**Backend** (`handlers` package: `Login`):

1. Client posts credentials to `/login` endpoint
2. Username and password are validated against bcrypt hash in database
3. On success: Session is created with `session.Values["authenticated"] = true`
4. Response sets HTTP header `HX-Trigger: login-success` to signal frontend success
5. Response includes OOB swap to update the hamburger menu with authenticated options

**Frontend** (`login-modal.html.tmpl`):

1. Form posts via HTMX with `hx-post="/login"` and `hx-swap="none"`
2. Hyperscript listens for the `login-success` event from the body: `on 'login-success' from body`
3. On event receipt, modal checkbox is unchecked: `set #login_modal.checked to false`
4. Modal closes and menu updates automatically via OOB swap

**Why Event-Driven?**

- Previous approach: Content-sniffing detection (checking if response includes 'Configuration' text) - brittle and unreliable
- Current approach: Explicit `HX-Trigger` header signals success cleanly
- Ensures modal closes only on successful login, not on validation errors

#### Logout Flow

**Backend** (`handlers` package: `Logout`):

1. Client posts to `/logout` endpoint (CSRF-protected)
2. Session is destroyed with `session.Options.MaxAge = -1`
3. Response returns OOB swap to update hamburger menu
4. Response returns 200 OK to indicate successful logout

**Frontend** (`logout-modal.html.tmpl`):

1. Form posts via HTMX with `hx-post="/logout"` and `hx-swap="none"`
2. Hyperscript listens for successful requests: `on htmx:afterRequest if event.detail.successful`
3. On success, modal checkbox is unchecked: `set #logout_modal.checked to false`
4. Modal closes and menu reflects unauthenticated state

**Why Session MaxAge=-1?**

- `MaxAge = -1` tells the browser to delete the cookie immediately
- This is the standard way to invalidate session cookies
- Previous approach: Setting flag in response - less reliable
- Session is now properly destroyed server-side

---

## Security Model

### Defense in Depth

Multiple security layers protect against common attacks:

#### 1. Authentication & Authorization

- **bcrypt hashed passwords**: Resistant to brute force
- **Session-based auth**: No credentials in URLs
- **authMiddleware**: Protects administrative routes (e.g., `/config`, `/logout`) and debug endpoints (`/debug/pprof/`).
- **Public access**: Gallery, image, and metadata endpoints (e.g., `/gallery/{id}`, `/info/...`) are currently public, but they utilize `addAuthToTemplateData` to conditionally show admin UI elements.

#### 2. CSRF Protection

**Cross-Origin Protection** (`crossOriginProtection` middleware):

- **Unsafe methods** (POST, PUT, DELETE, PATCH) require `Origin` header
- **Origin validation**: Must match request `Host`
- **Safe methods** (GET, HEAD, OPTIONS) allowed without Origin
- **Behavior**: Mirrors Go 1.25 stdlib `http.CrossOriginProtection`

#### 3. Path Traversal Prevention

**File Serving**:

- All file paths stored **relative** to `imagesDir`
- Database queries by **ID** (integers), not paths
- `removeImagesDirPrefix()` normalizes paths
- Absolute path resolution with validation

**Image Handlers**:

```go
// Lookup by ID (integer), not path
fileView, err := queries.GetFileViewByID(ctx, fileID)

// Construct absolute path from trusted database + filesystem
imagePath := filepath.Join(app.imagesDir, fileView.Path)
```

#### 4. Session Security

- **HttpOnly**: Prevents JavaScript access (XSS mitigation)
- **Secure**: HTTPS-only in production
- **SameSite=Lax**: CSRF protection
- **Configurable**: Environment-based for dev/prod

#### 5. Input Validation

- **Password confirmation**: Must match before update
- **SQL injection**: Prevented by parameterized queries (sqlc)
- **XSS**: Template auto-escaping (Go html/template)

#### 6. Error Handling

- **No sensitive leaks**: Generic error messages to clients
- **Structured logging**: Detailed errors logged server-side
- **500 errors**: `serverError()` helper logs and returns generic message

---

## Concurrency & Performance

### Worker Pool

**Design**: Fixed-size worker pool with shared queue

```
Queue (10,000 capacity)
    ↓
Worker 1 ────┐
Worker 2 ────┤
Worker 3 ────┼──→ Database Pools (RO + RW)
   ...       │
Worker N ────┘
```

**Configuration**:

- **Workers**: `2 * runtime.NumCPU()` (default)
- **Queue size**: `10,000` paths
- **Timeout**: `10s` idle timeout before worker exits

**Benefits**:

- **Bounded concurrency**: Prevents resource exhaustion
- **Backpressure**: Queue blocks when full
- **Graceful shutdown**: Workers drain queue before exit

### Memory Efficiency

**File Processing**:

- **Streaming**: Read files in chunks, don't buffer entirely
- **Seek-based**: Multiple passes using `file.Seek(0, 0)` - relies on OS disk cache
- **Clear pixel data**: `clear(img.Pix)` zeros image data efficiently (Go 1.21+)

**Why seek instead of ReadAll?**

With concurrent workers processing large images:

- **Seeks**: Low memory per worker (~small working set)
- **ReadAll**: High memory per worker (full file in RAM)
- **OS cache**: First read loads to disk cache, subsequent seeks are fast
- **Tradeoff**: Lower RAM usage for slightly more syscalls (cached)

### Caching

The application uses a sophisticated, multi-layer caching strategy to optimize performance and reduce bandwidth.

#### HTTP Cache Initialization

**Critical Timing**: The HTTP cache middleware is initialized **after** the application configuration is fully loaded. This is essential because:

- The cache enabled/disabled state comes from `app.config.EnableHTTPCache`
- Configuration values are loaded in precedence order: **Defaults** → **Database** → **Environment Variables** → **CLI Flags**
- By initializing after `applyConfig()` in the `Run()` method, the cache respects the full precedence chain
- This allows users to configure the cache via the web UI (stored in database) without requiring CLI flags

**Initialization Sequence** in `Run()`:

1. Parse command-line flags and environment variables → stored in `app.opt`
2. Load configuration from database → stored in `app.config`
3. Call `app.applyConfig()` to merge all sources with proper precedence
4. Call `app.initializeHTTPCache()` to initialize cache middleware based on `app.config.EnableHTTPCache`

This ensures the cache works out-of-the-box with default settings, and respects user changes made in the Configuration modal.

#### SQLite HTTP Response Cache (`cachelite`)

- **Purpose**: Persistently cache entire HTTP responses (including compressed bodies) in the SQLite database. This replaces the previous in-memory gallery cache.
- **Mechanism**: A middleware (`cachelite.Middleware`) intercepts outgoing responses. If a response is cachable (e.g., status 200, not marked `no-store`), its headers and compressed body are submitted to the unified WriteBatcher for async database writes.
- **Cache Key**: `METHOD:/path?query|encoding`. This ensures that a gzipped response and a brotli response for the same URL are cached as two separate entries.
- **Cache Hits**: On subsequent requests, the middleware checks the database for a matching key. If found, the cached response is served directly, bypassing the handler, database queries, and template rendering entirely.
- **Async Writes (Feb 2026)**: Cache entries are now written through the unified WriteBatcher, eliminating the old dedicated cache write queue and worker. This consolidates all database writes into a single path, reducing lock contention.
- **Eviction**: The cache has a configured size limit (e.g., 500MB). When the limit is reached, the least recently used (LRU) entries are evicted to make space.
- **Cleanup**: A background goroutine periodically cleans up expired cache entries.

#### Client-Side Caching (Browser Cache)

- **Mechanism**: The application sets `ETag` and `Last-Modified` headers on cachable responses (gallery pages, images, etc.).
- **`ConditionalMiddleware`**: This middleware intercepts requests with `If-None-Match` or `If-Modified-Since` headers. If the ETag or modification time matches the server's version, it returns a `304 Not Modified` status, saving bandwidth.
- **`Cache-Control`**: Handlers set `Cache-Control: public, max-age=...` headers to instruct browsers and intermediate caches how long to keep a copy of the response.

### Path Normalization Caching

**Optimization** (Nov 2025):

- **Problem**: `filepath.ToSlash(app.imagesDir)` called repeatedly in hot path
- **Solution**: Cache normalized path in `app.normalizedImagesDir`
- **Benefit**: Eliminates repeated allocations during file processing

---

## Configuration

### Configuration Precedence

The application loads configuration in the following order, with later sources overriding earlier ones:

1. **Defaults**: Built-in defaults in the `Config` struct (e.g., `EnableHTTPCache = true`, `Port = 8081`)
2. **Database**: Values persisted in the `config` table (set via the web UI Configuration modal)
3. **Environment Variables**: Values from environment (e.g., `SFG_PORT=8082`, `SFG_HTTP_CACHE=false`)
4. **Command-Line Flags**: Values passed explicitly (e.g., `-port 8083`, `-http-cache=true`)

**Timing**: This precedence is enforced in the `Run()` method:

1. Parse CLI flags and env vars → `app.opt`
2. Load database config → `app.config`
3. Call `app.applyConfig()` to merge sources with proper precedence
4. Initialize dependent components (cache, etc.) based on final config

**Benefits**:

- Secure defaults work out-of-the-box
- Database UI changes respected without CLI flags
- Environment variables for containerized deployments
- CLI flags for override in specific scenarios

### Environment Variables

| Variable                | Required | Default | Purpose                           |
| ----------------------- | -------- | ------- | --------------------------------- |
| `SEPG_SESSION_SECRET`   | **Yes**  | -       | Session cookie encryption key     |
| `SEPG_SESSION_HTTPONLY` | No       | `true`  | HttpOnly cookie flag              |
| `SEPG_SESSION_SECURE`   | No       | `true`  | Secure cookie flag                |
| `SFPG_ROOT_DIR`         | No       | `./`    | Application root directory        |
| `SFPG_PORT`             | No       | `8080`  | HTTP server port                  |
| `SFG_HTTP_CACHE`        | No       | `true`  | Enable SQLite HTTP response cache |
| `SFG_COMPRESSION`       | No       | `true`  | Enable gzip/brotli compression    |

### Directory Structure

```
${SFPG_ROOT_DIR}/
├── DB/
│   ├── sfpg.db          # SQLite database
│   ├── sfpg.db-shm      # Shared memory (WAL)
│   └── sfpg.db-wal      # Write-ahead log
├── Images/              # Source images (scanned)
│   ├── folder1/
│   └── folder2/
├── Thumbnails/          # Generated thumbnails
│   ├── file/
│   └── folder/
└── logs/
    └── sfpg-*.log       # Application logs
```

### Command-Line Flags

Defined in `app.go`:

- `-root-dir`: Override root directory
- `-debug-delay`: Add artificial delay (testing)

---

## Testing Strategy

### Test Helpers

**`test_helpers.go`**:

- **`CreateApp(t, startPool bool)`**: Creates a fully isolated test application instance with temporary directories and a dedicated database. This is the primary helper for setting up integration tests.
- **`MakeAuthCookie(t, app)`**: Generates an authenticated session cookie for a given test app instance, used for testing protected endpoints.

### Test Categories

#### 1. Unit Tests

- **`files_test.go`**: Image processing, thumbnail generation, pixel clearing
- **`server_test.go`**: Router, middleware, path handling
- **`app_test.go`**: Configuration, database setup, directory initialization

#### 2. Integration Tests

- **`handlers_test.go`**: End-to-end request/response testing
- **`file_integration_test.go`**: Full file processing pipeline
- **`security_test.go`**: Security scenarios (auth, CSRF, path traversal)

#### 3. Security Tests

**`security_test.go`** (comprehensive suite):

- Path traversal attempts
- Cross-origin protection
- Session security flags
- Input validation
- Authentication requirements
- File access boundaries

### Testing Philosophy

- **Isolation**: Each test gets clean temporary directories
- **Realistic**: Use production `getSessionOptions()` for session tests
- **Comprehensive**: Cover happy paths, error paths, and security scenarios
- **Fast**: Unit tests run in ~22 seconds

---

## Key Design Decisions

### 1. Why Two Database Pools?

**Decision**: Separate RO and RW connection pools

**Rationale**:

- Web requests are read-heavy, benefit from larger pool
- Background processing needs write access, smaller pool suffices
- Isolation prevents background work from blocking user requests
- Makes access intent explicit in code

### 2. Why WAL Mode?

**Decision**: SQLite WAL (Write-Ahead Logging) mode

**Rationale**:

- Allows concurrent reads during writes
- Better performance for read-heavy workloads
- No database locks for readers
- Standard for modern SQLite applications

### 3. Why Cookie Sessions?

**Decision**: Cookie-based sessions (gorilla/sessions)

**Rationale**:

- Stateless server (no session storage needed)
- Encrypted session data
- Simple deployment (no Redis/Memcached)
- Suitable for single-instance deployment

### 4. Why Worker Pool?

**Decision**: Fixed worker pool instead of unbounded goroutines

**Rationale**:

- Prevents resource exhaustion (bounded concurrency)
- Predictable resource usage
- Better for large image directories (thousands of files)
- Easier to reason about performance

### 5. Why HTMX?

**Decision**: HTMX for UI interactivity instead of SPA framework

**Rationale**:

- Server-side rendering (simpler, more secure)
- Progressive enhancement
- Less JavaScript to maintain
- Better initial page load

---

## Future Considerations

### Scalability

**Current**: Single-instance deployment

**Future Options**:

- Horizontal scaling: Multiple read replicas (SQLite replication)
- Load balancer: Distribute web requests
- Separate workers: Dedicated file processing instances
- Caching layer: Redis for gallery cache

### Authentication

**Current**: Hardcoded admin credentials in database

**Future**:

- Random password generation on first run
- Pluggable auth providers (LDAP, OAuth2)
- Multi-user support with roles
- 2FA support

### Performance

**Optimizations Implemented**:

- ✅ `clear()` builtin for pixel zeroing
- ✅ Path normalization caching
- ✅ Database connection pooling
- ✅ Worker pool concurrency

**Future**:

- Lazy loading for large galleries
- Virtual scrolling
- Image CDN integration
- Pre-warming caches

---

## Maintenance & Operations

### Logging

**Levels**:

- **DEBUG**: Detailed request/response info, timing
- **INFO**: Application lifecycle, configuration
- **ERROR**: Errors that need attention

**Format**: Structured logging with `slog`

**Storage**: `${SFPG_ROOT_DIR}/logs/sfpg-YYYY-MM-DD_HH-MM-SS.log`

### Database Maintenance

**Automatic**:

- `PRAGMA optimize` runs every hour (scheduled in `setDB()`)
- WAL checkpoint on shutdown

**Manual**:

```bash
# Vacuum database (reclaim space)
sqlite3 DB/sfpg.db "VACUUM;"

# Analyze tables (update statistics)
sqlite3 DB/sfpg.db "ANALYZE;"
```

### Monitoring

**Health Checks**:

- HTTP server listening: `curl http://localhost:8080/login`
- Database connectivity: Check RO pool `Get()`
- Worker pool status: Monitor queue length

---

## References

### Related Packages

- **`internal/server/config`**: Config service, loader, saver, validator, exporter
- **`internal/server/files`**: File processing, walker, thumbnail, metadata; `FileProcessor` implementation
- **`internal/server/handlers`**: HTTP handlers (auth, gallery, config, admin, health)
- **`internal/server/middleware`**: Auth, compress, conditional, CSRF, logging
- **`internal/server/session`**: Session manager, cookie options, CSRF helpers
- **`internal/server/ui`**: Template rendering
- **`internal/server/validation`**: Username/password validation
- **`internal/server/interfaces`**: `HandlerQueries` and other shared interfaces
- **`internal/gallerydb`**: Database queries (generated by sqlc)
- **`internal/gallerylib`**: File import logic
- **`internal/dbconnpool`**: Connection pool implementation
- **`internal/workerpool`**: Worker pool implementation
- **`internal/writebatcher`**: Batch processing for efficient database writes
- **`internal/queue`**: Thread-safe queue
- **`internal/cachelite`**: SQLite-backed HTTP response cache
- **`web`**: Embedded templates and static assets

### External Dependencies

- **`gorilla/sessions`**: Session management
- **`golang/crypto/bcrypt`**: Password hashing
- **`golang-migrate`**: Database migrations
- **`ncruces/go-sqlite3`**: Pure-Go SQLite driver (WebAssembly-based)

---

**Last Updated**: February 2026  
**Version**: Reflects unified WriteBatcher architecture (Feb 2026), domain-driven package structure (config, files, handlers, middleware, session, ui, validation, interfaces), minimal orchestrator `App`, and service interfaces (`ConfigService`, `FileProcessor`, `SessionManager`, `HandlerQueries`). Includes test reorganization with build tags for unit/integration separation.
