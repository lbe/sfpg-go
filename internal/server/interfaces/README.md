# interfaces package

Purpose: host shared contracts consumed by both the server orchestrator (`internal/server`) and the handlers package (`internal/server/handlers`) without creating import cycles. Interfaces in this package should represent cross-cutting dependencies that are injected into handlers.

Current contents:

- `HandlerQueries`: read-only gallery queries used by handlers and wired from `App` via `gallerydb` generated queries.

Guidelines:

- Add an interface here only if it is consumed by both `server` and `handlers` (or other subpackages) and would otherwise create a dependency loop.
- Keep handler-only or server-only interfaces close to their packages; avoid growing this directory into a dumping ground.
- Prefer small, focused interfaces that map to handler needs (e.g., read-only query sets) and can be satisfied by generated query types or mocks.

Future candidates:

- If `HandlerQueries` expands with EXIF/IPTC reads, consider either extending the interface here or adding a sibling `MetadataQueries` when multiple packages need it.
- Login-related persistence (if shared across packages) could be added here; otherwise keep it local to handlers.
