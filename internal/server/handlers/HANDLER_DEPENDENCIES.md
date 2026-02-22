# Handler Dependencies

- **AuthHandlers**: Manages login, logout, and session status.
  - **Dependencies**: `AuthService` (for login/lockout/user), `SessionManager` (for clearing sessions), `CookieStore` (direct session management).
- **ConfigHandlers**: Manages application settings, export/import, and admin credential updates.
  - **Dependencies**: `ConfigService` (load/save/export/import), `AuthService` (for updating admin credentials), `SessionManager` (for auth and CSRF checks), `CookieStore`.
- **GalleryHandlers**: Manages image viewing, folder navigation, and thumbnail retrieval.
  - **Dependencies**: `HandlerQueries` (folder/file/thumbnail queries), read-only DB pools, `imagesDir` root.
- **HealthHandlers**: Provides system health checks and version information.

**Note**: Handlers use focused interfaces (`AuthService`, `SessionManager`, `HandlerQueries`) to isolate dependencies and facilitate unit testing. The old `LoginStore` interface and `AdminHandler` logic have been consolidated into the `AuthService`.

| Handler                      | Route(s)            | Package / location  |
| ---------------------------- | ------------------- | ------------------- |
| `healthHandler`              | `GET /health`       | server, handlers.go |
| `loginHandler`               | `POST /login`       | server, handlers.go |
| `logoutHandler`              | `POST /logout`      | server, handlers.go |
| `rootGalleryRedirectHandler` | `GET /`             | server, handlers.go |
| `galleryByIDHandler`         | `GET /gallery/{id}` | server, handlers.go |
