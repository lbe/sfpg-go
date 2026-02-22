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
        AuthMW[Auth Middleware]
        CacheMW[Cache Middleware]
        Handlers[Handler Groups]
    end

    subgraph "Application Layer"
        App[App Orchestrator]
        ConfigSvc[Config Service]
        FileProc[File Processor]
        SessionMgr[Session Manager]
    end

    subgraph "Data Layer"
        SQLite[(SQLite Database)]
        ROConn[(Read-Only Pool)]
        RWConn[(Read-Write Pool)]
    end

    subgraph "Background Workers"
        Pool[Worker Pool]
        CacheQ[Cache Write Queue]
        Preload[Cache Preload]
    end

    subgraph "Storage"
        FileSystem[Image Files]
        Thumbnails[Thumbnails]
    end

    Browser --> Router
    Router --> AuthMW
    AuthMW --> CacheMW
    CacheMW --> Handlers

    Handlers --> App
    App --> ConfigSvc
    App --> FileProc
    App --> SessionMgr

    App --> ROConn
    App --> RWConn
    ROConn --> SQLite
    RWConn --> SQLite

    Handlers <--> CacheQ
    CacheQ --> Preload
    Preload --> ROConn

    FileProc --> Pool
    Pool --> FileSystem
    Pool --> Thumbnails
    Pool --> RWConn
```

---

## Request Flow

Detailed flow of a typical HTTP request through the system:

```mermaid
sequenceDiagram
    participant Client as Browser
    participant Router as HTTP Router
    participant AuthMW as Auth Middleware
    participant CacheMW as Cache Middleware
    participant Handler as Handler
    participant Service as Service
    participant DB as Database

    Client->>Router: GET /gallery/1
    Router->>AuthMW: Forward
    AuthMW->>AuthMW: Check session cookie
    AuthMW->>Router: Forward (authenticated)

    Router->>CacheMW: Forward
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
        CacheMW->>CacheMW: Store in cache
        CacheMW-->>Client: Return response

        par Async
            CacheMW->>DB: Queue cache write
        end
    end
```

---

## Authentication Flow

Login and session management flow:

```mermaid
stateDiagram-v2
    [*] --> Unauthenticated
    Unauthenticated --> LoginForm: GET /login
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

The unified WriteBatcher consolidates all high-volume database writes (added Feb 2026):

```mermaid
graph TB
    subgraph "Write Sources"
        FileProc[File Processor<br/>File metadata + thumbnails]
        InvalidFile[Invalid File Tracker<br/>Failed processing records]
        CacheMW[Cache Middleware<br/>HTTP response cache]
    end

    subgraph "Unified Batcher"
        Adapter[Batcher Adapter<br/>UnifiedBatcher interface]
        Queue[Write Queue<br/>Bounded: 10000 items, 10MB]
        Worker[Background Worker<br/>Flushes periodically]
    end

    subgraph "Database"
        Tx[Single Transaction]
        Files[Files Table]
        Invalid[Invalid Files Table]
        Cache[HTTP Cache Table]
    end

    subgraph "Resource Management"
        Cleanup[Cleanup Function<br/>Returns pooled resources]
        ThumbnailPool[Thumbnail Buffer Pool]
        CachePool[Cache Entry Pool]
    end

    FileProc -->|SubmitFile| Adapter
    InvalidFile -->|SubmitInvalidFile| Adapter
    CacheMW -->|SubmitCache| Adapter

    Adapter --> Queue
    Queue --> Worker
    Worker --> Tx

    Tx --> Files
    Tx --> Invalid
    Tx --> Cache

    Worker --> Cleanup
    Cleanup --> ThumbnailPool
    Cleanup --> CachePool

    style Adapter fill:#e1f5e1
    style Worker fill:#ffe1e1
    style Tx fill:#e1e1ff
    style Cleanup fill:#fff4e1
```

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
        RO[Read-Only Pool<br/>10 connections]
        RW[Read-Write Pool<br/>2 connections]
    end

    subgraph "Database File"
        SQLiteFile[sfpg.db]
    end

    subgraph "Schema Tables"
        Files[files<br/>---------<br/>id, folder_id,<br/>filename, mime_type,<br/>width, height,<br/>exif_json,<br/>last_modified]
        Folders[folders<br/>-----------<br/>id, path,<br/>parent_id,<br/>name]
        Thumbnails[thumbnails<br/>-------------<br/>id, file_id,<br/>size, width,<br/>height, mime_type,<br/>created_at]
        Config[config<br/>-------<br/>key, value,<br/>type]
        HTTPCache[http_cache<br/>-----------<br/>id, path, etag,<br/>content_length,<br/>created_at]
        Admin[admin<br/>------<br/>id, username,<br/>password_hash,<br/>failed_attempts,<br/>locked_until]
    end

    RO --> SQLiteFile
    RW --> SQLiteFile

    RO -.->|SELECT| Files
    RO -.->|SELECT| Folders
    RO -.->|SELECT| Thumbnails
    RO -.->|SELECT| Config
    RO -.->|SELECT| HTTPCache

    RW -->|INSERT/UPDATE| Files
    RW -->|INSERT/UPDATE| Folders
    RW -->|INSERT/UPDATE| Thumbnails
    RW -->|INSERT/UPDATE| Config
    RW -->|INSERT/DELETE| HTTPCache
    RW -->|UPDATE| Admin

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
        CLI[CLI Flags]
        ENV[Environment<br/>Variables]
    end

    subgraph "Loading Process"
        Merge1[Merge Defaults + DB]
        Merge2[Override with CLI/ENV]
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
    CLI --> Merge2
    ENV --> Merge2
    Merge2 --> Validate
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
        HealthH[handlers/health_handlers.go]
    end

    subgraph "Services"
        ConfigSvc[config/service.go]
        FileProc[files/processor.go]
        SessionMgr[session/manager.go]
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
        Validation[validation/rules.go]
        WriteBatch[writebatcher/]
        PathUtil[pathutil/path.go]
    end

    Router --> AuthH
    Router --> GalleryH
    Router --> ConfigH
    Router --> HealthH

    App --> ConfigSvc
    App --> FileProc
    App --> SessionMgr
    App --> BatchWrite
    App --> BatchFlush
    App --> BatchAdapter

    App --> Router
    App --> Server

    AuthH --> AuthMW
    AuthH --> SessionMgr
    GalleryH --> FileProc
    GalleryH --> DBConn
    ConfigH --> ConfigSvc

    Router --> CacheMW
    Router --> CSRFMW
    Router --> LogMW

    CacheMW --> CacheSubmit
    CacheSubmit --> BatchAdapter
    BatchAdapter --> WriteBatch

    GalleryH --> UI
    ConfigH --> Validation

    FileProc --> BatchAdapter
    FileProc --> PathUtil

    BatchFlush --> FileProc
    BatchFlush --> CacheMW

    style App fill:#f9f
    style ConfigSvc fill:#9cf
    style FileProc fill:#9cf
    style SessionMgr fill:#9cf
    style BatchWrite fill:#e1e1ff
    style BatchFlush fill:#e1e1ff
    style BatchAdapter fill:#e1e1ff
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
