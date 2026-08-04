#!/usr/bin/env bash
# Smoke test for PGO discovery helper.
# Sources wait lib only (NOT generate_default_pgo.sh).
# Works bare against live air on http://localhost:8083 via defaults.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/generate_default_pgo-wait.sh"

wait_for_health
wait_for_discovery
log "Discovery complete"
exit 0
