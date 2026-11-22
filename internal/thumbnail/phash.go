package thumbnail

import (
	"fmt"
	"image"

	"github.com/lbe/go-image-phash/phash"
)

func sourcePHash64(src image.Image) (uint64, error) {
	p, err := phash.NewFromImage(src, 32, phash.Options{})
	if err != nil {
		return 0, err
	}
	h, err := p.PHash(phash.Options{Geometry: "64", Method: "average"})
	if err != nil {
		return 0, err
	}
	if h.BitLen() != 64 {
		return 0, fmt.Errorf("source pHash has %d bits, want 64", h.BitLen())
	}
	return h.Uint64()
}
