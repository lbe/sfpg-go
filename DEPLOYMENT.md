# Deployment Guide

This guide explains how to deploy SFPG to production securely behind a reverse proxy, how session cookies are configured, and what to check before going live.

## Overview

- Run the app as a single static binary.
- Place it behind a TLS-terminating reverse proxy (e.g., Nginx or Caddy).
- Keep default-secure session cookie flags; only relax in local dev/test.
- Store the SQLite DB and Images on persistent storage and back them up.

## Runtime Configuration

Configuration is loaded from (in order of precedence, lowest to highest):

1. **Defaults** (hard-coded in `config.DefaultConfig()`)
2. **Database** (settings changed via the web UI and persisted to `sfpg.db`)
3. **`config.yaml`** (in the executable directory or `~/.config/sfpg/` / `%APPDATA%\sfpg\`)
4. **Environment Variables / CLI Flags** (merged into a single tier; CLI flags override environment variables for the same setting)

Common flags/variables:

- `-port` (`SFG_PORT`): HTTP listen port (effective default `8081`).
- `-discover` (`SFG_DISCOVER`): Run file discovery on startup (effective default `true`; CLI flag zero-value is `false`).
- `-cache-preload` (`SFG_CACHE_PRELOAD`): Enable cache preloading when folders are opened (effective default `true`).
- `-compression` (`SFG_COMPRESSION`): Enable gzip/brotli response compression (effective default `true`).
- `-http-cache` (`SFG_HTTP_CACHE`): Enable SQLite HTTP response cache (effective default `true`).
- `-unlock-account` (`SFG_UNLOCK_ACCOUNT`): Unlock a locked account by username (e.g. `admin`).
- `-restore-last-known-good` (`SFG_RESTORE_LAST_KNOWN_GOOD`): Restore last known good configuration from DB on startup (default `false`).
- `-debug-delay-ms` (`SFG_DEBUG_DELAY_MS`): Artificial handler delay (default `0`).
- `-increment-etag` _(CLI-only)_: Increment application-wide ETag version on startup.
- `-cache-batch-load` _(CLI-only)_: Warm the HTTP cache and exit.

Example:

```bash
./sfpg-go -port 8081 -discover=true -cache-preload=true
```

## Required Environment

- `SEPG_SESSION_SECRET` (required): A strong random secret for session cookies.
  - Must be at least 32 bytes long; the application enforces this minimum at startup.
  - Generate at least 32 bytes of entropy. Example:
    ```bash
    head -c 48 /dev/urandom | base64
    ```

- `SEPG_SESSION_HTTPONLY` (default: `true`):
  - Controls the `HttpOnly` flag on the session cookie.
  - Keep `true` in production to mitigate XSS. Set to `false` only for local development/testing.

- `SEPG_SESSION_SECURE` (default: `true`):
  - Controls the `Secure` flag on the session cookie.
  - Keep `true` in production so cookies are sent only over HTTPS. Set to `false` only when running tests or local HTTP.

- `SEPG_SESSION_MAX_AGE` (default: `604800` seconds / 7 days):
  - Controls the maximum age of session cookies in seconds.
  - Users must re-authenticate after this duration.
  - Common values: `3600` (1 hour), `86400` (24 hours), `604800` (7 days), `2592000` (30 days).
  - Balance security (shorter) vs. usability (longer) based on your requirements.

- `SEPG_SESSION_SAMESITE` (default: `Lax`):
  - Controls the `SameSite` attribute for CSRF protection.
  - `Lax` (recommended): Strong CSRF protection with good user experience.
  - `Strict`: Maximum CSRF protection; users following external links won't be logged in.
  - `None`: Disables SameSite protection; only use with `Secure=true` and explicit CSRF tokens.

Notes:

- The application logs which values are in effect at startup.
- Defaults are safe for production. Only override for specific deployment requirements.

## Login Security (Config Modal + Optional Env)

Per-account lockout and per-IP login rate limiting are configured in the config modal **Session** tab under **Login security**:

| Setting                    | Config key                | Default      | Hot reload |
| -------------------------- | ------------------------- | ------------ | ---------- |
| Login rate limit (per IP)  | `login_rate_limit_per_ip` | `10` per 60s | Yes        |
| Lockout threshold          | `lockout_threshold`       | `3`          | Yes        |
| Lockout duration (seconds) | `lockout_duration`        | `3600`       | Yes        |

- **`login_rate_limit_per_ip`:** Maximum `POST /login` requests per client IP per 60-second window. **`0` disables** IP rate limiting. Uses the direct connection address (`RemoteAddr`), not `X-Forwarded-For`. Behind a reverse proxy, all browser clients may appear as the proxy IP unless you terminate TLS at the app or otherwise preserve distinct client addresses.
- **Lockout:** Applies per username after failed password attempts; independent of the IP limiter.
- **Startup env override (IP limit only):** `SEPG_LOGIN_RATE_LIMIT_PER_IP` overrides the database value on startup (no CLI flag). See [ENV_CONFIGURATION.md](ENV_CONFIGURATION.md).

For local development and automated tests against a shared `air` instance, consider `SEPG_LOGIN_RATE_LIMIT_PER_IP=0` or setting the limit to `0` in the config modal so repeated admin logins from one machine are not throttled.

## Theme Cookie (Accepted Risk)

The application uses a client-readable cookie named `theme` to persist the user's theme selection (e.g., light/dark). When a user selects a theme via the config modal, `ThemePostHandler` sets this cookie with:

- `HttpOnly: false` — the cookie **must** be readable by client-side JavaScript because Hyperscript reads it on page load to apply the selected theme without a server round-trip.
- `SameSite: Lax` — protects against cross-site CSRF-style theme changes.
- `Secure: true` when over HTTPS (automatically detected from `r.TLS`).

### Accepted Risk

The `HttpOnly: false` flag means the theme cookie is accessible to JavaScript running in the browser. In the event of a cross-site scripting (XSS) vulnerability, an attacker could read or modify the theme cookie. This is a **known and accepted risk**:

- The theme cookie contains only a theme name (one of the configured themes, e.g., `"dark"` or `"light"`), **not** a session token or any sensitive data.
- An attacker who can read the cookie gains no access to user sessions, credentials, or gallery content.
- The session cookie (`session`) remains `HttpOnly: true` (default) and is never exposed to client-side JavaScript.
- The CSRF-protected `ThemePostHandler` requires a valid CSRF token to change the theme.

### Hardening (Future Work)

If client-side theme switching is no longer required (e.g., theme is always server-rendered), the `HttpOnly` flag can be set to `true` and the Hyperscript cookie read removed from the templates. This would eliminate the cookie's client-side exposure entirely.

## Ephemeral CSRF Tokens on Unauthenticated Pages (Accepted Risk)

Public read-only pages such as the gallery, image view, and lightbox are served with long-term HTTP caching and do not require authentication. The CSRF token rendered in the base layout for unauthenticated visitors is intentionally ephemeral: when no session cookie exists, the application emits a fresh random token without saving it to a session cookie. This avoids setting a session cookie on cacheable public responses, which would either prevent caching or cause cached responses to carry session identifiers.

State-changing endpoints (login, logout, configuration, server controls, and theme selection) issue their own session-bound CSRF token via `EnsureCSRFToken` when the form or modal is rendered, and validate that token on POST. Unsafe HTTP methods are additionally protected by the same-origin middleware. Because unauthenticated public pages are read-only and do not perform server-side mutations, the lack of a session-bound CSRF token on those pages is a known and accepted risk.

## Symlink Trust Model

The application serves image files by their database IDs using the capability-URL pattern `/raw-image/{id}`. Image paths are resolved via `SafeImagePath`, which validates that the resolved absolute path stays within the configured images directory.

**Symlinks inside the images directory are not resolved by the application.** The OS kernel handles them transparently at serving time. If a symlink inside the images directory points outside it, the file will be served — the path-prefix check passes because the symlink itself is inside the boundary. This is the current trust model.

**Hardening options:**

- Run the application with a dedicated user that has no write access to the images directory to prevent unauthorized symlink creation.
- Consider mounting the images directory with `nosymfollow` (Linux) or equivalent filesystem option if symlink traversal is a concern in your deployment.
- A future configuration flag may add `filepath.EvalSymlinks` resolution before the prefix check.

## Public Image URLs (Capability-URL Model)

The `/raw-image/{id}` endpoint serves full-resolution image files **without authentication**. Access control relies on the **capability-URL model**: image IDs are unpredictable numeric database keys, so the URL itself acts as the access credential.

**Implications:**

- Anyone who knows or guesses an image ID can access that image directly.
- Image IDs are sequential and could be enumerated by an attacker guessing IDs in range.
- Do not rely on `/raw-image/{id}` as a substitute for access control. If your images are sensitive, serve the application behind a reverse proxy that requires authentication for all routes, or restrict access at the network/firewall level.
- The lightbox and image view pages (`/lightbox/{id}`, `/image/{id}`) also render without authentication, but they include the raw image URL in the page source, making it discoverable.

## Reverse Proxy Expectations

The server enforces same-origin protection for unsafe HTTP methods (POST/PUT/PATCH/DELETE) by checking that the request `Origin` (or `Referer` as a fallback) matches the request `Host`. The `Origin` header is authoritative when present; the `Referer` header is used only as a fallback when `Origin` is absent. Behind a reverse proxy:

- Terminate TLS at the proxy and forward HTTP to the backend.
- Preserve the original `Host` header when proxying to the backend; the same-origin check compares the request `Host` to the `Origin` or `Referer` header and does not consume `X-Forwarded-*` headers.
- Serve the application on a single origin (domain + port) to satisfy the same-origin checks.

Required headers to pass:

- `Host` (must match the public origin)
- `Referer` should also be preserved for correct operation when browsers do not send `Origin` on same-origin requests.

You may also pass standard proxy headers such as `X-Forwarded-Proto` and `X-Forwarded-For` for logging or upstream use, but they are not used by the application's security checks.

### Example: Nginx

```nginx
upstream sfpg_backend {
    server 127.0.0.1:8081;
}

server {
    listen 443 ssl http2;
    server_name gallery.example.com;

    ssl_certificate     /etc/ssl/certs/fullchain.pem;
    ssl_certificate_key /etc/ssl/private/privkey.pem;

    # (Recommended) redirect HTTP->HTTPS in a separate server block on 80

    location / {
        proxy_set_header Host $host;                # REQUIRED: preserve host for Origin checks
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_pass http://sfpg_backend;
    }
}
```

### Example: Caddy

```caddy
{
  # global options, e.g., email for ACME
}

gallery.example.com {
  encode zstd gzip
  reverse_proxy 127.0.0.1:8081 {
    header_up Host {host}
    header_up X-Forwarded-Proto {scheme}
    header_up X-Forwarded-For {remote}
  }
}
```

## Systemd Service (Optional)

```ini
[Unit]
Description=SFPG
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=sfpg
Group=sfpg
WorkingDirectory=/opt/sfpg
Environment=SEPG_SESSION_SECRET=REPLACE_WITH_STRONG_SECRET
# Defaults are secure; only override for specific deployment needs
Environment=SEPG_SESSION_HTTPONLY=true
Environment=SEPG_SESSION_SECURE=true
Environment=SEPG_SESSION_MAX_AGE=604800
Environment=SEPG_SESSION_SAMESITE=Lax
ExecStart=/opt/sfpg/sfpg-go -port 8081 -discover=true
Restart=always
RestartSec=2
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
```

## Production Checklist

- Secrets & Cookies
  - [ ] Set a strong `SEPG_SESSION_SECRET` (>= 32 bytes entropy)
  - [ ] Ensure `SEPG_SESSION_HTTPONLY=true` and `SEPG_SESSION_SECURE=true` (defaults)
  - [ ] Verify `SEPG_SESSION_MAX_AGE` is appropriate for your security requirements (default: 7 days)
  - [ ] Verify `SEPG_SESSION_SAMESITE` is set to `Lax` or `Strict` (default: Lax)
  - [ ] Serve only over HTTPS via a reverse proxy (HSTS recommended)
- Network & Proxy
  - [ ] Expose only port 443 on the proxy; firewall backend port
  - [ ] Preserve `Host` header; pass `X-Forwarded-*` headers
- Filesystem & Data
  - [ ] Run as a dedicated, least-privileged user
  - [ ] Ensure `DB/` and `Images/` directories exist and are writable by the service user
  - [ ] Back up `DB/sfpg.db` (and WAL files), `DB/thumbs/thumbs.db` (and WAL files), and `Images/` regularly
  - [ ] Back up `DB/sfpg.db-dque/` (auto-created persistent write overflow queue) alongside the DB to preserve in-flight pending writes across restarts
- Operations
  - [ ] Configure systemd (or equivalent) with restart policy
  - [ ] Monitor logs in `logs/` and rotate as needed (log files are timestamped per startup)
  - [ ] Health checks: probe a static asset under `/static/` for liveness
- Application
  - [ ] Set the initial admin credentials via `/config` after first login
  - [ ] Review **Login security** in the config modal (Session tab): IP rate limit, lockout threshold, and lockout duration
  - [ ] If behind a reverse proxy, understand that IP rate limiting uses `RemoteAddr` (often the proxy IP for all users)
  - [ ] Optionally set `SEPG_LOGIN_RATE_LIMIT_PER_IP` at startup if the database value should differ from the default
  - [ ] Optional: tune `-discover` (leave `true` for automatic discovery)
- Security Hardening
  - [ ] Review the [Symlink Trust Model](#symlink-trust-model) and apply filesystem hardening if needed
  - [ ] Review the [Public Image URLs](#public-image-urls-capability-url-model) capability-URL model and assess risk for your content
  - [ ] Pprof is disabled by default; enable only if you need runtime profiling. When enabled, pprof endpoints (`/debug/pprof/`) are protected behind authentication
  - [ ] Consider restricting pprof access further via reverse-proxy rules (e.g., allow only localhost or internal IP ranges) if enabled

## Local Development vs Production

- Local dev/test:
  - May set `SEPG_SESSION_HTTPONLY=false SEPG_SESSION_SECURE=false` when serving over plain HTTP.
  - Can use extended `SEPG_SESSION_MAX_AGE` for convenience (e.g., 30 days).
  - Tests use these overrides plus an `Origin` header on unsafe requests.
- Production:
  - Keep defaults (`true` for HttpOnly and Secure, `Lax` for SameSite, `604800` for MaxAge).
  - Serve exclusively over HTTPS with a reverse proxy and enable HSTS.
  - Use `Strict` SameSite for maximum CSRF protection if user experience allows.

## Health Checks and Monitoring

For production deployments, implement health checks to monitor application availability.

### Dedicated Health Endpoint

The application exposes a lightweight health endpoint:

```bash
# Returns {"status":"ok"} and does not require authentication
curl -f http://localhost:8081/health -o /dev/null -s
```

### Liveness Check (Basic Availability)

Check if the server is responding:

```bash
# Dedicated health endpoint (recommended)
curl -f http://localhost:8081/health -o /dev/null -s

# Or a static asset (doesn't require authentication)
curl -f http://localhost:8081/static/favicon/favicon.svg -o /dev/null -s
```

For Kubernetes/Docker health probes:

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8081
  initialDelaySeconds: 5
  periodSeconds: 10
```

### Readiness Check (Application Ready)

Verify the server is serving requests:

```bash
curl -f http://localhost:8081/health -o /dev/null -s -w "%{http_code}\n"

# Expected: 200
```

### Behind a Reverse Proxy

When using a reverse proxy, health checks should target the backend directly to avoid false positives from proxy caching:

```bash
# Backend health check
curl -f http://127.0.0.1:8081/health

# Or via the proxy with the correct Host header
curl -f -H "Host: gallery.example.com" \
  https://gallery.example.com/health
```

### Monitoring Recommendations

- **Liveness**: Check `/health` every 10-30 seconds
- **Readiness**: Check `/health` after startup and on deploy
- **Logs**: Monitor `logs/sfpg-*.log` for ERROR level entries
- **Disk**: Alert when `DB/` or `Images/` partitions exceed 80% usage
- **dque Disk Quota**: The write-batcher overflow queue (`DB/sfpg.db-dque/`) has a configurable disk quota to prevent runaway disk usage.
  When the quota is exceeded, batcher `Submit` returns `ErrQuotaExceeded`. Monitor the `disk_usage_bytes` and `disk_quota_bytes`
  metrics in the dashboard to track proximity to the limit.
- **Metrics**: Track response times for `/gallery/1` (requires auth setup)

### Profiling Endpoints (pprof)

The application exposes Go's standard `net/http/pprof` debugging endpoints under `/debug/pprof/`.

**Default: disabled.** Pprof is disabled by default (`enable_pprof: false`). To enable it, set `enable_pprof: true` in the config file or database (via the Config UI) and restart the application.

**Security:** When enabled, all pprof routes are protected behind the application's authentication middleware — only authenticated admin sessions can access them.

**Hardening (optional):** For defence-in-depth, you can restrict pprof access further at the reverse-proxy level:

```nginx
# Nginx: block pprof from external access
location /debug/pprof/ {
    allow 127.0.0.1;
    deny all;
}
```

**Available endpoints:**

- `/debug/pprof/` — index page listing available profiles
- `/debug/pprof/cmdline` — command line arguments
- `/debug/pprof/profile` — CPU profile (30-second sampling)
- `/debug/pprof/symbol` — symbol lookup
- `/debug/pprof/trace` — execution trace

For local profiling during development:

```bash
# CPU profile (requires auth cookie from login)
curl -b /tmp/cookies.txt http://localhost:8083/debug/pprof/profile?seconds=30 > cpu.pprof

# Analyze with go tool
# go tool pprof cpu.pprof
```

### Example: Systemd Watchdog

Add to your systemd service file:

```ini
[Service]
# ... existing config ...
WatchdogSec=30
# The application doesn't support sd_notify yet, but systemd will restart on timeout
```
