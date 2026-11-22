package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// count is the number of generated images. Every 25th draws the same picture
// as the one before it, with its size and format chosen independently, and
// every 50th is also copied byte for byte into dupes/.
const count = 3000

type format int

const (
	jpegRGB format = iota
	jpegGray
	pngRGB
	pngAlpha
	pngGray16
	gifStill
	gifAnimated
)

// spec describes one generated image.
type spec struct {
	name    string // slash-separated path below the synthetic directory
	w, h    int
	format  format
	quality int    // JPEG quality
	picture uint64 // seed for picture
}

// plan returns the spec for image i. It depends only on i.
func plan(i int) spec {
	r := rand.New(rand.NewPCG(1, uint64(i)))
	s := spec{picture: uint64(i)}
	if i%25 == 24 {
		s.picture = uint64(i - 1)
	}

	switch p := r.IntN(100); {
	case p < 20:
		s.w, s.h = 16+r.IntN(400), 16+r.IntN(400)
	case p < 98:
		s.w, s.h = 640+r.IntN(1800), 480+r.IntN(1400)
	default:
		s.w, s.h = 3000+r.IntN(1000), 2000+r.IntN(1000)
	}
	if r.IntN(50) == 0 {
		s.w, s.h = 4000, 40+r.IntN(80)
		if r.IntN(2) == 0 {
			s.w, s.h = s.h, s.w
		}
	}

	ext := ".png"
	switch p := r.IntN(100); {
	case p < 60:
		s.format, ext = jpegRGB, ".jpg"
	case p < 70:
		s.format, ext = jpegGray, ".jpg"
	case p < 78:
		s.format = pngRGB
	case p < 84:
		s.format = pngAlpha
	case p < 88:
		s.format = pngGray16
	case p < 96:
		s.format, ext = gifStill, ".gif"
	default:
		s.format, ext = gifAnimated, ".gif"
	}
	if ext == ".png" {
		// PNGs are stored uncompressed (see encode); keep them small.
		s.w, s.h = min(s.w, 1024), min(s.h, 1024)
	}
	if ext == ".jpg" {
		s.quality = 50 + r.IntN(46)
		if r.IntN(10) == 0 {
			ext = ".jpeg"
		}
	}
	if r.IntN(20) == 0 {
		ext = strings.ToUpper(ext)
	}

	elems := make([]string, 1+r.IntN(3), 4)
	for j := range elems {
		elems[j] = fmt.Sprintf("d%d", r.IntN(8))
	}
	s.name = path.Join(append(elems, fmt.Sprintf("img%04d%s", i, ext))...)
	return s
}

// synthetic writes the generated images to dir, encoding up to workers at once.
func synthetic(dir string, workers int) error {
	work := make(chan int)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range work {
				errs[i] = generate(dir, plan(i))
			}
		})
	}
	for i := range count {
		work <- i
	}
	close(work)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	for i := 49; i < count; i += 50 {
		s := plan(i)
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(s.name)))
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, "dupes", path.Base(s.name)), b); err != nil {
			return err
		}
	}
	return nil
}

func generate(dir string, s spec) error {
	b, err := encode(s)
	if err != nil {
		return fmt.Errorf("%s: %w", s.name, err)
	}
	return writeFile(filepath.Join(dir, filepath.FromSlash(s.name)), b)
}

func encode(s spec) ([]byte, error) {
	m := picture(s.picture, s.w, s.h)
	// Store PNG data uncompressed. compress/flate chooses block types from
	// floating-point estimates, which the compiler may compute with fused
	// multiply-adds on some architectures, so compressed output can differ
	// between machines.
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	var b bytes.Buffer
	var err error
	switch s.format {
	case jpegRGB:
		err = jpeg.Encode(&b, m, &jpeg.Options{Quality: s.quality})
	case jpegGray:
		g := image.NewGray(m.Bounds())
		draw.Draw(g, g.Bounds(), m, image.Point{}, draw.Src)
		err = jpeg.Encode(&b, g, &jpeg.Options{Quality: s.quality})
	case pngRGB:
		err = enc.Encode(&b, m)
	case pngAlpha:
		err = enc.Encode(&b, fade(m))
	case pngGray16:
		g := image.NewGray16(m.Bounds())
		draw.Draw(g, g.Bounds(), m, image.Point{}, draw.Src)
		err = enc.Encode(&b, g)
	case gifStill:
		err = gif.Encode(&b, m, nil)
	case gifAnimated:
		g := &gif.GIF{}
		for k := range uint64(3) {
			if k > 0 {
				m = picture(s.picture+k<<32, s.w, s.h)
			}
			f := image.NewPaletted(m.Bounds(), palette.WebSafe)
			draw.FloydSteinberg.Draw(f, f.Bounds(), m, image.Point{})
			g.Image = append(g.Image, f)
			g.Delay = append(g.Delay, 50)
		}
		err = gif.EncodeAll(&b, g)
	}
	return b.Bytes(), err
}

// fade returns m with alpha falling from opaque at the left edge to nearly
// transparent at the right.
func fade(m *image.RGBA) *image.NRGBA {
	n := image.NewNRGBA(m.Bounds())
	draw.Draw(n, n.Bounds(), m, image.Point{}, draw.Src)
	w := m.Bounds().Dx()
	for y := range m.Bounds().Dy() {
		for x := range w {
			n.Pix[n.PixOffset(x, y)+3] = uint8(255 - 255*x/w)
		}
	}
	return n
}

// shape is a filled circle or square. Coordinates are fractions of the image
// size in 16-bit fixed point.
type shape struct {
	circle  bool
	x, y, r int64
	c       color.RGBA
}

// picture draws a two-color diagonal gradient with up to eight shapes on it.
// The layout depends only on seed, so the same seed at another size gives a
// visually similar picture. It uses integer arithmetic only: Go may fuse
// floating-point multiply-adds on some architectures, which would change
// pixels between machines.
func picture(seed uint64, w, h int) *image.RGBA {
	r := rand.New(rand.NewPCG(2, seed))
	c0, c1 := randColor(r), randColor(r)
	shapes := make([]shape, 1+r.IntN(8))
	for i := range shapes {
		shapes[i] = shape{
			circle: r.IntN(2) == 0,
			x:      r.Int64N(1 << 16),
			y:      r.Int64N(1 << 16),
			r:      3000 + r.Int64N(20000),
			c:      randColor(r),
		}
	}

	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		fy := int64(2*y+1) << 15 / int64(h)
		for x := range w {
			fx := int64(2*x+1) << 15 / int64(w)
			t := (fx + fy) / 2
			c := color.RGBA{mix(c0.R, c1.R, t), mix(c0.G, c1.G, t), mix(c0.B, c1.B, t), 255}
			for _, s := range shapes {
				dx, dy := fx-s.x, fy-s.y
				if s.circle && dx*dx+dy*dy <= s.r*s.r ||
					!s.circle && max(dx, -dx) <= s.r && max(dy, -dy) <= s.r {
					c = s.c
				}
			}
			m.SetRGBA(x, y, c)
		}
	}
	return m
}

// mix blends a and b, with t from 0 (all a) to 1<<16 (all b).
func mix(a, b uint8, t int64) uint8 {
	return uint8((int64(a)*(1<<16-t) + int64(b)*t) >> 16)
}

func randColor(r *rand.Rand) color.RGBA {
	return color.RGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 255}
}

// edge writes files that exercise discovery's handling of unusual names,
// mislabeled and damaged images, and files it must ignore.
func edge(dir string) error {
	k := uint64(1 << 40)
	var errs []error
	fixture := func(f format, w, h int) []byte {
		k++
		b, err := encode(spec{w: w, h: h, format: f, quality: 85, picture: k})
		errs = append(errs, err)
		return b
	}
	jpg := func() []byte { return fixture(jpegRGB, 320, 240) }
	noise := func(n int) []byte {
		k++
		b := make([]byte, n)
		r := rand.New(rand.NewPCG(3, k))
		for i := range b {
			b[i] = byte(r.Uint32())
		}
		return b
	}
	truncated := jpg()
	truncated = truncated[:len(truncated)/2]

	files := []struct {
		name string
		data []byte
	}{
		{"empty.jpg", nil},
		{"truncated.jpg", truncated},
		{"soi-only.jpg", []byte{0xFF, 0xD8, 0xFF}},
		{"trailing-garbage.jpg", append(jpg(), noise(1024)...)},
		{"random-bytes.gif", noise(4096)},
		{"png-named.jpg", fixture(pngRGB, 320, 240)},
		{"jpeg-named.png", jpg()},
		{"UPPER.JPG", jpg()},
		{"Mixed.JpEg", jpg()},
		{"space name.jpg", jpg()},
		{"unicode-名前.jpg", jpg()},
		{"one-by-one.png", fixture(pngRGB, 1, 1)},
		{"no-extension", jpg()},
		{"photo.jpg.txt", jpg()},
		{"notes.txt", []byte("not an image\n")},
		{".hidden/inside.jpg", jpg()},
		{"deep/1/2/3/4/5/6/7/8/9/10/deep.jpg", jpg()},
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	for _, f := range files {
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(f.name)), f.data); err != nil {
			return err
		}
	}
	return os.MkdirAll(filepath.Join(dir, "empty-dir"), 0o755)
}
