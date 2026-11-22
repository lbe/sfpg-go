package thumbnail

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"sync/atomic"

	wuffs "github.com/lbe/wuffs-wasm"
	"golang.org/x/image/draw"
	"golang.org/x/sync/semaphore"
)

// RetainedBytes is the most scratch memory all Generators keep between files.
const RetainedBytes = 256 << 20

const (
	// A Generator keeps at most this much source buffer and this much decode
	// memory (decoded pixels plus the gallery canvas) between files. A file
	// needing more takes the oversized slot and uses memory dropped after it.
	sourceRetainBytes = 8 << 20
	decodeRetainBytes = 16 << 20

	// inflightBytes bounds the source bytes being read by all Generators at
	// once. Each file is charged its size from the walk, at least readChunk,
	// so a file this large or larger reads alone.
	inflightBytes = 192 << 20
	readChunk     = 32 << 10

	galleryCanvasBytes = galleryThumbMaxW * galleryThumbMaxH * 4
)

// Pool hands out Generators and owns the memory budgets they share.
type Pool struct {
	inflight  *semaphore.Weighted
	oversized *semaphore.Weighted
	retained  atomic.Int64 // bytes kept by all Generators, at most RetainedBytes

	mu     sync.Mutex
	idle   []*Generator
	closed bool
}

// NewPool returns an empty pool.
func NewPool() *Pool {
	return &Pool{
		inflight:  semaphore.NewWeighted(inflightBytes),
		oversized: semaphore.NewWeighted(1),
	}
}

// Get returns an idle Generator or a new one. The caller owns it until Put.
func (p *Pool) Get() *Generator {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := len(p.idle); n > 0 {
		g := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		return g
	}
	return &Generator{pool: p}
}

// Put ends g's current file and keeps g for reuse. After Close, g is
// released instead.
func (p *Pool) Put(g *Generator) {
	g.endFile()
	p.mu.Lock()
	closed := p.closed
	if !closed {
		p.idle = append(p.idle, g)
	}
	p.mu.Unlock()
	if closed {
		g.release()
	}
}

// Close releases every idle Generator's memory and decoder. A Generator still
// out is released when it is Put.
func (p *Pool) Close() {
	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.closed = true
	p.mu.Unlock()
	for _, g := range idle {
		g.release()
	}
}

// retain charges n bytes against RetainedBytes and reports whether they fit.
// It never waits: a file that does not fit uses memory dropped after it.
func (p *Pool) retain(n int64) bool {
	for {
		used := p.retained.Load()
		if used+n > RetainedBytes {
			return false
		}
		if p.retained.CompareAndSwap(used, used+n) {
			return true
		}
	}
}

// Generator makes the thumbnail and hashes for one file at a time. It keeps
// the file's bytes, and reuses its scratch memory and decoder for the next
// file. It is not safe for concurrent use.
type Generator struct {
	pool *Pool

	// Kept between files and charged to the pool's retention budget.
	source []byte // backing for data, up to sourceRetainBytes
	decode []byte // decoded PNG and GIF pixels
	canvas []byte // the gallery thumbnail's pixels

	// decoder is made on the first PNG or GIF and kept for later files. It is
	// closed after a failed Wuffs decode and at release.
	decoder *wuffs.Decoder
	probe   [1]byte // reads past a full buffer

	// The current file.
	data      []byte // the file's bytes
	inflight  int64  // charge held against the pool's in-flight budget
	oversized bool   // holds the pool's oversized slot
}

// Read reads the whole file from r, ending any file g read before. size is
// the size seen by the walk: it sets the file's charge against the in-flight
// budget, taken before reading, and pre-sizes the buffer. The returned bytes
// belong to g until it is Put.
func (g *Generator) Read(ctx context.Context, r io.Reader, size int64) ([]byte, error) {
	g.endFile()
	weight := min(max(size, readChunk), inflightBytes)
	if err := g.pool.inflight.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	g.inflight = weight

	buf := g.source[:0]
	var err error
	if size > 0 && size <= inflightBytes && int(size) > cap(buf) {
		if buf, err = g.grow(ctx, buf, int(size)); err != nil {
			return nil, err
		}
	}
	for empty := 0; ; {
		var n int
		var readErr error
		if len(buf) < cap(buf) {
			n, readErr = r.Read(buf[len(buf):min(cap(buf), len(buf)+readChunk)])
			buf = buf[:len(buf)+n]
		} else {
			// The buffer is full, often because the walk size was exact. Grow
			// only if the file has more.
			if n, readErr = r.Read(g.probe[:]); n > 0 {
				if buf, err = g.grow(ctx, buf, nextCapacity(len(buf))); err != nil {
					return nil, err
				}
				buf = append(buf, g.probe[0])
			}
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if n > 0 {
			empty = 0
		} else if empty++; empty >= 100 {
			return nil, io.ErrNoProgress
		}
	}
	g.data = buf
	return buf, nil
}

// nextCapacity returns the capacity to grow a full buffer of n bytes to:
// double, but no more than sourceRetainBytes while the file still fits in it.
func nextCapacity(n int) int {
	need := n + readChunk
	capacity := max(need, 2*n)
	if need <= sourceRetainBytes {
		capacity = min(capacity, sourceRetainBytes)
	}
	return capacity
}

// grow returns buf copied into a backing of the given capacity. A backing
// within sourceRetainBytes is kept for later files if the budget allows;
// a larger one takes the oversized slot. The decoder is kept either way.
func (g *Generator) grow(ctx context.Context, buf []byte, capacity int) ([]byte, error) {
	if capacity > sourceRetainBytes {
		if err := g.takeOversized(ctx); err != nil {
			return nil, err
		}
	}
	next := make([]byte, len(buf), capacity)
	copy(next, buf)
	if capacity <= sourceRetainBytes && g.pool.retain(int64(capacity-cap(g.source))) {
		g.source = next
	}
	return next, nil
}

// takeOversized takes the pool's oversized slot for the current file.
func (g *Generator) takeOversized(ctx context.Context) error {
	if g.oversized {
		return nil
	}
	if err := g.pool.oversized.Acquire(ctx, 1); err != nil {
		return err
	}
	g.oversized = true
	return nil
}

// GenerateThumbnailAndHashes decodes the file read by Read, makes the gallery
// thumbnail, and hashes the file: MD5 of its bytes, pHash of its decoded
// pixels. srcW and srcH are the image's dimensions. A pHash that cannot be
// computed is logged and returned as 0.
func (g *Generator) GenerateThumbnailAndHashes(ctx context.Context, srcW, srcH int) (thumb *bytes.Buffer, md5Hex string, phash int64, err error) {
	if g.data == nil {
		return nil, "", 0, errors.New("thumbnail: no file read")
	}
	if err = ctx.Err(); err != nil {
		return nil, "", 0, err
	}
	var src image.Image
	switch mime := http.DetectContentType(g.data); mime {
	case "image/png":
		src, err = g.decodeWuffs(ctx, wuffs.FormatPNG)
	case "image/gif":
		src, err = g.decodeWuffs(ctx, wuffs.FormatGIF)
	default:
		src, err = g.decodeOther(ctx, mime == "image/jpeg", srcW, srcH)
	}
	if err != nil {
		return nil, "", 0, err
	}
	if err = ctx.Err(); err != nil {
		return nil, "", 0, err
	}

	view := g.galleryCanvas(fitInsideBoxDims(galleryThumbMaxW, galleryThumbMaxH, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.ApproxBiLinear.Scale(view, view.Bounds(), src, src.Bounds(), draw.Src, nil)
	thumb = GetBytesBuffer()
	if err = jpeg.Encode(thumb, view, nil); err != nil {
		PutBytesBuffer(thumb)
		return nil, "", 0, err
	}

	sum := md5.Sum(g.data)
	hash, phashErr := sourcePHash64(src)
	if phashErr != nil {
		slog.Error("GenerateThumbnailAndHashes source pHash", "err", phashErr)
		hash = 0
	}
	if err = ctx.Err(); err != nil {
		PutBytesBuffer(thumb)
		return nil, "", 0, err
	}
	return thumb, hex.EncodeToString(sum[:]), int64(hash), nil
}

// decodeOther decodes a JPEG or a format registered with the image package.
// A decode estimated to need more than the retained decode memory takes the
// oversized slot first.
func (g *Generator) decodeOther(ctx context.Context, jpegFile bool, srcW, srcH int) (image.Image, error) {
	estimate, ok := estimateDecodeBytes(srcW, srcH, jpegFile)
	if !ok {
		return nil, fmt.Errorf("invalid decode dimensions %dx%d", srcW, srcH)
	}
	if jpegFile {
		// go-scaled-jpeg keeps a progressive frame's full coefficients
		// between scans.
		if f, ok := parseJPEGFrame(g.data); ok && f.progressive {
			coefficients, ok := progressiveCoefficientBytes(f)
			if !ok {
				return nil, fmt.Errorf("invalid progressive JPEG dimensions %dx%d", f.width, f.height)
			}
			estimate += coefficients
		}
	}
	if estimate > decodeRetainBytes {
		if err := g.takeOversized(ctx); err != nil {
			return nil, err
		}
	}
	return decodeImage(g.data, jpegFile, srcW, srcH)
}

// estimateDecodeBytes returns the RGBA bytes a decode of a srcW×srcH image
// needs, at the DCT scale for a JPEG. ok is false when the dimensions are
// invalid or the size would overflow.
func estimateDecodeBytes(srcW, srcH int, jpegFile bool) (int64, bool) {
	if srcW < 1 || srcH < 1 || srcW > math.MaxInt/galleryThumbMaxW || srcH > math.MaxInt/galleryThumbMaxW {
		return 0, false
	}
	width, height := int64(srcW), int64(srcH)
	if jpegFile {
		dct := int64(chooseJPEGDCTSize(srcW, srcH))
		width, height = (width*dct+7)/8, (height*dct+7)/8
	}
	if width > math.MaxInt/4/height {
		return 0, false
	}
	return width * height * 4, true
}

// galleryCanvas returns a cleared w×h view at the origin of the 200×150 canvas
// the thumbnail is drawn into: g's retained canvas, or a new one when the
// budget is spent.
func (g *Generator) galleryCanvas(w, h int) *image.RGBA {
	if cap(g.canvas) == 0 && g.pool.retain(galleryCanvasBytes) {
		g.canvas = make([]byte, galleryCanvasBytes)
	}
	pix := g.canvas
	if pix == nil {
		pix = make([]byte, galleryCanvasBytes)
	}
	clear(pix)
	return &image.RGBA{Pix: pix, Stride: galleryThumbMaxW * 4, Rect: image.Rect(0, 0, w, h)}
}

// endFile releases the current file's in-flight charge, oversized slot and
// bytes. The decoder is kept for the next file.
func (g *Generator) endFile() {
	g.data = nil
	if g.oversized {
		g.pool.oversized.Release(1)
		g.oversized = false
	}
	if g.inflight > 0 {
		g.pool.inflight.Release(g.inflight)
		g.inflight = 0
	}
}

// release drops g's decoder and the memory it keeps between files.
func (g *Generator) release() {
	g.dropDecoder()
	g.pool.retained.Add(-int64(cap(g.source) + cap(g.decode) + cap(g.canvas)))
	g.source, g.decode, g.canvas = nil, nil, nil
}
