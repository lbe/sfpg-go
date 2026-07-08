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

| Sub-package        | Purpose                                                                                                                                                                  |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **`auth`**         | Authentication service interface and implementation (`AuthService`).                                                                                                     |
| **`cachebatch`**   | Batch cache loading manager for warming HTTP cache entries.                                                                                                              |
| **`cachepreload`** | Cache preload scheduler and folder preload task execution.                                                                                                               |
| **`compress`**     | Pure functions for compression content negotiation: encoding selection, content type checking, path extension checking.                                                  |
| **`conditional`**  | Pure functions for HTTP conditional requests: ETag matching, Last-Modified comparison.                                                                                   |
| **`config`**       | Configuration management: loader, saver, validator, exporter, and `ConfigService` implementation. Loads/saves config from the database, validates, exports/imports YAML. |
| **`database`**     | Database setup, DSN configuration, connection pool creation, and migration orchestration for `sfpg.db` and `thumbs.db`.                                                  |
| **`files`**        | File processing: discovery, MIME detection, EXIF extraction, thumbnail generation. Exposes `FileProcessor` interface and worker-pool integration.                        |
| **`handlers`**     | HTTP handlers (auth, gallery, config, dashboard, server, menu, theme, health). Split into focused handler groups, each with minimal dependencies.                        |
| **`interfaces`**   | Shared interfaces such as `HandlerQueries` (DB queries used by handlers).                                                                                                |
| **`logging`**      | Bootstrap and runtime logging setup/reload.                                                                                                                              |
| **`metrics`**      | Centralized metrics collection for the dashboard.                                                                                                                        |
| **`middleware`**   | Reusable middleware: auth, compress, conditional (ETag/304), CSRF, logging.                                                                                              |
| **`modulestate`**  | Persistence of module run state (active, last started/finished) in the `module_state` table.                                                                             |
| **`pathutil`**     | Path manipulation utilities: directory prefix removal with path traversal security checks.                                                                               |
| **`security`**     | Pure functions for security calculations: account lockout thresholds, lockout duration formatting, failed attempt tracking.                                              |
| **`session`**      | Session store, CSRF helpers, cookie options. `SessionManager` interface and `Manager` implementation.                                                                    |
| **`template`**     | Pure functions for building template data maps: authentication state, CSRF token addition.                                                                               |
| **`ui`**           | Template parsing and rendering. Used by handlers for HTML output.                                                                                                        |
| **`validation`**   | Input validation (e.g. username, password rules). Used by config and admin handlers.                                                                                     |

**Root `server`** retains: `App`, `server.go` (lifecycle, middleware wiring), `router.go` (route registration), `handler_manager.go`, `subsystem_manager.go`, `runtime_manager.go`, `infrastructure_service.go`, `auth_service.go`, `config_manager.go`, `restart.go`, and test helpers. The root files are not subpackages.

---

## Core Components

### App Struct (Minimal Orchestrator)

The `App` struct (`app.go`) is the central application context. It acts as a **minimal orchestrator**: it embeds focused managers that own infrastructure, configuration, authentication, handlers, runtime lifecycle, and background subsystems. It holds only what cannot live inside a single manager.

**Embedded managers:**

| Field                   | Type                     | Purpose                                                                                       |
| ----------------------- | ------------------------ | --------------------------------------------------------------------------------------------- |
| `InfrastructureService` | `*InfrastructureService` | Database pools, HTTP cache, write batcher, file-system paths, importer factory.               |
| `ConfigManager`         | `*ConfigManager`         | Loaded `Config`, `ConfigService`, ETag version.                                               |
| `AuthService`           | `*AuthService`           | Session store, session manager, authentication service, CSRF helpers.                         |
| `HandlerManager`        | `*HandlerManager`        | All HTTP handler groups (auth, gallery, config, dashboard, server, menu, theme, health).      |
| `RuntimeManager`        | `*RuntimeManager`        | Context/cancel, HTTP server, restart state, profiler, gallery stats cache.                    |
| `SubsystemManager`      | `*SubsystemManager`      | Worker pool, queue, file processor, scheduler, cache preload, cache batch load, module state. |

**Root-level fields:** `logger`, `opt`, and test seams.

**Note on Unified WriteBatcher (Feb 2026, updated Jun 2026):** The application uses a single `writeBatcher` instance to handle all high-volume database writes (file metadata and HTTP cache entries), consolidating these two previously independent write paths into one batched, transactional writer. The `BatchedWrite` union has two variants: `File` (file metadata + EXIF + thumbnails) and `CacheEntry` (HTTP response cache). Invalid-file cleanup happens inside the `File` flush path via the `HadInvalidEntry` flag rather than a separate batched variant; invalid-file records themselves are written directly to the RW pool as processing failures occur (`recordInvalidFileFromPath`). When the in-memory channel fills, writes overflow to a persistent on-disk queue (`dque`, stored in `DB/sfpg.db-dque/`), which absorbs bursts and recovers pending writes across process restarts.

### Key Files

- **`app.go`**: Application initialization, configuration, database setup, service wiring.
- **`server.go`**: HTTP server lifecycle, middleware helpers, auth middleware.
- **`router.go`**: Route registration; wires handler groups and middleware.
- **`handlers/`**: HTTP handlers (auth, gallery, config, dashboard, server, menu, theme, health).
- **`config/`**: Config domain (service, loader, saver, validator, exporter).
- **`files/`**: File processing (processor, service, walker, thumbnail, metadata).
- **`ui/`**: Template rendering.
- **`middleware/`**: Auth, compress, conditional, CSRF, logging.
- **`session/`**: Session manager and options.
- **`validation/`**: Username/password validation.
- **`helpers_test.go`**: Shared helpers for tests (`CreateApp`, `MakeAuthCookie`, etc.).

---

## Service Interfaces

Domain logic is accessed through interfaces. App creates concrete implementations and injects them into `Handlers` and other consumers.

### ConfigService (`config`)

```go
type ConfigService interface {
    // ConfigStore
    Load(ctx context.Context) (*Config, error)
    Save(ctx context.Context, cfg *Config) error
    Validate(cfg *Config) error

    // ConfigAdmin
    Export(ctx context.Context) (string, error)
    Import(yamlContent string, ctx context.Context) error
    RestoreLastKnownGood(ctx context.Context) (*Config, error)
    EnsureDefaults(ctx context.Context, rootDir string) error
    GetConfigValue(ctx context.Context, key string) (string, error)
    IncrementETag(ctx context.Context) (string, error)
}
```

**Implementation**: `config.NewService(dbRwPool, dbRoPool)`. Uses loader, saver, validator, and exporter under the hood.

### FileProcessor (`files`)

```go
type FileProcessor interface {
    ProcessFile(ctx context.Context, path string) (*File, error)
    ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*File, error)
    CheckIfModified(ctx context.Context, path string) (bool, error)
    GenerateThumbnail(ctx context.Context, file *File) error
    RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error
    SubmitFileForWrite(file *File) error
    PendingWriteCount() int64
    Close() error
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
    GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error)
    SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error
    IsAuthenticated(r *http.Request) bool
    SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error
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
    GetPreloadRoutesByFolderID(ctx context.Context, parentID sql.NullInt64) (*sql.Rows, error)
    GetGalleryStatistics(ctx context.Context) (gallerydb.GetGalleryStatisticsRow, error)
}
```

Abstracts the subset of DB queries used by gallery/image/thumbnail handlers. Implemented by `*gallerydb.CustomQueries`; tests can inject alternatives via `app.testHookHandlerQueries`.

### AuthService (`auth`)

```go
type AuthService interface {
    Authenticate(ctx context.Context, username, password string) (*session.User, error)
    CheckLockout(ctx context.Context, username string) (bool, error)
    RecordFailedAttempt(ctx context.Context, username string) error
    ClearAttempts(ctx context.Context, username string) error
    UpdateCredentials(ctx context.Context, opts CredentialUpdateOptions, store CredentialStore) (*CredentialUpdateResult, error)
}
```

**Implementation**: `auth.NewService(store)`. Validates credentials against bcrypt hashes and manages account lockout.

### Handlers (`handlers`)

The `handlers` package contains discrete handler groups for different app domains. Each group is initialized with only the dependencies it needs (Dependency Injection).

**Key Handler Groups:**

- **`AuthHandlers`**: Manages login, logout, and session status.
- **`ConfigHandlers`**: Manages application settings, export/import, and admin credential updates. Depends on `AuthService` and `SessionManager`.
- **`GalleryHandlers`**: Manages image viewing, folder navigation, and thumbnail retrieval. Depends on `HandlerQueries` and read-only DB pools.
- **`DashboardHandlers`**: Serves the admin dashboard and `/api/metrics`.
- **`ServerHandlers`**: Provides server operations such as discovery, shutdown, and cache batch load.
- **`MenuHandlers`**: Serves the hamburger menu partial.
- **`ThemeHandlers`**: Handles theme selection modal and theme changes.
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
[CSRFProtection] - Enforces same-origin policy for unsafe methods.
    ↓
[Mux] -> Dispatches to handler groups (e.g. `h.GalleryByID`, `h.ConfigGet`).
```

**Route-Specific Middleware:**

- **`authMiddleware`**: Applied only to protected routes (e.g., `/config`, `/dashboard`, `/logout`, `/server/...`, `/debug/pprof/`) inside the mux. Public routes such as `/gallery/{id}` and `/image/{id}` do not use it.
- **`ConditionalMiddleware`**: This middleware, which handles `304 Not Modified` responses by buffering the response, is **selectively applied** only to lightweight page handlers (like `/gallery/{id}`, `/info/...`, etc.). It is explicitly **not** applied to large file handlers (`/raw-image/{id}`) to prevent buffering large files in memory.

### Authenticated Request Example

```
Client Request (with session cookie and "If-None-Match" ETag) to a protected route
    ↓
HTTP Router
    ↓
[loggingMiddleware]
    ↓
[cachelite.Middleware] - MISS: No entry in SQLite cache.
    ↓
[CompressMiddleware] - Wraps response writer for gzip/brotli.
    ↓
[CSRFProtection] - Allows GET request.
    ↓
[Mux] -> matches protected route
    ↓
[authMiddleware] (route-specific) - Authenticated, proceeds.
    ↓
[ConditionalMiddleware] - Wraps response writer to buffer response for 304 check.
    ↓
[handler] - Fetches data, sets ETag header, renders template into buffer.
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

- **Configurable workers**: Default `NumCPU - 2` (when NumCPU > 4; `2` for 3–4 cores; `1` otherwise); overridable via config `WorkerPoolMax` / `WorkerPoolMinIdle`
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
5. **EXIF Extraction**: Parse EXIF data (camera, orientation, datetime, etc.); only EXIF metadata is extracted
6. **Thumbnail Generation**:
   - Decode image (prefer embedded EXIF thumbnail when available)
   - Resize to thumbnail dimensions
   - Encode as JPEG
   - Store as a JPEG blob in `DB/thumbs/thumbs.db`
7. **Perceptual Hash**: Calculate pHash for duplicate detection
8. **Unified Batcher Submission**: Submit file metadata and thumbnail blob to the unified WriteBatcher
9. **Folder Tile Update**: Set folder's representative image

### 4. Unified WriteBatcher (Feb 2026 Refactoring, updated Jun 2026)

All file writes are now routed through a **single unified WriteBatcher** at the App level:

**Implementation Components:**

- **`internal/server/batched_write.go`**: Defines the `BatchedWrite` union type with two variants:
  - `File`: Complete file metadata including EXIF and thumbnail data
  - `CacheEntry`: HTTP response cache entries
  - Also implements `GobEncode`/`GobDecode` so items can be persisted to the on-disk overflow queue (`dque`).
- **`internal/server/batched_write_flush.go`**: Unified flush function that processes all write types in a single transaction; threads prepared statements via `WithTx(tx)` and constructs one per-batch `gallerylib.Importer`.
- **`internal/server/batcher_adapter.go`**: Adapter pattern that breaks circular dependency between `server` and `files` packages; returns `ErrClosed` when the batcher is nil.
- **`internal/server/files/gob.go`**: `GobEncode`/`GobDecode` for `files.File` (serializes the `*bytes.Buffer` thumbnail as raw `[]byte`).
- **`files.UnifiedBatcher` interface**: Allows `files` package to submit writes without depending on `server`

**Benefits:**

- **Eliminated Lock Contention**: One writer instead of several competing for SQLite's exclusive lock
- **Improved Throughput**: Batching reduces transaction overhead (from many small transactions to fewer large ones)
- **Burst Absorption & Crash Recovery**: Excess writes overflow to `dque` (in `DB/sfpg.db-dque/`) and survive restarts
- **Resource Cleanup**: Automatic return of pooled resources (thumbnail buffers, cache entries) after flush

**Flow:**

```
File Processor → UnifiedBatcher.SubmitFile() → WriteBatcher channel
Cache Middleware → UnifiedBatcher.SubmitCache() → WriteBatcher channel
                                    ↓
                       (on overflow) spill to dque (<db>-dque/)
                                    ↓
                          Background worker periodically flushes
                          (drains channel + dque, interleaved)
                                    ↓
                    flushBatchedWrites() in single transaction
                    (prepared statements via WithTx, per-batch Importer)
                                    ↓
                   Cleanup pooled resources (thumbnails, cache entries)
```

#### Persistent Overflow Queue (dque)

When `DQueDirPath` is configured (derived as `<db>-dque/`), a full in-memory channel causes `Submit` to overflow to `dque` rather than return `ErrFull`. Each overflow signals a buffer-1 `dqNotify` channel that wakes the worker's drain loop. The drain loop flushes `dque` items in `MaxBatchSize` batches **during** the drain, interleaves new channel items so they are not starved, and drains on context cancel / channel close / `Close()`. `overflowMu` and `overflowWG` plus the `mu`-then-`overflowMu` lock ordering in `Close()` guarantee concurrent `Submit`-during-`Close` loses nothing. `dque` holds a `flock` on its directory, so reconfiguration closes the old batcher before creating a new one.

Crash recovery: on startup `New()` seeds `pendingCount` from the existing `dque` size and, if the recovered queue exceeds channel capacity, buffers items locally before starting the worker to avoid startup deadlock.

#### Write-Path Throughput Optimizations

- **Prepared-statement threading**: `BeginTx` borrows a pooled connection and captures its prepared `*gallerydb.CustomQueries` into `app.batcherQueries`; `flushBatchedWrites` calls `WithTx(tx)` to bind all prepared statements to the transaction so every statement reuses its compiled plan instead of recompiling raw SQL per call. (`internal/gallerydb/prepared_invariant_test.go` pins this routing.)
- **Per-batch Importer memoization**: One `gallerylib.Importer` is constructed per batch and reused across all files. Its `folderCache` (path → folder ID) eliminates repeated per-segment `GetFolderByPath` queries in `UpsertPathChain`, and `tiledDirs` skips redundant folder-tile view queries and tile-chain updates for subsequent files in the same directory.
- **Skip no-op invalid_files delete**: The processor records `File.HadInvalidEntry`; `WriteFileInTx` only issues `DeleteInvalidFileByPath` when a row actually existed, removing a per-file no-op round-trip during fresh preloads.

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

The application uses **two SQLite databases**:

- **`DB/sfpg.db`**: Main application database (files, folders, config, sessions, module state, HTTP cache).
- **`DB/thumbs/thumbs.db`**: Thumbnail database (thumbnail metadata and JPEG blobs).

The RW and RO pools operate on the main database; the `thumbs` database is attached so that thumbnail reads and writes use the same connection lifecycle.

### Connection Pools

**Two separate pools** for different access patterns:

#### Read-Only Pool (`dbRoPool`)

- **Purpose**: Serve read-heavy web requests (gallery, image, search)
- **DSN**: `file:DB/sfpg.db?mode=ro`
- **Configuration**:
  - Max open connections: `db_max_pool_size` (default `100`)
  - Min idle connections: `db_min_idle_connections` (default `10`)
  - Read-only mode enforced by `mode=ro`; WAL mode is persistent from the RW pool.
- **WAL Mode**: Allows concurrent reads without blocking

#### Read-Write Pool (`dbRwPool`)

- **Purpose**: File processing, configuration updates, login
- **DSN**: `file:DB/sfpg.db?_pragma=journal_mode(WAL)&_txlock=immediate&mode=rwc` (plus shared pragmas)
- **Configuration**:
  - Max open connections: `db_max_pool_size` (default `100`, shared with RO)
  - Min idle connections: `db_min_idle_connections` (default `10`, shared with RO)
  - `journal_mode(WAL)` and `_txlock=immediate`
- **Single writer**: SQLite serializes writes, but WAL allows concurrent reads

Both pools share the same configurable size and are reconciled against effective values at startup/restart via `reconfigurePoolsFromConfig()`.

### Why Separate Pools?

1. **Isolation**: Web requests don't compete with background processing
2. **Performance**: Read-only pool can be larger (more concurrent web requests)
3. **Safety**: Read-only pool can't accidentally modify data
4. **Clarity**: Code makes access intent explicit

### Database Schema

**Key Tables (in `DB/sfpg.db`)**:

- **`files`**: Image metadata (path, size, mtime, md5, width, height, pHash)
- **`folders`**: Directory hierarchy, tile image references
- **`config`**: Application configuration (username, password hash)
- **`module_state`**: Tracks active state and run timestamps for background modules

**Key Tables (in `DB/thumbs/thumbs.db`)**:

- **`thumbnails`**: Thumbnail metadata
- **`thumbnail_blobs`**: JPEG thumbnail byte arrays

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

| Environment Variable    | Default  | Purpose                                    |
| ----------------------- | -------- | ------------------------------------------ |
| `SEPG_SESSION_HTTPONLY` | `true`   | Prevent JavaScript access (XSS protection) |
| `SEPG_SESSION_SECURE`   | `true`   | Require HTTPS (prevent interception)       |
| `SEPG_SESSION_MAX_AGE`  | `604800` | Cookie max age in seconds (default 7 days) |
| `SEPG_SESSION_SAMESITE` | `Lax`    | SameSite policy (Strict, Lax, None)        |

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
4. Response sets HTTP header `HX-Trigger: auth-changed` to signal frontend success
5. Response body is empty (menu refreshes independently via GET /hamburger-menu)

**Frontend** (`login-modal.html.tmpl`):

1. Form posts via HTMX with `hx-post="/login"` and `hx-swap="none"`
2. Hyperscript listens for the `auth-changed` event from the body: `on 'auth-changed' from body`
3. On event receipt, modal checkbox is unchecked: `set #login_modal.checked to false`
4. The `auth-changed` event also triggers the hamburger menu refresh via the `<ul>`'s `hx-trigger="pageshow from:window, auth-changed from:body"`
5. The menu fetches items from `GET /hamburger-menu` which reads the session directly

**Why Event-Driven?**

- Previous approach: Content-sniffing detection (checking if response includes 'Configuration' text) - brittle and unreliable
- Current approach: Explicit `HX-Trigger` header signals success cleanly
- The hamburger menu is decoupled from cached pages via a dedicated uncached endpoint (`GET /hamburger-menu`), avoiding stale auth state in HTTP cache

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

**Cross-Origin Protection** (`CSRFProtection` middleware):

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

- **Workers**: `NumCPU - 2` default (when NumCPU > 4; `2` for 3–4 cores; `1` otherwise); overridable via config `WorkerPoolMax` / `WorkerPoolMinIdle`
- **Queue size**: `10,000` paths (overridable via config `QueueSize`)
- **Timeout**: `10s` idle timeout before worker exits (overridable via `WorkerPoolMaxIdleTime`)

**Benefits**:

- **Bounded concurrency**: Prevents resource exhaustion
- **Backpressure**: Queue blocks when full
- **Graceful shutdown**: Workers drain queue before exit

### Memory Efficiency

**File Processing**:

- **Streaming**: Read files in chunks, don't buffer entirely
- **Seek-based**: Multiple passes using `file.Seek(0, 0)` - relies on OS disk cache

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

| Variable                      | Required | Default  | Purpose                                    |
| ----------------------------- | -------- | -------- | ------------------------------------------ |
| `SEPG_SESSION_SECRET`         | **Yes**  | -        | Session cookie encryption key              |
| `SEPG_SESSION_HTTPONLY`       | No       | `true`   | HttpOnly cookie flag                       |
| `SEPG_SESSION_SECURE`         | No       | `true`   | Secure cookie flag                         |
| `SEPG_SESSION_MAX_AGE`        | No       | `604800` | Cookie max age in seconds (default 7 days) |
| `SEPG_SESSION_SAMESITE`       | No       | `Lax`    | SameSite policy (Strict, Lax, None)        |
| `SFG_PORT`                    | No       | `8081`   | HTTP server port                           |
| `SFG_DISCOVER`                | No       | `false`  | Run discovery on startup                   |
| `SFG_DEBUG_DELAY_MS`          | No       | `0`      | Artificial debug delay in milliseconds     |
| `SFG_PROFILE`                 | No       | `""`     | Profiling mode (cpu, mem, block, ...)      |
| `SFG_COMPRESSION`             | No       | `true`   | Enable gzip/brotli compression             |
| `SFG_HTTP_CACHE`              | No       | `true`   | Enable SQLite HTTP response cache          |
| `SFG_CACHE_PRELOAD`           | No       | `true`   | Enable cache preloading on folder open     |
| `SFG_UNLOCK_ACCOUNT`          | No       | `""`     | Unlock a locked account by username        |
| `SFG_RESTORE_LAST_KNOWN_GOOD` | No       | `false`  | Restore last known good configuration      |

### Directory Structure

```
<application-root>/
├── DB/
│   ├── sfpg.db          # Main SQLite database
│   ├── sfpg.db-shm      # Shared memory (WAL)
│   ├── sfpg.db-wal      # Write-ahead log
│   ├── sfpg.db-dque/    # Persistent write overflow queue (dque, auto-created)
│   └── thumbs/
│       └── thumbs.db    # Thumbnail metadata and JPEG blobs
├── Images/              # Source images (scanned)
│   ├── folder1/
│   └── folder2/
└── logs/
    └── sfpg-*.log       # Application logs
```

### Command-Line Flags

Defined in `internal/getopt/opt.go`:

- `-port <int>`: TCP port for the HTTP server
- `-discover`: Run discovery on startup
- `-restore-last-known-good`: Restore last known good configuration from the database
- `-debug-delay-ms <int>`: Artificial debug delay in milliseconds
- `-profile <mode>`: Profiling mode (`cpu`, `mem`, `block`, ...)
- `-compression`: Enable gzip/brotli compression
- `-http-cache`: Enable SQLite HTTP response caching
- `-cache-preload`: Enable cache preloading when folders are opened
- `-unlock-account <username>`: Unlock a locked account
- `-increment-etag`: Increment application-wide ETag version on startup
- `-cache-batch-load`: Run cache batch load (warm HTTP cache) and exit

---

## Testing Strategy

### Test Helpers

**`helpers_test.go`**:

- **`CreateApp(t testing.TB, opts ...AppOption)`**: Creates a fully isolated test application instance with temporary directories and a dedicated database. Options include `WithPool()`, `WithRoot(dir)`, and `WithGetoptOpt(opt)`.
- **`MakeAuthCookie(t, app)`**: Generates an authenticated session cookie for a given test app instance, used for testing protected endpoints.

### Test Categories

#### 1. Unit Tests

- **`files_test.go`**: Image processing and thumbnail generation
- **`server_test.go`**: Router, middleware, path handling
- **`app_test.go`**: Configuration, database setup, directory initialization

#### 2. Integration Tests

- **`handlers_test.go`**: End-to-end request/response testing
- **`files_integration_test.go`**: Full file processing pipeline
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

**Storage**: `<application-root>/logs/sfpg-YYYY-MM-DD_HH-MM-SS.log`

### Database Maintenance

**Automatic**:

- `PRAGMA optimize` runs every hour (scheduled in `setDB()`)
- WAL checkpoint on shutdown and via the writebatcher `OnAfterCommit` callback, which forces a TRUNCATE checkpoint every 5 minutes or when the WAL file exceeds 256MB (lowered from 2GB). Running the checkpoint in the writebatcher worker avoids races with active transactions.

**Manual**:

```bash
# Vacuum database (reclaim space)
sqlite3 DB/sfpg.db "VACUUM;"

# Analyze tables (update statistics)
sqlite3 DB/sfpg.db "ANALYZE;"
```

### Monitoring

**Health Checks**:

- HTTP server listening: `curl http://localhost:8081/login`
- Database connectivity: Check RO pool `Get()`
- Worker pool status: Monitor queue length

---

## References

### Related Packages

- **`internal/server/auth`**: Authentication service interface and implementation
- **`internal/server/cachebatch`**: Batch HTTP cache warming manager
- **`internal/server/cachepreload`**: Cache preload scheduler and folder preload tasks
- **`internal/server/config`**: Config service, loader, saver, validator, exporter
- **`internal/server/database`**: Database setup, DSN configuration, and connection pool creation
- **`internal/server/files`**: File processing, walker, thumbnail, metadata; `FileProcessor` implementation
- **`internal/server/handlers`**: HTTP handlers (auth, gallery, config, dashboard, server, menu, theme, health)
- **`internal/server/logging`**: Bootstrap and runtime logging setup
- **`internal/server/metrics`**: Centralized metrics collection for the dashboard
- **`internal/server/middleware`**: Auth, compress, conditional, CSRF, logging
- **`internal/server/modulestate`**: `module_state` persistence
- **`internal/server/security`**: Pure functions for lockout calculations and security helpers
- **`internal/server/session`**: Session manager, cookie options, CSRF helpers
- **`internal/server/ui`**: Template rendering
- **`internal/server/validation`**: Username/password validation
- **`internal/server/interfaces`**: `HandlerQueries` and other shared interfaces
- **`internal/gallerydb`**: Database queries (generated by sqlc)
- **`internal/gallerylib`**: File import logic (per-batch folder/tiled-dir memoization)
- **`internal/dbconnpool`**: Connection pool implementation
- **`internal/workerpool`**: Worker pool implementation
- **`internal/writebatcher`**: Batch processing for efficient database writes (with optional persistent overflow)
- **`internal/dque`**: Persistent on-disk FIFO overflow queue used by writebatcher
- **`internal/flock`**: Cross-platform file locking used by dque
- **`internal/errors`**: Error sentinels used by dque
- **`internal/queue`**: Thread-safe queue
- **`internal/cachelite`**: SQLite-backed HTTP response cache
- **`internal/gen-test-files`**: Utility for generating synthetic test files and directory structures
- **`web`**: Embedded templates and static assets

### External Dependencies

- **`gorilla/sessions`**: Session management
- **`golang/crypto/bcrypt`**: Password hashing
- **`golang-migrate`**: Database migrations
- **`ncruces/go-sqlite3`**: Pure-Go SQLite driver (WebAssembly-based)

---

**Last Updated**: June 2026  
**Version**: Reflects manager-embedded `App` (`InfrastructureService`, `ConfigManager`, `AuthService`, `HandlerManager`, `RuntimeManager`, `SubsystemManager`), unified WriteBatcher architecture with persistent on-disk overflow queue (`dque`), gob serialization of `BatchedWrite`/`files.File`, prepared-statement threading (`BeginTx`/`WithTx`), per-batch `Importer` memoization, configurable DB pool sizing (`db_max_pool_size`/`db_min_idle_connections`), two-database setup (`sfpg.db` + `thumbs/thumbs.db`), domain-driven package structure including `auth`, `cachebatch`, `cachepreload`, `database`, `logging`, `metrics`, `modulestate`, and `security`, and service interfaces (`ConfigService`, `FileProcessor`, `SessionManager`, `HandlerQueries`, `auth.AuthService`).
