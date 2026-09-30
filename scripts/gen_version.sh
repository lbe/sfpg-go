#!/bin/bash
# gen_version.sh — sole writer of version.go (invoked via go generate / air).

VERSION_FILE="version.go"

unlock_version_file() {
  chmod u+w "$VERSION_FILE" 2> /dev/null || true
  if command -v chattr > /dev/null 2>&1; then
    chattr -i "$VERSION_FILE" 2> /dev/null || true
  fi
}

lock_version_file() {
  chmod a-w "$VERSION_FILE" 2> /dev/null || true
  if command -v chattr > /dev/null 2>&1; then
    chattr +i "$VERSION_FILE" 2> /dev/null || true
  fi
}

unlock_version_file
CURRENT=$(sed -n 's/^const Version = "\([^"]*\)"$/\1/p' "$VERSION_FILE")
IFS='.' read -r major minor patch <<< "$CURRENT"
NEW_VERSION="$major.$minor.$((patch + 1))"

cat > "$VERSION_FILE" << EOF
package main

// Version is the application release version string.
const Version = "$NEW_VERSION"
EOF

lock_version_file
