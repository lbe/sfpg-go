# interfaces package

Purpose: host shared contracts consumed by both the server orchestrator (`internal/server`) and the handlers package (`internal/server/handlers`) without creating import cycles. Interfaces in this package should represent cross-cutting dependencies that are injected into handlers.

Current contents:

- `HandlerQueries`: read-only gallery queries used by handlers and wired from `App` via `gallerydb` generated queries.
- `MetadataQueries`: EXIF and IPTC metadata reads consumed by handlers and satisfied by `*gallerydb.Queries` directly (no adapter).
- `ServerDeps`: the primary dependency-injection interface — 24 methods covering credentials, config operations, gallery queries, server control, and template rendering. Implemented by `*server.App`. Replaces the previous 15+ callback fields and 3 adapter types.
- `StartCacheBatchLoadResult`: struct shared by server and handlers for cache batch load outcomes.

Guidelines:

- Add an interface here only if it is consumed by both `server` and `handlers` (or other subpackages) and would otherwise create a dependency loop.
- Keep handler-only or server-only interfaces close to their packages; avoid growing this directory into a dumping ground.
- Prefer small, focused interfaces that map to handler needs (e.g., read-only query sets) and can be satisfied by generated query types or mocks.

Future candidates:

- Login-related persistence (if shared across packages beyond `ServerDeps`) could be factored into a separate interface here; currently handled via `ServerDeps` credential methods.
