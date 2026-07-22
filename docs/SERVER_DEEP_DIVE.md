# Server Package Deep Dive

**Package**: `github.com/lbe/sfpg-go/internal/server`

> **Note:** This document is an archived entry point to the server package. For the full, up-to-date architectural coverage — including the middleware stack, route table, handler design, session management, caching strategy, background processing, security model, configuration, and testing approach — see **[docs/ARCHITECTURE.md](ARCHITECTURE.md)** (the authoritative architecture reference).

---

## Quick Summary

The `internal/server` package implements the entire web server layer of SFPG. It is structured around these key components:

| Component                   | File(s)                     | Purpose                                                                                                                                                                      |
| --------------------------- | --------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **`App`**                   | `app.go`                    | Central orchestrator; embeds manager structs for infrastructure, config, auth, handlers, runtime, and subsystems                                                             |
| **`InfrastructureService`** | `infrastructure_service.go` | Owns database pools, HTTP cache, write batcher, filesystem paths, importer factory                                                                                           |
| **`ConfigManager`**         | `config/config_manager.go`  | `config.ConfigManager` — loaded `Config`, `ConfigService`, ETag version; `App` delegates to it                                                                               |
| **`SessionAuthFacade`**     | `auth_service.go`           | Session store, session manager, authentication                                                                                                                               |
| **`HandlerManager`**        | `handler_manager.go`        | All HTTP handler groups (auth, gallery, config, dashboard, server, menu, theme, health)                                                                                      |
| **`RuntimeManager`**        | `runtime_manager.go`        | Context/cancel, HTTP server, restart state, profiler, gallery stats cache                                                                                                    |
| **`SubsystemManager`**      | `subsystem_manager.go`      | Worker pool, queue, file processor, scheduler, cache preload, cache batch load, module state                                                                                 |
| **`testseams.go`**          | `testseams.go`              | Optional test doubles for `App` and managers (`AppTestSeams`, `InfrastructureTestSeams`, `RuntimeManagerTestSeams`, `HandlerManagerTestSeams`); zero value = production      |
| **`Server`**                | `server.go`                 | HTTP server lifecycle, middleware helpers, auth middleware wiring                                                                                                            |
| **Router**                  | `router.go`                 | Route registration; wires handler groups and middleware into the mux                                                                                                         |
| **`batched_write.go`**      | —                           | Unified WriteBatcher: high-throughput batched writes for file metadata + HTTP cache, with persistent on-disk overflow queue (`dque`) for burst absorption and crash recovery |

### Subpackages

- **`auth/`** — Authentication service (credential validation, bcrypt, lockout, `AuthService` interface)
- **`cachebatch/`** — Batch cache loading engine (route enumeration, parallel HTTP fetch)
- **`cachepreload/`** — Cache preload scheduler and folder preload task execution
- **`conditional/`** — Conditional request handling (`If-None-Match`, `If-Modified-Since`)
- **`config/`** — Config domain (service, loader, saver, validator, exporter)
- **`database/`** — SQLite pool creation, WAL mode configuration, schema access
- **`files/`** — File processing (processor, service, walker, thumbnail, metadata)
- **`handlers/`** — HTTP handler groups (auth, config, dashboard, gallery, health, menu, server, theme)
- **`interfaces/`** — Shared interfaces (`HandlerQueries`, `Server`, etc.) for testability
- **`logging/`** — Structured request/response logging middleware
- **`metrics/`** — Prometheus-style metrics (gallery stats, cache hit rates, worker pool depth)
- **`middleware/`** — Auth, conditional, logging middleware implementations (`http.CrossOriginProtection` is wired in `router.go`)
- **`modulestate/`** — Application module state tracking and lifecycle
- **`pathutil/`** — Path normalization and validation utilities
- **`security/`** — Security helpers (account lockout thresholds, lockout duration formatting, failed attempt tracking, per-IP login rate limiting via `IPRateLimiter`)
- **`session/`** — Session manager and cookie configuration
- **`template/`** — Template data helper functions (auth data, common data injection for template rendering)
- **`ui/`** — Template file registration and rendering coordination
- **`validation/`** — Username/password validation

### Architecture Principles

1. **Separation of Concerns**: Database logic in `gallerydb`, UI templates in `ui`, caching in `cachelite`, server orchestration here.
2. **Interface-Based Design**: Heavy use of interfaces (`HandlerQueries`, `ConfigService`, `FileProcessor`, `SessionManager`, `AuthService`) for testability and decoupling.
3. **Minimal Orchestrator**: The `App` struct embeds focused managers rather than holding fields directly — each manager owns a clear domain.
4. **Idempotency**: File processing is idempotent; re-running produces the same database state.
5. **Security First**: Multiple layers of protection (auth, cross-origin protection, path validation, session security).
6. **Explicit Test Seams**: Optional doubles in `testseams.go` wired through unexported `testSeams` fields; no `testHook*` pollution on production structs. See [ARCHITECTURE.md §Test Seams](ARCHITECTURE.md#test-seams).

---

## Request Flow

```
Incoming Request
  → Logging Middleware
    → HTTP Cache Middleware (cachelite: SQLite-backed response cache)
      → CrossOriginProtection (same-origin check for unsafe methods)
        → Route Mux
          → Authentication (applied selectively to protected routes)
            → Route Handler
```

Route handlers are organized by domain (auth, gallery, config, dashboard, server, menu, theme, health) and registered in `router.go`. Full route tables are documented in `ARCHITECTURE.md`.

---

## Key Design Decisions

1. **Two Database Pools** — Separate `dbRoPool` (read-only, long-lived queries) and `dbRwPool` (read-write, short-lived transactions) prevent read-heavy gallery browsing from blocking write operations.
2. **WAL Mode** — SQLite Write-Ahead Logging enables concurrent reads during writes, critical for the read-heavy gallery workload.
3. **Cookie Sessions** — No external session store; encrypted cookies keep deployment simple; COP handles cross-site request protection.
4. **Worker Pool** — Dynamic scaling based on queue depth; processes file imports and thumbnail generation concurrently without overwhelming system resources.
5. **Unified WriteBatcher** — Consolidates file metadata and HTTP cache writes into a single batched, transactional writer with persistent overflow queue (`dque`), reducing lock contention and improving throughput.
6. **HTMX-Based UI** — Server-rendered HTML with HTMX for interactivity minimizes JavaScript complexity while maintaining a responsive experience.

---

## References

- **[docs/ARCHITECTURE.md](ARCHITECTURE.md)** — Full application architecture, middleware stack, route table, database schema, caching strategy, security model, configuration, and testing approach.
- **External Dependencies**: See [ARCHITECTURE.md §Appendix](ARCHITECTURE.md#external-dependencies).
