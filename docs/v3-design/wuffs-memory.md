# Wuffs decoder memory: design

Proposed on 2026-11-21; updated on 2026-11-22 for wuffs-wasm v0.5.0, then
implemented and measured with v0.6.0 on 2026-11-22.

The memory changes were made in a different repository, `wuffs-wasm`
(`github.com/lbe/wuffs-wasm`), by its owner, from the handover
`/tmp/wuffs-wasm-handover.md`. They were released as v0.6.0.

## On v0.5.0

sfpg-go is on wuffs-wasm v0.5.0 (commit `build(deps): move to wuffs-wasm
v0.5.0`). v0.5.0 puts a decoder's work buffer after the pixels in the
destination slot and reports its size as `Meta.WorkbufLen`, so
`decodeWuffs` reserves `Stride*Height + WorkbufLen`. A new decoder allocates
32 MiB of guest memory instead of 128 MiB, and the memory-limit reservation
per live decoder is 32 MiB. PNGs that failed from 4230×4230 on v0.4.0 now
decode up to 5600×5600; 6000×6000 returns a `Reserve` error.

## Cause

A decoder's guest memory is still a Go slice. When `Reserve` needs more than
the default slots (the top 8 MiB of the initial 32 MiB, so for a decode larger
than about 1000×1000 RGBA), the generated `memory_grow` grows it with `append`:
a new slice, rounded up to 40, 50 or 62.5 MiB, gets a copy of the old one. Every
page of the grown slice is resident, and the old 32 MiB stays resident until it
is collected and scavenged. A decoder never shrinks.

Measured in fresh processes (MiB):

| Case                               | v0.4.0 RSS | v0.5.0 RSS |
| ---------------------------------- | ---------- | ---------- |
| New decoder, fresh memory          | 4          | 4          |
| 8 decoders after 512×512 decodes   | 27         | 26         |
| 1 decoder after 2000×2000 decode   | 51         | 83         |
| 8 decoders after 1024×1024 decodes | 87         | 434        |

Gallery acceptance runs (22 workers, 16.1 M files):

| Run     | wuffs-wasm | Thumbnails | Time      | Peak RSS | Decoders at the peak   |
| ------- | ---------- | ---------- | --------- | -------- | ---------------------- |
| v3-run6 | v0.4.0     | 127,073    | 31 m 27 s | 1.618 GB | 22 at 128 MiB          |
| v3-run7 | v0.5.0     | 132,045    | 33 m 42 s | 1.622 GB | 10 at 32 MiB, 12 grown |
| v3-run8 | v0.5.0     | 132,045    | 31 m 43 s | 1.780 GB | 8 at 32 MiB, 14 grown  |
| v3-run9 | v0.4.0     | 132,045    | 36 m 09 s | 1.754 GB | 22 at 128 MiB          |

Grown decoders held 40 to 62.5 MiB each, 580 to 670 MiB in all. Decoder heap
fell from 2,816 MB to about 0.9 GB, but peak RSS did not fall: runs of one
build on the same workload differ by about 150 MB, and within that v0.4.0 and
v0.5.0 are the same. v3-run7 started with the host at load 11.65 and v3-run9
ran beside other work, which affects their times.

The workload grew from 127,073 to 132,045 thumbnails after v3-run6, and both
versions now cross the 1.68 GB limit in some runs.

## Made in wuffs-wasm v0.6.0 (outside this repository)

These changes were made in `wuffs-wasm` by its owner, from the handover.

1. **Guest memory off the Go heap.** The guest imports its memory; wuffs-wasm
   maps it (unix `mmap` reserve and `mprotect` commit, Windows `VirtualAlloc`,
   `append` elsewhere), following go-sqlite3 v0.35.0. Pages become resident
   only when written, and growing never copies.
2. **`(*Decoder).Close() error`** unmaps the memory.
3. **`(*Decoder).Trim()`** returns the pages from the bump base (9 MiB) to the
   end of memory and keeps the decoder usable. In v0.5.0 that range holds only
   per-call data.

## Change in sfpg-go (made with v0.6.0)

- `go.mod`: wuffs-wasm v0.6.0 (approved).
- `internal/thumbnail/wuffs.go`:
  - `wuffsDecoderBytes` and the `limit.Reserve`/`Release` calls are removed.
    The decoder memory is no longer on the Go heap.
  - `dropDecoder` calls `Close`.
  - After a Wuffs decode whose output exceeds `wuffsTrimBytes` (4 MiB), the
    Generator calls `Trim`. A kept decoder then holds about 10 MiB resident.
- `internal/thumbnail/generator.go`: the decoder is dropped only after a
  failed Wuffs decode, and at `Close`. An oversized source or an oversized
  Wuffs decode trims the decoder instead of dropping it. The oversized slot
  itself is unchanged.
- `internal/memlimit`: `Limit.Reserve`, `Limit.Release`, `adjust` and the
  `reserved` field lose their only caller and are deleted with their tests.
  The package is new in v3, so this needs no approval.
- `internal/thumbnail/pool_test.go`: the decoder-reservation checks are
  removed. The tests check that the decoder is kept after an oversized source
  and an oversized decode, and is dropped after a failed decode.
- Docs: `docs/v3-design/thumbnail.md` (Decoder bullet),
  `docs/ARCHITECTURE.md` (thumbnail and memlimit), and a note in
  `decoder-retention.md` that this design supersedes its drop rule.

## Verification

1. **In wuffs-wasm, by its owner (outside this repository).** The tests listed
   in the handover, including `Close`, decode-`Trim`-decode pixel equality for
   every format, and the RSS checks.
2. **In sfpg-go, with v0.6.0.** Done on 2026-11-22:
   - `scripts/v3-check.sh` passes, `make test-all` passes, and
     `scripts/v3-parity.sh tmp/corpus` shows no new differences.
   - RSS programs (MiB):

     | Case                                    | v0.4.0 | v0.5.0 | v0.6.0 |
     | --------------------------------------- | ------ | ------ | ------ |
     | 8 decoders after 1024×1024 decodes      | 87     | 434    | 78     |
     | The same, each trimmed after its decode |        |        | 16     |
     | The same, after `Close` on all eight    |        |        | 14     |
     | 1 decoder after 2000×2000, then `Trim`  |        |        | 19     |

     The 19 MiB includes the 16 MiB `dst.Pix` on the Go heap. PNGs up to
     5600×5600 decode equal to the source before and after `Trim`.

   - Gallery runs (132,045 thumbnails; `finish.md`, "Wuffs memory"): v0.6.0
     peaked at 1.30 GB in both runs (33 m 25 s, 32 m 04 s), against 1.62 and
     1.78 GB for v0.5.0 and 1.75 GB for v0.4.0. At the peaks, decoder memory
     no longer appears in the Go heap.

## Stop conditions

- The wuffs-wasm release does not provide `Close` and `Trim`, or its memory is
  still on the Go heap.
- A pixel comparison fails after `Trim`.
- The acceptance runs' peak RSS does not fall below v0.5.0's, or their time
  exceeds the limit.

## Decisions

1. **Trim threshold: 4 MiB of output**, checked against decode time in the
   acceptance runs.
2. **Acceptance limits.** The 1.68 GB and 34 m 02 s limits, set for 127,073
   thumbnails, still apply to 132,045. Decided by the user on 2026-11-22.
