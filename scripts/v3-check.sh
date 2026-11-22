#!/usr/bin/env bash
# v3-check.sh runs the mechanical checks for refactorv3 work, comparing the
# working tree with the commit where the branch left dev:
#
#   - package, exported API and go.mod changes, each of which must be listed
#     in docs/v3-approvals.txt;
#   - dead code that dev does not already have: functions reachable only from
#     tests (deadcode, staticcheck U1000) and packages no production binary
#     imports;
#   - database types on changed lines of the image-stack packages (R16), a
#     text check that backs up depguard;
#   - findings of the v3 lint rules in .golangci-v3.yml that dev does not
#     already have.
#
# Findings are compared without line numbers, so code that moves is not
# reported again. The checks cover the deployment target, linux/amd64.
#
# It prints every finding and exits 1 if there are any, or 2 if a check could
# not run. Output is kept in tmp/v3-check. Run it at task completion, not from
# a commit hook.
set -uo pipefail

# The script hashes itself into the baseline key, so find it before cd.
self=$(realpath "$0")
root=$(git rev-parse --show-toplevel)
cd "$root" || exit 2
out=tmp/v3-check
basedir=.worktrees/v3-base
approvals=docs/v3-approvals.txt
mkdir -p "$out"

die() {
  echo "v3-check: $*" >&2
  exit 2
}

# One check at a time per worktree: runs share $out and $basedir.
exec 9> "$out/.lock"
flock -n 9 || die "another v3-check.sh is running in this worktree"

for tool in apidiff deadcode staticcheck golangci-lint jq go git flock sha256sum; do
  command -v "$tool" > /dev/null || die "$tool not found"
done
base=$(git merge-base HEAD dev) || die "no merge base with dev"

# Pin everything that changes what the tools see.
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS= GOWORK=off LC_ALL=C
unset GOLANGCI_DIFF_PROCESSOR_PATCH
{
  go version
  for t in apidiff deadcode staticcheck golangci-lint; do sha256sum "$(command -v "$t")"; done
  sha256sum .golangci-v3.yml "$self"
} > "$out/tools.txt" 2>&1 || die "cannot record tool versions"
# Cached baselines depend on the commit, the tools, the lint config and this
# script.
key=$base-$(sha256sum < "$out/tools.txt" | cut -c1-12)

# vendored matches the vendored packages, which are out of scope.
vendored='internal/(dque|queue|humanize|osza)/'
# imagestack lists the packages where R16 bans database types.
imagestack=(internal/discovery internal/memlimit internal/metadata internal/parallelwalkdir internal/thumbnail)

fail=0
section() { printf '\n== %s\n' "$1"; }

# publish writes stdin to $1 through a temporary file, so an interrupted run
# never leaves a partial baseline.
publish() {
  cat > "$1.tmp" && mv "$1.tmp" "$1"
}

# A clean checkout of the base commit, with its submodules, for the baselines.
clean() {
  local status subs
  [[ $(git -C "$basedir" rev-parse HEAD 2> /dev/null) == "$base" ]] || return 1
  status=$(git -C "$basedir" status --porcelain --untracked-files=all --ignore-submodules=none) || return 1
  subs=$(git -C "$basedir" submodule status) || return 1
  [[ -z $status ]] && ! grep -q '^[-+U]' <<< "$subs"
}
if ! clean 2> /dev/null; then
  if [[ -e $basedir ]]; then
    git worktree remove --force "$basedir" || die "cannot remove $basedir"
  fi
  git worktree add --quiet --detach "$basedir" "$base" || die "cannot create $basedir"
  git -C "$basedir" submodule --quiet update --init || die "cannot init submodules in $basedir"
  clean || die "$basedir is not a clean checkout of $base"
fi

# --- Package, API and go.mod changes.

# packages lists each package's import path and declared name.
packages() {
  (cd "$1" && go list -f '{{.ImportPath}} {{.Name}}' ./...) || die "go list failed in $1"
}
packages "$basedir" | sort > "$out/base-packages.txt" || exit 2
packages . | sort > "$out/packages.txt" || exit 2

module=$(go list -m) || die "go list -m failed"
basemodule=$(cd "$basedir" && go list -m) || die "go list -m failed in $basedir"
api=$out/dev-$key.api
if [[ ! -s $api ]]; then
  (cd "$basedir" && apidiff -m -allow-internal -w "$root/$api.tmp" "$basemodule") \
    || die "apidiff could not write the baseline"
  mv "$api.tmp" "$api"
fi
apidiff -m -allow-internal "$api" "$module" > "$out/apidiff.txt" 2>&1 \
  || die "apidiff failed: $(cat "$out/apidiff.txt")"

# declarations prints "api: PKG.SYM: LINE" for each line of the complete
# declaration of symbol $2 in package $1, or of every symbol in the package if
# $2 is empty: struct fields, interface methods, grouped constants and
# variables, and the methods of types. Comments and blank lines are left out,
# so editing a comment does not need a new approval: go doc indents doc
# comments by four spaces outside braces and writes comments with // inside.
declarations() {
  local pkg=$1 sym=$2 doc names name
  if [[ -z $sym ]]; then
    names=$(go doc -short "$pkg") || die "go doc $pkg failed"
    names=$(sed -nE 's/^(func|type|const|var) ([A-Za-z0-9_]+).*/\2/p' <<< "$names" | sort -u)
    for name in $names; do
      declarations "$pkg" "$name" || return 2
    done
    return 0
  fi
  # apidiff also reports the exported methods of unexported types, which go
  # doc shows only with -u.
  local unexported=()
  if [[ $sym =~ ^[a-z_] ]]; then
    unexported=(-u)
  fi
  doc=$(go doc "${unexported[@]}" "$pkg" "$sym") || die "go doc $pkg $sym failed"
  awk -v prefix="api: $pkg.$sym: " '
    /^(package|import) / || /^[A-Z]+$/ { next }
    # A comment starts with // at the start of the line or after a space, so
    # a URL in a struct tag is kept. Braces are counted without comments.
    {
      text = $0; sub(/(^|[ \t])\/\/.*$/, "", text); sub(/[ \t]+$/, "", text)
      line = text; open = gsub(/{/, "{", line); shut = gsub(/}/, "}", line)
    }
    depth > 0 {
      if (text !~ /^[ \t]*$/) print prefix text
      depth += open - shut; next
    }
    /^    / || /^$/ { next }
    { print prefix $0; depth += open - shut }' <<< "$doc"
}

# apikeys turns apidiff output into approval keys. Additions are expanded to
# their complete declarations, because apidiff names an added symbol but not
# its type, and reports neither the contents of an added package nor the
# methods and fields of an added type.
#
# Main packages are skipped: nothing can import them, and the root package's
# Version constant changes on every build.
apikeys() {
  local line pkg sym mains owner
  mains=$(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... | sed "s|^$module|.|") \
    || die "go list failed"
  while IFS= read -r line; do
    if [[ $line =~ ^package\ ([^:]+): ]]; then
      owner=${BASH_REMATCH[1]/#$module/.}
    elif [[ $line =~ ^(\./[^ .]+)\. ]]; then
      owner=${BASH_REMATCH[1]}
    else
      owner=.
    fi
    if grep -qxF -- "$owner" <<< "$mains"; then
      continue
    fi
    if [[ $line =~ ^package\ (.+):\ added$ ]]; then
      echo "api: $line"
      declarations "${BASH_REMATCH[1]}" "" || return 2
    elif [[ $line =~ ^(\./[^ .]+)\.([^ ]+):\ added$ ]]; then
      # A method added to an existing type is named (*T).M or T.M.
      pkg=${BASH_REMATCH[1]} sym=${BASH_REMATCH[2]/#\(\*/}
      declarations "$pkg" "${sym/\)/}" || return 2
    else
      echo "api: $line"
    fi
  done < <(sed -n 's/^- //p' "$out/apidiff.txt")
}

# gomod prints every go.mod field as canonical JSON, one line per value, so a
# directive the script does not know about is still compared.
gomod() {
  go mod edit -json "$1" \
    | jq -c 'to_entries[] | .key as $k | (if (.value | type) == "array" then .value[] else .value end) | {($k): .}' \
    || die "cannot read $1"
}
gomod "$basedir/go.mod" | sort > "$out/base-gomod.txt" || exit 2
gomod go.mod | sort > "$out/gomod.txt" || exit 2
apikeys > "$out/api-keys.txt" || exit 2

{
  comm -13 "$out/base-packages.txt" "$out/packages.txt" | sed 's/^/package + /' || exit 2
  comm -23 "$out/base-packages.txt" "$out/packages.txt" | sed 's/^/package - /' || exit 2
  cat "$out/api-keys.txt" || exit 2
  comm -13 "$out/base-gomod.txt" "$out/gomod.txt" | sed 's/^/module + /' || exit 2
  comm -23 "$out/base-gomod.txt" "$out/gomod.txt" | sed 's/^/module - /' || exit 2
} | sort -u > "$out/changes.txt" || die "cannot list changes"
if [[ -e $approvals ]]; then
  { grep -v -e '^#' -e '^$' "$approvals" || (($? == 1)); } | sort -u > "$out/approved.txt" \
    || die "cannot read $approvals"
else
  : > "$out/approved.txt"
fi

section "unapproved package, API and go.mod changes"
comm -23 "$out/changes.txt" "$out/approved.txt" > "$out/unapproved.txt" || die "comm failed"
if [[ -s $out/unapproved.txt ]]; then
  cat "$out/unapproved.txt"
  fail=1
fi
comm -13 "$out/changes.txt" "$out/approved.txt" > "$out/stale.txt" || die "comm failed"
if [[ -s $out/stale.txt ]]; then
  echo "approved but no longer present (remove from $approvals):"
  cat "$out/stale.txt"
  fail=1
fi

# normalize drops blank lines, vendored packages, line numbers and the
# standalone numbers in messages from the findings in file $1, so a finding
# dev already has still matches when a count in it changes. An existing finding
# that gets worse is therefore not reported; review covers that. Numbers inside
# names and paths, such as v2.go, are kept.
normalize() {
  { grep -v '^$' "$1" || (($? == 1)); } | { grep -Ev "$vendored" || (($? == 1)); } \
    | sed -E 's/:[0-9]+:[0-9]+:/:/; s/(^|[ (:])[0-9]+/\1#/g' | sort
}

# newfindings prints the findings in file $1 that are not in baseline $2.
newfindings() {
  normalize "$1" > "$1.norm" || die "cannot normalize $1"
  comm -13 "$2" "$1.norm" || die "comm failed"
}

# --- Dead code.

# dead lists the dead code in the module at $1: functions deadcode or
# staticcheck finds unreachable, and packages no production binary imports.
dead() {
  local dir=$1 status report all prod
  report=$(cd "$dir" && deadcode -generated . ./cmd/sfpg-go-dashboard 2>&1) || die "deadcode failed in $dir: $report"
  printf '%s\n' "$report"
  report=$(cd "$dir" && staticcheck -tests=false -checks U1000 ./... 2>&1)
  status=$?
  # staticcheck exits 1 when it reports findings; anything else, or any line
  # that is not a finding, means it did not run.
  if ((status > 1)) || grep -Ev '^[^ :]+:[0-9]+:[0-9]+: ' <<< "$report" | grep -q .; then
    die "staticcheck failed in $dir: $report"
  fi
  printf '%s\n' "$report"
  all=$(cd "$dir" && go list ./... | sort) || die "go list failed in $dir"
  # With default.pgo, go list -deps names each package "path [main package]".
  prod=$(cd "$dir" && go list -deps . ./cmd/sfpg-go-dashboard | sed 's/ \[.*\]$//' | sort -u) \
    || die "go list -deps failed in $dir"
  comm -23 <(printf '%s\n' "$all") <(printf '%s\n' "$prod") | sed 's/^/package not imported by production: /'
}
section "dead code not in dev"
deadbase=$out/dev-$key.dead
if [[ ! -s $deadbase ]]; then
  dead "$basedir" > "$out/base-dead.raw" || exit 2
  normalize "$out/base-dead.raw" | publish "$deadbase" || die "cannot write $deadbase"
fi
dead . > "$out/dead.raw" || exit 2
newfindings "$out/dead.raw" "$deadbase" > "$out/dead-new.txt" || exit 2
if [[ -s $out/dead-new.txt ]]; then
  cat "$out/dead-new.txt"
  fail=1
fi
echo "($(wc -l < "$deadbase") findings dev already has are listed in $deadbase)"

# --- R16 on changed lines.

# depguard sees only import lines, so a database type used through an import
# dev already had would pass it. This text check is a backstop; review covers
# what it cannot see, such as a type reached through a function result.
section "database types on changed image-stack lines (R16)"
git diff -U0 "$base" -- "${imagestack[@]/%//*.go}" ':!*_test.go' > "$out/r16.diff" || die "git diff failed"
{ grep -E '^\+[^+]' "$out/r16.diff" || (($? == 1)); } \
  | { grep -E '\b(sql|gallerydb|dbconnpool)\.' || (($? == 1)); } > "$out/r16.txt" || die "R16 scan failed"
if [[ -s $out/r16.txt ]]; then
  cat "$out/r16.txt"
  fail=1
fi

# --- Lint.

# lint runs the v3 rules over the module at $1. Findings are compared with
# dev's rather than limited to changed lines, because some linters (gocognit,
# nestif) report on a line the change did not touch.
lint() {
  local dir=$1 status
  (cd "$dir" && golangci-lint run -c "$root/.golangci-v3.yml" \
    --output.text.path=stdout --output.text.print-issued-lines=false --output.text.print-linter-name=true ./...) \
    > "$out/lint-$2.raw" 2>&1
  status=$?
  ((status <= 1)) || die "golangci-lint failed in $dir: $(cat "$out/lint-$2.raw")"
  # Keep the findings; drop the summary lines.
  { grep -E '^[^ :]+:[0-9]+:[0-9]+: ' "$out/lint-$2.raw" || (($? == 1)); } > "$out/lint-$2.txt"
}
section "lint findings not in dev (.golangci-v3.yml)"
lintbase=$out/dev-$key.lint
if [[ ! -s $lintbase ]]; then
  lint "$basedir" base || exit 2
  normalize "$out/lint-base.txt" | publish "$lintbase" || die "cannot write $lintbase"
fi
lint . candidate || exit 2
newfindings "$out/lint-candidate.txt" "$lintbase" > "$out/lint-new.txt" || exit 2
if [[ -s $out/lint-new.txt ]]; then
  cat "$out/lint-new.txt"
  fail=1
fi
echo "($(wc -l < "$lintbase") findings dev already has are listed in $lintbase)"

section "result"
if ((fail)); then
  echo "FAIL"
else
  echo "ok"
fi
exit "$fail"
