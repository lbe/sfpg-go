package bodycodec

import (
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/gzip"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/zstd"
)

func RegisterDefaults(r *Registry) error {
	if err := r.Register(zstd.NewCodec()); err != nil {
		return err
	}
	return r.Register(gzip.NewCodec())
}
