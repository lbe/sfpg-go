# Accessing Profiling Endpoints with curl

The application exposes standard Go `pprof` profiling endpoints, which are protected by
the same authentication system as the administrative interface. This guide explains how
to authenticate via `curl` and capture profiling data.

> **Important:** Pprof is available on **loopback only** (`127.0.0.1` / `::1`). The
> public hostname will **not** work — you will receive a `404` regardless of your
> session cookie.

## 1. Authentication

Since the endpoints are protected by `authMiddleware`, you must first obtain a session cookie.

**Step 1: Login and save the cookie**
Replace `admin` with your actual credentials if you have changed them. The application allows login without a CSRF token for new sessions, making it `curl`-friendly.

```bash
curl -c /tmp/sfpg_cookies.txt \
  -d "username=admin" \
  -d "password=admin" \
  http://127.0.0.1:8081/login
```

## 2. Accessing Profiling Endpoints

Once you have the cookie in `/tmp/sfpg_cookies.txt`, you can use it to access any `/debug/pprof/` endpoint. All examples use `127.0.0.1` — replace the port as needed.

### CPU Profile (30 seconds)

```bash
curl -b /tmp/sfpg_cookies.txt \
  "http://127.0.0.1:8081/debug/pprof/profile?seconds=30" \
  -o cpu.prof
```

### Execution Trace (5 seconds)

```bash
curl -b /tmp/sfpg_cookies.txt \
  "http://127.0.0.1:8081/debug/pprof/trace?seconds=5" \
  -o trace.out
```

### Command-Line Arguments

```bash
curl -b /tmp/sfpg_cookies.txt \
  "http://127.0.0.1:8081/debug/pprof/cmdline"
```

### Symbol Lookup

```bash
curl -b /tmp/sfpg_cookies.txt \
  "http://127.0.0.1:8081/debug/pprof/symbol"
```

## 3. Analyzing the Profiles

Once you have downloaded the profile files, use the `go tool pprof` command to analyze them.

### Interactive CLI

```bash
go tool pprof cpu.prof
```

### Web Interface (Visual Graph)

```bash
go tool pprof -http=:8082 cpu.prof
```

### Analyzing Execution Traces

```bash
go tool trace trace.out
```

## Available Endpoints Summary

Only the following five routes are registered:

| Endpoint               | Description                     |
| :--------------------- | :------------------------------ |
| `/debug/pprof/`        | Index page listing all profiles |
| `/debug/pprof/cmdline` | Command-line arguments          |
| `/debug/pprof/profile` | CPU profile (default 30s)       |
| `/debug/pprof/symbol`  | Symbol lookup                   |
| `/debug/pprof/trace`   | Execution trace                 |
