# Architecture Diagrams

This document contains Mermaid diagrams illustrating the SFPG application architecture.

> **Note:** Key diagrams are embedded directly in [`ARCHITECTURE.md`](../ARCHITECTURE.md) where they're explained in context.
> This file collects all diagrams in one place for easy reference, editing, and exporting.

## Table of Contents

1. [System Overview](#system-overview)
2. [Request Flow](#request-flow)
3. [Authentication Flow](#authentication-flow)
4. [File Processing Pipeline](#file-processing-pipeline)
5. [Cache Architecture](#cache-architecture)
6. [Database Architecture](#database-architecture)
7. [Configuration Flow](#configuration-flow)
8. [Component Dependencies](#component-dependencies)

---

## System Overview

High-level architecture showing major components and their relationships:

```mermaid
graph TB
    subgraph "Client Layer"
        Browser[Web Browser]
    end

    subgraph "Server Layer"
        Router[HTTP Router]
        CacheMW[Cache Middleware]
        CompressMW[Compression Middleware]
        CSRFMW[CSRF Protection]
        Handlers[Handler Groups]
    end

    subgraph "Application Layer"
        App[App Orchestrator]
        ConfigSvc[Config Service]
        FileProc[File Processor]
        SessionMgr[Session Manager]
        AuthSvc[Auth Service]
    end

    subgraph "Data Layer"
        MainDB[(Main SQLite DB<br/>sfpg.db)]
        ThumbsDB[(Thumbs SQLite DB<br/>thumbs/thumbs.db)]
        ROConn[(Read-Only Pool)]
        RWConn[(Read-Write Pool)]
    end

    subgraph "Background Workers"
        Pool[Worker Pool]
        WriteBatcher[Unified WriteBatcher<br/>File metadata + cache writes]
        Preload[Cache Preload]
    end

    subgraph "Storage"
        FileSystem[Image Files]
    end

    Browser --> Router
    Router --> CacheMW
    CacheMW --> CompressMW
    CompressMW --> CSRFMW
    CSRFMW --> Handlers

    Handlers --> App
    App --> ConfigSvc
    App --> FileProc
    App --> SessionMgr
    App --> AuthSvc

    App --> ROConn
    App --> RWConn
    ROConn --> MainDB
    RWConn --> MainDB
    RWConn --> ThumbsDB

    Handlers --> WriteBatcher
    WriteBatcher --> RWConn
    Handlers --> Preload
    Preload --> ROConn

    FileProc --> Pool
    Pool --> FileSystem
    Pool --> RWConn
```

---

## Request Flow

Detailed flow of a typical HTTP request through the system:

```mermaid
sequenceDiagram
    participant Client as Browser
    participant Router as HTTP Router
    participant CacheMW as Cache Middleware
    participant CompressMW as Compression Middleware
    participant CSRFMW as CSRF Protection
    participant Handler as Handler
    participant Service as Service
    participant DB as Database

    Client->>Router: GET /gallery/1
    Router->>CacheMW: Forward
    CacheMW->>CompressMW: Forward
    CompressMW->>CSRFMW: Forward
    CSRFMW->>CSRFMW: Allow safe method

    CSRFMW->>CacheMW: Forward
    CacheMW->>CacheMW: Check cache (cache key)
    alt Cache Hit
        CacheMW-->>Client: Return cached response (304 or 200)
    else Cache Miss
        CacheMW->>Handler: Forward
        Handler->>Service: Fetch data
        Service->>DB: Query
        DB-->>Service: Results
        Service-->>Handler: Data

        Handler-->>CacheMW: Response
        CacheMW->>CacheMW: Submit to write batcher
        CacheMW-->>Client: Return response
    end
```

---

## Authentication Flow

Login and session management flow:

```mermaid
stateDiagram-v2
    [*] --> Unauthenticated
    Unauthenticated --> LoginForm: GET /login-form
    LoginForm --> Unauthenticated: Cancel

    LoginForm --> Validating: POST /login
    Validating --> CredentialsCheck: Validate input
    CredentialsCheck --> Unauthenticated: Invalid
    CredentialsCheck --> SessionCreation: Valid

    SessionCreation --> Authenticated: Session created
    Authenticated --> Authenticated: Request with session cookie
    Authenticated --> Unauthenticated: Logout / Session expires

    note right of CredentialsCheck
        Uses bcrypt to verify
        against hashed password
        from database
    end note

    note right of SessionCreation
        Creates secure cookie
        with CSRF token
    end note
```

---

## File Processing Pipeline

How images are discovered, processed, and stored (updated Feb 2026 for unified WriteBatcher):

```mermaid
flowchart TD
    Start([App Start]) --> Walk{Walk Images Dir}
    Walk -->|File Found| Enqueue[Enqueue to Queue]
    Enqueue --> Worker{Worker Pool}

    Worker --> CheckModified{Modified Since<br/>Last Processed?}
    CheckModified -->|No| Skip[Skip Processing]
    CheckModified -->|Yes| MIME{Detect MIME Type}

    MIME -->|Not Image| Skip
    MIME -->|Image| ExtractEXIF[Extract EXIF Metadata]

    ExtractEXIF --> GenerateThumb[Generate Thumbnail]
    GenerateThumb --> SubmitBatcher[Submit to<br/>Unified WriteBatcher]

    SubmitBatcher --> Done([Processing Complete])
    Skip --> Done

    Walk -->|No More Files| Drain{Drain Queue}
    Drain --> DoneAll([All Workers Complete])

    style Worker fill:#f9f,stroke:#333,stroke-width:2px
    style SubmitBatcher fill:#bbf,stroke:#333,stroke-width:2px
```

---

## Unified WriteBatcher Architecture

The unified WriteBatcher consolidates all high-volume database writes (added Feb 2026, persistent overflow added Jun 2026):

```mermaid
graph TB
    subgraph "Write Sources"
        FileProc[File Processor<br/>File metadata + thumbnails]
        CacheMW[Cache Middleware<br/>HTTP response cache]
    end

    subgraph "Unified Batcher"
        Adapter[Batcher Adapter<br/>UnifiedBatcher interface]
        Channel[In-memory Channel<br/>bounded: 4096 items, 8MB]
        DQue["On-disk Overflow Queue<br/>dque: &lt;db&gt;-dque/<br/>segment-backed FIFO"]
        Worker[Background Worker<br/>flushes periodically + drains dque]
    end

    subgraph "Database"
        Tx[Single Transaction<br/>prepared statements via WithTx]
        Files[Files Table]
        Cache[HTTP Cache Table]
    end

    subgraph "Resource Management"
        Cleanup[Cleanup Function<br/>Returns pooled resources]
        ThumbnailPool[Thumbnail Buffer Pool]
        CachePool[Cache Entry Pool]
    end

    FileProc -->|SubmitFile| Adapter
    CacheMW -->|SubmitCache| Adapter

    Adapter --> Channel
    Channel -->|full| DQue
    Channel --> Worker
    DQue -->|dqNotify wake + drain| Worker

    Worker --> Tx
    Tx --> Files
    Tx --> Cache

    Worker --> Cleanup
    Cleanup --> ThumbnailPool
    Cleanup --> CachePool

    style Adapter fill:#e1f5e1
    style Worker fill:#ffe1e1
    style Tx fill:#e1e1ff
    style DQue fill:#e1f1ff
    style Cleanup fill:#fff4e1
```

Invalid-file cleanup now happens inside the `File` flush path via the `HadInvalidEntry` flag (not a separate batched variant). `dque` acquires a `flock` via `internal/flock`; pending writes in `dque` survive process restarts (crash recovery) and are drained on `Close()`/context cancel.

---

## Cache Architecture

HTTP cache with preload and unified batcher integration (updated Feb 2026):

```mermaid
graph TB
    subgraph "Request Path (Synchronous)"
        Request[Incoming Request]
        CacheMW[HTTP Cache Middleware]
        Handler[Handler]
        Response[Response to Client]
    end

    subgraph "Cache Layer"
        CacheDB[(HTTP Cache DB)]
        Index[Indexes: content_length,<br/>created_at]
    end

    subgraph "Unified Write Path"
        Batcher[Unified WriteBatcher]
        FlushWorker[Background Flush Worker]
        AtomicCounter[Atomic Size Counter]
    end

    subgraph "Async Workers"
        PreloadWorker[Preload Worker]
    end

    subgraph "Post-Flush"
        EvictStep[maybeEvictCacheEntries]
    end

    Request --> CacheMW
    CacheMW -->|Cache Check| CacheDB
    CacheDB -->|Hit| Response
    CacheDB -->|Miss| Handler
    Handler -->|Generate| Response
    Response -->|Submit Entry| Batcher

    Batcher -->|Queue| FlushWorker
    FlushWorker -->|Write Batch| CacheDB
    FlushWorker --> AtomicCounter

    Batcher -->|OnSuccess| EvictStep
    EvictStep -->|Check Size| CacheDB
    EvictStep -->|EvictLRU| CacheDB

    Response -->|Gallery Hit| PreloadWorker
    PreloadWorker -->|Fetch Related| CacheDB

    CacheDB -.-> Index

    style CacheMW fill:#bfb
    style Batcher fill:#e1e1ff
    style FlushWorker fill:#fbb
    style AtomicCounter fill:#ff9
    style EvictStep fill:#fbf
```

---

## Database Architecture

Connection pooling and schema organization:

```mermaid
graph TB
    subgraph "Connection Pools"
        RO[Read-Only Pool<br/>db_max_pool_size, default 100]
        RW[Read-Write Pool<br/>db_max_pool_size, default 100]
    end

    subgraph "Database Files"
        SQLiteFile[sfpg.db]
        ThumbsFile[thumbs/thumbs.db]
    end

    subgraph "Main Schema Tables"
        Files[files<br/>---------<br/>id, folder_id, path_id,<br/>filename, mime_type,<br/>width, height,<br/>size_bytes, mtime,<br/>md5, phash]
        Folders[folders<br/>-----------<br/>id, path_id, parent_id,<br/>name, mtime, tile_id]
        FilePaths[file_paths<br/>-----------<br/>id, path]
        FolderPaths[folder_paths<br/>---------------<br/>id, path]
        Exif[exif_metadata<br/>-----------------<br/>file_id, json]
        Config[config<br/>-------<br/>key, value,<br/>category, ...]
        HTTPCache[http_cache<br/>-----------<br/>key, method, path,<br/>encoding, etag,<br/>body, content_length,<br/>created_at, expires_at]
        LoginAttempts[login_attempts<br/>-------------------<br/>username, failed_attempts,<br/>locked_until, last_attempt_at]
        ModuleState[module_state<br/>---------------<br/>name, active,<br/>last_started_at]
    end

    subgraph "Thumbs Schema Tables"
        Thumbnails[thumbnails<br/>-------------<br/>file_id, size_label,<br/>width, height,<br/>format, blob_id]
        ThumbnailBlobs[thumbnail_blobs<br/>-----------------<br/>id, data]
    end

    RO --> SQLiteFile
    RW --> SQLiteFile
    RW --> ThumbsFile

    RO -.->|SELECT| Files
    RO -.->|SELECT| Folders
    RO -.->|SELECT| FilePaths
    RO -.->|SELECT| FolderPaths
    RO -.->|SELECT| Exif
    RO -.->|SELECT| Config
    RO -.->|SELECT| HTTPCache
    RO -.->|SELECT| LoginAttempts
    RO -.->|SELECT| ModuleState

    RW -->|INSERT/UPDATE| Files
    RW -->|INSERT/UPDATE| Folders
    RW -->|INSERT/UPDATE| FilePaths
    RW -->|INSERT/UPDATE| FolderPaths
    RW -->|INSERT/UPDATE| Exif
    RW -->|INSERT/UPDATE| Config
    RW -->|INSERT/DELETE| HTTPCache
    RW -->|INSERT/UPDATE| LoginAttempts
    RW -->|INSERT/UPDATE| ModuleState
    RW -->|INSERT/UPDATE| Thumbnails
    RW -->|INSERT/UPDATE| ThumbnailBlobs

    style RO fill:#e1f5e1
    style RW fill:#ffe1e1
```

---

## Configuration Flow

How configuration is loaded, validated, and persisted:

```mermaid
flowchart LR
    subgraph "Sources"
        Defaults[Default Values]
        DB[(Database Config)]
        YAML[YAML Files]
        CLI[CLI Flags]
        ENV[Environment<br/>Variables]
    end

    subgraph "Loading Process"
        Merge1[Merge Defaults + DB]
        Merge2[Override with YAML]
        Merge3[Override with CLI/ENV]
        Validate[Validate]
        Apply[Apply to App]
    end

    subgraph "Runtime"
        RuntimeConfig[Runtime Config]
        ConfigService[Config Service]
    end

    subgraph "Persistence"
        Save[Save to DB]
        Export[Export YAML]
        Import[Import YAML]
    end

    Defaults --> Merge1
    DB --> Merge1
    Merge1 --> Merge2
    YAML --> Merge2
    Merge2 --> Merge3
    CLI --> Merge3
    ENV --> Merge3
    Merge3 --> Validate
    Validate --> Apply

    Apply --> RuntimeConfig
    RuntimeConfig --> ConfigService

    ConfigService <--> Save
    Save --> DB

    ConfigService <--> Export
    Import --> ConfigService

    style Validate fill:#ff9
    style RuntimeConfig fill:#9cf
```

---

## Component Dependencies

Package dependency graph showing coupling (updated Feb 2026):

```mermaid
graph TD
    subgraph "Root server"
        App[app.go: App]
        Server[server.go: Serve]
        Router[router.go: Routes]
        BatchWrite[batched_write.go: BatchedWrite]
        BatchFlush[batched_write_flush.go: flushBatchedWrites]
        BatchAdapter[batcher_adapter.go: Adapter]
        CacheSubmit[cache_submit.go: submitCacheWrite]
    end

    subgraph "Handler Groups"
        AuthH[handlers/auth_handlers.go]
        GalleryH[handlers/gallery_handlers.go]
        ConfigH[handlers/config_handlers.go]
        DashboardH[handlers/dashboard_handlers.go]
        ServerH[handlers/server_handlers.go]
        ThemeH[handlers/theme_handlers.go]
        MenuH[handlers/menu_handlers.go]
        HealthH[handlers/health_handlers.go]
    end

    subgraph "Services"
        ConfigSvc[config/service.go]
        FileProc[files/processor.go]
        SessionMgr[session/manager.go]
        AuthSvc[auth/service.go]
    end

    subgraph "Middleware"
        AuthMW[middleware/auth.go]
        CacheMW[cachelite/middleware.go]
        CSRFMW[middleware/csrf.go]
        LogMW[middleware/logging.go]
    end

    subgraph "Database"
        DBConn[dbconnpool/]
        GalleryDB[gallerydb/queries.sql]
    end

    subgraph "Support"
        UI[ui/templates.go]
        TemplateData[template/data.go]
        Validation[validation/rules.go]
        Security[security/lockout.go]
        WriteBatch[writebatcher/]
        DQue[dque/]
        Flock[flock/]
        GalleryLib[gallerylib/importer.go]
        PathUtil[pathutil/path.go]
        CacheBatch[cachebatch/]
        CachePreload[cachepreload/]
        ModuleState[modulestate/]
        Metrics[metrics/]
    end

    Router --> AuthH
    Router --> GalleryH
    Router --> ConfigH
    Router --> DashboardH
    Router --> ServerH
    Router --> ThemeH
    Router --> MenuH
    Router --> HealthH

    App --> ConfigSvc
    App --> FileProc
    App --> SessionMgr
    App --> AuthSvc
    App --> BatchWrite
    App --> BatchFlush
    App --> BatchAdapter
    App --> CacheBatch
    App --> CachePreload
    App --> ModuleState
    App --> Metrics

    App --> Router
    App --> Server

    AuthH --> AuthMW
    AuthH --> SessionMgr
    AuthH --> AuthSvc
    GalleryH --> FileProc
    GalleryH --> DBConn
    ConfigH --> ConfigSvc
    DashboardH --> Metrics
    ServerH --> CacheBatch

    Router --> CacheMW
    Router --> CSRFMW
    Router --> LogMW

    CacheMW --> CacheSubmit
    CacheSubmit --> BatchAdapter
    BatchAdapter --> WriteBatch
    WriteBatch --> DQue
    DQue --> Flock

    GalleryH --> UI
    ConfigH --> Validation
    ConfigH --> TemplateData
    AuthSvc --> Security

    FileProc --> BatchAdapter
    FileProc --> PathUtil
    FileProc --> GalleryLib

    BatchFlush --> FileProc
    BatchFlush --> GalleryLib
    BatchFlush --> CacheMW
    CacheBatch --> CachePreload

    style App fill:#f9f
    style ConfigSvc fill:#9cf
    style FileProc fill:#9cf
    style SessionMgr fill:#9cf
    style BatchWrite fill:#e1e1ff
    style BatchFlush fill:#e1e1ff
    style BatchAdapter fill:#e1e1ff
    style DQue fill:#e1f1ff
    style Flock fill:#e1f1ff
    style GalleryLib fill:#e1f5e1
```

---

## How to View These Diagrams

### Option 1: GitHub/GitLab rendering

Simply view this file on GitHub or GitLab - they render Mermaid diagrams natively.

### Option 2: VS Code

Install the "Markdown Preview Mermaid Support" extension and open this file.

### Option 3: Online

- https://mermaid.live/ - Live editor
- Copy any diagram code to preview

### Option 4: CLI

```bash
npx @mermaid-js/mermaid-cli -i docs/diagrams/ARCHITECTURE_DIAGRAMS.md -o output.png
```

---

## Diagram Maintenance Tips

1. **Keep diagrams simple**: Focus on the most important flows
2. **Update with code changes**: When you refactor, update the diagrams
3. **Use consistent styling**: Similar components should use similar colors
4. **Add notes**: Use `note right of` to explain complex logic
5. **Test rendering**: View in GitHub before committing

---

## Next Steps

Consider adding:

- Performance optimization flow (cache preload decision tree)
- Error handling flows
- Restart/reload flow
- Test architecture diagrams
