// Package zstd provides a bodycodec.Codec implementation using
// github.com/klauspost/compress/zstd at compression level SpeedFastest.
//
// It does not import the bodycodec package — callers use duck typing
// via the exported Codec type and NewCodec constructor.
package zstd

import (
	"fmt"

	"github.com/klauspost/compress/zstd"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

const maxRetainedCapacity = 16 * 1024

type encoderSlot struct {
	enc     *zstd.Encoder
	scratch []byte
}

type decoderSlot struct {
	dec *zstd.Decoder
}

func newEncoder() (*zstd.Encoder, error) {
	return zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1),
		zstd.WithLowerEncoderMem(true),
	)
}

func newDecoder() (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
}

var encoderPool = gensyncpool.New(
	func() *encoderSlot {
		enc, err := newEncoder()
		if err != nil {
			panic(err)
		}
		return &encoderSlot{enc: enc}
	},
	func(s *encoderSlot) {
		if cap(s.scratch) > maxRetainedCapacity {
			s.scratch = nil
		} else {
			s.scratch = s.scratch[:0]
		}
	},
)

var decoderPool = gensyncpool.New(
	func() *decoderSlot {
		dec, err := newDecoder()
		if err != nil {
			panic(err)
		}
		return &decoderSlot{dec: dec}
	},
	func(*decoderSlot) {},
)

// Codec implements the bodycodec.Codec duck-type interface for zstd-1.
type Codec struct{}

// NewCodec returns a usable zero-value Codec backed by package-level
// pooled encoder and decoder instances.
func NewCodec() *Codec { return &Codec{} }

// ID returns "zstd-1".
func (c *Codec) ID() string { return "zstd-1" }

// Magic returns the zstd frame magic bytes.
func (c *Codec) Magic() [][]byte { return [][]byte{{0x28, 0xB5, 0x2F, 0xFD}} }

// Compress encodes src using the pooled zstd encoder.
func (c *Codec) Compress(src []byte) ([]byte, error) {
	slot := encoderPool.Get()
	defer encoderPool.Put(slot)
	out := slot.enc.EncodeAll(src, slot.scratch[:0])
	// Retain capacity for reuse even if EncodeAll reallocated.
	slot.scratch = out
	result := make([]byte, len(out))
	copy(result, out)
	return result, nil
}

// Decompress decodes src using the pooled zstd decoder.
func (c *Codec) Decompress(src []byte, uncompressedLen int) ([]byte, error) {
	slot := decoderPool.Get()
	defer decoderPool.Put(slot)
	out, err := slot.dec.DecodeAll(src, nil)
	if err != nil {
		return nil, err
	}
	if uncompressedLen > 0 && len(out) != uncompressedLen {
		return nil, fmt.Errorf("decompress length mismatch: got %d want %d", len(out), uncompressedLen)
	}
	return out, nil
}
