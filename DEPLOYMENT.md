# Deployment Guide

This guide explains how to deploy SFPG to production securely behind a reverse proxy, how session cookies are configured, and what to check before going live.

## Overview

- Run the app as a single static binary.
- Place it behind a TLS-terminating reverse proxy (e.g., Nginx or Caddy).
- Keep default-secure session cookie flags; only relax in local dev/test.
- Store the SQLite DB and Images on persistent storage and back them up.

## Runtime Configuration

Configuration is loaded from (in order of precedence):

1. **CLI Flags**
2. **Environment Variables**
3. **`config.yaml`** (in executable directory or `~/.config/sfpg/` / `%APPDATA%/sfpg/`)
4. **Database** (settings changed via UI)
5. **Defaults**

Common flags/variables:

- `-port` (`SFG_PORT`): HTTP listen port (default `8081`).
- `-discover` (`SFG_DISCOVER`): Enable background filesystem scan (default `true`).
- `-cache-preload` (`SFG_CACHE_PRELOAD`): Enable cache preloading when folders are opened (default `false`).
- `-unlock-account` (`SFG_UNLOCK_ACCOUNT`): Unlock a locked account by username (e.g. `admin`).
- `-restore-last-known-good` (`SFG_RESTORE_LAST_KNOWN_GOOD`): Restore last known good configuration from DB on startup.
- `-debug-delay-ms` (`SFG_DEBUG_DELAY_MS`): Artificial handler delay (default `0`).

Example:

```bash
./sfpg-go -port 8081 -discover=true -cache-preload=true
```

## Required Environment

- `SEPG_SESSION_SECRET` (required): A strong random secret for session cookies.
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

## Reverse Proxy Expectations

The server enforces a same-origin protection for unsafe HTTP methods (POST/PUT/PATCH/DELETE) by requiring a valid `Origin` header that matches the request `Host`. Behind a reverse proxy:

- Terminate TLS at the proxy and forward HTTP to the backend.
- Preserve the original `Host` header when proxying to the backend.
- Serve the application on a single origin (domain + port) to satisfy the Origin checks.

Recommended headers to pass:

- `Host`
- `X-Forwarded-Proto`
- `X-Forwarded-For`

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
        proxy_set_header Host $host;                # preserve host for Origin checks
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
  - [ ] Back up `DB/sfpg.db` (and WAL files) and `Images/` regularly
- Operations
  - [ ] Configure systemd (or equivalent) with restart policy
  - [ ] Monitor logs in `logs/` and rotate as needed (log files are timestamped per startup)
  - [ ] Health checks: probe a static asset under `/static/` for liveness
- Application
  - [ ] Set the initial admin credentials via `/config` after first login
  - [ ] Optional: tune `-discover` (leave `true` for automatic discovery)

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

For production deployments, implement health checks to monitor application availability. The application doesn't have a dedicated `/health` endpoint, but you can use existing routes:

### Liveness Check (Basic Availability)

Check if the server is responding:

```bash
# Check static asset (doesn't require authentication)
curl -f http://localhost:8081/static/favicon.svg -o /dev/null -s

# Or test the login page
curl -f http://localhost:8081/login -o /dev/null -s
```

For Kubernetes/Docker health probes:

```yaml
livenessProbe:
  httpGet:
    path: /static/favicon.svg
    port: 8081
  initialDelaySeconds: 5
  periodSeconds: 10
```

### Readiness Check (Application Ready)

Verify database connectivity by attempting login page render:

```bash
# Login page requires DB access for session store
curl -f -H "User-Agent: HealthCheck/1.0" \
  http://localhost:8081/login \
  -o /dev/null -s -w "%{http_code}\n"

# Expected: 200
```

### Behind a Reverse Proxy

When using a reverse proxy, health checks should target the backend directly to avoid false positives from proxy caching:

```bash
# Nginx upstream health check (nginx-plus or third-party module)
curl -f http://127.0.0.1:8081/static/favicon.svg

# Or via the proxy with a specific header
curl -f -H "Host: gallery.example.com" \
  https://gallery.example.com/static/favicon.svg
```

### Monitoring Recommendations

- **Liveness**: Check `/static/favicon.svg` every 10-30 seconds
- **Readiness**: Check `/login` after startup and on deploy
- **Logs**: Monitor `logs/sfpg-*.log` for ERROR level entries
- **Disk**: Alert when `DB/` or `Images/` partitions exceed 80% usage
- **Metrics**: Track response times for `/gallery/1` (requires auth setup)

### Example: Systemd Watchdog

Add to your systemd service file:

```ini
[Service]
# ... existing config ...
WatchdogSec=30
# The application doesn't support sd_notify yet, but systemd will restart on timeout
```
