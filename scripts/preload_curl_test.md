# Preload / Gallery curl and SQL test reference

Use this to generate curl commands and SQL expectations for testing gallery and cache preload. Substitute `FOLDER_ID` (e.g. `1` for gallery/1) and your DB path.

---

## 1. Folder entries expected for a gallery (subfolders)

For **gallery/1** the app uses `GetFoldersViewsByParentIDOrderByName` with `parent_id = 1`. Equivalent SQL:

```sql
-- Replace 1 with the gallery folder id (e.g. 1 for gallery/1)
SELECT id, parent_id, path, name, mtime, created_at, updated_at
  FROM folder_view
 WHERE parent_id = 1
 ORDER BY name;
```

**Run with sqlite3** (DB path is typically `{rootDir}/sfpg.db`, e.g. `./tmp/sfpg.db` or from env):

```bash
sqlite3 -header -column /path/to/sfpg.db \
  "SELECT id, parent_id, path, name, mtime, created_at, updated_at FROM folder_view WHERE parent_id = 1 ORDER BY name;"
```

Expected: one row per direct subfolder of folder 1; columns match `folder_view` (id, parent_id, path, name, mtime, created_at, updated_at).

---

## 2. File entries expected for a gallery (images in folder)

For **gallery/1** the app uses `GetFileViewsByFolderIDOrderByFileName` with `folder_id = 1`. Equivalent SQL:

```sql
-- Replace 1 with the gallery folder id (e.g. 1 for gallery/1)
SELECT id, folder_id, folder_path, path, filename, size_bytes, mtime, md5, phash, mime_type, width, height, created_at, updated_at
  FROM file_view
 WHERE folder_id = 1
 ORDER BY filename;
```

**Run with sqlite3**:

```bash
sqlite3 -header -column /path/to/sfpg.db \
  "SELECT id, folder_id, folder_path, path, filename, size_bytes, mtime, md5, phash, mime_type, width, height, created_at, updated_at FROM file_view WHERE folder_id = 1 ORDER BY filename;"
```

Expected: one row per file (image) in folder 1; columns match `file_view`; ordered by filename.

---

## 3. Curl command to retrieve gallery/1

**Cache key format (v3):** `METHOD:/path?query|Variant=<variant>`

| Variant           | When                                                               |
| ----------------- | ------------------------------------------------------------------ |
| `full`            | Gallery full page (non-partial)                                    |
| `gallery-content` | HTMX gallery folder-tile partial                                   |
| `box_info`        | `/info/folder/`, `/info/image/` (any request style)                |
| `lightbox-ui`     | `/lightbox/` (all HX-Target values collapse via NormalizedVariant) |

**Full-page (no HTMX)** — same as browser loading `/gallery/1`:

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  "http://localhost:8081/gallery/1?v=20260202-04"
```

Cache key: `GET:/gallery/1?v=20260202-04|Variant=full`

Optional: use the app's current ETag version for `v=` (check response header `Etag` or app config). If omitted, the server may redirect or serve; the `v` query matches cache keys.

**HTMX partial (folder tile)** — same as clicking a folder tile:

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "HX-Request: true" \
  -H "HX-Target: gallery-content" \
  -H "Accept-Encoding: gzip" \
  "http://localhost:8081/gallery/1?v=20260202-04"
```

Cache key: `GET:/gallery/1?v=20260202-04|Variant=gallery-content`

**Expected (both)**:

- `HTTP_CODE:200`
- Body: HTML (full page has `<html>` and layout; partial is a fragment for `#gallery-content`).
- Headers: `X-Cache: HIT` or `MISS`; full-page may have `Cache-Control: public, max-age=2592000`; partial has `Cache-Control: no-store`.

---

## 5. Authentication (Optional)

While gallery routes are currently public, administrative routes (like `/config`) and pprof endpoints require authentication. If you protect gallery routes in the future, use these steps:

1. **Login and save session cookie**:

   ```bash
   curl -c /tmp/sfpg_cookies.txt -d "username=admin&password=admin" http://localhost:8081/login
   ```

2. **Use the cookie in subsequent requests**:
   ```bash
   curl -b /tmp/sfpg_cookies.txt -s -w "\nHTTP_CODE:%{http_code}\n" \
     "http://localhost:8081/gallery/1?v=20260202-04"
   ```

---

## 6. Minimal curl set for preload/cache testing

Use these against a running server (e.g. `localhost:8081`) and compare with SQL expectations and response headers.

Cache keys use v3 format: `METHOD:/path?query|Variant=<name>`.

```bash
# Full-page gallery/1 → |Variant=full
curl -s -o /tmp/gallery1_full.html -w "HTTP_CODE:%{http_code}\n" \
  "http://localhost:8081/gallery/1?v=20260202-04"

# HTMX partial gallery/1 → |Variant=gallery-content
curl -s -o /tmp/gallery1_partial.html -w "HTTP_CODE:%{http_code}\n" \
  -H "HX-Request: true" \
  -H "HX-Target: gallery-content" \
  -H "Accept-Encoding: gzip" \
  "http://localhost:8081/gallery/1?v=20260202-04"

# Info folder (e.g. folder 9) → |Variant=box_info
curl -s -o /tmp/info_folder_9.html -w "HTTP_CODE:%{http_code}\n" \
  -H "HX-Request: true" \
  -H "HX-Target: box_info" \
  -H "Accept-Encoding: gzip" \
  "http://localhost:8081/info/folder/9?v=20260202-04"

# Lightbox (e.g. image 12) → |Variant=lightbox-ui
curl -s -o /tmp/lightbox_12.html -w "HTTP_CODE:%{http_code}\n" \
  -H "HX-Request: true" \
  -H "HX-Target: lightbox_content" \
  -H "Accept-Encoding: gzip" \
  "http://localhost:8081/lightbox/12?v=20260202-04"
```

Expected for each: `HTTP_CODE:200`. Cache keys: gallery full → `|Variant=full`; gallery HTMX → `|Variant=gallery-content`; info → `|Variant=box_info`; lightbox → `|Variant=lightbox-ui`. After preload, repeat the same curl and expect `X-Cache: HIT` in the response headers (check with `-D -` or `-v` and inspect headers).

For a full-gallery sweep with SQL summary, use `scripts/verify_http_cache_keys.sh` (requires running server + `SFPG_DB` pointing at `sfpg.db`).
