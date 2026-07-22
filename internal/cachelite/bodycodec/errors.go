package bodycodec

import "errors"

var (
	ErrUnrecognizedCacheBody = errors.New("bodycodec: unrecognized cache body")
	ErrNilCodec              = errors.New("bodycodec: nil codec")
	ErrNoMagic               = errors.New("bodycodec: no magic sequences")
	ErrEmptyMagic            = errors.New("bodycodec: empty magic entry")
	ErrDuplicateMagic        = errors.New("bodycodec: duplicate magic")
	ErrDuplicateID           = errors.New("bodycodec: duplicate codec id")
	ErrUnknownCodecID        = errors.New("bodycodec: unknown codec id")
)
