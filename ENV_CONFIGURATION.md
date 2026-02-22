# Environment Configuration

This document describes the environment variables used to configure SFPG. Configuration can also be provided via CLI flags or a `config.yaml` file (located in the executable directory or platform-specific config path).

## Application Configuration

General application settings.

### `SFG_PORT`

**Default**: `8081`
**Description**: TCP port for the HTTP server.

### `SFG_DISCOVER`

**Default**: `true`
**Description**: Enable background filesystem scan for new images on startup.

### `SFG_CACHE_PRELOAD`

**Default**: `false`
**Description**: Enable cache preloading. When a folder is opened, the server proactively caches thumbnails and metadata for its contents to speed up subsequent requests.

### `SFG_UNLOCK_ACCOUNT`

**Description**: Unlocks a locked account by username. Useful if an admin gets locked out due to excessive failed login attempts.
**Usage**: Set this variable to the username (e.g., `admin`) and restart the application.

### `SFG_RESTORE_LAST_KNOWN_GOOD`

**Default**: `false`
**Description**: If `true`, restores the last known good configuration from the database on startup. Useful for recovering from bad configuration changes.

## Session Security Flags

The session cookie security flags are controlled by environment variables to allow different settings between development, testing, and production.

### `SEPG_SESSION_HTTPONLY`

**Type**: Boolean (string: `"false"` or any other value)  
**Default**: `true` (HttpOnly enabled)  
**Description**: Controls the `HttpOnly` flag on session cookies

- `HttpOnly: true` - JavaScript cannot access cookies (prevents XSS-based session theft)
- `HttpOnly: false` - JavaScript can access cookies (only for local development/testing)

**Security Impact**:

- Production: **MUST** be `true` (or use default)
- Development: Can be `false` for easier testing

**Example**:

```bash
# Production (default, HttpOnly enabled)
$ ./sfpg

# Development (HttpOnly disabled for testing)
$ SEPG_SESSION_HTTPONLY=false ./sfpg
```

### `SEPG_SESSION_SECURE`

**Type**: Boolean (string: `"false"` or any other value)  
**Default**: `true` (Secure enabled)  
**Description**: Controls the `Secure` flag on session cookies

- `Secure: true` - Cookies only sent over HTTPS (requires TLS)
- `Secure: false` - Cookies sent over HTTP or HTTPS (only for local development)

**Security Impact**:

- Production: **MUST** be `true` (or use default)
  - Requires reverse proxy (nginx/caddy) to handle TLS termination
  - Backend talks to proxy over HTTP (trusted internal network)
  - Clients connect to proxy over HTTPS
- Development: Can be `false` for local testing without HTTPS

**Architecture Note**:

With a reverse proxy architecture (recommended):

```
Client --HTTPS--> nginx/caddy --HTTP--> Go Backend
                   (TLS termination)   (trusted network)
```

The backend can safely set `Secure: true` because:

1. Clients connect to proxy over HTTPS (network layer security)
2. Proxy connects to backend over HTTP (trusted internal network)
3. Backend doesn't need to detect HTTPS - proxy ensures it

**Example**:

```bash
# Production (default, Secure enabled)
$ ./sfpg

# Development (Secure disabled for HTTP testing)
$ SEPG_SESSION_SECURE=false ./sfpg
```

### `SEPG_SESSION_MAX_AGE`

**Type**: Integer (seconds)  
**Default**: `604800` (7 days)  
**Description**: Controls the maximum age of session cookies in seconds

- Session cookies will expire after this duration
- Browser will delete expired cookies automatically
- Users must re-authenticate after expiration

**Common Values**:

- `604800` (7 days) - Default, suitable for most applications
- `86400` (24 hours) - Higher security, more frequent re-authentication
- `3600` (1 hour) - Very high security for sensitive operations
- `2592000` (30 days) - Extended sessions for low-risk applications

**Security Impact**:

- Production: Balance security vs. usability (7 days recommended)
- High Security: Use shorter durations (1-24 hours)
- Development: Can use longer durations for convenience

**Example**:

```bash
# Production (7 days, default)
$ ./sfpg

# High security (1 hour sessions)
$ SEPG_SESSION_MAX_AGE=3600 ./sfpg

# Development (30 days for convenience)
$ SEPG_SESSION_MAX_AGE=2592000 ./sfpg
```

### `SEPG_SESSION_SAMESITE`

**Type**: String (`"Strict"`, `"Lax"`, or `"None"`)
**Default**: `"Lax"`  
**Description**: Controls the `SameSite` attribute on session cookies for CSRF protection

- `SameSite: Lax` - Cookies sent with same-site requests and top-level navigations (recommended)
- `SameSite: Strict` - Cookies only sent with same-site requests (maximum CSRF protection)
- `SameSite: None` - Cookies sent with all requests (requires `Secure: true`)

**Security Impact**:

- **Lax (default, recommended)**: Strong CSRF protection with good user experience
  - Cookies sent when users follow external links to your site
  - Prevents CSRF attacks from embedded forms/AJAX
  - Best balance of security and usability

- **Strict**: Maximum CSRF protection
  - Cookies NOT sent even when following external links
  - Users must navigate directly to your domain to be logged in
  - Best for highly sensitive applications (banking, health records)

- **None**: Disables SameSite CSRF protection
  - Only use if cross-site requests require authentication
  - Requires `Secure: true` (HTTPS only)
  - Relies entirely on CSRF token validation

**Example**:

```bash
# Production (Lax, default - recommended)
$ ./sfpg

# High security (Strict - maximum CSRF protection)
$ SEPG_SESSION_SAMESITE=Strict ./sfpg

# Cross-site embedding (requires HTTPS)
$ SEPG_SESSION_SAMESITE=None SEPG_SESSION_SECURE=true ./sfpg
```

## Combined Configuration

### Production Deployment

```bash
# All security flags enabled with defaults
$ ./sfpg

# Or explicitly set all options:
$ SEPG_SESSION_HTTPONLY=true SEPG_SESSION_SECURE=true SEPG_SESSION_MAX_AGE=604800 SEPG_SESSION_SAMESITE=Lax ./sfpg
```

**Nginx Configuration** (example):

```nginx
upstream app {
    server backend:8081;
}

server {
    listen 443 ssl http2;
    ssl_certificate /etc/ssl/cert.pem;
    ssl_certificate_key /etc/ssl/key.pem;

    location / {
        proxy_pass http://app;
    }
}
```

### Development Deployment

```bash
# Security flags disabled, extended session for local development
$ SEPG_SESSION_HTTPONLY=false SEPG_SESSION_SECURE=false SEPG_SESSION_MAX_AGE=2592000 ./sfpg
```

Access via: `http://localhost:8081` (no HTTPS needed)

### Testing

```bash
# Run tests with development settings
$ SEPG_SESSION_HTTPONLY=false SEPG_SESSION_SECURE=false go test -race ./...
```

## Deployment Examples

### Docker Compose

```yaml
services:
  backend:
    image: sfpg:latest
    environment:
      SEPG_SESSION_HTTPONLY: "true"
      SEPG_SESSION_SECURE: "true"
      SEPG_SESSION_MAX_AGE: "604800"
      SEPG_SESSION_SAMESITE: "Lax"
      SEPG_SESSION_SECRET: "${SEPG_SESSION_SECRET}"

  proxy:
    image: caddy:latest
    ports:
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
```

### Kubernetes

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: sfpg-config
data:
  SEPG_SESSION_HTTPONLY: "true"
  SEPG_SESSION_SECURE: "true"
  SEPG_SESSION_MAX_AGE: "604800"
  SEPG_SESSION_SAMESITE: "Lax"

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sfpg-backend
spec:
  template:
    spec:
      containers:
        - name: sfpg
          env:
            - name: SEPG_SESSION_HTTPONLY
              valueFrom:
                configMapKeyRef:
                  name: sfpg-config
                  key: SEPG_SESSION_HTTPONLY
            - name: SEPG_SESSION_SECURE
              valueFrom:
                configMapKeyRef:
                  name: sfpg-config
                  key: SEPG_SESSION_SECURE
```

### systemd Service

```ini
[Service]
Type=simple
ExecStart=/usr/local/bin/sfpg
Environment="SEPG_SESSION_HTTPONLY=true"
Environment="SEPG_SESSION_SECURE=true"
Environment="SEPG_SESSION_MAX_AGE=604800"
Environment="SEPG_SESSION_SAMESITE=Lax"
Environment="SEPG_SESSION_SECRET=%i"
```

## Logging

Session configuration is logged on startup:

```
2026-02-01T08:02:52Z INFO Session cookie options configured maxAge=604800 httpOnly=true secure=true sameSite=Lax
```

Monitor logs to verify correct configuration is loaded.

## Security Checklist

Before production deployment:

- [ ] Verify `SEPG_SESSION_HTTPONLY=true` (or using default)
- [ ] Verify `SEPG_SESSION_SECURE=true` (or using default)
- [ ] Verify `SEPG_SESSION_MAX_AGE` is appropriate for your security requirements (default: 7 days)
- [ ] Verify `SEPG_SESSION_SAMESITE=Lax` or `Strict` (or using default)
- [ ] Verify reverse proxy handles TLS termination
- [ ] Verify reverse proxy connects to backend over trusted network
- [ ] Review proxy configuration for HSTS headers, security headers, etc.
- [ ] Verify SEPG_SESSION_SECRET environment variable is set securely
- [ ] Test with production-like settings before full rollout

## Related Configuration

- `SEPG_SESSION_SECRET`: Secret key for session signing (required, set via environment variable)

See `app.go` for how SEPG_SESSION_SECRET is validated.
