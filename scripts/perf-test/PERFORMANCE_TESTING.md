# SFPG-Go Performance Testing Plan

**Version**: 1.1  
**Date**: March 5, 2026  
**Status**: Active

---

## Table of Contents

1. [Overview](#overview)
2. [Tool Selection](#tool-selection)
3. [Test Scenarios](#test-scenarios)
4. [Metrics](#metrics)
5. [Test Phases](#test-phases)
6. [Test Data Setup](#test-data-setup)
7. [Running Tests](#running-tests)
8. [Analyzing Results](#analyzing-results)
9. [Expected Benchmarks](#expected-benchmarks)
10. [Troubleshooting](#troubleshooting)

---

## Overview

This document describes a comprehensive performance testing suite for SFPG-Go using `vegeta` for load testing. The tests measure:

- **Throughput**: Requests per second under varying loads
- **Latency**: Response time percentiles (p50, p95, p99, max)
- **Error Rate**: Failed requests (4xx, 5xx) under stress
- **Cache Effectiveness**: HTTP cache hit rate and impact
- **Resource Utilization**: Database pool contention, CPU/memory usage

The suite tests realistic scenarios:

- Gallery browsing (read-heavy, cacheable)
- Image viewing (read-heavy, cacheable)
- Configuration access (authenticated, non-cacheable)
- Mixed workload (realistic user behavior)
- Thumbnail serving (database-backed, cacheable)

---

## Tool Selection

### Vegeta

**Primary tool for comprehensive load testing**

**Why Vegeta?**

- Detailed metrics (histogram, percentiles, JSON output)
- Flexible attack patterns (constant rate, ramp, variable duration)
- Multiple result formats (text, JSON, binary)
- Can simulate realistic request distributions
- Excellent for detailed analysis and trending

**Install**:

```bash
go install github.com/tsenart/vegeta@latest
```

**Typical Usage**:

```bash
# Attack phase: generate requests
vegeta attack -duration=60s -rate=100/s < attacks/gallery.txt > results.bin

# Report phase: analyze results
vegeta report -type=text results.bin
vegeta report -type=json results.bin > results.json
vegeta plot results.bin > results.html # Visual timeline
```

---

## Test Scenarios

### Scenario A: Gallery List (Cacheable, Read-Heavy)

**Endpoint**: `GET /gallery/{id}`  
**Characteristics**: Most common user action, highly cacheable, tests template rendering + cache middleware  
**Test Duration**: 90 seconds (30s warm-up + 60s measured)  
**Metrics**:

- With cache enabled: Measure cache hit rate (expect >95% after warm-up)
- Without cache: Measure database query overhead

**Vegeta Attack File**: `attacks/gallery.txt`

```
GET http://localhost:8083/gallery/1
GET http://localhost:8083/gallery/2
GET http://localhost:8083/gallery/3
GET http://localhost:8083/gallery/4
GET http://localhost:8083/gallery/5
```

**Expected Latency**:

- With cache: p50 = 5-10ms, p99 = 20-50ms
- Without cache: p50 = 25-50ms, p99 = 100-200ms

---

### Scenario B: Image Viewing (Cacheable, Read-Heavy)

**Endpoint**: `GET /image/{id}`  
**Characteristics**: Secondary user action, cacheable metadata, tests DB queries + cache  
**Test Duration**: 90 seconds  
**Metrics**: Cache hit rate, latency percentiles

**Vegeta Attack File**: `attacks/image.txt`

```
GET http://localhost:8083/image/1
GET http://localhost:8083/image/2
GET http://localhost:8083/image/3
...
```

**Expected Latency**: Similar to gallery, +10-20% due to additional metadata rendering

---

### Scenario C: Thumbnail Serving (Binary, Cached)

**Endpoint**: `GET /thumbnail/file/{id}`  
**Characteristics**: Fast, typically cached or served from database blob, tests concurrent read pool  
**Test Duration**: 90 seconds  
**Metrics**: Throughput (expect higher than pages due to smaller payloads)

**Vegeta Attack File**: `attacks/thumbnail.txt`

```
GET http://localhost:8083/thumbnail/file/1
GET http://localhost:8083/thumbnail/file/2
...
```

**Expected Throughput**: 2-3x higher than gallery (smaller payloads)

---

### Scenario D: Authenticated Endpoint (Non-Cacheable)

**Endpoint**: `GET /config` (with authentication)  
**Characteristics**: Not cacheable, per-session, tests auth middleware overhead  
**Test Duration**: 60 seconds  
**Concurrency**: 4-5 concurrent authenticated sessions  
**Metrics**: Auth overhead, database read pool saturation

**Test Method**:

```bash
# 1. Log in to establish sessions
curl -s -X POST http://localhost:8083/login \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=admin&password=admin" \
  -c "${PROJECT_ROOT}/tmp/session1.txt"

# 2. Use cookies in vegeta attack
# Manually edit attacks/config-auth.txt to include Cookie headers
```

**Expected Latency**: 50-100ms (auth validation + DB read + template render)

---

### Scenario E: Mixed Workload (Realistic)

**Endpoint Mix**:

- 60% GET /gallery/{id} (cached)
- 20% GET /image/{id} (cached)
- 10% GET /thumbnail/file/{id} (cached)
- 10% GET /lightbox/{id} (cached HTMX partial)

**Characteristics**: Realistic user behavior, tests cache effectiveness with varied load  
**Test Duration**: 120 seconds (warm-up + sustained)  
**Rate Progression**: 10 → 50 → 100 req/s (3 stages of 30s each)

**File**: `attacks/mixed-workload.txt` (generated by `gen-attack-files.sh`)

---

## Metrics

### Primary Metrics

| Metric                 | Tool                | Extraction                 | Interpretation                         |
| ---------------------- | ------------------- | -------------------------- | -------------------------------------- |
| **Throughput (req/s)** | vegeta              | `"Requests/sec"` in report | Higher is better; compare cache on/off |
| **Latency p50**        | vegeta              | `"Median"`                 | Typical response time                  |
| **Latency p95**        | vegeta              | From histogram             | User experience (95% under this)       |
| **Latency p99**        | vegeta              | From histogram             | Worst-case for most users              |
| **Latency Max**        | vegeta              | From report                | Identify outliers                      |
| **Error Rate**         | vegeta              | `"Errors"` count / total   | Should be 0% under normal load         |
| **Cache Hit Rate**     | Custom header parse | Extract `X-Cache` header   | Expect >95% after warm-up              |

### Secondary Metrics

| Metric                        | Method                               | Tool                           |
| ----------------------------- | ------------------------------------ | ------------------------------ |
| **Database Pool Utilization** | Query active connections during test | `sqlite3` / application logs   |
| **CPU Usage**                 | `top` / Activity Monitor             | Manual observation during test |
| **Memory Usage**              | `top` / Activity Monitor             | Monitor for memory leaks       |
| **GC Pause Time**             | Go profiling                         | `go tool pprof` (optional)     |

---

## Test Phases

### Phase 1: Test Data Setup

**Duration**: ~1 minute  
**Prerequisites**: Air dev server running with file discovery already completed  
**Steps**:

1. Verify server health (`/health` endpoint)
2. Query `./tmp/DB/sfpg.db` for real folder and file IDs via `gen-attack-files.sh`
3. Generate attack files in `attacks/`
4. Warm HTTP cache by requesting each URL once

**Script**: `setup-test-data.sh` (calls `gen-attack-files.sh` internally)

---

### Phase 2: Baseline (Cache Enabled)

**Duration**: 90 seconds (30s warm-up + 60s measured)  
**Load Pattern**: Progressive ramp (10 → 50 → 100 req/s)  
**Concurrency**: 8 worker threads  
**Scenarios**: Gallery, Image, Thumbnail individually

**Output Files**:

- `results/baseline-gallery-cache-on.bin`
- `results/baseline-image-cache-on.bin`
- `results/baseline-thumbnail-cache-on.bin`

**Script**: `run-baseline.sh`

---

### Phase 3: Sustained Load (Cache Enabled)

**Duration**: 120 seconds  
**Load Pattern**: Constant rate (50 req/s, 100 req/s, 200 req/s)  
**Concurrency**: 16 worker threads  
**Scenarios**: Each individual scenario at multiple load levels

**Output Files**:

- `results/sustained-gallery-50rps.bin`
- `results/sustained-gallery-100rps.bin`
- `results/sustained-gallery-200rps.bin`
- (similar for image and thumbnail)

**Script**: `run-sustained.sh`

---

### Phase 4: Cache Impact Analysis (Cache Disabled)

**Duration**: 60 seconds per scenario  
**Setup**: Disable HTTP cache via environment variable
**Load Pattern**: Same as Phase 2 baseline

**Comparison**: Cache on vs off

**Output Files**:

- `results/baseline-gallery-cache-off.bin`
- `results/baseline-image-cache-off.bin`
- `results/baseline-thumbnail-cache-off.bin`

**Script**: `compare-cache-impact.sh`

---

### Phase 5: Mixed Workload (Realistic)

**Duration**: 120 seconds  
**Load Pattern**: Progressive ramp (10 → 50 → 100 req/s)  
**Concurrency**: 12 worker threads  
**Request Mix**: 60% gallery, 20% image, 10% thumbnail, 10% lightbox

**Output Files**:

- `results/mixed-workload-high.bin`
- `results/mixed-workload-high.json`
- `results/mixed-workload-high.txt`

**Script**: `run-mixed-workload.sh`

---

### Phase 6: Authenticated Heavy (Non-Cacheable)

**Duration**: 60 seconds  
**Setup**: 4-5 concurrent authenticated sessions  
**Endpoint**: `/config` (requires auth)  
**Concurrency**: 4-5 worker threads

**Output Files**:

- `results/authenticated-config.bin`

**Script**: `run-baseline.sh` (with auth option)

---

## Test Data Setup

### Prerequisites

1. **HTTP Server Running**: `air` on port 8083
2. **Database**: Empty or reset to default state
3. **Images Directory**: Empty or reset

### Test Data Generation

The performance test suite uses the application's **real data** — it does not generate synthetic images. The dev server must already have files discovered before running any tests.

`gen-attack-files.sh` queries `./tmp/DB/sfpg.db` and generates attack files with real IDs:

- `attacks/gallery.txt` — folder IDs from the `folders` table
- `attacks/image.txt` — file IDs from the `files` table
- `attacks/thumbnail.txt` — same file IDs, using `/thumbnail/file/{id}` route
- `attacks/lightbox.txt` — file IDs for HTMX lightbox partial
- `attacks/mixed-workload.txt` — proportional mix of all four

Environment overrides:

| Variable        | Default                 | Purpose                     |
| --------------- | ----------------------- | --------------------------- |
| `DB_PATH`       | `./tmp/DB/sfpg.db`      | Path to SQLite database     |
| `SERVER_URL`    | `http://localhost:8083` | Target server URL           |
| `GALLERY_COUNT` | `30`                    | Max folder URLs to generate |
| `IMAGE_COUNT`   | `50`                    | Max image URLs to generate  |

**Script**: `gen-attack-files.sh`

---

## Running Tests

### Quick Start (All Tests)

```bash
# 1. Set up test data
./scripts/perf-test/setup-test-data.sh

# 2. Run all test phases
make perf-test

# 3. View final report
cat results/perf-report-final.txt
```

### Run Individual Test

```bash
# Baseline with cache enabled
./scripts/perf-test/run-baseline.sh

# Sustained load testing
./scripts/perf-test/run-sustained.sh

# Compare cache on vs off
./scripts/perf-test/compare-cache-impact.sh

# Realistic mixed workload
./scripts/perf-test/run-mixed-workload.sh
```

### Makefile Target

The `Makefile` in the project root includes all targets:

```makefile
make perf-test              # Full suite: setup + baseline + sustained + mixed + analyze
make perf-test-setup        # Generate attack files from DB and warm cache
make perf-gen-attacks       # (Re)generate attack files from live DB only
make perf-test-compare-cache # Interactive cache on vs off comparison
make perf-test-clean        # Remove results/ and temp artifacts
make perf-test-help         # Show all commands
```

### Manual Testing with Vegeta

```bash
# Generate test URLs
echo "GET http://localhost:8083/gallery/1" > attacks/test.txt

# Attack: 30s at 50 req/s
vegeta attack -duration=30s -rate=50 -workers=8 < attacks/test.txt > results.bin

# Report
vegeta report -type=text results.bin
vegeta report -type=json results.bin | jq .

# Histogram
vegeta report -type=hist results.bin
```

---

## Analyzing Results

### Extract Metrics from Vegeta Results

**Text Report**:

```bash
vegeta report -type=text results/baseline-gallery-cache-on.bin
```

Output includes:

```
[200]   95,000  # Status code distribution
[500]       0

Status 200 (95000 requests)
  # Status code 200
  Early termination: false
  Requests      [total, rate, throughput] 95000, 1583.34, 1576.48
  Duration      [total, attack, wait]     60.071s, 60.060s, 11.070ms
  Latencies     [min, mean, 50th, 90th, 95th, 99th, max]
                7.1ms, 31.4ms, 18.9ms, 42.1ms, 68.3ms, 156.2ms, 423.8ms
  Bytes In      [total, mean] 12380000, 130
  Bytes Out     [total, mean] 0, 0
  Error Set:
```

**JSON Analysis**:

```bash
vegeta report -type=json results/baseline-gallery-cache-on.bin | jq '.latencies'
vegeta report -type=json results/baseline-gallery-cache-on.bin | jq '.throughput'
```

### Generate Comparison Report

Script: `analyze-results.sh`

Compares:

- **Cache On vs Off**: Percentage improvement
- **Scenario Comparison**: Gallery vs Image vs Thumbnail
- **Load Level Impact**: p50/p95/p99 at 50/100/200 req/s
- **Error Rate Emergence**: Where errors start appearing

Outputs:

- `results/perf-report-final.txt` (summary)

---

## Expected Benchmarks

These are baseline expectations based on typical hardware (8-core CPU, 16GB RAM):

### Cache Enabled (Production Scenario)

| Scenario              | Throughput         | p50  | p95  | p99   | Error Rate |
| --------------------- | ------------------ | ---- | ---- | ----- | ---------- |
| Gallery at 50 req/s   | 50 req/s           | 5ms  | 15ms | 50ms  | 0%         |
| Gallery at 100 req/s  | 100 req/s          | 8ms  | 25ms | 100ms | 0%         |
| Image at 50 req/s     | 48 req/s           | 12ms | 30ms | 80ms  | 0%         |
| Thumbnail at 50 req/s | 50 req/s           | 2ms  | 5ms  | 15ms  | 0%         |
| Mixed at 50 req/s     | 50 req/s           | 8ms  | 20ms | 60ms  | 0%         |
| **Cache Hit Rate**    | >95% after warm-up | —    | —    | —     | —          |

### Cache Disabled (Baseline Overhead)

| Scenario              | Throughput  | p50  | p95   | p99   | Error Rate |
| --------------------- | ----------- | ---- | ----- | ----- | ---------- |
| Gallery at 50 req/s   | 20-25 req/s | 40ms | 100ms | 300ms | 0%         |
| Image at 50 req/s     | 15-20 req/s | 60ms | 150ms | 400ms | 0%         |
| Thumbnail at 50 req/s | 30-35 req/s | 5ms  | 20ms  | 80ms  | 0%         |

### Cache Effectiveness

| Metric                         | Value                  |
| ------------------------------ | ---------------------- |
| Gallery Throughput Improvement | 2-4x                   |
| Gallery Latency Improvement    | 80-95% reduction       |
| Image Latency Improvement      | 70-90% reduction       |
| Thumbnail Impact               | Minimal (already fast) |

---

## Troubleshooting

### Server Not Responding

```bash
# Check if air is running
curl -s http://localhost:8083/gallery/1 | head -5

# If empty or error, restart air
# kill existing air process and restart
```

### Vegeta Install Issues

```bash
# Ensure Go is installed
go version

# Install vegeta
go install github.com/tsenart/vegeta@latest

# Verify installation
vegeta --version
```

### Permission Denied on Scripts

```bash
chmod +x scripts/perf-test/*.sh
```

### Results Directory Not Created

```bash
mkdir -p results
```

### Cache Not Populating

- Verify HTTP cache is enabled: Check config or logs for `cache enabled:`
- Run warm-up phase longer: Test expects >95% hit rate after sufficient requests
- Check response headers: `curl -i http://localhost:8083/gallery/1 | grep -i x-cache`

### Database Lock Errors

- Ensure only one `air` process running
- Wait for file discovery to complete before testing
- Check logs for SQLite busy errors

### High Variance in Results

- Ensure no other processes (browsers, IDE watchers, etc.) accessing server
- Run tests during quiet system time
- Close other applications
- May need longer test duration (60s → 120s) for stability

---

## Performance Testing Best Practices

1. **Warm Up First**: Always run 30-60s warm-up before measuring
2. **Single Variable**: Test one scenario at a time (gallery, image, etc.)
3. **Measure Twice**: Run each test 2-3 times, average results
4. **Monitor CPU/Memory**: Use Activity Monitor during test
5. **Compare Apples-to-Apples**: Same request mix, duration, concurrency
6. **Document Baseline**: Save baseline before code changes
7. **Test After Changes**: Run same tests after optimizations
8. **Review Logs**: Check application logs for errors, GC pauses
9. **Cache Warm-Up**: Always verify cache is populated (>95% hit rate)
10. **System Quiet**: Stop other workloads during critical tests

---

## Next Steps

1. **Run** `make perf-test-setup` to generate attack files and warm the cache
2. **Execute** `make perf-test` to run all test phases
3. **Analyze** results in `results/perf-report-final.txt`
4. **Establish** baseline metrics for future comparisons
5. **Integrate** into CI/CD for regression detection

---

**Document Version**: 1.1  
**Last Updated**: March 5, 2026  
**Status**: Active
