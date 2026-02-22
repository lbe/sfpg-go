#!/usr/bin/env bash
set -euo pipefail

# Install repository-local git hooks by setting core.hooksPath
# Run this once after cloning the repo to enable the hooks in .githooks

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "Installing git hooks from $REPO_ROOT/.githooks"
git -C "$REPO_ROOT" config core.hooksPath "$REPO_ROOT/.githooks"

echo "Git hooks installed (core.hooksPath set to $REPO_ROOT/.githooks)"
