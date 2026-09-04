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

## Operations

### Migration 020 (`file_folder_index`)

Migration 020 adds the `file_folder_index` table for fast image folder navigation. On large galleries the initial backfill is CPU-intensive and blocks startup. Plan a maintenance window for the first deploy.

#### Within-discovery staleness

From walk enqueue through drain until the `file_folder_index` rebuild completes, newly discovered files are **not** in the index. During this window:

- The info box may show `imageIndex = -1` / `imageCount = 0` (HTTP 200).
- The lightbox may return 404 until the rebuild finishes.

Manual discovery during peak traffic may briefly expose stale nav until the rebuild completes.

#### Post-rollback cleanup

If rolling back past Migration 020 (`migrate.Steps(-1)` / `Migrate(19)`), wait for the stale `file_folder_index_to_be_dropped` table to be dropped by the async cleanup goroutine, or drop it manually before running the reverse migration.

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

- **`login_rate_limit_per_ip`:** Maximum `POST /login` requests per client IP per 60-second window. **`0` disables** IP rate limiting. Uses the direct connection address (`RemoteAddr`), not `X-Forwarded-For`. Behind a reverse proxy, all browser clients appear as the proxy IP, so the in-app limiter is ineffective or blocks all users — **rate-limit `POST /login` at the proxy instead** (see [Login rate limiting at the reverse proxy](#login-rate-limiting-at-the-reverse-proxy)) and set `login_rate_limit_per_ip` to **`0`** when the edge handles it.
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

## Public gallery browsing (no authentication on media routes)

**Gallery browsing is public by design** (similar to classic [Single File PHP Gallery](http://sye.dk/sfpg/)). Login protects **administration only** — configuration, discovery, dashboard, shutdown/restart, and related routes. It does **not** gate viewing photos.

**Unauthenticated routes** (anyone who can reach the host can use these without logging in):

| Route pattern                                   | Purpose                     |
| ----------------------------------------------- | --------------------------- |
| `GET /gallery/{id}`                             | Folder grid                 |
| `GET /image/{id}`                               | Image view page             |
| `GET /lightbox/{id}`                            | Lightbox                    |
| `GET /raw-image/{id}`                           | Full-resolution file stream |
| `GET /thumbnail/file/{id}`                      | File thumbnail              |
| `GET /thumbnail/folder/{id}`                    | Folder thumbnail            |
| `GET /info/folder/{id}`, `GET /info/image/{id}` | Info panels                 |

**Image IDs are sequential autoincrement integers** assigned at discovery time. They are **not** secret, unpredictable, or a security boundary. A crawler can start at `/gallery/1` and collect every image link from HTML; guessing `/raw-image/{n}` in sequence is trivial.

**Implications:**

- A host reachable from the internet exposes the full gallery to anyone who can connect — enumeration of IDs is unnecessary because pages already list them.
- `/raw-image/{id}` streams bytes without a session check; treat network placement and edge policy as your access control.
- If your photos are sensitive, **do not rely on URL shape or ID obscurity**. Use VPN/firewall restrictions, bind to loopback and tunnel, or [authenticate at the reverse proxy](#protecting-a-private-gallery-at-the-reverse-proxy) for all routes.

### Protecting a private gallery at the reverse proxy

When the gallery must not be world-readable, choose one of:

| Approach                  | When to use                                                                     |
| ------------------------- | ------------------------------------------------------------------------------- |
| **Network restriction**   | VPN, tailnet, home LAN, firewall allowlist — simplest for personal libraries    |
| **Loopback + SSH tunnel** | Bind the app to `127.0.0.1`; access via `ssh -L`                                |
| **Edge authentication**   | Internet-facing host where viewers need a shared password before any page loads |

**Edge authentication vs app login:** Proxy `basicauth` (or equivalent) gates **every** HTTP request, including gallery pages and static assets. The in-app admin session is still required for `/config`, `/dashboard`, and `/server/*` after the proxy allows the connection. Use separate credentials: proxy accounts for viewers, admin account for configuration.

**Verify edge authentication** (replace URL and credentials):

```bash
# Without proxy credentials → 401 Unauthorized
curl -s -o /dev/null -w "%{http_code}" https://gallery.example.com/gallery/1
# Expected: 401

# With proxy credentials → 200 (gallery HTML)
curl -s -o /dev/null -w "%{http_code}" -u viewer:secret https://gallery.example.com/gallery/1
# Expected: 200

# Admin routes still require app login after proxy auth
curl -s -o /dev/null -w "%{http_code}" -u viewer:secret https://gallery.example.com/config
# Expected: 401 (no session cookie)
```

Tracked templates [`deploy/Caddyfile`](deploy/Caddyfile) and [`deploy/Caddyfile.local`](deploy/Caddyfile.local) do **not** include viewer authentication. Add `basicauth` (Caddy) or `auth_basic` (Nginx) when deploying a private library.

#### Example: Nginx (viewer basic auth on all routes)

Generate a password file once (`viewer` is an example username):

```bash
sudo apt-get install -y apache2-utils # provides htpasswd
sudo htpasswd -c /etc/nginx/sfpg-viewers.htpasswd viewer
```

Add `auth_basic` to the `location /` block from the [login rate-limit example](#example-nginx) (login rate limiting and viewer auth can coexist):

```nginx
server {
    listen 443 ssl http2;
    server_name gallery.example.com;

    ssl_certificate     /etc/ssl/certs/fullchain.pem;
    ssl_certificate_key /etc/ssl/private/privkey.pem;

    # Viewer gate — required before any gallery or admin page loads
    auth_basic           "Private gallery";
    auth_basic_user_file /etc/nginx/sfpg-viewers.htpasswd;

    location = /login {
        limit_req zone=sfpg_login burst=5 nodelay;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_pass http://sfpg_backend;
    }

    location / {
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_pass http://sfpg_backend;
    }
}
```

Reload after changes: `nginx -t && systemctl reload nginx`.

#### Example: Caddy (viewer basic auth on all routes)

Generate a bcrypt hash (Caddy v2):

```bash
caddy hash-password --plaintext 'your-viewer-password'
```

Add `basicauth` inside the site block (stock Caddy; no custom build required):

```caddyfile
gallery.example.com {
	header Strict-Transport-Security "max-age=31536000; includeSubDomains"
	encode zstd gzip

	# Viewer gate — separate from in-app admin credentials
	basicauth {
		viewer $2a$14$Zkx19XLiYW6vqSQ8jo3YfuO1QJC/r7FzEd7odz6JDOMy6xInkv0a
	}

	reverse_proxy 127.0.0.1:8081 {
		header_up Host {host}
		header_up X-Forwarded-Proto {scheme}
		header_up X-Forwarded-For {remote}
	}
}
```

Replace the hash with output from `caddy hash-password`. Combine with [login rate limiting](#example-caddy) on a custom `caddy-ratelimit` build if both viewer auth and per-IP login limits are required.

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

### Login rate limiting at the reverse proxy

In production, terminate TLS at Nginx or Caddy and **rate-limit `POST /login` at the edge** using each client's real IP. The application intentionally keys `login_rate_limit_per_ip` on `RemoteAddr` only (it does not trust `X-Forwarded-For`), so the in-app IP limiter cannot distinguish clients behind a proxy.

**Recommended pairing:**

| Layer             | Setting                                                                                                      |
| ----------------- | ------------------------------------------------------------------------------------------------------------ |
| **Reverse proxy** | Per-client-IP limit on `POST /login` (examples below; default **10 per 60s** matches the app)                |
| **Application**   | `login_rate_limit_per_ip` = **`0`** (disable in-app IP limiting when the proxy handles it)                   |
| **Application**   | Keep **lockout** enabled (`lockout_threshold`, `lockout_duration`) — per-username, still enforced in the app |

Set `login_rate_limit_per_ip` to `0` in the config modal (**Session** → **Login security**) or via startup env `SEPG_LOGIN_RATE_LIMIT_PER_IP=0`. Per-account lockout remains independent.

**Direct exposure (no proxy):** Leave the in-app default (`10` per 60s) or tune in the config modal. No edge configuration required.

**Over-limit behavior:** The proxy returns **HTTP 429** (configure explicitly on Nginx; default for `caddy-ratelimit`). This is expected — it is not the application's HTML login form error.

**Verify edge rate limiting** (replace URL; use valid credentials only on the first request):

```bash
# Repeat until 429 (proxy), not 403 (COP) or 200
for i in $(seq 1 15); do
  curl -s -o /dev/null -w "%{http_code}\n" -X POST https://gallery.example.com/login \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -H "Origin: https://gallery.example.com" \
    -H "Sec-Fetch-Site: same-origin" \
    -d "username=admin" -d "password=wrong"
done
# Expected: mostly 200 (invalid credentials), then 429 from the proxy
```

Tracked templates [`deploy/Caddyfile`](deploy/Caddyfile) and [`deploy/Caddyfile.local`](deploy/Caddyfile.local) use **stock Caddy** (no rate limiting). The Caddy example below requires a custom build — do not paste `rate_limit` into those files unless you switch to a `caddy-ratelimit` binary.

### Example: Nginx

Nginx includes `limit_req` in standard builds. The example below limits **`/login`** per client IP (**10 requests per minute**, with a small burst), returns **429** when exceeded, and proxies everything else unchanged.

Place `limit_req_zone` in the `http` context (e.g. `/etc/nginx/nginx.conf` inside `http { }`, or an included snippet):

```nginx
upstream sfpg_backend {
    server 127.0.0.1:8081;
}

# Per-client-IP login rate limit (matches app default: 10/min).
# ~10m shared zone holds on the order of 160k client IP states.
limit_req_zone $binary_remote_addr zone=sfpg_login:10m rate=10r/m;
limit_req_status 429;

server {
    listen 443 ssl http2;
    server_name gallery.example.com;

    ssl_certificate     /etc/ssl/certs/fullchain.pem;
    ssl_certificate_key /etc/ssl/private/privkey.pem;

    # (Recommended) redirect HTTP->HTTPS in a separate server block on 80

    location = /login {
        limit_req zone=sfpg_login burst=5 nodelay;

        proxy_set_header Host $host;                # REQUIRED: preserve host for Origin checks
        # Sec-Fetch-Site and Origin pass through by default; do not proxy_hide_header them
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_pass http://sfpg_backend;
    }

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

**Tuning:**

- `rate=10r/m` — ten requests per minute per IP (align with `login_rate_limit_per_ip` default).
- `burst=5 nodelay` — allow short bursts; reduce `burst` for stricter limiting.
- `limit_req_status 429` — return Too Many Requests (default without this is 503).
- Brute-force attacks use `POST /login`; `GET /login` returns 400 from the app. Limiting the `/login` location covers both; gallery and static traffic are unaffected.

Reload after changes: `nginx -t && systemctl reload nginx`.

### Example: Caddy

**Stock Caddy** (`caddy` package, `caddy:latest` Docker image) does **not** include HTTP rate limiting. Use the community [**`caddy-ratelimit`**](https://github.com/mholt/caddy-ratelimit) module and build a custom binary with [xcaddy](https://github.com/caddyserver/xcaddy):

```bash
xcaddy build --with github.com/mholt/caddy-ratelimit
# Install the resulting `caddy` binary, or use it in your container image.
```

Production template without rate limiting: [`deploy/Caddyfile`](deploy/Caddyfile) (replace `gallery.example.com`). Local smoke: [`deploy/Caddyfile.local`](deploy/Caddyfile.local) (`tls internal` on `:8443`).

**Caddyfile with login rate limiting** (custom build only; **10 `POST /login` per minute per client IP**, matching the app default):

```caddy
{
	# global options, e.g., email for ACME
	# rate_limit is ordered before basicauth by default in caddy-ratelimit
}

gallery.example.com {
	header Strict-Transport-Security "max-age=31536000; includeSubDomains"
	encode zstd gzip

	rate_limit {
		zone login_post {
			match {
				method POST
				path /login
			}
			key {remote_host}
			events 10
			window 1m
		}
	}

	reverse_proxy 127.0.0.1:8081 {
		header_up Host {host}
		# Sec-Fetch-Site and Origin pass through by default; do not strip client headers
		header_up X-Forwarded-Proto {scheme}
		header_up X-Forwarded-For {remote}
	}
}
```

**Tuning:**

- `events 10` / `window 1m` — same semantics as the app's `login_rate_limit_per_ip` default.
- `key {remote_host}` — real client IP at the edge (not the backend's `127.0.0.1`).
- `match { method POST path /login }` — only login POSTs count; gallery, static assets, and other routes are not throttled.
- Optional: `ipv6_prefix 64` inside the zone to bucket IPv6 clients by `/64` (mitigates address cycling within a prefix).

**Docker:** Replace `image: caddy:latest` with an image built from `xcaddy build --with github.com/mholt/caddy-ratelimit`, or mount a custom binary. Do not add `rate_limit` to [`deploy/Caddyfile`](deploy/Caddyfile) until the runtime image includes the module.

Standard Caddy builds include `zstd` and `gzip` only (`encode` defaults to both). Brotli requires a custom Caddy build — do not add it unless your binary has `http.encoders.brotli`.

### Example: Caddy (without rate limiting)

Minimal stock-Caddy edge (no custom build). Use in-app `login_rate_limit_per_ip` only if the app is reached **directly**; behind this proxy, set in-app IP limiting to **`0`** and add edge rate limiting via the [Caddy example above](#example-caddy) or Nginx.

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

After deploying behind Caddy with the configuration above, verify edge offload
(TLS/HSTS, `encode`, Host/COP header pass-through). Prefer the automated local
smoke when developing:

```bash
# Terminal A — keep air on :8083, or run a prod-like binary on :8081
SFPG_BACKEND_PORT=8083 caddy run --config deploy/Caddyfile.local

# Terminal B
./scripts/caddy-smoke.sh
```

`deploy/Caddyfile.local` listens on `https://localhost:8443` with `tls internal`
(curl uses `-k`). Default upstream is `127.0.0.1:8081`; set `SFPG_BACKEND_PORT`
to point at `air` (`8083`) without starting a second app.

For a production hostname, the same checks apply (replace the URL; drop `-k` when
the cert is trusted):

```bash
# 1. Gallery page loads with 200
curl -s -o /dev/null -w "%{http_code}" https://gallery.example.com/gallery/1
# Expected: 200

# 2. Login POST succeeds (same-origin COP headers)
curl -s -X POST https://gallery.example.com/login \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "Origin: https://gallery.example.com" \
  -H "Sec-Fetch-Site: same-origin" \
  -d "username=admin" -d "password=admin" \
  -c /tmp/caddy-test-cookies.txt -o /dev/null -w "%{http_code}"
# Expected: 200

# 3. HSTS header is present
curl -s -I https://gallery.example.com/gallery/1 | grep -i strict-transport
# Expected: Strict-Transport-Security: max-age=31536000; includeSubDomains

# 4. Also run the COP verification curls in Reverse Proxy Expectations above
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
  - [ ] `image_directory` may be any readable path on the host (e.g. `/mnt/photos`); the admin chooses it — it is not jailed under the app root
  - [ ] Back up `DB/sfpg.db` (and WAL files), `DB/thumbs/thumbs.db` (and WAL files), and `Images/` regularly
  - [ ] Back up `DB/sfpg.db-dque/` (auto-created persistent write overflow queue) alongside the DB to preserve in-flight pending writes across restarts
- Operations
  - [ ] Configure systemd (or equivalent) with restart policy
  - [ ] Set `log_level` to **`info`** or **`warn`** in production (default is `debug` for troubleshooting; verbose on busy galleries)
  - [ ] Monitor logs in `logs/` and rotate as needed (log files are timestamped per startup)
  - [ ] Health checks: probe a static asset under `/static/` for liveness
- Application
  - [ ] Set the initial admin credentials via `/config` after first login
  - [ ] Review **Login security** in the config modal (Session tab): lockout threshold and lockout duration
  - [ ] If behind a reverse proxy: rate-limit `POST /login` at the edge ([Nginx](#example-nginx) / [Caddy](#example-caddy)); set `login_rate_limit_per_ip` to **`0`** in the app
  - [ ] If exposing the app directly (no proxy): keep or tune in-app `login_rate_limit_per_ip` (default `10` per 60s)
  - [ ] Optional: tune `-discover` (leave `true` for automatic discovery)
- Security Hardening
  - [ ] Review the [Symlink Trust Model](#symlink-trust-model) and apply filesystem hardening if needed
  - [ ] Review [public gallery browsing](#public-gallery-browsing-no-authentication-on-media-routes): sequential IDs are not secret; if content is private, restrict the network or add [edge viewer authentication](#protecting-a-private-gallery-at-the-reverse-proxy)
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
- **dque Disk Quota**: The write-batcher overflow queue (`DB/sfpg.db-dque/`) has a configurable disk quota (`dque_max_disk_bytes`) to
  prevent runaway disk usage. The default is 50 GiB; set it to `0` for unlimited. Changes hot-reload without a restart. When the
  quota is exceeded, batcher `Submit` returns `ErrQuotaExceeded`. Monitor current usage and the configured quota in the dashboard
  Write Batcher card (`#wb-dque-disk-usage` / `#wb-dque-disk-quota`, backed by the `disk_usage_bytes` and `disk_quota_bytes`
  metrics) to track proximity to the limit.
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
