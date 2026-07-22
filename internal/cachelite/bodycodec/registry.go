package bodycodec

import (
	"fmt"
	"sort"
	"sync"
)

type magicEntry struct {
	prefix []byte
	codec  Codec
}

// Registry holds registered codecs, keyed by ID, and supports magic-prefix
// matching and highest-magic-length-first dispatch order.
type Registry struct {
	mu       sync.RWMutex
	byID     map[string]Codec
	entries  []magicEntry // sorted longest prefix first
	maxMagic int
}

// NewRegistry returns an empty ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:    make(map[string]Codec),
		entries: make([]magicEntry, 0, 8),
	}
}

// Register adds c to the registry. It returns an error for nil, empty magic,
// duplicate IDs, or duplicate magic byte sequences.
func (r *Registry) Register(c Codec) error {
	if c == nil {
		return ErrNilCodec
	}
	magic := c.Magic()
	if len(magic) == 0 {
		return ErrNoMagic
	}
	for _, m := range magic {
		if len(m) == 0 {
			return ErrEmptyMagic
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[c.ID()]; exists {
		return ErrDuplicateID
	}

	// Check for duplicate magic sequences and clone.
	cloned := make([][]byte, len(magic))
	for i, m := range magic {
		for _, entry := range r.entries {
			if prefixesMatch(entry.prefix, m) {
				return fmt.Errorf(
					"%w: magic %x already registered to codec %q",
					ErrDuplicateMagic, m, entry.codec.ID(),
				)
			}
		}
		cloned[i] = make([]byte, len(m))
		copy(cloned[i], m)
	}

	r.byID[c.ID()] = c

	for _, m := range cloned {
		r.entries = append(r.entries, magicEntry{prefix: m, codec: c})
		if len(m) > r.maxMagic {
			r.maxMagic = len(m)
		}
	}

	// Sort entries by descending prefix length (longest first).
	sort.SliceStable(r.entries, func(i, j int) bool {
		return len(r.entries[i].prefix) > len(r.entries[j].prefix)
	})

	return nil
}

// prefixesMatch reports whether a and b are equal byte slices.
func prefixesMatch(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Lookup returns the codec identified by id, or ErrUnknownCodecID.
func (r *Registry) Lookup(id string) (Codec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, ErrUnknownCodecID
	}
	return c, nil
}

// Match finds the first codec whose magic prefix matches head, scanning in
// longest-prefix-first order. Returns nil when no codec matches.
func (r *Registry) Match(head []byte) Codec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if len(head) < len(entry.prefix) {
			continue
		}
		if headMatches(head, entry.prefix) {
			return entry.codec
		}
	}
	return nil
}

func headMatches(head, prefix []byte) bool {
	for i := range prefix {
		if head[i] != prefix[i] {
			return false
		}
	}
	return true
}

// MaxMagicLen returns the length of the longest magic prefix in the registry.
func (r *Registry) MaxMagicLen() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxMagic
}

// EncodeWith compresses src using the codec identified by writeCodecID.
//
// The identity string bypasses compression entirely. Inputs smaller than
// MinCompressBytes are returned as-is. If compression does not shrink the
// payload the original src is returned (expand guard).
func (r *Registry) EncodeWith(writeCodecID string, src []byte) ([]byte, error) {
	if writeCodecID == "identity" {
		return src, nil
	}
	if len(src) < MinCompressBytes {
		return src, nil
	}
	codec, err := r.Lookup(writeCodecID)
	if err != nil {
		return nil, err
	}
	compressed, err := codec.Compress(src)
	if err != nil {
		return nil, err
	}
	if len(compressed) >= len(src) {
		return src, nil
	}
	return compressed, nil
}

// DecodeForServe decodes a stored cache body for serving. It first tries
// magic-prefix matching to decompress the body; if no codec matches, the body
// is treated as uncompressed identity bytes and is returned as-is when its
// length matches uncompressedLen. Returns ErrUnrecognizedCacheBody when the
// body cannot be recognized or the length does not match.
func (r *Registry) DecodeForServe(stored []byte, uncompressedLen int) ([]byte, error) {
	if codec := r.Match(stored); codec != nil {
		plain, err := codec.Decompress(stored, uncompressedLen)
		if err != nil {
			return nil, err
		}
		if uncompressedLen > 0 && len(plain) != uncompressedLen {
			return nil, ErrUnrecognizedCacheBody
		}
		return plain, nil
	}

	// No magic prefix: body is uncompressed/identity. The storage layer always
	// records ContentLength, so require the stored bytes to match it exactly.
	// This removes the HTML-only assumption from the codec and still allows
	// empty bodies (len(stored) == uncompressedLen == 0) such as HEAD responses.
	if len(stored) != uncompressedLen {
		return nil, ErrUnrecognizedCacheBody
	}
	return stored, nil
}
