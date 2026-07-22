// Package gzip provides a bodycodec.Codec implementation using
// compress/gzip at compression level 6.
//
// It does not import the bodycodec package — callers use duck typing
// via the exported Codec type and NewCodec constructor.
package gzip

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

const maxRetainedCapacity = 16 * 1024

type encoderSlot struct {
	zw  *gzip.Writer
	buf bytes.Buffer
}

type decoderSlot struct {
	zr *gzip.Reader
}

var encoderPool = gensyncpool.New(
	func() *encoderSlot {
		s := &encoderSlot{}
		zw, err := gzip.NewWriterLevel(&s.buf, 6)
		if err != nil {
			panic(err)
		}
		s.zw = zw
		return s
	},
	func(s *encoderSlot) {
		s.buf.Reset()
		if s.buf.Cap() > maxRetainedCapacity {
			s.buf = *bytes.NewBuffer(make([]byte, 0, 256))
		}
	},
)

var decoderPool = gensyncpool.New(
	func() *decoderSlot { return &decoderSlot{} },
	func(s *decoderSlot) { s.zr = nil },
)

// Codec implements the bodycodec.Codec duck-type interface for gzip-6.
type Codec struct{}

// NewCodec returns a usable zero-value Codec backed by package-level
// pooled encoder and decoder instances.
func NewCodec() *Codec { return &Codec{} }

// ID returns "gzip-6".
func (c *Codec) ID() string { return "gzip-6" }

// Magic returns the gzip magic bytes.
func (c *Codec) Magic() [][]byte { return [][]byte{{0x1F, 0x8B}} }

// Compress encodes src using the pooled gzip writer at level 6.
func (c *Codec) Compress(src []byte) ([]byte, error) {
	slot := encoderPool.Get()
	defer encoderPool.Put(slot)
	slot.buf.Reset()
	slot.zw.Reset(&slot.buf)
	if _, err := slot.zw.Write(src); err != nil {
		return nil, err
	}
	if err := slot.zw.Close(); err != nil {
		return nil, err
	}
	result := make([]byte, slot.buf.Len())
	copy(result, slot.buf.Bytes())
	return result, nil
}

// Decompress decodes src using the pooled gzip reader.
func (c *Codec) Decompress(src []byte, uncompressedLen int) ([]byte, error) {
	slot := decoderPool.Get()
	defer decoderPool.Put(slot)
	r := bytes.NewReader(src)
	var err error
	if slot.zr == nil {
		slot.zr, err = gzip.NewReader(r)
	} else {
		err = slot.zr.Reset(r)
	}
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(slot.zr)
	if err != nil {
		return nil, err
	}
	if uncompressedLen > 0 && len(out) != uncompressedLen {
		return nil, fmt.Errorf("decompress length mismatch: got %d want %d", len(out), uncompressedLen)
	}
	return out, nil
}
