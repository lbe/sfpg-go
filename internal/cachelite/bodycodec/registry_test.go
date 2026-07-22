package bodycodec

import (
	"errors"
	"testing"
)

// testStubCodec is a simple codec that prepends 0xDE, 0xAD magic bytes.
type testStubCodec struct{}

func (testStubCodec) ID() string { return "test-stub" }
func (testStubCodec) Magic() [][]byte {
	return [][]byte{{0xDE, 0xAD}}
}
func (testStubCodec) Compress(src []byte) ([]byte, error) {
	out := make([]byte, 2+len(src))
	out[0], out[1] = 0xDE, 0xAD
	copy(out[2:], src)
	return out, nil
}
func (testStubCodec) Decompress(src []byte, _ int) ([]byte, error) {
	if len(src) < 2 || src[0] != 0xDE || src[1] != 0xAD {
		return nil, errors.New("bad stub")
	}
	out := make([]byte, len(src)-2)
	copy(out, src[2:])
	return out, nil
}

// expandStubCodec is a codec whose Compress expands input rather than shrinking it.
type expandStubCodec struct{}

func (expandStubCodec) ID() string      { return "expand-stub" }
func (expandStubCodec) Magic() [][]byte { return [][]byte{{0xEE, 0xFF}} }
func (expandStubCodec) Compress(src []byte) ([]byte, error) {
	out := make([]byte, 0, 2*len(src))
	out = append(out, src...)
	out = append(out, make([]byte, len(src))...)
	return out, nil
}
func (expandStubCodec) Decompress(src []byte, _ int) ([]byte, error) {
	return src, nil
}

// dupMagicCodec has the same magic as testStubCodec but a different ID.
type dupMagicCodec struct{}

func (dupMagicCodec) ID() string      { return "dup-magic" }
func (dupMagicCodec) Magic() [][]byte { return [][]byte{{0xDE, 0xAD}} }
func (dupMagicCodec) Compress(src []byte) ([]byte, error) {
	return nil, nil
}
func (dupMagicCodec) Decompress(src []byte, _ int) ([]byte, error) {
	return nil, nil
}

func TestRegister_duplicateMagic(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testStubCodec{}); err != nil {
		t.Fatal(err)
	}
	err := r.Register(dupMagicCodec{})
	if err == nil {
		t.Fatal("expected ErrDuplicateMagic, got nil")
	}
	if !errors.Is(err, ErrDuplicateMagic) {
		t.Fatalf("expected ErrDuplicateMagic, got %v", err)
	}
}

func TestRegister_duplicateID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testStubCodec{}); err != nil {
		t.Fatal(err)
	}
	err := r.Register(testStubCodec{})
	if err == nil {
		t.Fatal("expected ErrDuplicateID, got nil")
	}
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("expected ErrDuplicateID, got %v", err)
	}
}

func TestLookup_unknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Lookup("nonexistent")
	if err == nil {
		t.Fatal("expected ErrUnknownCodecID, got nil")
	}
	if !errors.Is(err, ErrUnknownCodecID) {
		t.Fatalf("expected ErrUnknownCodecID, got %v", err)
	}
}

func TestEncodeWith_identity(t *testing.T) {
	r := NewRegistry()
	src := []byte("<html><body>hello</body></html>")
	out, err := r.EncodeWith("identity", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(src) {
		t.Fatalf("identity: got len %d, want %d", len(out), len(src))
	}
	for i := range src {
		if out[i] != src[i] {
			t.Fatalf("identity: byte %d differs: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

func TestEncodeWith_minSize(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testStubCodec{}); err != nil {
		t.Fatal(err)
	}
	// 100 bytes < MinCompressBytes (256)
	src := make([]byte, 100)
	for i := range src {
		src[i] = 'x'
	}
	out, err := r.EncodeWith("test-stub", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(src) {
		t.Fatalf("minSize: got len %d, want %d", len(out), len(src))
	}
	for i := range src {
		if out[i] != src[i] {
			t.Fatalf("minSize: byte %d differs", i)
		}
	}
}

func TestEncodeWith_expandGuard(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(expandStubCodec{}); err != nil {
		t.Fatal(err)
	}
	// 512 bytes >= MinCompressBytes
	src := make([]byte, 512)
	for i := range src {
		src[i] = 'y'
	}
	out, err := r.EncodeWith("expand-stub", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(src) {
		t.Fatalf("expandGuard: got len %d, want %d", len(out), len(src))
	}
	for i := range src {
		if out[i] != src[i] {
			t.Fatalf("expandGuard: byte %d differs", i)
		}
	}
}

func TestDecodeForServe_magic(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testStubCodec{}); err != nil {
		t.Fatal(err)
	}
	src := []byte("<html><body>hello world</body></html>")
	compressed, err := r.EncodeWith("test-stub", src)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := r.DecodeForServe(compressed, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != len(src) {
		t.Fatalf("roundtrip: got len %d, want %d", len(plain), len(src))
	}
	for i := range src {
		if plain[i] != src[i] {
			t.Fatalf("roundtrip: byte %d differs: got %02x, want %02x", i, plain[i], src[i])
		}
	}
}

func TestDecodeForServe_plainHTML(t *testing.T) {
	r := NewRegistry()
	src := []byte("<html><body>x</body></html>")
	plain, err := r.DecodeForServe(src, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != len(src) {
		t.Fatalf("plainHTML: got len %d, want %d", len(plain), len(src))
	}
	for i := range src {
		if plain[i] != src[i] {
			t.Fatalf("plainHTML: byte %d differs", i)
		}
	}
}

func TestDecodeForServe_garbage(t *testing.T) {
	r := NewRegistry()
	stored := []byte{0x00, 0x01, 0x02, 0x03}
	_, err := r.DecodeForServe(stored, len(stored)+1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnrecognizedCacheBody) {
		t.Fatalf("expected ErrUnrecognizedCacheBody, got %v", err)
	}
}

func TestDecodeForServe_identityNonHTML(t *testing.T) {
	r := NewRegistry()
	// Non-HTML bytes with matching length should be returned as-is.
	stored := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	plain, err := r.DecodeForServe(stored, len(stored))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plain) != len(stored) {
		t.Fatalf("got len %d, want %d", len(plain), len(stored))
	}
	for i := range stored {
		if plain[i] != stored[i] {
			t.Fatalf("byte %d differs", i)
		}
	}
}

func TestDecodeForServe_lengthMismatch(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testStubCodec{}); err != nil {
		t.Fatal(err)
	}
	src := []byte("short payload")
	compressed, err := r.EncodeWith("test-stub", src)
	if err != nil {
		t.Fatal(err)
	}
	// Pass wrong uncompressedLen (too large).
	_, err = r.DecodeForServe(compressed, len(src)+100)
	if err == nil {
		t.Fatal("expected ErrUnrecognizedCacheBody, got nil")
	}
	if !errors.Is(err, ErrUnrecognizedCacheBody) {
		t.Fatalf("expected ErrUnrecognizedCacheBody, got %v", err)
	}
}
