package cachelite

import (
	"fmt"
	"sync"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec"
)

var (
	bodyCodecMu      sync.RWMutex
	bodyWriteCodecID = "zstd-1"
	bodyRegistry     *bodycodec.Registry

	initBodyRegistryOnce sync.Once
	initBodyRegistryErr  error
)

// initBodyRegistry creates and populates the process-scoped codec registry.
func initBodyRegistry() (*bodycodec.Registry, error) {
	r := bodycodec.NewRegistry()
	if err := bodycodec.RegisterDefaults(r); err != nil {
		return nil, err
	}
	return r, nil
}

// getRegistry returns the process-scoped registry, initializing it lazily on
// the first call.
func getRegistry() (*bodycodec.Registry, error) {
	initBodyRegistryOnce.Do(func() {
		r, err := initBodyRegistry()
		if err != nil {
			initBodyRegistryErr = err
			return
		}
		bodyRegistry = r
	})
	if initBodyRegistryErr != nil {
		return nil, initBodyRegistryErr
	}
	return bodyRegistry, nil
}

// ConfigureBodyCodec sets the write codec for new cache rows. It is safe for
// hot-reload. An empty string is treated as "zstd-1". Unknown IDs return an
// error and must not change bodyWriteCodecID (validate-then-assign). The
// "identity" codec skips registry lookup.
func ConfigureBodyCodec(writeCodecID string) error {
	id := writeCodecID
	if id == "" {
		id = "zstd-1"
	}

	if id != "identity" {
		reg, err := getRegistry()
		if err != nil {
			return fmt.Errorf("ConfigureBodyCodec: registry unavailable: %w", err)
		}
		if _, err := reg.Lookup(id); err != nil {
			return fmt.Errorf("ConfigureBodyCodec: %w", err)
		}
	}

	bodyCodecMu.Lock()
	bodyWriteCodecID = id
	bodyCodecMu.Unlock()
	return nil
}

// ValidateWriteCodecID validates a codec ID without mutating process state.
// Empty string is valid (means default at configure time). "identity" is
// always valid. Other IDs must be registered in the process-scoped registry.
func ValidateWriteCodecID(id string) error {
	if id == "" || id == "identity" {
		return nil
	}
	reg, err := getRegistry()
	if err != nil {
		return fmt.Errorf("ValidateWriteCodecID: registry unavailable: %w", err)
	}
	if _, err := reg.Lookup(id); err != nil {
		return fmt.Errorf("ValidateWriteCodecID: %w", err)
	}
	return nil
}

// FinalizeForStorage compresses entry.Body in place when it is not already
// stored form (i.e. already prefixed with a known codec magic). It does not
// modify ContentLength — that stays uncompressed.
//
// Nil entry, empty body, or body smaller than MinCompressBytes result in a
// no-op. Body slices that already carry a registered magic prefix are also
// returned unchanged (idempotency guard).
func FinalizeForStorage(entry *HTTPCacheEntry) error {
	if entry == nil || len(entry.Body) == 0 {
		return nil
	}

	reg, err := getRegistry()
	if err != nil {
		return err
	}

	// Already stored form — idempotency guard.
	if reg.Match(entry.Body) != nil {
		return nil
	}

	bodyCodecMu.RLock()
	writeCodecID := bodyWriteCodecID
	bodyCodecMu.RUnlock()

	stored, err := reg.EncodeWith(writeCodecID, entry.Body)
	if err != nil {
		return err
	}

	entry.Body = stored
	return nil
}

// decodeCacheBodyForServe decodes a stored (possibly compressed) cache body.
// It returns the uncompressed plaintext or an error. Errors include
// ErrUnrecognizedCacheBody (length mismatch or garbage payload), corrupt data,
// or decompress failures.
func decodeCacheBodyForServe(stored []byte, uncompressedLen int) ([]byte, error) {
	reg, err := getRegistry()
	if err != nil {
		return nil, err
	}
	return reg.DecodeForServe(stored, uncompressedLen)
}
