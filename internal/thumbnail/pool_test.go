package thumbnail

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
	"testing/synctest"

	wuffs "github.com/lbe/wuffs-wasm"
)

func newPool(t *testing.T) *Pool {
	t.Helper()
	p := NewPool()
	t.Cleanup(p.Close)
	return p
}

// gradient returns a w×h opaque image with a gradient, so that thumbnails and
// hashes depend on its content.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func encoded(t *testing.T, encode func(*bytes.Buffer) error) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := encode(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// generate reads data with a Generator from p and returns its results.
func generate(t *testing.T, p *Pool, data []byte, w, h int) (thumb image.Image, md5Hex string, phash int64, err error) {
	t.Helper()
	g := p.Get()
	defer p.Put(g)
	if _, err = g.Read(t.Context(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	buf, md5Hex, phash, err := g.GenerateThumbnailAndHashes(t.Context(), w, h)
	if err != nil {
		return nil, "", 0, err
	}
	defer PutBytesBuffer(buf)
	if thumb, err = jpeg.Decode(buf); err != nil {
		t.Fatalf("thumbnail is not a JPEG: %v", err)
	}
	return thumb, md5Hex, phash, nil
}

func TestGenerateThumbnailAndHashes(t *testing.T) {
	src := gradient(400, 300)
	tests := []struct {
		name       string
		data       []byte
		wantW      int
		wantH      int
		pixelsFrom func([]byte) (image.Image, error)
	}{
		{
			name:  "JPEG",
			data:  encoded(t, func(b *bytes.Buffer) error { return jpeg.Encode(b, src, nil) }),
			wantW: 200, wantH: 150,
			pixelsFrom: func(d []byte) (image.Image, error) { return decodeImage(d, true, 400, 300) },
		},
		{
			name:  "PNG through Wuffs",
			data:  encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, src) }),
			wantW: 200, wantH: 150,
			pixelsFrom: func(d []byte) (image.Image, error) { return png.Decode(bytes.NewReader(d)) },
		},
		{
			name:  "GIF through Wuffs",
			data:  encoded(t, func(b *bytes.Buffer) error { return gif.Encode(b, src, nil) }),
			wantW: 200, wantH: 150,
			pixelsFrom: func(d []byte) (image.Image, error) { return gif.Decode(bytes.NewReader(d)) },
		},
	}
	p := newPool(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thumb, md5Hex, phash, err := generate(t, p, tt.data, 400, 300)
			if err != nil {
				t.Fatal(err)
			}
			if b := thumb.Bounds(); b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Errorf("thumbnail is %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}
			sum := md5.Sum(tt.data)
			if want := hex.EncodeToString(sum[:]); md5Hex != want {
				t.Errorf("MD5 = %s, want %s", md5Hex, want)
			}
			// For an opaque image, Wuffs and the standard decoders give the
			// same pixels, so the pHash is the same.
			pixels, err := tt.pixelsFrom(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			want, err := sourcePHash64(pixels)
			if err != nil {
				t.Fatal(err)
			}
			if phash != int64(want) {
				t.Errorf("pHash = %x, want %x", uint64(phash), want)
			}
		})
	}
}

func TestGenerateThumbnailAndHashesErrors(t *testing.T) {
	p := newPool(t)
	pngData := encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, gradient(40, 30)) })
	for name, data := range map[string][]byte{
		"truncated PNG": pngData[:len(pngData)/2],
		"not an image":  []byte("plain text"),
	} {
		if _, _, _, err := generate(t, p, data, 40, 30); err == nil {
			t.Errorf("%s: no error", name)
		}
	}

	g := p.Get()
	defer p.Put(g)
	if _, _, _, err := g.GenerateThumbnailAndHashes(t.Context(), 1, 1); err == nil {
		t.Error("GenerateThumbnailAndHashes before Read: no error")
	}
}

func TestPoolReusesGeneratorsAndDecoder(t *testing.T) {
	p := newPool(t)
	data := encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, gradient(40, 30)) })
	if _, _, _, err := generate(t, p, data, 40, 30); err != nil {
		t.Fatal(err)
	}
	g := p.Get()
	if g.decoder == nil {
		t.Error("the PNG decoder was not kept for the next file")
	}
	if p.retained.Load() == 0 {
		t.Error("no retained memory is charged after a file")
	}
	p.Put(g)

	// A failed decode drops the decoder when the file ends.
	if _, _, _, err := generate(t, p, data[:len(data)/2], 40, 30); err == nil {
		t.Fatal("truncated PNG: no error")
	}
	g = p.Get()
	if g.decoder != nil {
		t.Error("the decoder was kept after a failed decode")
	}
	p.Put(g)

	p.Close()
	if n := p.retained.Load(); n != 0 {
		t.Errorf("after Close, %d bytes still charged", n)
	}
}

func TestReadInflightBudget(t *testing.T) {
	p := newPool(t)
	g := p.Get()
	// A walk size above the budget charges all of it, so the file reads alone.
	if _, err := g.Read(t.Context(), bytes.NewReader([]byte("x")), inflightBytes+1); err != nil {
		t.Fatal(err)
	}
	if p.inflight.TryAcquire(1) {
		t.Fatal("budget left while a file larger than it is read")
	}

	p.Put(g)
	if !p.inflight.TryAcquire(inflightBytes) {
		t.Error("the budget was not given back at Put")
	}
	p.inflight.Release(inflightBytes)
}

func TestReadOversizedSlot(t *testing.T) {
	p := newPool(t)
	big := make([]byte, sourceRetainBytes+1)
	g := p.Get()
	if _, err := g.Read(t.Context(), bytes.NewReader(big), int64(len(big))); err != nil {
		t.Fatal(err)
	}
	if !g.oversized || p.oversized.TryAcquire(1) {
		t.Fatal("a source larger than the retained size does not hold the oversized slot")
	}
	if cap(g.source) > sourceRetainBytes {
		t.Errorf("kept a %d-byte source buffer, want at most %d", cap(g.source), sourceRetainBytes)
	}

	p.Put(g)
	if !p.oversized.TryAcquire(1) {
		t.Error("the oversized slot was not given back at Put")
	}
	p.oversized.Release(1)
}

func TestRetainBudget(t *testing.T) {
	p := newPool(t)
	if !p.retain(RetainedBytes) {
		t.Fatal("the whole budget does not fit an empty pool")
	}
	if p.retain(1) {
		t.Error("retained past the budget")
	}
	// A file still works when the budget is spent; its memory is not kept.
	data := encoded(t, func(b *bytes.Buffer) error { return jpeg.Encode(b, gradient(40, 30), nil) })
	if _, _, _, err := generate(t, p, data, 40, 30); err != nil {
		t.Fatal(err)
	}
	if n := p.retained.Load(); n != RetainedBytes {
		t.Errorf("%d bytes charged, want %d", n, RetainedBytes)
	}
	p.retained.Add(-RetainedBytes)
}

// TestGenerateMatchesV2 pins the results for real files to the values
// refactorv2/image-stack produced for the same files.
func TestGenerateMatchesV2(t *testing.T) {
	tests := []struct {
		path     string
		w, h     int
		md5      string
		phash    int64
		thumbLen int
	}{
		{"../../testdata/Metadata_test_file_-_includes_data_in_IIM,_XMP,_and_Exif.jpg", 650, 488,
			"2818cd0b3832dc98974f9e4dcdc4a382", 2005852506668665937, 5611},
		// The generated fixtures are flat images, whose pHash has every bit set.
		{"../../testdata/thumbnail/no-exif-thumb.jpg", 800, 600, "2b93cb1584ef241288a719fe3f17d994", -1, 0},
		{"../../testdata/thumbnail/exif-thumb.jpg", 800, 600, "7c61368264b52fa250833374de7abf81", -1, 0},
	}
	p := newPool(t)
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			data, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			g := p.Get()
			defer p.Put(g)
			if _, err = g.Read(t.Context(), bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatal(err)
			}
			thumb, md5Hex, phash, err := g.GenerateThumbnailAndHashes(t.Context(), tt.w, tt.h)
			if err != nil {
				t.Fatal(err)
			}
			defer PutBytesBuffer(thumb)
			if md5Hex != tt.md5 || phash != tt.phash {
				t.Errorf("MD5, pHash = %s, %d; want %s, %d", md5Hex, phash, tt.md5, tt.phash)
			}
			if tt.thumbLen != 0 && thumb.Len() != tt.thumbLen {
				t.Errorf("thumbnail is %d bytes, want %d", thumb.Len(), tt.thumbLen)
			}
		})
	}

	// A JPEG whose APP1 segment is cut short fails to decode.
	data, err := os.ReadFile("../../testdata/thumbnail/truncated-app1.jpg")
	if err != nil {
		t.Fatal(err)
	}
	g := p.Get()
	defer p.Put(g)
	if _, err = g.Read(t.Context(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = g.GenerateThumbnailAndHashes(t.Context(), 800, 600); err == nil {
		t.Error("truncated-app1.jpg: no error")
	}
}

// readInto reads data with g.
func readInto(t *testing.T, g *Generator, data []byte) {
	t.Helper()
	if _, err := g.Read(t.Context(), bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
}

// samePixels reports the first pixel where got and want differ, comparing
// premultiplied 8-bit RGBA over want's bounds.
func samePixels(t *testing.T, got, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), want.Bounds())
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			g, w := rgba8(got.At(x, y)), rgba8(want.At(x, y))
			if g != w {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, g, w)
			}
		}
	}
}

// rgba8 returns c as premultiplied 8-bit RGBA.
func rgba8(c color.Color) [4]uint32 {
	r, g, b, a := c.RGBA()
	return [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
}

func TestWuffsTranslucentPNG(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			src.SetNRGBA(x, y, color.NRGBA{uint8(x * 8), uint8(y * 10), 200, uint8(x * 255 / 31)})
		}
	}
	data := encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, src) })
	p := newPool(t)
	g := p.Get()
	defer p.Put(g)
	readInto(t, g, data)
	got, err := g.decodeWuffs(t.Context(), wuffs.FormatPNG)
	if err != nil {
		t.Fatal(err)
	}
	samePixels(t, got, src)
}

func TestWuffsGIFFirstFrameAtItsBounds(t *testing.T) {
	pal := color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	first := image.NewPaletted(image.Rect(5, 4, 25, 20), pal)
	for y := 4; y < 20; y++ {
		for x := 5; x < 25; x++ {
			first.SetColorIndex(x, y, uint8(1+(x+y)%2))
		}
	}
	second := image.NewPaletted(image.Rect(0, 0, 40, 30), pal)
	data := encoded(t, func(b *bytes.Buffer) error {
		return gif.EncodeAll(b, &gif.GIF{
			Image:  []*image.Paletted{first, second},
			Delay:  []int{10, 10},
			Config: image.Config{ColorModel: pal, Width: 40, Height: 30},
		})
	})
	want, err := gif.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	p := newPool(t)
	g := p.Get()
	defer p.Put(g)
	readInto(t, g, data)
	got, err := g.decodeWuffs(t.Context(), wuffs.FormatGIF)
	if err != nil {
		t.Fatal(err)
	}
	samePixels(t, got, want)
}

// TestReadWaitsOnBudgets checks that a Read blocked on the in-flight budget
// or the oversized slot waits until it is canceled.
func TestReadWaitsOnBudgets(t *testing.T) {
	big := make([]byte, sourceRetainBytes+1)
	for name, tt := range map[string]struct {
		holderData []byte
		holderSize int64
		data       []byte
	}{
		"in flight": {[]byte("x"), inflightBytes + 1, []byte("x")},
		"oversized": {big, int64(len(big)), big},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := NewPool()
				defer p.Close()
				holder := p.Get()
				defer p.Put(holder)
				if _, err := holder.Read(t.Context(), bytes.NewReader(tt.holderData), tt.holderSize); err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithCancel(t.Context())
				other := p.Get()
				defer p.Put(other)
				done := make(chan error, 1)
				go func() {
					_, err := other.Read(ctx, bytes.NewReader(tt.data), int64(len(tt.data)))
					done <- err
				}()
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("Read returned %v while the budget was held", err)
				default:
				}
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Errorf("Read = %v, want %v", err, context.Canceled)
				}
			})
		})
	}
}

// generateOn decodes a 40×30 PNG with g, so g makes its decoder.
func generateOn(t *testing.T, g *Generator) {
	t.Helper()
	readInto(t, g, encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, gradient(40, 30)) }))
	if _, _, _, err := g.GenerateThumbnailAndHashes(t.Context(), 40, 30); err != nil {
		t.Fatal(err)
	}
	if g.decoder == nil {
		t.Fatal("no decoder after a PNG")
	}
}

// A progressive JPEG whose coefficients exceed the retained decode memory
// takes the oversized slot before it is decoded. It leaves the Wuffs decoder
// for the Generator's next PNG or GIF, even when its decode fails.
func TestDecoderKeptAfterLargeJPEG(t *testing.T) {
	p := newPool(t)
	g := p.Get()
	generateOn(t, g)
	decoder := g.decoder
	readInto(t, g, append(jpeg420Header(0xc2, 12000, 9000), jpegEOI()...))
	if _, _, _, err := g.GenerateThumbnailAndHashes(t.Context(), 12000, 9000); err == nil {
		t.Fatal("a header-only JPEG decoded")
	}
	if !g.oversized {
		t.Fatal("the oversized slot was not taken")
	}
	p.Put(g)

	g = p.Get()
	defer p.Put(g)
	if g.decoder != decoder {
		t.Errorf("decoder = %p after a large JPEG, want the PNG's decoder %p", g.decoder, decoder)
	}
}

// A source larger than the retained size keeps the decoder for the
// Generator's next file.
func TestDecoderKeptAfterOversizedSource(t *testing.T) {
	p := newPool(t)
	g := p.Get()
	generateOn(t, g)
	decoder := g.decoder
	readInto(t, g, make([]byte, sourceRetainBytes+1))
	if g.decoder != decoder {
		t.Fatalf("decoder = %p before the oversized file ends, want the PNG's decoder %p", g.decoder, decoder)
	}
	p.Put(g)

	g = p.Get()
	defer p.Put(g)
	if g.decoder != decoder {
		t.Errorf("decoder = %p after an oversized source, want the PNG's decoder %p", g.decoder, decoder)
	}
}

// A PNG whose decode exceeds the retained decode memory takes the oversized
// slot, and the decoder is kept for the Generator's next file.
func TestDecoderKeptAfterOversizedDecode(t *testing.T) {
	p := newPool(t)
	g := p.Get()
	generateOn(t, g)
	decoder := g.decoder
	readInto(t, g, encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, gradient(2100, 2100)) }))
	if _, _, _, err := g.GenerateThumbnailAndHashes(t.Context(), 2100, 2100); err != nil {
		t.Fatal(err)
	}
	if !g.oversized {
		t.Fatal("the oversized slot was not taken")
	}
	p.Put(g)

	g = p.Get()
	defer p.Put(g)
	if g.decoder != decoder {
		t.Errorf("decoder = %p after an oversized decode, want the PNG's decoder %p", g.decoder, decoder)
	}
}

// A Wuffs decode that fails closes the decoder when the decode returns.
func TestDecoderDroppedAfterFailedDecode(t *testing.T) {
	p := newPool(t)
	g := p.Get()
	defer p.Put(g)
	generateOn(t, g)
	data := encoded(t, func(b *bytes.Buffer) error { return png.Encode(b, gradient(40, 30)) })
	readInto(t, g, data[:len(data)/2])
	if _, _, _, err := g.GenerateThumbnailAndHashes(t.Context(), 40, 30); err == nil {
		t.Fatal("truncated PNG: no error")
	}
	if g.decoder != nil {
		t.Error("the decoder was kept after a failed decode")
	}
}
