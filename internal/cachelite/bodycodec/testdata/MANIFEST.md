# bodycodec testdata manifest

Gallery HTML fixtures are stored in git as `galleries.tar.gz`. Tests extract them
lazily via `internal/cachelite/bodycodec/fixtures` on first use. Regenerate with
`scripts/extract_bodycodec_testdata.sh` (requires a local gallery DB).

| file | id | key | path | content_length | stored_length |
| ---- | -- | --- | ---- | -------------- | ------------- |
| gallery_small_1.html | 2 | GET:/gallery/2?v=20260718-03/Variant=gallery-content | /gallery/2 | 3175 | 3175 |
| gallery_small_2.html | 3161 | GET:/gallery/1142?v=20260718-03/Variant=gallery-content | /gallery/1142 | 3259 | 3259 |
| gallery_small_3.html | 6662 | GET:/gallery/13?v=20260718-03/Variant=gallery-content | /gallery/13 | 3339 | 3339 |
| gallery_med_1.html | 15470 | GET:/gallery/20693?v=20260718-03/Variant=gallery-content | /gallery/20693 | 153581 | 153581 |
| gallery_med_2.html | 12231 | GET:/gallery/18356?v=20260718-03/Variant=full | /gallery/18356 | 153628 | 153628 |
| gallery_med_3.html | 12050 | GET:/gallery/1826?v=20260718-03/Variant=full | /gallery/1826 | 153517 | 153517 |
| gallery_large_1.html | 7943 | GET:/gallery/1355?v=20260718-03/Variant=full | /gallery/1355 | 10302955 | 10302955 |
| gallery_large_2.html | 14335 | GET:/gallery/20172?v=20260718-03/Variant=full | /gallery/20172 | 10256653 | 10256653 |
| gallery_large_3.html | 7947 | GET:/gallery/1355?v=20260718-03/Variant=gallery-content | /gallery/1355 | 10248809 | 10248809 |
