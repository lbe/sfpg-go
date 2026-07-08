package thumbnail

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanoberholster/imagemeta/imagehash"
)

func TestExtractEXIFThumbnail(t *testing.T) {
	testdata := filepath.Join("..", "..", "testdata", "thumbnail")

	cases := []struct {
		name      string
		filename  string
		wantThumb bool
		wantErr   bool
		wantSOI   bool
	}{
		{
			name:      "JPEG with EXIF thumbnail",
			filename:  "exif-thumb.jpg",
			wantThumb: true,
			wantSOI:   true,
		},
		{
			name:      "JPEG without EXIF thumbnail",
			filename:  "no-exif-thumb.jpg",
			wantThumb: false,
		},
		{
			name:      "truncated APP1 segment",
			filename:  "truncated-app1.jpg",
			wantThumb: false,
		},
		{
			name:      "WebP with EXIF prefix",
			filename:  "exif-thumb.webp",
			wantThumb: true,
			wantSOI:   true,
		},
		{
			name:      "WebP with EXIF no prefix",
			filename:  "exif-thumb-no-prefix.webp",
			wantThumb: true,
			wantSOI:   true,
		},
		{
			name:      "TIFF with IFD1 thumbnail",
			filename:  "exif-thumb.tiff",
			wantThumb: true,
			wantSOI:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(testdata, tc.filename)
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open %s: %v", tc.filename, err)
			}
			defer f.Close()

			var buf bytes.Buffer
			err = extractEXIFThumbnail(f, &buf)
			gotThumb := err == nil && buf.Len() > 0

			if gotThumb != tc.wantThumb {
				t.Fatalf("extractEXIFThumbnail: got thumb=%v, want thumb=%v (err=%v)", gotThumb, tc.wantThumb, err)
			}
			if tc.wantSOI {
				b := buf.Bytes()
				if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
					t.Fatalf("extracted thumbnail missing JPEG SOI marker")
				}
			}
		})
	}
}

func TestExtractEXIFThumbnailMalformed(t *testing.T) {
	// JPEG with APP1/Exif header but the EXIF segment contains an invalid
	// TIFF byte order, so findIFD1Thumb returns errNoThumb.
	var badJPEG bytes.Buffer
	badJPEG.Write([]byte{0xFF, 0xD8}) // SOI
	badJPEG.Write([]byte{0xFF, 0xE1}) // APP1 marker
	binary.Write(&badJPEG, binary.BigEndian, uint16(2+6+8))
	badJPEG.WriteString("Exif\x00\x00")
	badJPEG.WriteString("BADORDER") // invalid TIFF header

	r := bytes.NewReader(badJPEG.Bytes())
	var buf bytes.Buffer
	if err := extractEXIFThumbnail(r, &buf); err == nil {
		t.Fatal("expected error for malformed EXIF TIFF header")
	}
	if buf.Len() != 0 {
		t.Fatal("buffer should remain empty on failure")
	}

	// WebP with a valid RIFF/EXIF container but the EXIF data points at
	// non-JPEG bytes, so the SOI sanity check fails.
	badTIFF := makeTestTIFFWithThumb([]byte("not a jpeg"))
	var badWebP bytes.Buffer
	badWebP.WriteString("RIFF")
	badWebP.Write([]byte{0, 0, 0, 0})
	badWebP.WriteString("WEBP")
	badWebP.WriteString("EXIF")
	binary.Write(&badWebP, binary.LittleEndian, uint32(len(badTIFF)))
	badWebP.Write(badTIFF)
	data := badWebP.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	r = bytes.NewReader(data)
	buf.Reset()
	if err := extractEXIFThumbnail(r, &buf); err == nil {
		t.Fatal("expected error when extracted thumbnail is not JPEG")
	}
}

func TestFindIFD1ThumbMalformed(t *testing.T) {
	thumb := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}

	// Invalid byte order signature.
	var badOrder bytes.Buffer
	badOrder.WriteString("XX") // invalid order
	binary.Write(&badOrder, binary.LittleEndian, uint16(0x002a))
	binary.Write(&badOrder, binary.LittleEndian, uint32(8))
	binary.Write(&badOrder, binary.LittleEndian, uint16(0))
	binary.Write(&badOrder, binary.LittleEndian, uint32(14))
	r := bytes.NewReader(badOrder.Bytes())
	if _, _, err := findIFD1Thumb(r, 0); err == nil {
		t.Fatal("expected error for invalid TIFF byte order")
	}

	// IFD1 pointer is zero (no thumbnail IFD).
	var noIFD1 bytes.Buffer
	noIFD1.WriteString("II")
	binary.Write(&noIFD1, binary.LittleEndian, uint16(0x002a))
	binary.Write(&noIFD1, binary.LittleEndian, uint32(8))
	binary.Write(&noIFD1, binary.LittleEndian, uint16(0))
	binary.Write(&noIFD1, binary.LittleEndian, uint32(0))
	r = bytes.NewReader(noIFD1.Bytes())
	if _, _, err := findIFD1Thumb(r, 0); err == nil {
		t.Fatal("expected error when IFD1 pointer is zero")
	}

	// IFD1 exists but lacks the required tags.
	var missingTags bytes.Buffer
	missingTags.WriteString("II")
	binary.Write(&missingTags, binary.LittleEndian, uint16(0x002a))
	binary.Write(&missingTags, binary.LittleEndian, uint32(8))
	// IFD0: 0 entries, next IFD at 14.
	binary.Write(&missingTags, binary.LittleEndian, uint16(0))
	binary.Write(&missingTags, binary.LittleEndian, uint32(14))
	// IFD1: 1 entry (wrong tag), next IFD 0.
	binary.Write(&missingTags, binary.LittleEndian, uint16(1))
	binary.Write(&missingTags, binary.LittleEndian, uint16(0x010F)) // Make tag
	binary.Write(&missingTags, binary.LittleEndian, uint16(2))      // ASCII
	binary.Write(&missingTags, binary.LittleEndian, uint32(1))
	binary.Write(&missingTags, binary.LittleEndian, uint32(0))
	binary.Write(&missingTags, binary.LittleEndian, uint32(0))
	r = bytes.NewReader(missingTags.Bytes())
	if _, _, err := findIFD1Thumb(r, 0); err == nil {
		t.Fatal("expected error when IFD1 lacks thumbnail tags")
	}

	// Thumbnail length exceeds maxThumbSize.
	var tooBig bytes.Buffer
	makeTIFFWithThumbLen(&tooBig, thumb, maxThumbSize+1)
	r = bytes.NewReader(tooBig.Bytes())
	if _, _, err := findIFD1Thumb(r, 0); err == nil {
		t.Fatal("expected error when thumbnail length exceeds maxThumbSize")
	}
}

func TestFindJPEGExifMalformed(t *testing.T) {
	// APP1 segment with length < 2, which is impossible.
	var bad bytes.Buffer
	bad.Write([]byte{0xFF, 0xD8})       // SOI
	bad.Write([]byte{0xFF, 0xE1, 0, 1}) // segLen == 1
	r := bytes.NewReader(bad.Bytes())
	if _, err := findJPEGExif(r); err == nil {
		t.Fatal("expected error for APP1 segLen < 2")
	}
}

func TestFindIFD1ThumbTooManyEntries(t *testing.T) {
	// IFD0 declares more entries than maxIFDEntries, which should be rejected.
	var buf bytes.Buffer
	buf.WriteString("II")
	binary.Write(&buf, binary.LittleEndian, uint16(0x002a))
	binary.Write(&buf, binary.LittleEndian, uint32(8))
	binary.Write(&buf, binary.LittleEndian, uint16(maxIFDEntries+1))
	r := bytes.NewReader(buf.Bytes())
	if _, _, err := findIFD1Thumb(r, 0); err == nil {
		t.Fatal("expected error when IFD0 entry count exceeds maxIFDEntries")
	}

	// IFD0 is valid, IFD1 declares too many entries.
	var buf2 bytes.Buffer
	buf2.WriteString("II")
	binary.Write(&buf2, binary.LittleEndian, uint16(0x002a))
	binary.Write(&buf2, binary.LittleEndian, uint32(8))
	binary.Write(&buf2, binary.LittleEndian, uint16(0))
	binary.Write(&buf2, binary.LittleEndian, uint32(14))
	binary.Write(&buf2, binary.LittleEndian, uint16(maxIFDEntries+1))
	r = bytes.NewReader(buf2.Bytes())
	if _, _, err := findIFD1Thumb(r, 0); err == nil {
		t.Fatal("expected error when IFD1 entry count exceeds maxIFDEntries")
	}
}

func TestExtractEXIFThumbnailWebPNoEXIF(t *testing.T) {
	// Valid RIFF/WebP container but no EXIF chunk.
	var webp bytes.Buffer
	webp.WriteString("RIFF")
	webp.Write([]byte{0, 0, 0, 0})
	webp.WriteString("WEBP")
	webp.WriteString("VP8 ")
	binary.Write(&webp, binary.LittleEndian, uint32(0))
	data := webp.Bytes()
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	r := bytes.NewReader(data)
	var out bytes.Buffer
	if err := extractEXIFThumbnail(r, &out); err == nil {
		t.Fatal("expected error for WebP without EXIF chunk")
	}
	if out.Len() != 0 {
		t.Fatal("output buffer should be empty")
	}
}

func TestGenerateThumbnailAndHashesEmbeddedDecodeFallback(t *testing.T) {
	// Build a JPEG with a valid EXIF thumbnail pointer, but the thumbnail
	// bytes are not a valid JPEG. The function should fall back to decoding
	// the full image and still produce a 200x150 thumbnail.
	mainImg := imageFromColor(800, 600, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	mainJPEG := encodeJPEGBytes(mainImg)

	badThumb := []byte{0xFF, 0xD8, 0xFF, 0xDB, 0, 0} // starts like JPEG but truncated/invalid
	tiffBlob := makeTestTIFFWithThumb(badThumb)

	var jpeg bytes.Buffer
	jpeg.Write([]byte{0xFF, 0xD8}) // SOI
	jpeg.Write([]byte{0xFF, 0xE1}) // APP1
	segLen := 2 + 6 + len(tiffBlob)
	binary.Write(&jpeg, binary.BigEndian, uint16(segLen))
	jpeg.WriteString("Exif\x00\x00")
	jpeg.Write(tiffBlob)
	jpeg.Write(mainJPEG[2:]) // skip SOI of main JPEG

	r := bytes.NewReader(jpeg.Bytes())
	thumb, md5, phash, err := GenerateThumbnailAndHashes(r)
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	if thumb == nil || thumb.Len() == 0 {
		t.Fatal("expected non-empty thumbnail")
	}
	if !md5.Valid || md5.String == "" {
		t.Error("expected valid md5")
	}
	if !phash.Valid || phash.Int64 == 0 {
		t.Error("expected valid phash")
	}

	decoded, format, err := image.Decode(thumb)
	if err != nil {
		t.Fatalf("failed to decode thumbnail: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("expected jpeg, got %s", format)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != 200 || bounds.Dy() != 150 {
		t.Errorf("expected 200x150, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	PutBytesBuffer(thumb)
	PutNullInt64(phash)
	PutNullString(md5)
}

func TestGeneratorFuncAdapter(t *testing.T) {
	// Compile-time and runtime check that generatorFunc satisfies Generator
	// and delegates to GenerateThumbnailAndHashes.
	var g Generator = generatorFunc(GenerateThumbnailAndHashes)

	img := imageFromColor(400, 300, color.RGBA{R: 0, G: 128, B: 0, A: 255})
	data := encodeJPEGBytes(img)

	thumb, md5, phash, err := g.GenerateThumbnailAndHashes(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("generatorFunc adapter failed: %v", err)
	}
	if thumb == nil || thumb.Len() == 0 {
		t.Fatal("expected non-empty thumbnail from adapter")
	}
	if !md5.Valid || md5.String == "" {
		t.Error("expected valid md5 from adapter")
	}
	if !phash.Valid || phash.Int64 == 0 {
		t.Error("expected valid phash from adapter")
	}

	PutBytesBuffer(thumb)
	PutNullInt64(phash)
	PutNullString(md5)
}

func imageFromColor(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func encodeJPEGBytes(img image.Image) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// makeTestTIFFWithThumb returns a minimal little-endian TIFF with IFD0/IFD1
// and thumb as the embedded thumbnail data.
func makeTestTIFFWithThumb(thumb []byte) []byte {
	var buf bytes.Buffer
	makeTIFFWithThumbLen(&buf, thumb, uint32(len(thumb)))
	return buf.Bytes()
}

func makeTIFFWithThumbLen(buf *bytes.Buffer, thumb []byte, length uint32) {
	buf.WriteString("II")
	binary.Write(buf, binary.LittleEndian, uint16(0x002a))
	binary.Write(buf, binary.LittleEndian, uint32(8))

	// IFD0: 0 entries, next IFD at 14.
	binary.Write(buf, binary.LittleEndian, uint16(0))
	binary.Write(buf, binary.LittleEndian, uint32(14))

	// IFD1: 2 entries, next IFD 0.
	ifd1Start := buf.Len()
	binary.Write(buf, binary.LittleEndian, uint16(2))

	thumbOffset := ifd1Start + 2 + 2*12 + 4

	buf.Write([]byte{0x01, 0x02}) // 0x0201 JPEGInterchangeFormat
	buf.Write([]byte{0x03, 0x00}) // LONG
	binary.Write(buf, binary.LittleEndian, uint32(1))
	binary.Write(buf, binary.LittleEndian, uint32(thumbOffset))

	buf.Write([]byte{0x02, 0x02}) // 0x0202 JPEGInterchangeFormatLength
	buf.Write([]byte{0x03, 0x00}) // LONG
	binary.Write(buf, binary.LittleEndian, uint32(1))
	binary.Write(buf, binary.LittleEndian, length)

	binary.Write(buf, binary.LittleEndian, uint32(0)) // next IFD
	buf.Write(thumb)
}

// failingSeeker wraps an io.ReadSeeker and returns errSeekFail after the
// specified number of successful Seek calls.
type failingSeeker struct {
	inner       io.ReadSeeker
	allowed     int
	calls       int
	errSeekFail error
}

func (f *failingSeeker) Read(p []byte) (int, error) {
	return f.inner.Read(p)
}

func (f *failingSeeker) Seek(offset int64, whence int) (int64, error) {
	f.calls++
	if f.calls > f.allowed {
		return 0, f.errSeekFail
	}
	return f.inner.Seek(offset, whence)
}

func TestGenerateThumbnailAndHashesSeekErrors(t *testing.T) {
	img := imageFromColor(800, 600, color.RGBA{R: 0, G: 0, B: 255, A: 255})
	data := encodeJPEGBytes(img)

	// First seek is inside extractEXIFThumbnail; after that fails we fall back
	// and the second seek (to decode the full image) fails.
	fs := &failingSeeker{inner: bytes.NewReader(data), allowed: 0, errSeekFail: errors.New("seek failed")}
	if _, _, _, err := GenerateThumbnailAndHashes(fs); err == nil {
		t.Fatal("expected error when fallback seek fails")
	}

	// Allow the first two seeks (extractEXIFThumbnail + full decode reset) but
	// fail the third seek before MD5 calculation.
	fs = &failingSeeker{inner: bytes.NewReader(data), allowed: 2, errSeekFail: errors.New("seek failed")}
	if _, _, _, err := GenerateThumbnailAndHashes(fs); err == nil {
		t.Fatal("expected error when MD5 seek fails")
	}
}

func TestGenerateThumbnailAndHashes_HookedErrors(t *testing.T) {
	img := imageFromColor(400, 300, color.RGBA{R: 128, G: 64, B: 32, A: 255})
	data := encodeJPEGBytes(img)

	cases := []struct {
		name      string
		setup     func()
		cleanup   func()
		wantErr   bool
		checkFunc func(t *testing.T, thumb *bytes.Buffer, md5 *sql.NullString, phash *sql.NullInt64)
	}{
		{
			name: "jpeg_encode_fails",
			setup: func() {
				jpegEncodeHook = func(io.Writer, image.Image, *jpeg.Options) error {
					return errors.New("jpeg encode failed")
				}
			},
			cleanup: func() { jpegEncodeHook = jpeg.Encode },
			wantErr: true,
		},
		{
			name: "md5_copy_fails",
			setup: func() {
				ioCopyHook = func(io.Writer, io.Reader) (int64, error) {
					return 0, errors.New("md5 copy failed")
				}
			},
			cleanup: func() { ioCopyHook = io.Copy },
			wantErr: true,
		},
		{
			name: "phash_fails",
			setup: func() {
				newPHash64Hook = func(image.Image) (imagehash.PHash64, error) { return 0, errors.New("phash failed") }
			},
			cleanup: func() { newPHash64Hook = imagehash.NewPHash64 },
			wantErr: false,
			checkFunc: func(t *testing.T, thumb *bytes.Buffer, md5 *sql.NullString, phash *sql.NullInt64) {
				if thumb == nil || thumb.Len() == 0 {
					t.Fatal("expected non-empty thumbnail")
				}
				if !md5.Valid || md5.String == "" {
					t.Error("expected valid md5")
				}
				if !phash.Valid || phash.Int64 != 0 {
					t.Errorf("expected phash Int64 == 0, got %d", phash.Int64)
				}
				PutBytesBuffer(thumb)
				PutNullString(md5)
				PutNullInt64(phash)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			defer tc.cleanup()

			thumb, md5, phash, err := GenerateThumbnailAndHashes(bytes.NewReader(data))
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error=%v, got err=%v", tc.wantErr, err)
			}
			if tc.checkFunc != nil {
				tc.checkFunc(t, thumb, md5, phash)
			} else if thumb != nil {
				PutBytesBuffer(thumb)
			}
		})
	}
}

func TestFindIFD1Thumb_SeekErrors(t *testing.T) {
	thumb := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}
	tiff := makeTestTIFFWithThumb(thumb)

	for allowed := 0; allowed <= 3; allowed++ {
		t.Run(fmt.Sprintf("allowed_%d", allowed), func(t *testing.T) {
			fs := &failingSeeker{
				inner:       bytes.NewReader(tiff),
				allowed:     allowed,
				errSeekFail: errors.New("seek failed"),
			}
			if _, _, err := findIFD1Thumb(fs, 0); err == nil {
				t.Fatal("expected error when Seek fails")
			}
		})
	}
}
