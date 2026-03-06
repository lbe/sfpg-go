# Performance Testing Infrastructure

**Status**: Active  
**Created**: March 5, 2026

## Files

### Documentation

- **PERFORMANCE_TESTING.md** — comprehensive testing plan with scenarios, metrics, phases, and expected benchmarks

### Attack Pattern Files (vegeta)

Generated at runtime by `gen-attack-files.sh` from the live database. Do not edit manually.

- `attacks/gallery.txt` — GET /gallery/{id} URLs
- `attacks/image.txt` — GET /image/{id} URLs
- `attacks/thumbnail.txt` — GET /thumbnail/file/{id} URLs
- `attacks/lightbox.txt` — GET /lightbox/{id} URLs
- `attacks/mixed-workload.txt` — 60% gallery / 20% image / 10% thumbnail / 10% lightbox

### Test Scripts (executable bash)

- **gen-attack-files.sh**
  - Queries `./tmp/DB/sfpg.db` for real folder and file IDs
  - Generates all attack files in `attacks/`
  - Env overrides: `DB_PATH`, `SERVER_URL`, `GALLERY_COUNT`, `IMAGE_COUNT`

- **setup-test-data.sh**
  - Checks server health, verifies vegeta and sqlite3
  - Calls `gen-attack-files.sh` to generate attack files from real DB
  - Warms the HTTP cache by requesting each URL once

- **run-baseline.sh**
  - Tests gallery, image, thumbnail scenarios
  - Measures baseline performance (cache enabled)
  - Progressive load ramp (10→50 req/s)

- **run-sustained.sh**
  - Sustained load testing at 50/100/200 req/s
  - Identifies breaking points and error emergence
  - Tests cache stability under pressure

- **run-mixed-workload.sh**
  - Realistic mixed workload (60% gallery, 20% image, 10% thumbnail, 10% lightbox)
  - Uses pre-generated `attacks/mixed-workload.txt`
  - Tests cache effectiveness with varied requests

- **compare-cache-impact.sh**
  - Interactive cache ON vs OFF comparison
  - Prompts user to toggle cache via the web UI between runs
  - Prints side-by-side latency and throughput table

- **analyze-results.sh**
  - Parses all `results/*.json` vegeta output files
  - Extracts throughput, p50/p95/p99/max latency, success rate
  - Writes `results/perf-report-final.txt`

### Makefile Targets

- `make perf-test` — Full test suite (setup + baseline + sustained + mixed + analyze)
- `make perf-test-setup` — Generate attack files from DB and warm cache
- `make perf-gen-attacks` — (Re)generate attack files from live DB only
- `make perf-test-compare-cache` — Interactive cache on vs off comparison
- `make perf-test-clean` — Remove results and temp artifacts
- `make perf-test-help` — Show all performance testing commands

## Quick Start

1. Ensure the air dev server is running with data already discovered:

   ```bash
   curl http://localhost:8083/gallery/1
   ```

2. Setup (generate attack files + warm cache):

   ```bash
   make perf-test-setup
   ```

3. Run all performance tests:

   ```bash
   make perf-test
   ```

4. View results:

   ```bash
   cat results/perf-report-final.txt
   ```

5. Compare cache on vs off (interactive):

   ```bash
   make perf-test-compare-cache
   ```
