#!/bin/bash
# gen_version.sh

VERSION_FILE="version.go"
CURRENT=$(sed -n 's/^const Version = "\([^"]*\)"$/\1/p' $VERSION_FILE)
IFS='.' read -r major minor patch <<< "$CURRENT"
NEW_VERSION="$major.$minor.$((patch + 1))"

cat > $VERSION_FILE << EOF
package main

const Version = "$NEW_VERSION"
EOF
