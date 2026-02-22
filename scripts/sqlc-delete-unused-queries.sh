#!/usr/bin/env bash

set -euo pipefail

declare -r MAP="./tmp/sqlc-gen-map.txt"
rm -f $MAP > /dev/null 2>&1
declare -r F_REGEX="./tmp/sqlc-gen-func-regex.txt"
rm -f $F_REGEX > /dev/null 2>&1
declare -r USED="./tmp/sqlc-used.txt"
rm -f $USED > /dev/null 2>&1
declare -r TO_BE_DELETED="./tmp/sqlc-to-be-deleted.txt"
rm -f $TO_BE_DELETED > /dev/null 2>&1

declare -x GEN_MAP="./scripts/sqlc-generate-map"
declare -x DEL_QUERIES="./scripts/sqlc-delete-queries"

# generate the map of query name to go variable and function
$GEN_MAP > $MAP

# generate the regex of function calls for each query
rg -t go '.+\t(\S+?)$' $MAP -r '^.*\b($1)(?:Params)?\b.*' | rg -v 'N\/A|function' > $F_REGEX

echo "Total queries: $(wc -l < $F_REGEX)"

# find all used queries by searching for function calls in the codebase
rg -NI -t go -g '!*_test.go' -g '!*/gallerydb/*' -f $F_REGEX \
  | perl -nlE '@ary = ($_ =~ m/\b[qQ]\S*?\.(\S+?)\b/g); for (@ary) { say "\\b$_\\b" }' \
  | sort -u > $USED

echo "Used queries: $(wc -l < $USED)"

# find queries that are not used - to be deleted
rg -NI '.+\t(\S+?)$' -r '$1' $MAP | rg -v 'N\/A|function|IPTC|XMP' | rg -v -f $USED > $TO_BE_DELETED

echo "Unused queries: $(wc -l < $TO_BE_DELETED)"

echo "IPTC/XMP queries skipped: $(rg -NI '.+\t(\S+?)$' -r '$1' $MAP | rg 'IPTC|XMP' | wc -l)"

# delete the unused queries
cat $TO_BE_DELETED | $DEL_QUERIES

# generate the code again after deletion
sqlc generate
