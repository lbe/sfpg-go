#!/usr/bin/env bash
# List Prettier-managed files (tracked and untracked) on stdout, NUL-delimited.
# Prune rules mirror .prettierignore; extension list matches project Prettier usage.

set -euo pipefail

find . \( \
  -path './.git' -o \
  -path './.agents' -o \
  -path './node_modules' -o \
  -path './bin' -o \
  -path './tmp' -o \
  -path './bench' -o \
  -path './vendor' -o \
  -path './internal/imagemeta' -o \
  -path './zarchive' -o \
  -path './scripts/perf-test/attacks' -o \
  -path './results' -o \
  -path './results/*' -o \
  -path '*/testdata' -o \
  -path '*/testdata/*' \
  \) -prune -o \
  -type f \
  ! -name '*.log' \
  ! -name '*.min.*' \
  ! -name 'coverage.out' \
  ! -name 'coverage.html' \
  ! -name 'sfpg-go-gemini' \
  ! -path './scripts/pre-commit-check.sh' \
  ! -path './scripts/list-prettier-files.sh' \
  \( \
    -name '*.html.tmpl' -o \
    -name '*.md' -o \
    -name '*.sh' -o \
    -name '*.json' -o \
    -name '*.css' -o \
    -name '*.yaml' -o \
    -name '*.yml' -o \
    -name '*.ts' \
  \) -print0
