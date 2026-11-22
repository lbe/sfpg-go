# METADATA component spec (v2 -> v3)

Sources: `refactorv2/image-stack` ("v2"), `dev`. Line numbers are v2 file lines unless noted. Items marked (inference) are not verified in code.

## 1. Behavior

### 1.1 Extractor (`internal/metadata/extractor.go`, all new in v2; dev had no `internal/metadata`)

- Wraps `github.com/lbe/go-exiftool-wasm` v0.6.0 (`go.mod`), an in-process WASM ExifTool run with `-stay_open`. One `exiftool.Server` per pool slot. `extractor.go:8,228-236`.
- Command line per file, `extractor.go:34-35`: `-json -G1 -n -struct -b -all -XMP:XMP --ThumbnailImage --PreviewImage --PhotoshopThumbnail --JpgFromRaw --OtherImage <abs path>`. Raw XMP rides in the tag `XMP:XMP`.
- `Extract(ctx, path) (map[string]any, []byte, error)` `:87`.
  - Rejects: ctx already done; empty path or path containing `\r \n \x00` ("metadata path cannot be represented") `:91-93`.
  - Makes the path absolute (`filepath.Abs`) `:94`. The wasm server resolves relative paths against the cwd recorded at `NewServer` (`../go-exiftool-wasm/server.go:173-175`).
  - Admission: waits for a slot, `ctx.Done`, or `closing` (returns `ErrClosed`) `:99-105`.
  - After the command, a cancelled ctx discards the result and returns `ctx.Err()` `:112-115`. Cancellation does not interrupt a running command; Extract waits for the synchronous command (doc comment `:86`).
- Pool: `PoolSize() = min(4, max(2, NumCPU/3))` `:80-83`. Servers start lazily: only when every started server is in use and a slot is held `:165-197`. First server startup runs `-ver` as a readiness check `:181`.
- Startup failure is sticky: `initErr` is stored and every later call returns `metadata startup: ...` `:176-186`. A failed server stays in `e.servers` and is shut down at Close.
- Result decoding `decodeTags :271`:
  - JSON must be an array with exactly one non-nil object.
  - `ExifTool:Error` non-empty returns the error "ExifTool reported an extraction error" `:279-281`. Warnings are ignored.
  - Drops keys with prefix `System:` and `Composite:LensID` `:284`.
  - `takeRawXMP :251`: removes `XMP:XMP`; a string beginning `base64:` is base64-decoded (StdEncoding); otherwise the bytes are copied. Non-string value is an error.
- Retirement `:24-28,121-132`: after an extraction the server is shut down and removed instead of returned to idle if the source file size is `>= recycleFileBytes = 16 << 20` (stat before the command; stat failure counts as 0) or the JSON output is `>= recycleOutputBytes = 1 << 20`. Reason in the comment: WASM linear memory never shrinks. Note the output size test runs after an errored command with `encoded` possibly nil.
- Idle shutdown `:32,199-236`: `idleTimeout = 30 * time.Second`. A janitor goroutine ticks every `idleTimeout` and retires idle servers whose idle age is `>= idleTimeout`. Effective idle lifetime is therefore between 30 s and 60 s, not exactly 30 s (derived from code; tests at `extractor_test.go:632-705` exercise it). The janitor starts with the first server and is joined at Close. The janitor takes slots without blocking so it cannot exceed pool size or race Close.
- Close `:292-325`: `closeOnce`; closes `closing`, joins janitor, acquires every slot (waits for in-flight extractions), shuts down all servers in `e.servers`, joins errors, returns the same `closeErr` to every caller. After Close, `Extract` returns `ErrClosed` (`var ErrClosed = errors.New("metadata extractor closed")` `:22`).
- Errors from `Extract` fail the file (see 1.4). `Shutdown` errors on retire are discarded (`:157`, nolint).

### 1.2 Mapping tags to the file payload (`internal/server/files/exiftool_metadata.go`, `iptc_metadata.go`, `metadata_xmp.go`)

`applyExifToolMetadata(f, tags, rawXMP)` first zeroes `f.Exif`, `f.Itpc`, `f.XmpRaw`, `f.XmpProps` `exiftool_metadata.go:15-18`, so re-applying clears stale data. It returns nil always.

EXIF columns (`exif_metadata`), tag -> column:

- `IFD0:Make` -> `camera_make` (trimmed, then `strings.ToLower`) `:19-31`. Dev also lowercased (dev `metadata.go` ExtractExifData).
- `IFD0:Model` -> `camera_model`; `ExifIFD:LensModel` -> `lens_model`.
- `ExifIFD:FocalLength` -> `focal_length`, text `%.2f` + `mm` (e.g. `50.00mm`), only if `>= 0` `:32-34`.
- `ExifIFD:FNumber` -> `aperture`; `%.2f`, or `%.0f` when n*100 is an exact multiple of 100 `:35-41`.
- `ExifIFD:ExposureTime` -> `shutter_speed`: `1/N` (N = round(1/n, 3 decimals) cast to uint64) when n < 1, else `%.2f`; only if n > 0 `:42-55`.
- `ExifIFD:ISO` -> `iso`. `IFD0:Orientation` -> `orientation` only when 1..8 `:56-61`.
- GPS `:76-100`: `GPS:GPSLatitude/Longitude/Altitude`; |lat|>90 or |lon|>180 discarded; `S`/`W` refs force negative; `GPSAltitudeRef == 1` forces negative altitude; delegates to `setGPSFromExif` (`gps.go:12`, not in scope).
- `capture_date` (unix seconds): first parseable of `ExifIFD:DateTimeOriginal`+`OffsetTimeOriginal`, then `ExifIFD:CreateDate`+`OffsetTimeDigitized` `:66-77`. Layouts in `parseExifToolDate :146`, fallback `parseIPTCDate`.
- `-n` is passed, so ExifTool returns numerics, and the code accepts float64/int/json.Number; NaN/Inf rejected.

IPTC (`iptc_metadata.go:13-22`), XMP value wins over IIM; blank/missing/non-string falls through:

- `title` = `XMP-dc:Title` | `IPTC:ObjectName`; `description` = `XMP-dc:Description` | `IPTC:Caption-Abstract`; `creator` = `XMP-dc:Creator` | `IPTC:By-line` (lists joined with `"; "`, trimmed, blanks dropped); `copyright` = `XMP-dc:Rights` | `IPTC:CopyrightNotice`; `credit` = `XMP-photoshop:Credit` | `IPTC:Credit`; `source` = `XMP-photoshop:Source` | `IPTC:Source`.
- `keywords` (column, JSON string array) = `XMP-dc:Subject` | `IPTC:Keywords` `:172-187`. String = one keyword; list = present even when empty (empty XMP list suppresses IIM keywords); entries trimmed, non-strings and blanks dropped, exact duplicates dropped in first-seen order, case preserved, never split on `,` or `;`. Absent -> invalid (NULL).
- `created_date` = `XMP-photoshop:DateCreated`, else `IPTC:DateCreated` [+ ` ` + `IPTC:TimeCreated`] `:24-45`; 16 layouts `:68-85`.
- Non-localized only: tests assert `XMP-dc:Title-fr` is ignored (`iptc_metadata_test.go:43`).

XMP (`metadata_xmp.go`): `populateFileXMPFromRaw :78` sets `XmpRaw.RawXml` to the packet verbatim (whitespace preserved) when non-empty, and, if both GPS strings are found by an `encoding/xml` token scan (attribute or element form, any prefix) `:18-55`, two properties: namespace `exif`, property `GPSLatitude`/`GPSLongitude`, raw strings. `applyExifGPSFromXMP :107` fills EXIF lat/long from those strings when EXIF GPS is not already valid (`SetGPSFromStrings`; parse failure logged at Debug `"XMP GPS string parse failed"` `:116`). Constants `xmpNamespaceExif` etc. `:141-145` (moved from dev `xmp_persist.go`).

### 1.3 Persistence (`internal/server/database/storage/metadata_snapshot.go`, `xmp.go`)

`persistMetadataSnapshot(ctx, q, fileID, f)` runs inside the caller's write transaction (`storage/file.go:160`; error wrapped `"replace metadata %s: %w"`). It REPLACES, not merges:

1. Validate `f.Itpc.Keywords` JSON first (must be a JSON array of strings) `:27,61-82`; error `invalid IPTC keywords JSON: ...` before any SQL.
2. `DeleteExif(file_id)`; if any EXIF field valid (12 columns `:84-89`; `software` column not written) -> `UpsertExif`.
3. `DeleteIPTCKeywordsByFile`; if any IPTC field valid or keywords non-empty `:91-95` -> `UpsertIPTC` then one `InsertIPTCKeywordForFile` row per keyword (`iptc_keywords(file_id, keyword)`, in order); else `DeleteIPTC`.
4. `persistXMP` `xmp.go:18`: `DeleteXMPRaw`, `DeleteXMPPropertiesByFile`; if `RawXml.Valid`, `UpsertXMPRaw` then `InsertXMPPropertyForFile` per property.
   Any SQL error aborts the file write (returned up, whole batch transaction rolls back per the storage writer; not in scope). Inputs are not mutated, so a rolled-back payload can be retried (`:23-25`; tests `metadata_retry_integration_test.go`).

Tables: `exif_metadata`, `iptc_metadata`, `iptc_keywords`, `xmp_raw`, `xmp_properties` (`migrations/010_init.up.sql:67-108`). Both `iptc_keywords.id` and `xmp_properties.id` are `INTEGER PRIMARY KEY` (rowid), so inserts without `id` work and no migration is needed. No schema change in v2 for these tables.

### 1.4 How it is called (owned by the processing component, listed for the metadata lifecycle)

- `files/metadata.go:21 extractFileMetadata(ctx, f, path, extractor)`: Extract, `ctx.Err()` re-check, `applyExifToolMetadata`.
- `files/processor.go:93`: called after MIME check (`image/*`) and before `image.DecodeConfig`; error -> `"failed to extract EXIF data: %w"`, file fails. The extractor receives the on-disk path (`ImagesDir` + path), not the in-memory snapshot, so ExifTool re-reads the file.
- Ownership: `fileProcessor.extractor` (`service.go:71`); `configureProcessor` creates `metadata.New()` if none given (`service.go:99`); `WithMetadataExtractor` transfers ownership; the processor's Close joins `extractor.Close()` and `ProcessDiscoveryFile` returns `metadata.ErrClosed` after close (`service.go:144`). Lifecycle is lazy: no ExifTool starts until the first Extract.
- `subsystem_manager.go:183`: `maxWorkers = memlimit.Apply(maxWorkers, metadata.PoolSize())` so worker count accounts for ExifTool pool memory.
- `files/metadata.go:15 ExtractExifData(f, *os.File)` creates and closes a whole extractor per call; v2 non-test callers: none (grep of `ExtractExifData(` finds only the definition). Dead in production.

### 1.5 Config keys, flags, log lines

- No config key, flag or env var for metadata (pool size, idle and recycle values are constants).
- Log lines: Debug `"XMP GPS string parse failed"` (`metadata_xmp.go:116`). The extractor logs nothing. Dev logs removed: `"ExtractExifData timed out - possible corrupted file"` (Warn), `"imagemeta.Decode failed"` (Warn), `"imagemeta.Decode: no exif"` (Debug), `"upsert exif"`, `"upsert xmp raw"`, `"upsert xmp property"` (Error). I found no test asserting these dev lines (inference from not greping every test).

### 1.6 What dev already did vs what is new

Dev (`internal/server/files/metadata.go`): `imagemeta` (replaced by `./internal/imagemeta` in go.mod) decoded EXIF in-process; JPEG XMP via `jpeg.ScanJPEGWithSourceContext`; 5 s `exifTimeout`; on non-NoExif decode error: Warn, best-effort XMP, return nil (file continues); EXIF row written only if `CameraMake.Valid` and failure only logged (`thumbnail.go:128-137`); XMP raw+props upserted non-fatally with synthetic property id `fileID*10+{1,2,9}` (`xmp_persist.go:20-30`); no IPTC extraction at all (`Itpc` never populated, `UpsertIPTC` never called), no `Orientation`, no delete-on-rescan.
New in v2: ExifTool extractor pool; IPTC (columns, keywords JSON, `iptc_keywords` rows); orientation; replace semantics with deletes; strict failure on extraction error; tests and fixtures.

## 2. Approved changes vs dev, and where they appear here

- ExifTool metadata and IPTC: all of 1.1-1.3.
- Removal of the JPEG marker pre-filter: `ValidateJpegMarkers`, `DetectMimeType`, `BufPool`, `HasValidJpegMarkers` leave `files/metadata.go` and the `File` type (dev `metadata.go:29-100`, `processor.go:131`, `gob.go:24`). Malformed-JPEG failure now surfaces in decode (processing component) or as an ExifTool error here.
- pHash, Wuffs: not in this component.

Unapproved changes: need a decision

1. Extraction error now fails the file. Dev swallowed non-NoExif decode errors (returned nil, kept the file) and only failed on timeout. v2 fails the file on any ExifTool error, on `ExifTool:Error`, and on startup failure (sticky: every later file in the run fails with `metadata startup`). `files_test.go:430 TestExtractExifData_DecodeError` asserts it. "Malformed JPEG fails in decode" does not clearly cover "ExifTool reports Error". I did not run ExifTool on a malformed JPEG to see whether it emits `Error` or `Warning`; unknown.
2. EXIF row rule: dev wrote only when `CameraMake.Valid`; v2 writes when any of 12 fields is valid, and deletes the row when none (rescan clears stale data). Same for IPTC and XMP raw (dev never deleted).
3. Orientation is a new populated column; IPTC `iptc_keywords` rows are new (needed by whoever reads them; I found no v2 reader of `iptc_keywords` or `xmp_properties` outside tests: only `GetIPTCByFile` through `interfaces.MetadataQueries`; confirm in the handlers component).
4. Value formats may differ from dev: dev used `imagemeta` `FocalLength.String()`, `FNumber.String()`, `ExposureTime.String()`; v2 formats `50.00mm`, `1.7`/`2`, `1/1600`. I did not verify dev output strings (the `internal/imagemeta` source is not in the checkout). `scripts/v3-parity.sh` should compare these; capture date selection also differs (dev `SelectedDate()`, v2 DateTimeOriginal then CreateDate with offsets).
5. The 5 s extraction timeout is gone; ExifTool commands cannot be interrupted, so a hung command stalls a worker and Close. Intended? (cancellation "waits for the synchronous commands", `extractor.go:86`.)
6. `go.mod`: bumps of `golang.org/x/{crypto,image,net,sync,sys,text}` and removal of `x/tools`, `klauspost/cpuid`, etc. in the same diff (go.mod lines in 3 below). Several are not metadata-related; R3 needs approval for each.

## 3. Exported API delta vs dev

Packages existing in dev:

- `internal/server/files`: removed `BufPool`, `DetectMimeType`, `ValidateJpegMarkers`; `ExtractExifData` kept, signature unchanged in type (`func(*File, *os.File) error`), body replaced; removed `File.HasValidJpegMarkers` (File becomes alias `= storagepayload.File`, other component). Added (service.go, processing component) `MetadataExtractor`, `ProcessorOption`, `WithMetadataExtractor`.
- `internal/gallerydb` (generated): added `Queries.DeleteExif`, `DeleteIPTCKeywordsByFile`, `InsertIPTCKeywordForFile`, `DeleteXMPPropertiesByFile`, `InsertXMPPropertyForFile` and their `...Params`, and `Querier` entries; `exif.sql.go`, `iptc.sql.go`, `xmp.sql.go`, `db.go` prepared statements.
- `internal/gallerylib`: `importerQueries` is unexported, but the exported `Importer` constructor takes it: removed `UpsertXMPProperty`, added `DeleteExif`, `UpsertIPTC`, `DeleteIPTC`, `DeleteIPTCKeywordsByFile`, `InsertIPTCKeywordForFile`, `DeleteXMPRaw`, `DeleteXMPPropertiesByFile`, `InsertXMPPropertyForFile`.
- `internal/server/interfaces/metadata_queries.go`: no API change; only the package doc comment moved/removed (comment-only diff, `-4` lines). DROP that edit.
- `sqlc/queries`: `embed.go` removes `CountFilesForFolderIndexRebuildSQL` (folder-index component, not mine).

New package: `internal/metadata`: `New() *Extractor`, `PoolSize() int`, `(*Extractor).Extract(ctx, path) (map[string]any, []byte, error)`, `(*Extractor).Close() error`, `ErrClosed`, type `Extractor`. (`internal/storagepayload` and `internal/server/database/storage` are other components.)

## 4. File verdicts

Production

- `internal/metadata/extractor.go`: REWRITE. Violations: R9 `extractorTestSeams` field `:38,64,239`; R8 `commandServer` interface exists for the seam `:37-40`; R19 hand-rolled semaphore (`slot chan` `:48`), once/done/closeErr (`:51-53`), janitor wait; R15 `janitorStarted` bool and `initErr` flag `:56-61`; R14 mutable package var `extractArgs :34` (slice var); R20 four `ctx.Err()`/`closing` checks in `Extract :90,106,109,112`; R10 `New()` reads `PoolSize` and the seam-bearing struct; R21 none. Must keep: arg list, abs path, path validation, lazy start, sticky startup error (confirm, see 6), 16 MiB / 1 MiB retirement, idle shutdown 30 s, pool 2..4 = NumCPU/3, one-object JSON decode, `ExifTool:Error` failure, `System:` and `Composite:LensID` stripping, base64 raw XMP, `ErrClosed`, Close waits for in-flight and returns one joined error, raw bytes not aliased to the server buffer (`extractor_test.go:368`).
- `internal/server/files/exiftool_metadata.go`: REWRITE. `sql.Null*` is the domain payload (R16); `applyExifToolMetadata` returns an error that is always nil (R12/R20 `:14,81`); `exifToolFloat` handles float32/int/int64/json.Number although `json.Unmarshal` into `any` yields only float64 (R20, speculative branches `:107-130`); `exifToolInt` has an `int64` branch that cannot occur. Keep all mappings in 1.2.
- `internal/server/files/iptc_metadata.go`: REWRITE. R16 `sql.Null*` return types; `[]string` branches `:126-130,196-201` unreachable from JSON (R20); 16-entry layout table is behavior, keep. Keep precedence and keyword rules in 1.2.
- `internal/server/files/metadata.go`: REWRITE to nothing but `extractFileMetadata` if the processor needs it; DROP `ExtractExifData` (no production caller, R5; starts a whole extractor per call, R25) and `extractFileMetadata` pass-through (R10) when the processor calls Extract + apply directly.
- `internal/server/files/metadata_xmp.go`: REWRITE small. Keep XML token scan and GPS fallback; R16 `sql.Null*`/`gallerydb.UpsertXMPPropertyParams` in a files-package payload; the `xmpGPSStringsFromProps` round trip (props -> strings) exists only to reuse the property slice and could use the strings directly (R17/R20, SHOULD).
- `internal/server/files/xmp_persist.go` (deleted in v2): DELETE confirmed correct. v3 deletes it and its test; constants move into the XMP file.
- `internal/server/database/storage/metadata_snapshot.go`: REWRITE. R6 uses the `...ByFile`/`...ForFile` duplicates; R8 `metadataSnapshotQueries` / `xmpPersistenceQueries` interfaces exist for a fake (R26); keywords JSON round trip (encode in files, decode here at `:61-82`) means a `[]string` should be carried instead of a JSON string in the payload (needs the payload component to agree); "valid" checks `hasMappedEXIF` / `hasIPTCMetadata` are behavior, keep. Keep ordering and error wrapping in 1.3.
- `internal/server/database/storage/xmp.go`: REWRITE (merge into the one metadata persist function; R8 interface, R6 queries).
- `internal/server/interfaces/metadata_queries.go`: DROP the v2 edit (comment only; also R23/not in scope). File otherwise COPY as in dev.
- `sqlc/queries/exif.sql`: COPY the `DeleteExif` addition (new job, no existing equivalent; verified `exif.sql` has no delete in dev).
- `sqlc/queries/iptc.sql`, `xmp.sql`: REWRITE per section 5.
- `internal/gallerydb/{exif,iptc,xmp}.sql.go`, `db.go`, `querier.go`: regenerate with sqlc after the `.sql` edits; do not hand edit.
- `go.mod`/`go.sum`: needs approval, see 6.

Tests

- Worth keeping (behavior): `files/exiftool_metadata_test.go` (EXIF mapping, GPS fallback), `files/iptc_metadata_test.go` (precedence, keywords, dates), `files/metadata_xmp_test.go` (+ `testdata/lightroom_sidecar.xmp`, `imagemeta-LICENSE`, README; the fixture came from the removed imagemeta repo; keep with provenance), `metadata/extractor_integration_test.go:TestV2METARealExtractor` (real ExifTool), `storage/metadata_snapshot_integration_test.go`, `storage/metadata_retry_integration_test.go`, `files/metadata_integration_test.go` (real storage snapshots, cutover), `metadata/extractor_test.go` cases that assert observable behavior: parser/errors, XMP field, raw not aliasing, pool concurrency and Close, recycle thresholds, idle reaping.
- Seam-only or evidence (R26/R21/R27): `extractor_integration_test.go:TestV2METATimingEvidence` (evidence, DROP, with `observedMetadataServer` and `v2MetaTiming` flag); `repair_integration_test.go` (repair evidence for symlinked operands, DROP unless the user wants the behavior; I did not determine what "original symlink operands" protects, ask); `storage/metadata_snapshot_test.go` (`metadataQueryFake` call-sequence fake, `TestV2METAMetadataSQLFailures`: DROP; keep `InvalidKeywordsBeforeWrites`, `MetadataValues`, `EmptyKeywordsAndZeroValues` against a real DB); `storage/xmp_test.go` (fake spy, DROP; real-DB equivalent exists); `files/metadata_test_helpers_test.go` and `files/files_test.go` extractor fixtures (exist to feed `WithMetadataExtractor`, seam); `TestExtractExifData_CooperativeCancellation` tests a fixture, not the extractor. Idle tests in `extractor_test.go` use a clock/ticker with sleeps (R27); redo with `testing/synctest`.
- Test names carry `V2META` (R23: no "v2" references).

## 5. Boundary notes and the R6 query question

### Package boundaries (`go list` on v2)

- `internal/metadata` imports only `go-exiftool-wasm` (no sfpg-go package). Imported by `internal/server` (`PoolSize`), `internal/server/files`. It is a real owner (ExifTool process lifecycle). Keep it; it is new, so R1 needs approval. It does not exist only to break a cycle. `PoolSize` used by the server to size workers is a cross-concern export; acceptable (SHOULD: have `memlimit.Apply` take the number from the caller of `metadata.New`, to avoid the server importing `metadata`).
- Tag-to-column mapping lives in `server/files` (imports `gallerydb`, `storagepayload`, `metadata`, `coords`, `storage`...). R17/R16 pressure: the mapping is metadata knowledge. Proposal: put `applyExifToolMetadata`, IPTC and XMP mapping in `internal/metadata` as `Extract` returning a domain struct (`metadata.Info` with plain Go types: strings, `*float64`-free, `[]string` keywords, `time.Time`) so `metadata` depends on no DB type, and `storage` converts to `gallerydb` params. `depguard` already forbids `gallerydb`/`database/sql` in `metadata` (R16). This removes the `sql.Null*` leakage into `files`. Needs approval (exported API of a new package). `setGPSFromExif`/`SetGPSFromStrings` (`files/gps.go`, imports `coords`) would move with it or be called via `coords`.
- `server/database/storage` imports `gallerydb`, `gallerylib`, `storagepayload`, ... and owns persistence (correct owner). Persist function should take the metadata domain struct. `storagepayload` exists in v2 to share `File` between `files` and `storage` (cycle-breaker, R11); owner is the payload component, flag it.
- `internal/server/interfaces`: imports `dbconnpool`, `gallerydb`, `config`, `session`; imported by `server`, `server/handlers`, `server/files`?? (list shows `cachepreload`, `server`, `handlers`, `files` import `interfaces`? The `go list` output shows `internal/metadata`, `server`, `cachepreload`, `files`, `handlers`, `interfaces` matching two patterns; the exact reverse edges per package were not separated. Treat as unknown.) `MetadataQueries` interface has one consumer (`InfoBoxImage`); it returns `gallerydb` row types (R16 for new code; dev code, not touched).

### R6: queries v3 needs

Production call sites in v2: only `persistMetadataSnapshot` and `persistXMP`. The by-id queries `InsertIPTCKeyword`, `DeleteIPTCKeyword`, `UpsertXMPProperty`, `DeleteXMPProperty` have no production caller in v2 (verified by grep: only `gallerydb_db_test.go` and generated code), and in dev the only production caller was `upsertFileXMP` (`UpsertXMPProperty`), which v2 replaces. In dev `InsertIPTCKeyword` and `DeleteIPTCKeyword` had no production caller at all.

v3 needs exactly these (replace semantics, by file):

| Job                             | Query                                                                                      | Change in place                                                                                                                                                                                                                                      |
| ------------------------------- | ------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| delete EXIF                     | `DeleteExif :exec` (`WHERE file_id = ?`)                                                   | new, no equivalent                                                                                                                                                                                                                                   |
| upsert EXIF                     | `UpsertExif`                                                                               | unchanged                                                                                                                                                                                                                                            |
| delete IPTC row                 | `DeleteIPTC`                                                                               | unchanged                                                                                                                                                                                                                                            |
| upsert IPTC row                 | `UpsertIPTC`                                                                               | unchanged                                                                                                                                                                                                                                            |
| delete keywords of a file       | `DeleteIPTCKeyword`                                                                        | change body to `DELETE FROM iptc_keywords WHERE file_id = ?`; rename to `DeleteIPTCKeywords` (plural, by file)                                                                                                                                       |
| insert a keyword                | `InsertIPTCKeyword`                                                                        | change to `INSERT INTO iptc_keywords (file_id, keyword) VALUES (?, ?)` (drop `id`); name kept; params become `{FileID, Keyword}`                                                                                                                     |
| delete XMP raw                  | `DeleteXMPRaw`                                                                             | unchanged                                                                                                                                                                                                                                            |
| upsert XMP raw                  | `UpsertXMPRaw`                                                                             | unchanged                                                                                                                                                                                                                                            |
| delete XMP properties of a file | `DeleteXMPProperty`                                                                        | change to `WHERE file_id = ?`; rename `DeleteXMPProperties`                                                                                                                                                                                          |
| insert an XMP property          | `UpsertXMPProperty`                                                                        | change to plain `INSERT INTO xmp_properties (file_id, namespace, property, value) VALUES (?, ?, ?, ?)`; rename `InsertXMPProperty` (it is no longer an upsert because rows are replaced by delete+insert and the synthetic `fileID*10+n` id is gone) |
| read                            | `GetIPTCKeywords`, `GetXMPPropertiesByFile`, `GetIPTCByFile`, `GetExifByFile`, `GetXMPRaw` | unchanged                                                                                                                                                                                                                                            |

Drop all four `...ByFile`/`...ForFile` v2 additions. Renames and param changes change exported `gallerydb` API (R2, approval). If the user refuses renames: keep names `DeleteIPTCKeyword` and `DeleteXMPProperty` with a changed WHERE (the signature `(ctx, int64)` is unchanged, so only the meaning changes; misleading names, I recommend against) and `UpsertXMPProperty` as an INSERT (a lie; recommend against). Alternative that needs no rename: keep all four names unchanged and make `iptc_keywords` / `xmp_properties` writes true upserts by file via UNIQUE(file_id, keyword) etc. (needs a migration; not recommended).
Also gallerylib `importerQueries` shrinks to the same set. Rows written by dev for XMP properties have synthetic ids; delete-by-file removes them fine.

### Extractor shape proposal (rewrite guidance, not a requirement)

Replace `slot`, `idle`, `closing`, `done`, `closeOnce`, `janitorStarted`, janitor goroutine with: `sem *semaphore.Weighted` (x/sync; already a dependency, R7/R19) for admission, `mu` plus an `idle []*server` slice, per-server `time.AfterFunc(30s)` that removes and shuts down that server (exact 30 s, no janitor goroutine, stoppable at Close), `closed bool` under `mu`. Close: set closed, stop timers, `sem.Acquire(ctx, size)` to join in-flight, shut down idle servers. Whether `AfterFunc` goroutines count as owned (R18): the timer is stopped and the callback is bounded by `mu`; state this in the code comment.

## 6. Open questions

1. Is a sticky startup error right? v2 makes one failure poison the run forever (`initErr`). Recommend: do not cache; return the error for that call and retry on the next (a transient failure no longer fails every later file). Behavior change vs v2, say if you want v2 parity.
2. Extraction error fails the file (section 2, item 1). Recommend: keep v2 parity for `ExifTool:Error`, but check on the corpus whether truncated/malformed JPEGs produce `Error`; if ExifTool only warns, the file proceeds to decode and fails there, which is the approved behavior.
3. Move tag mapping into `internal/metadata` with a plain-type result (section 5)? Recommend yes: removes `sql.Null*` from `files` and puts ExifTool knowledge in one package. Needs R1/R2 approval since `files` exports change.
4. Approve the in-place query changes and renames (section 5)? Recommend yes (`DeleteIPTCKeywords`, `InsertIPTCKeyword` without id, `DeleteXMPProperties`, `InsertXMPProperty`, plus new `DeleteExif`).
5. Do you want `iptc_keywords` and `xmp_properties` still written? I found no v2 reader outside tests; `iptc_metadata.keywords` (JSON) holds the same keywords. Recommend: keep, since dev schema and v2 tests treat them as stored data and dropping rows is a visible behavior change; confirm with the handlers/search component whether any reader exists.
6. Testing the idle and retire logic without a seam (R9). Real ExifTool starts a WASM interpreter (cost not measured by me). Options: integration tests with real servers under `testing/synctest` (I do not know if wasm2go goroutines run inside a synctest bubble), or an unexported `newServer` field. Recommend: try synctest with the real server; if it fails, ask for a documented exception rather than ship `testSeams`.
7. `repair_integration_test.go` (symlink operand repair): what behavior is it protecting? Not determined; recommend DROP unless a production symlink handling exists in `Extract` (it only calls `filepath.Abs`).
8. `go.mod`: approve adding `lbe/go-exiftool-wasm v0.6.0` (+ indirect `lbe/cfsread`, `lbe/wasm2go-wasi-host`, `pierrec/lz4/v4`) and removing `evanoberholster/imagemeta` and its `replace`, `philhofer/fwd`, `tinylib/msgp`, `pkg/errors`? Recommend yes for the exiftool/imagemeta part only. The `golang.org/x` bumps and `x/tools`, `klauspost/cpuid` removal in the v2 go.mod diff should be taken only as `go mod tidy` consequences (not a separate bump), and need approval as they appear. I did not run `go mod tidy` to verify the indirect set.
9. Delete `internal/imagemeta` submodule directory and `testdata/imagemeta-LICENSE`? The fixture is MIT with a license file; recommend keep fixture and license (it is copied text), delete the dependency.
