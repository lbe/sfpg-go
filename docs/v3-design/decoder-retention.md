# Decoder retention: design

Status: approved by the user on 2026-11-20, with decisions 1 and 2 accepted.

A fix for the finish component's failed memory check (`finish.md` §4). v3's
thumbnail runs peaked at 2.50 GB and 2.94 GB RSS against run26's 1.53 GB. Their
time and row counts passed.

## Cause

A `thumbnail.Generator` keeps its Wuffs decoder (128 MiB of WASM linear memory,
one Go slice) between files. v2 dropped it at the end of a file only after:

- a source read larger than 8 MiB;
- a PNG or GIF decode larger than 16 MiB;
- a failed PNG or GIF decode.

v3 drops it after two more:

- **A large JPEG or WebP decode.** `takeOversized` sets `dropDecoderAtEnd`, and
  `decodeOther` calls it when a decode estimate exceeds 16 MiB. v2's equivalent
  took the oversized slot and kept the decoder.
- **Any failed decode.** `GenerateThumbnailAndHashes` sets `dropDecoderAtEnd` on
  every decode error. `decodeWuffs` already sets it for its own errors.

The worker's next PNG or GIF then makes a new decoder. The heap profiles show the
effect:

| Run      | Decoders made | Live at once | Peak RSS | When              |
| -------- | ------------- | ------------ | -------- | ----------------- |
| v2 run26 | 29            | 22           | 1.53 GB  |                   |
| v3-run1  | 91            | 22           | 2.50 GB  | in a burst of ~55 |
| v3-run3  | 100           | 22           | 2.94 GB  | in a burst of ~90 |

Peak RSS in both v3 runs falls inside a burst of new decoders. Mean in-use heap
is about the same in v2 and v3; the difference is how much of it is resident. A
decoder allocated from fresh OS memory becomes resident only where Wuffs writes.
One that reuses freed heap memory is zeroed by the Go runtime first, which makes
all 128 MiB resident. This last step is inferred from the runtime's behavior and
fits the measurements, but was not measured directly.

## Change

All in `internal/thumbnail/generator.go` and `internal/thumbnail/wuffs.go`. No
new package, rename, exported API change or `go.mod` change.

1. `takeOversized` takes the slot only. Its comment no longer mentions the
   decoder.
2. `grow` sets `dropDecoderAtEnd` when it takes the slot for a source larger
   than `sourceRetainBytes`, as v2 did for any file type.
3. `decodeWuffs` keeps setting it on an oversized decode and on an error. Its
   `cap(g.data) > sourceRetainBytes` check goes: every backing above that size
   comes from `grow`, which now sets the flag.
4. `GenerateThumbnailAndHashes` no longer sets it on a decode error.
5. The `dropDecoderAtEnd` field comment names the three cases.

The decoder rule becomes v2's: dropped at `Put` after an oversized source, an
oversized Wuffs decode or a failed Wuffs decode, and at `Close`.

## Tests

In `internal/thumbnail/pool_test.go`, behavior through the Pool (R26), no wall
clock (R27):

- **New: the decoder survives a large JPEG.** A Generator decodes a PNG, then the
  header-only progressive JPEG from `TestProgressiveJPEGTakesOversizedSlot`
  (12000×9000), which takes the oversized slot and fails to decode. After `Put`,
  the Generator still has its decoder, and the memory limit still carries one
  128 MiB reservation. This covers changes 1 and 4.
- **New: an oversized source drops the decoder.** A Generator decodes a PNG,
  then reads a source larger than `sourceRetainBytes`. After `Put`, the decoder
  is gone and its reservation released. This covers change 2.
- **Kept:** `TestPoolReusesGeneratorsAndDecoder` (kept after a PNG, dropped after
  a truncated PNG).

The first test fails on the current code and passes after the change.

## Documentation

- `docs/v3-design/thumbnail.md`, the Decoder bullet: the three drop cases.
- `docs/ARCHITECTURE.md` line 1039: "dropped after a very large source, a very
  large PNG or GIF decode, or a failed PNG or GIF decode".

## Verification

1. `scripts/format-go-changed.sh` on the changed files, `go build -o /dev/null .`,
   `go test ./internal/thumbnail/`.
2. `scripts/v3-check.sh`.
3. `scripts/v3-parity.sh tmp/corpus`. The change affects memory only, so no
   output differences are expected.
4. The Sol review (`scripts/v3-review.sh`), with no unresolved MUST findings.
5. Commit, with your authorization.
6. One thumbnail acceptance run, after you approve it (about 33 minutes): build
   the v3 binary, copy it to `/home/whgi/tmp/gallery/sfpg`, roll back to reset3,
   and run it under `watch.sh`. **Pass:** peak RSS within 10% of run26 (at most
   1.68 GB), time within 10%, the same row counts as v3-run1, and about 29
   decoders made. If it passes, the second thumbnail run and the two
   discovery-only runs from `finish.md` §4 follow, then the completion checks
   (§5).

## Stop conditions

- The new large-JPEG test passes before the change: the cause is not what this
  design says. Stop and report.
- The acceptance run makes about 29 decoders but peak RSS still exceeds 1.68 GB:
  decoder churn was not the whole cause. Stop and report, with the profiles.
- Any parity difference.

## Decisions

1. **Restore v2's rule rather than a new one.** Recommended: v2's rule is the
   spec, and it met the memory target. Dropping a decoder only when its own
   memory grew would be tighter, but it is untested at scale.
2. **Confirm with one thumbnail run before rerunning all four.** Recommended: the
   failure was in the thumbnail runs only; the discovery-only runs make no
   thumbnails and passed.

## Review

Sol's sixth round raised two MUST findings in `Read`'s buffer sizing, for a
file whose size changes between the walk and the read. The user rejected both
on 2026-11-20: that case does not happen in practice.

## Superseded

With wuffs-wasm v0.6.0 the decoder's guest memory is outside the Go heap, so the
decoder is kept after oversized sources and decodes and trimmed instead. See
[wuffs-memory.md](wuffs-memory.md).
