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
- `-http-cache` (`SFG_HTTP_CACHE`): Enable SQLite HTTP response cache (effective default `true`).

> **Upgrade note:** On first startup after an upgrade, the HTTP cache is invalidated
> to prevent serving stale entries from a previous key format. This is a one-time
> cold-cache event; HTML responses may be slightly slower for the first request or
> two until the cache repopulates.
>
> **v3 key format (v0.9.50+):** The cache key format changed from `|HX=|HXTarget=|IsVariant=`
> to normalized `|Variant=`. Info/lightbox entries collapsed to one key per path;
> gallery keeps two distinct keys. The v3 upgrade cold-caches all HTML briefly.

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
  - `None`: Disables SameSite protection; only use with `Secure=true` and COP (HTTPS/HSTS recommended).

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
- The `ThemePostHandler` is protected by the `CrossOriginProtection` middleware, ensuring only same-origin requests can change the theme.

### Hardening (Future Work)

If client-side theme switching is no longer required (e.g., theme is always server-rendered), the `HttpOnly` flag can be set to `true` and the Hyperscript cookie read removed from the templates. This would eliminate the cookie's client-side exposure entirely.

> **Note:** Changing the default theme in the admin config invalidates the HTTP cache, so page loads will be briefly uncached while the cache is repopulated. This is expected — subsequent visits after the theme change will be served from the warmed cache.

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

The server uses Go's `http.CrossOriginProtection` middleware to protect unsafe HTTP methods (POST/PUT/PATCH/DELETE) against cross-site request forgery. The middleware inspects browser-sent `Sec-Fetch-Site` and `Origin` headers: same-origin requests (`Sec-Fetch-Site: same-origin` or matching `Origin`) are allowed, while cross-site requests are rejected.

**Fail-open behavior:** If an unsafe request arrives with **neither** `Sec-Fetch-Site` **nor** `Origin`, the standard library allows it. That is intentional for non-browser clients (e.g., `curl`, monitoring probes) but is also what happens when a reverse proxy **strips** both headers from browser traffic. In that misconfiguration, cross-site POSTs to `/login`, `/config/*`, `/server/shutdown`, `/theme`, and other unsafe routes are **not** blocked by COP. Browsers normally send at least one of these headers on form POSTs; the risk is proxy configuration, not typical browser behavior.

HTTPS with HSTS is recommended so browsers send the required security headers. Behind a reverse proxy:

- Terminate TLS at the proxy and forward HTTP to the backend.
- Preserve the original `Host` header when proxying to the backend; the middleware compares the request `Host` to the `Origin` header and does not consume `X-Forwarded-*` headers.
- **Forward `Sec-Fetch-Site` and `Origin` unchanged** from the client. Do not strip, rewrite, or replace them unless you fully understand the COP implications above.
- Serve the application on a single origin (domain + port) so that same-origin requests match correctly.

Required headers to pass through to the backend:

- `Host` (must match the public origin)
- `Sec-Fetch-Site` (when sent by the browser)
- `Origin` (when sent by the browser)

Most reverse proxies (Nginx, Caddy, Traefik) pass these through by default. Verify your deployment if you use a CDN, WAF, or custom `proxy_set_header` / `header_up` rules that might remove client headers.

You may also pass standard proxy headers such as `X-Forwarded-Proto` and `X-Forwarded-For` for logging or upstream use, but they are not used by the application's security checks.

**Verify header forwarding** (replace the URL with your public origin):

```bash
# Browser-like POST with Origin should reach the backend (200 for valid login body, not 403 from COP)
curl -s -o /dev/null -w "%{http_code}" -X POST https://gallery.example.com/login \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "Origin: https://gallery.example.com" \
  -H "Sec-Fetch-Site: same-origin" \
  -d "username=admin" -d "password=admin"
# Expected: 200 (or 429 if rate-limited), not 403

# Cross-site Origin should be rejected by COP (403)
curl -s -o /dev/null -w "%{http_code}" -X POST https://gallery.example.com/login \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "Origin: https://evil.example" \
  -H "Sec-Fetch-Site: cross-site" \
  -d "username=admin" -d "password=admin"
# Expected: 403
```

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
        # Sec-Fetch-Site and Origin pass through by default; do not proxy_hide_header them
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
  header Strict-Transport-Security "max-age=31536000; includeSubDomains"
  encode zstd gzip
  reverse_proxy 127.0.0.1:8081 {
    header_up Host {host}
    # Sec-Fetch-Site and Origin pass through by default; do not strip client headers
    header_up X-Forwarded-Proto {scheme}
    header_up X-Forwarded-For {remote}
  }
}
```

## App-direct development vs Caddy production

- **Development:** Browser → `air` on `:8083`. No reverse proxy required.
- **Production with Caddy:** Browser → Caddy (TLS) → Go backend on `localhost:8081`. Caddy preserves the original `Host` header for same-origin checks, forwards client `Sec-Fetch-Site` and `Origin` by default, and HSTS is enabled via `Strict-Transport-Security`.

### Phase 3 smoke checklist (Caddy)

After deploying behind Caddy with the configuration above, run these manual
checks to verify edge offload is working correctly:

```bash
# 1. Gallery page loads with 200
curl -s -o /dev/null -w "%{http_code}" https://gallery.example.com/gallery/1
# Expected: 200

# 2. Login POST succeeds
curl -s -X POST https://gallery.example.com/login \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=admin" -d "password=admin" \
  -c /tmp/caddy-test-cookies.txt -o /dev/null -w "%{http_code}"
# Expected: 200

# 3. HSTS header is present
curl -s -I https://gallery.example.com/gallery/1 | grep -i strict-transport
# Expected: Strict-Transport-Security: max-age=31536000; includeSubDomains
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
  - [ ] Ensure the proxy forwards client `Sec-Fetch-Site` and `Origin` to the backend (do not strip them)
  - [ ] Run the [COP header verification](#reverse-proxy-expectations) curls after deploy (same-origin 200, cross-site 403)
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
  - [ ] Pprof is always available on loopback only (127.0.0.1 / ::1); access via SSH tunnel or local curl. Requires admin auth even on loopback. Public hostname returns 404
  - [ ] Consider restricting pprof access further via reverse-proxy rules (e.g., allow only localhost or internal IP ranges; the application's loopback check already blocks remote access)

## Local Development vs Production

- Local dev/test:
  - May set `SEPG_SESSION_HTTPONLY=false SEPG_SESSION_SECURE=false` when serving over plain HTTP.
  - Can use extended `SEPG_SESSION_MAX_AGE` for convenience (e.g., 30 days).
  - Tests use these overrides for local development without a reverse proxy.
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

**Availability:** Always available on loopback only (`127.0.0.1` / `::1`). Access requires admin authentication even on loopback. The public hostname returns **404** even with a valid session cookie — you must access via SSH tunnel or local `curl` to `http://127.0.0.1:<port>/debug/pprof/...`.

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
