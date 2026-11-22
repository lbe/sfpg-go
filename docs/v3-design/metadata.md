# metadata: design

Status: approved by the user on 2026-11-18, with the work order change and
decisions 1 to 3 accepted.

The first refactorv3 component. It replaces `dev`'s imagemeta-based EXIF and XMP
reading with ExifTool, adds IPTC and orientation, and stores metadata by replacing a
file's rows on every scan. Behavior follows v2 (`docs/v3-spec/metadata.md`) and the
decisions in `docs/v3-spec.md`, items 14 to 18.

## Work order change

The spec's step 1, "schema and queries", cannot land alone. Each piece needs its
consumer in the same commit:

- The metadata query changes have no caller until this component, so they would be
  dead code (R5).
- v2's migration 024 (025 in v3) and the hash cutover clear `files.phash`. Landing them before the new
  pHash would let `dev`'s hash code write old-format values again.

So the query changes move into this component, v2's migration 024 and the cutover move into
the thumbnail component with the new pHash, and the folder-index query moves into the
write-path component. The new order is: metadata, thumbnail and memlimit, write path,
discovery, server wiring, finish.

## Package `internal/metadata` (new, approved)

```go
// Package metadata reads image metadata with ExifTool.
package metadata

// ErrClosed is returned by Extract after Close.
var ErrClosed = errors.New("metadata: extractor closed")

// Extractor runs a small pool of ExifTool servers. It is safe for concurrent use.
type Extractor struct{ /* unexported */ }

// New returns an Extractor. ExifTool servers start on first use.
func New() *Extractor

// Extract reads the metadata of the image at path.
func (e *Extractor) Extract(ctx context.Context, path string) (Info, error)

// Close waits for running extractions and shuts the servers down.
func (e *Extractor) Close() error

// Info is the metadata of one image. A zero field was not present.
type Info struct {
	Exif Exif
	IPTC IPTC
	XMP  XMP
}

type Exif struct {
	CameraMake   string    // lower case
	CameraModel  string
	LensModel    string
	FocalLength  string    // "50.00mm"
	Aperture     string    // "2", "1.70"
	ShutterSpeed string    // "1/1600", "2.50"
	ISO          int64
	Orientation  int64     // 1 to 8
	CaptureDate  time.Time
	Latitude     float64   // with Longitude, 0, 0 means no position
	Longitude    float64
	Altitude     float64
	HasAltitude  bool
}

type IPTC struct {
	Title, Description, Creator, Copyright, Credit, Source string
	Keywords []string
	Created  time.Time
}

type XMP struct {
	Packet                     []byte // the raw XMP packet
	GPSLatitude, GPSLongitude string // as written in the packet
}
```

Inside the package:

- **Pool.** A `semaphore.Weighted` of `min(4, max(2, NumCPU/3))` slots bounds running
  extractions. Idle servers are a slice of `{server, since}` under a mutex. A server
  starts only when no idle one exists. A failed start returns its error for that call
  only; the next call tries again (spec item 15).
- **Idle shutdown.** `New` starts one goroutine with a 30 s ticker that shuts down
  servers idle for 30 s or more. `Close` stops it and waits (spec item 18).
- **Retirement.** After an extraction, a server is shut down instead of returned to idle
  when the file is at least 16 MiB or the JSON output at least 1 MiB, because ExifTool's
  WASM memory never shrinks.
- **Command.** The same arguments as v2: `-json -G1 -n -struct -b -all -XMP:XMP` and the
  five embedded-image exclusions, then the absolute path. A path containing `\r`, `\n`
  or `\x00` is rejected. The arguments are built by a function, not held in a package
  variable (R14).
- **Results.** Exactly one JSON object. A non-empty `ExifTool:Error` fails the
  extraction (spec item 14). Tag mapping, formatting, date parsing, IPTC precedence and
  keyword rules are v2's (`docs/v3-spec/metadata.md` section 1.2), written over plain
  types. Lat 0 and lon 0 mean no position, as in `dev`'s `setGPSFromExif`. When EXIF has
  no position, the XMP GPS strings fill it, as in `dev`.
- **No timeout (spec item 16).** `exiftool.Server.Command` takes no context and cannot be
  interrupted. A timeout could only abandon a command that keeps running, holding its
  WASM memory, so it would trade a stalled worker for a leak. ExifTool does not have
  imagemeta's tight-loop problem that the 5 s timeout was for. `Extract` checks `ctx`
  before it starts a command.

## Changes to `dev` packages

### `internal/server/files`

- `File` loses `Exif`, `Itpc`, `XmpProps`, `XmpRaw` and `HasValidJpegMarkers` and gains
  `Metadata metadata.Info`. The gob wire struct in `gob.go` changes the same way.
- `fileProcessor` owns one `*metadata.Extractor`, made in `NewFileProcessor` and closed
  in `Close`, which the subsystem manager already calls at shutdown. The
  `NewFileProcessor` signature does not change.
- `processFileContents`: the MIME type comes from `http.DetectContentType` on the first
  512 bytes. Then `extractor.Extract`; an error fails the file as
  `failed to extract EXIF data: %w`, so `invalid_files.reason` is `exif` as in `dev`.
- Deleted, with their tests: `DetectMimeType`, `ValidateJpegMarkers`, `BufPool`,
  `ExtractExifData`, `exifTimeout`, the `imageMetaDecode` test seam (R9), the `jpeg-markers`
  case in `categorizeProcessError`, the imagemeta code in `metadata_xmp.go`, and
  `xmp_persist.go`. `metadata.go` and `metadata_xmp.go` go away entirely if nothing else
  remains in them.
- `WriteFileInTx` replaces a file's metadata inside the batch transaction (spec item 17):

  1. `DeleteExif`; `UpsertExif` if any EXIF field is set.
  2. `DeleteIPTCKeywords`. If any IPTC field or keyword is set: `UpsertIPTC`, then
     `InsertIPTCKeyword` per keyword in order. Otherwise `DeleteIPTC`.
  3. `DeleteXMPRaw` and `DeleteXMPProperties`. If there is a packet: `UpsertXMPRaw`, then
     `InsertXMPProperty` for each GPS string (`exif`, `GPSLatitude` and `GPSLongitude`).

  A SQL error fails the write and rolls back the batch, as in v2. `dev` only logged it.
  The conversion from `metadata.Info` to `gallerydb` parameters lives in this function
  and moves with it to `internal/server/database` in the write-path component.

### `internal/gallerydb` and `sqlc/queries`

Changed in place (R6), then regenerated with sqlc:

| Query               | Change                                                                                                                                    |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `DeleteExif`        | New: `DELETE FROM exif_metadata WHERE file_id = ?`. No existing query deletes EXIF.                                                       |
| `DeleteIPTCKeyword` | Renamed `DeleteIPTCKeywords`; `WHERE file_id = ?`.                                                                                        |
| `InsertIPTCKeyword` | Drops `id`: `INSERT INTO iptc_keywords (file_id, keyword) VALUES (?, ?)`.                                                                 |
| `DeleteXMPProperty` | Renamed `DeleteXMPProperties`; `WHERE file_id = ?`.                                                                                       |
| `UpsertXMPProperty` | Renamed `InsertXMPProperty`; a plain insert without `id`. Rows are replaced by delete then insert, so the synthetic `fileID*10+n` IDs go. |

`iptc_keywords.id` and `xmp_properties.id` are `INTEGER PRIMARY KEY`, so no migration is
needed. `gallerylib`'s `importerQueries` interface changes to match.

### `go.mod`

Adds `github.com/lbe/go-exiftool-wasm v0.6.0` and whatever it requires. imagemeta stays:
`dev`'s thumbnail code still uses its `imagehash`. The thumbnail component removes it.

## Approvals this design asks for

Exported API, `go.mod` and new-package changes, in the form `scripts/v3-check.sh` will
print them, added to `docs/v3-approvals.txt` after you approve:

- the new package `internal/metadata` and the API above;
- `files`: the `File` field changes and the removal of `DetectMimeType`,
  `ValidateJpegMarkers`, `BufPool` and `ExtractExifData`;
- `gallerydb`: the query changes above and their parameter types;
- `go.mod`: adding `go-exiftool-wasm`.

## Decisions

All three accepted by the user.

Also decided during implementation: ISO 0 reported by ExifTool is stored as NULL
(v2 stored 0), and `files.SetGPSFromStrings` is removed; its parsing moved into
`metadata` with the XMP GPS fallback.

Migration 024 indexes `file_id` on `iptc_keywords` and `xmp_properties`, so the
per-file deletes and the `ON DELETE CASCADE` from `files` use an index instead of
scanning the table (user, 2026-11-18). v2's `024_clear_legacy_phash` therefore
arrives with the thumbnail component as 025, with its content unchanged and the
hash cutover checking for a version below 25. A database migrated to 025 would
never run a 024 added later, so the numbers must stay in order.

1. **No extraction timeout**, for the reason above. I recommend accepting.
2. **An empty keyword list is stored as no keywords** (`keywords` NULL). v2 stored `"[]"`
   when XMP had an empty `dc:Subject` list. gob does not keep an empty slice distinct
   from a nil one, so keeping v2's distinction would need an extra field. I recommend
   NULL.
3. **`HasAltitude bool`** marks whether altitude was present, because 0 is a real
   altitude. I recommend it over a `*float64`.

## Tests

- `internal/metadata`, with real ExifTool on fixtures: the ExifTool sample images in the
  corpus, plus v2's IPTC and XMP fixtures copied in. They cover the EXIF mapping and
  formats, IPTC precedence and keywords, XMP GPS fallback, dates, an `ExifTool:Error`
  file, a rejected path, `ErrClosed`, and retirement of a server after a 16 MiB file.
  Idle shutdown uses `testing/synctest` if ExifTool's WASM runtime works inside a
  bubble. If it does not, the janitor's reap step takes the current time as an argument
  and is tested directly.
- `internal/server/files`: `WriteFileInTx` with the existing test database harness. A
  rescan with fewer tags removes stale EXIF, IPTC, keyword and XMP rows, and keywords
  are stored in order.
- Tests that only exercised the deleted code are deleted with it (R5, R26).

## Verification

- `scripts/format-go-changed.sh`, `go build`, the package tests, `make test-all` through
  the commit hook.
- `scripts/v3-check.sh` passes once the approvals are recorded.
- `scripts/v3-parity.sh` on the corpus: `exif_metadata`, `iptc_metadata`,
  `iptc_keywords`, `xmp_raw` and `xmp_properties` match v2, apart from decision 2. Other
  tables still differ until later components.
- `scripts/v3-review.sh` for the final review.
