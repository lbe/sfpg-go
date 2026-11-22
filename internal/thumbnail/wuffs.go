package thumbnail

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"math"

	wuffs "github.com/lbe/wuffs-wasm"
)

// wuffsTrimBytes is the decode output above which a Wuffs decode is followed by Trim.
const wuffsTrimBytes = 4 << 20

// decodeWuffs decodes the PNG or GIF in g.data with g's Wuffs decoder: a PNG
// as 8-bit premultiplied RGBA, a GIF as its first frame at the frame's bounds.
// The layout is checked before any memory is reserved or allocated.
func (g *Generator) decodeWuffs(ctx context.Context, format uint32) (img image.Image, err error) {
	defer func() {
		if err != nil {
			g.dropDecoder()
		}
	}()
	if g.decoder == nil {
		g.decoder = wuffs.New()
	}
	if err = g.decoder.Reserve(0, len(g.data)); err != nil {
		return nil, err
	}
	meta, err := g.decoder.Probe(g.data)
	if err != nil {
		return nil, err
	}
	width, height, stride, size, err := wuffsLayout(*meta, format)
	if err != nil {
		return nil, err
	}
	if size+galleryCanvasBytes > decodeRetainBytes {
		if err = g.takeOversized(ctx); err != nil {
			return nil, err
		}
	}
	// The guest decodes into a slot that holds the pixels and, after them,
	// the format's work buffer.
	if err = g.decoder.Reserve(size+int(meta.WorkbufLen), len(g.data)); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}

	dst := &image.RGBA{Pix: g.decodePixels(size), Stride: stride, Rect: image.Rect(0, 0, width, height)}
	if format == wuffs.FormatPNG {
		if _, err = g.decoder.DecodeRGBA(dst, g.data); err != nil {
			return nil, err
		}
		if size > wuffsTrimBytes {
			g.decoder.Trim()
		}
		return dst, nil
	}
	clear(dst.Pix)
	frame, err := g.decoder.DecodeFrame(dst, g.data, 0)
	if err != nil {
		return nil, err
	}
	if size > wuffsTrimBytes {
		g.decoder.Trim()
	}
	return frameView(dst, *frame)
}

// wuffsLayout checks the image's layout against both this package's and
// wuffs-wasm's limits (a uint32 stride and pixel count) and returns its
// dimensions, stride and size in bytes.
func wuffsLayout(meta wuffs.Meta, format uint32) (width, height, stride, size int, err error) {
	w, h := uint64(meta.Width), uint64(meta.Height)
	if meta.Err != 0 || meta.Format != format || w == 0 || h == 0 ||
		w > math.MaxUint32/4 || uint64(meta.Stride) != w*4 ||
		w*4 > uint64(math.MaxInt-galleryCanvasBytes)/h ||
		w*4*h > math.MaxUint32 {
		return 0, 0, 0, 0, fmt.Errorf("invalid Wuffs layout %dx%d stride %d format %08x", meta.Width, meta.Height, meta.Stride, meta.Format)
	}
	return int(w), int(h), int(w * 4), int(w * 4 * h), nil
}

// decodePixels returns size bytes to decode into: g's retained decode memory,
// grown if the retention budget allows, or memory dropped after the file.
func (g *Generator) decodePixels(size int) []byte {
	if size <= cap(g.decode) {
		return g.decode[:size:size]
	}
	if size <= decodeRetainBytes-galleryCanvasBytes && g.pool.retain(int64(size-cap(g.decode))) {
		g.decode = make([]byte, size)
		return g.decode
	}
	return make([]byte, size)
}

// frameView moves the rows of a GIF frame to the start of dst.Pix, packed at
// 4 bytes a pixel, and returns them as an image with the frame's bounds.
func frameView(dst *image.RGBA, frame wuffs.Frame) (*image.RGBA, error) {
	b := frame.Bounds
	if frame.Index != 0 || b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > dst.Rect.Max.X || b.Max.Y > dst.Rect.Max.Y || b.Empty() {
		return nil, fmt.Errorf("invalid GIF frame bounds %v in %v", b, dst.Rect)
	}
	rowBytes := b.Dx() * 4
	for y := range b.Dy() {
		src := (b.Min.Y+y)*dst.Stride + b.Min.X*4
		copy(dst.Pix[y*rowBytes:(y+1)*rowBytes], dst.Pix[src:src+rowBytes])
	}
	size := rowBytes * b.Dy()
	return &image.RGBA{Pix: dst.Pix[:size:size], Stride: rowBytes, Rect: b}, nil
}

// dropDecoder closes g's decoder, freeing its guest memory, and clears it. A
// Close error has no caller to return to, so it is logged.
func (g *Generator) dropDecoder() {
	if g.decoder != nil {
		if err := g.decoder.Close(); err != nil {
			slog.Warn("closing Wuffs decoder", "err", err)
		}
		g.decoder = nil
	}
}
