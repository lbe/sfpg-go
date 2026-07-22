package bodycodec

const MinCompressBytes = 256

type Codec interface {
	ID() string
	Magic() [][]byte
	Compress(src []byte) ([]byte, error)
	Decompress(src []byte, uncompressedLen int) ([]byte, error)
}
