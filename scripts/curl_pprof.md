# Accessing Profiling Endpoints with curl

The application exposes standard Go `pprof` profiling endpoints, which are protected by the same authentication system as the administrative interface. This guide explains how to authenticate via `curl` and capture profiling data.

## 1. Authentication

Since the endpoints are protected by `authMiddleware`, you must first obtain a session cookie.

**Step 1: Login and save the cookie**
Replace `admin` with your actual credentials if you have changed them. The application allows login without a CSRF token for new sessions, making it `curl`-friendly.

```bash
curl -c /tmp/sfpg_cookies.txt
-d "username=admin"
-d "password=admin"
http://localhost:8081/login
```

## 2. Accessing Profiling Endpoints

Once you have the cookie in `/tmp/sfpg_cookies.txt`, you can use it to access any `/debug/pprof/` endpoint.

### CPU Profile (30 seconds)

This will download a 30-second CPU profile to a file named `cpu.prof`.

```bash
curl -b /tmp/sfpg_cookies.txt
"http://localhost:8081/debug/pprof/profile?seconds=30"
-o cpu.prof
```

### Memory (Heap) Profile

Capture the current heap memory usage.

```bash
curl -b /tmp/sfpg_cookies.txt
"http://localhost:8081/debug/pprof/heap"
-o heap.prof
```

### Goroutine Stack Traces

Get a snapshot of all current goroutines.

```bash
curl -b /tmp/sfpg_cookies.txt
"http://localhost:8081/debug/pprof/goroutine?debug=1"
-o goroutines.txt
```

### Execution Trace (5 seconds)

Capture an execution trace for detailed concurrency analysis.

```bash
curl -b /tmp/sfpg_cookies.txt
"http://localhost:8081/debug/pprof/trace?seconds=5"
-o trace.out
```

### Allocation Profile

Capture recent memory allocations.

```bash
curl -b /tmp/sfpg_cookies.txt
"http://localhost:8081/debug/pprof/allocs"
-o allocs.prof
```

## 3. Analyzing the Profiles

Once you have downloaded the profile files, use the `go tool pprof` command to analyze them.

### Interactive CLI

```bash
go tool pprof cpu.prof
```

### Web Interface (Visual Graph)

This will open your browser with an interactive graph of the profile.

```bash
go tool pprof -http=:8082 cpu.prof
```

### Analyzing Execution Traces

```bash
go tool trace trace.out
```

## Available Endpoints Summary

| Endpoint                 | Description                                                     |
| :----------------------- | :-------------------------------------------------------------- |
| `/debug/pprof/`          | Index page listing all available profiles                       |
| `/debug/pprof/profile`   | CPU profile (default 30s)                                       |
| `/debug/pprof/heap`      | Memory allocation of live objects                               |
| `/debug/pprof/allocs`    | Past memory allocations                                         |
| `/debug/pprof/goroutine` | Stack traces of all current goroutines                          |
| `/debug/pprof/block`     | Stack traces that led to blocking on synchronization primitives |
| `/debug/pprof/mutex`     | Stack traces of holders of contended mutexes                    |
| `/debug/pprof/trace`     | Execution trace                                                 |
