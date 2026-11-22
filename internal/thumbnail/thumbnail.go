// Package thumbnail makes gallery thumbnails and hashes image files: MD5 of
// the file and a perceptual hash (pHash) of its pixels. A Pool hands out
// Generators, which reuse their memory and decoders from one file to the
// next under budgets shared by the whole Pool.
package thumbnail

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/png"

	jpegscaled "github.com/m8rge/go-scaled-jpeg"
	_ "golang.org/x/image/webp"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

// The gallery thumbnail fits inside a box this size. The constants are untyped
// so they convert to int and uint at each use.
const (
	galleryThumbMaxW = 200
	galleryThumbMaxH = 150
)

var bytesBufferPool = gensyncpool.New(
	func() *bytes.Buffer { return new(bytes.Buffer) },
	func(b *bytes.Buffer) { b.Reset() },
)

// GetBytesBuffer returns an empty buffer from the pool.
func GetBytesBuffer() *bytes.Buffer { return bytesBufferPool.Get() }

// PutBytesBuffer returns buf to the pool. The caller must not use it again.
func PutBytesBuffer(buf *bytes.Buffer) { bytesBufferPool.Put(buf) }

// fitInsideBoxDims returns the size that fits a srcW×srcH image inside a
// maxW×maxH box, keeping its aspect ratio and at least 1 in each dimension. It
// scales small images up.
func fitInsideBoxDims(maxW, maxH, srcW, srcH int) (int, int) {
	var newW, newH int
	if maxW*srcH <= maxH*srcW {
		newW = maxW
		newH = srcH * maxW / srcW
	} else {
		newH = maxH
		newW = srcW * maxH / srcH
	}
	return max(newW, 1), max(newH, 1)
}

// chooseJPEGDCTSize returns the go-scaled-jpeg DCT size, from 1 (1/8 scale) to
// 8 (full size), that decodes a srcW×srcH JPEG to at least the size it will
// have in the gallery box, so scaling it into the box never enlarges it.
func chooseJPEGDCTSize(srcW, srcH int) int {
	srcW, srcH = max(srcW, 1), max(srcH, 1)
	needW, needH := fitInsideBoxDims(galleryThumbMaxW, galleryThumbMaxH, srcW, srcH)
	dctW := (needW*8 + srcW - 1) / srcW // ceil(needW*8/srcW)
	dctH := (needH*8 + srcH - 1) / srcH
	return min(max(dctW, dctH, 1), 8)
}

// decodeImage decodes data: a JPEG (jpegFile) at the scale chooseJPEGDCTSize
// picks for srcW×srcH, anything else in a format registered with the image
// package at full size. A JPEG that fails to decode is an error; there is no
// fallback to the standard decoder.
func decodeImage(data []byte, jpegFile bool, srcW, srcH int) (image.Image, error) {
	if !jpegFile {
		img, _, err := image.Decode(bytes.NewReader(data))
		return img, err
	}
	return jpegscaled.Decode(bytes.NewReader(data), jpegscaled.DecodeOptions{DCTSizeScaled: chooseJPEGDCTSize(srcW, srcH)})
}
