#!/usr/bin/env bash
# v3-parity.sh compares what two builds record for the same image corpus. It
# builds the spec (default refactorv2/image-stack) and the working tree, runs
# startup discovery for each against CORPUS, one after the other, and diffs
# the resulting databases table by table. Rows are keyed by path, so row IDs
# and timestamps, which differ between runs, are left out. Thumbnails are
# compared by the SHA3-256 of their bytes.
#
# Usage: scripts/v3-parity.sh CORPUS [SPEC]
#
# CORPUS is a directory of images, normally made by tools/gencorpus. Output
# is in tmp/parity: a dump per side and table, diffs, each side's log, and
# summary.txt with the elapsed time and peak RSS of each run. It exits 1 if
# any table differs, or 2 if a run or a query failed. Linux only (it reads
# /proc for peak RSS). Needs the sqlite3 command-line shell and jq.
set -Eeuo pipefail
# Any failure outside the comparison is a failed run, not a finding.
trap 'exit 2' ERR

if (($# < 1 || $# > 2)); then
  echo "usage: $0 CORPUS [SPEC]" >&2
  exit 2
fi
die() {
  echo "v3-parity: $*" >&2
  exit 2
}
[[ $(uname -s) == Linux ]] || die "Linux only"
for tool in sqlite3 jq go git flock; do
  command -v "$tool" > /dev/null || die "$tool not found"
done
[[ -d $1 ]] || die "$1 is not a directory"
corpus=$(realpath "$1")
# The path goes into YAML as a double-quoted string.
[[ $corpus != *[\"\\]* && $corpus != *$'\n'* ]] || die "corpus path contains a quote, backslash or newline"
spec=${2:-refactorv2/image-stack}
root=$(git rev-parse --show-toplevel)
cd "$root"
out=$root/tmp/parity
specdir=$root/.worktrees/v3-spec
mkdir -p "$root/tmp"
# The script deletes both directories, so neither may hold the corpus.
for dir in "$out" "$specdir"; do
  dir=$(realpath -m "$dir")
  [[ $corpus/ != "$dir"/* && $dir/ != "$corpus"/* ]] || die "corpus and $dir overlap"
done
# One run at a time per worktree: runs share $out and $specdir.
exec 9> "$root/tmp/.v3-parity.lock"
flock -n 9 || die "another v3-parity.sh is running in this worktree"
timeout=${PARITY_TIMEOUT:-1800}
settle=10
export LC_ALL=C GOWORK=off GOFLAGS=
rm -rf "$out"
mkdir -p "$out/spec" "$out/candidate"

log() { printf '[%s] %s\n' "$(date +%T)" "$*"; }

# Build both sides. The spec is built from its own clean worktree so the
# working tree is left alone.
want=$(git rev-parse --verify "$spec^{commit}") || die "no commit $spec"
# clean reports whether the spec checkout is exactly $want, with no local
# changes, no untracked files and its submodules at their recorded commits.
clean() {
  local status subs
  [[ $(git -C "$specdir" rev-parse HEAD 2> /dev/null) == "$want" ]] || return 1
  status=$(git -C "$specdir" status --porcelain --untracked-files=all --ignore-submodules=none) || return 1
  subs=$(git -C "$specdir" submodule status) || return 1
  [[ -z $status ]] && ! grep -q '^[-+U]' <<< "$subs"
}
if ! clean 2> /dev/null; then
  if [[ -e $specdir ]]; then
    git worktree remove --force "$specdir" || die "cannot remove $specdir"
  fi
  git worktree add --quiet --detach "$specdir" "$want" || die "cannot create $specdir"
  git -C "$specdir" submodule --quiet update --init || die "cannot init submodules in $specdir"
  clean || die "$specdir is not a clean checkout of $want"
fi
log "building spec $(git rev-parse --short "$want") and candidate $(git rev-parse --short HEAD)"
(cd "$specdir" && CGO_ENABLED=0 go build -trimpath -o "$out/spec/sfpg" .) || die "spec build failed"
CGO_ENABLED=0 go build -trimpath -o "$out/candidate/sfpg" . || die "candidate build failed"

pid=
# Stop a run that is still going if the script exits early.
trap '[[ -n $pid ]] && kill -KILL "$pid" 2> /dev/null' EXIT

# sql runs query $2 against the databases in directory $1 and prints the rows
# as JSON, one object per line, which keeps NULL distinct from empty text.
sql() {
  sqlite3 -init /dev/null -readonly -json -cmd "ATTACH '$1/DB/thumbs/thumbs.db' AS thumbs" \
    "$1/DB/sfpg.db" "$2"
}

# run starts the binary in directory $1 with startup discovery and waits until
# its work is done: processing has completed, every file has its
# file_folder_index row, and the database files have not changed for $settle
# seconds. "File processing completed" alone is too early: queued writes and
# the folder index rebuild follow it. It records the time of the last
# database change and the peak RSS, then stops the binary and requires a
# clean exit.
run() {
  local dir=$1 port=$2 start elapsed=0 rss state last='' quiet=0 finished status
  # No restart after discovery: v2 defaults to it, and the re-exec would replace
  # the process whose peak RSS is measured.
  printf 'image-directory: "%s"\nrestart-after-discovery: false\n' "$corpus" > "$dir/config.yaml"
  start=$SECONDS
  (cd "$dir" && exec env -i PATH="$PATH" HOME="$HOME" \
    SEPG_SESSION_SECRET="$(head -c 48 /dev/urandom | base64)" SEPG_SESSION_SECURE=false \
    ./sfpg -discover -port="$port" -log-level=info > run.log 2>&1) &
  pid=$!
  while :; do
    kill -0 "$pid" 2> /dev/null || die "$dir: exited before its work was done; see $dir/run.log"
    ((SECONDS - start <= timeout)) || die "$dir: work not done within ${timeout}s; see $dir/run.log"
    sleep 1
    state=$(stat -c '%n %s %.9Y' "$dir"/DB/sfpg.db* "$dir"/DB/thumbs/thumbs.db* 2> /dev/null || true)
    if [[ $state != "$last" ]]; then
      last=$state quiet=0 elapsed=$((SECONDS - start))
      continue
    fi
    if ((++quiet < settle)) || ! grep -q 'File processing completed' "$dir/run.log"; then
      continue
    fi
    # Done means every file is indexed and discovery recorded a finished run.
    finished=$(sqlite3 -init /dev/null -readonly "$dir/DB/sfpg.db" \
      "SELECT (SELECT count(*) FROM files) = (SELECT count(*) FROM files f JOIN file_folder_index x ON x.file_id = f.id)
        AND EXISTS (SELECT 1 FROM module_state WHERE name = 'discovery' AND is_active = 0
          AND last_started_at IS NOT NULL AND last_finished_at >= last_started_at)" \
      2> /dev/null || true)
    if [[ $finished == 1 ]]; then
      break
    fi
  done
  rss=$(awk '/^VmHWM:/ {print $2, $3}' "/proc/$pid/status")
  kill -TERM "$pid"
  # Give shutdown a minute, then give up.
  for _ in {1..60}; do
    kill -0 "$pid" 2> /dev/null || break
    sleep 1
  done
  kill -0 "$pid" 2> /dev/null && die "$dir: did not stop within 60s of SIGTERM"
  status=0
  wait "$pid" || status=$?
  pid=
  ((status == 0)) || die "$dir: exited with status $status after SIGTERM; see $dir/run.log"
  printf '%s: %ds, peak RSS %s\n' "$(basename "$dir")" "$elapsed" "$rss" >> "$out/summary.txt"
}

log "running spec"
run "$out/spec" 18091
log "running candidate"
run "$out/candidate" 18092

# Each query is keyed by path. Columns that hold row IDs are replaced by the
# path of the row they refer to. folders.mtime is left out: the app records the
# time it saw the folder, not the directory's modification time. Which image
# becomes a folder's tile depends on processing order, which varies between
# runs, so the tile is checked rather than compared: it must be an image in
# the folder or below it. module_state is compared without its timestamps.
queries() {
  cat << 'EOF'
schema|SELECT type, name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name
thumbs_schema|SELECT type, name, sql FROM thumbs.sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name
migrations|SELECT version, dirty FROM schema_migrations
thumbs_migrations|SELECT version, dirty FROM thumbs.schema_migrations
folders|SELECT fp.path, pp.path AS parent, f.name, CASE WHEN f.tile_id IS NULL THEN 'none' WHEN tp.path IS NULL THEN 'missing' WHEN fp.path = '' OR substr(tp.path, 1, length(fp.path) + 1) = fp.path || '/' THEN 'ok' ELSE 'outside' END AS tile FROM folders f JOIN folder_paths fp ON fp.id = f.path_id LEFT JOIN folders p ON p.id = f.parent_id LEFT JOIN folder_paths pp ON pp.id = p.path_id LEFT JOIN files t ON t.id = f.tile_id LEFT JOIN file_paths tp ON tp.id = t.path_id ORDER BY 1
files|SELECT p.path, dp.path AS folder, f.filename, f.size_bytes, f.mtime, f.md5, CAST(f.phash AS TEXT) AS phash, f.mime_type, f.width, f.height FROM files f JOIN file_paths p ON p.id = f.path_id LEFT JOIN folders d ON d.id = f.folder_id LEFT JOIN folder_paths dp ON dp.id = d.path_id ORDER BY 1
invalid_files|SELECT i.path, i.mtime, i.size, i.reason, dp.path AS folder FROM invalid_files i LEFT JOIN folders d ON d.id = i.folder_id LEFT JOIN folder_paths dp ON dp.id = d.path_id ORDER BY 1
exif_metadata|SELECT p.path, e.camera_make, e.camera_model, e.lens_model, e.focal_length, e.aperture, e.shutter_speed, e.iso, e.orientation, e.latitude, e.longitude, e.altitude, e.capture_date, e.software FROM exif_metadata e JOIN files f ON f.id = e.file_id JOIN file_paths p ON p.id = f.path_id ORDER BY 1
iptc_metadata|SELECT p.path, m.title, m.description, m.keywords, m.creator, m.copyright, m.credit, m.source, m.created_date FROM iptc_metadata m JOIN files f ON f.id = m.file_id JOIN file_paths p ON p.id = f.path_id ORDER BY 1
iptc_keywords|SELECT p.path, k.keyword FROM iptc_keywords k JOIN files f ON f.id = k.file_id JOIN file_paths p ON p.id = f.path_id ORDER BY 1, 2
xmp_properties|SELECT p.path, x.namespace, x.property, x.value FROM xmp_properties x JOIN files f ON f.id = x.file_id JOIN file_paths p ON p.id = f.path_id ORDER BY 1, 2, 3, 4
xmp_raw|SELECT p.path, hex(sha3(x.raw_xml, 256)) AS raw_xml FROM xmp_raw x JOIN files f ON f.id = x.file_id JOIN file_paths p ON p.id = f.path_id ORDER BY 1
thumbnails|SELECT p.path, t.size_label, t.width, t.height, t.format, hex(sha3(b.data, 256)) AS data FROM thumbnails t JOIN files f ON f.id = t.file_id JOIN file_paths p ON p.id = f.path_id LEFT JOIN thumbs.thumbnail_blobs b ON b.thumbnail_id = t.id ORDER BY 1, 2
module_state|SELECT name, is_active, payload FROM module_state ORDER BY name
file_folder_index|SELECT p.path, dp.path AS folder, x.image_index, x.image_count, pp.path AS prev, np.path AS next, fp.path AS first, lp.path AS last FROM file_folder_index x JOIN files f ON f.id = x.file_id JOIN file_paths p ON p.id = f.path_id LEFT JOIN folders d ON d.id = x.folder_id LEFT JOIN folder_paths dp ON dp.id = d.path_id LEFT JOIN files pf ON pf.id = x.prev_id LEFT JOIN file_paths pp ON pp.id = pf.path_id LEFT JOIN files nf ON nf.id = x.next_id LEFT JOIN file_paths np ON np.id = nf.path_id LEFT JOIN files ff ON ff.id = x.first_id LEFT JOIN file_paths fp ON fp.id = ff.path_id LEFT JOIN files lf ON lf.id = x.last_id LEFT JOIN file_paths lp ON lp.id = lf.path_id ORDER BY 1
EOF
}

# dump writes one file per query for the databases in directory $1, one JSON
# row per line. A query that fails stops the run.
dump() {
  local dir=$1 name query
  mkdir -p "$dir/dump"
  while IFS='|' read -r name query; do
    sql "$dir" "$query" 2> "$dir/dump/$name.err" | jq -c '.[]' > "$dir/dump/$name.jsonl" \
      || die "$dir: query $name failed: $(cat "$dir/dump/$name.err")"
    [[ ! -s $dir/dump/$name.err ]] || die "$dir: query $name failed: $(cat "$dir/dump/$name.err")"
    rm "$dir/dump/$name.err"
  done < <(queries)
}
dump "$out/spec"
dump "$out/candidate"

# Each check lists rows that are wrong on their own, whatever the other side
# has. Path joins turn a dangling ID into NULL, which could match a real NULL,
# so references are checked here first.
checks() {
  cat << 'EOF'
bad_tiles|SELECT fp.path, f.tile_id FROM folders f JOIN folder_paths fp ON fp.id = f.path_id LEFT JOIN files t ON t.id = f.tile_id LEFT JOIN file_paths tp ON tp.id = t.path_id WHERE f.tile_id IS NOT NULL AND (tp.path IS NULL OR (fp.path <> '' AND substr(tp.path, 1, length(fp.path) + 1) <> fp.path || '/'))
dangling_index|SELECT x.file_id, x.prev_id, x.next_id, x.first_id, x.last_id FROM file_folder_index x WHERE (x.prev_id IS NOT NULL AND x.prev_id NOT IN (SELECT id FROM files)) OR (x.next_id IS NOT NULL AND x.next_id NOT IN (SELECT id FROM files)) OR (x.first_id IS NOT NULL AND x.first_id NOT IN (SELECT id FROM files)) OR (x.last_id IS NOT NULL AND x.last_id NOT IN (SELECT id FROM files)) OR x.folder_id NOT IN (SELECT id FROM folders) OR x.file_id NOT IN (SELECT id FROM files)
orphan_blobs|SELECT b.thumbnail_id FROM thumbs.thumbnail_blobs b WHERE b.thumbnail_id NOT IN (SELECT id FROM thumbnails)
EOF
}

status=0
{
  echo
  for side in spec candidate; do
    while IFS='|' read -r name query; do
      rows=$(sql "$out/$side" "$query" 2> "$out/$side/dump/$name.err" | jq -c '.[]') \
        || die "$side: check $name failed: $(cat "$out/$side/dump/$name.err")"
      [[ ! -s $out/$side/dump/$name.err ]] || die "$side: check $name failed: $(cat "$out/$side/dump/$name.err")"
      rm "$out/$side/dump/$name.err"
      if [[ -n $rows ]]; then
        printf '%s\n' "$rows" > "$out/$side/dump/$name.jsonl"
        printf '%-18s FAILS on %s: %d rows, see tmp/parity/%s/dump/%s.jsonl\n' "$name" "$side" \
          "$(wc -l < "$out/$side/dump/$name.jsonl")" "$side" "$name"
        status=1
      fi
    done < <(checks)
  done
  while IFS='|' read -r name _; do
    if diff -u --label "spec/$name" --label "candidate/$name" \
      "$out/spec/dump/$name.jsonl" "$out/candidate/dump/$name.jsonl" > "$out/$name.diff"; then
      printf '%-18s same (%d rows)\n' "$name" "$(wc -l < "$out/spec/dump/$name.jsonl")"
      rm "$out/$name.diff"
    else
      (($? == 1)) || die "diff failed for $name"
      printf '%-18s DIFFERS: %d lines changed, see tmp/parity/%s.diff\n' "$name" \
        "$(($(grep -c '^[-+]' "$out/$name.diff") - 2))" "$name"
      status=1
    fi
  done < <(queries)
} >> "$out/summary.txt"
cat "$out/summary.txt"
exit "$status"
