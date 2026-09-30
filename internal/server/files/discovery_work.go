package files

import (
	"encoding/binary"
	"fmt"
	"slices"
)

// DiscoveryPathWork is one discovery backlog item: gallery-relative path plus
// mtime and size captured at walk enqueue time (parallelwalkdir.ReportedFile).
type DiscoveryPathWork struct {
	Path      []byte
	MtimeUnix int64
	SizeBytes int64
}

// GobEncode implements gob encoding for dque segment persistence.
func (w DiscoveryPathWork) GobEncode() ([]byte, error) {
	pathBytes := w.Path
	buf := make([]byte, 8+8+4+len(pathBytes))
	binary.BigEndian.PutUint64(buf[0:8], uint64(w.MtimeUnix))
	binary.BigEndian.PutUint64(buf[8:16], uint64(w.SizeBytes))
	binary.BigEndian.PutUint32(buf[16:20], uint32(len(pathBytes)))
	copy(buf[20:], pathBytes)
	return buf, nil
}

// GobDecode implements gob decoding for dque segment persistence.
func (w *DiscoveryPathWork) GobDecode(data []byte) error {
	if len(data) < 20 {
		return fmt.Errorf("discovery path work: short gob payload (%d bytes)", len(data))
	}
	w.MtimeUnix = int64(binary.BigEndian.Uint64(data[0:8]))
	w.SizeBytes = int64(binary.BigEndian.Uint64(data[8:16]))
	pathLen := int(binary.BigEndian.Uint32(data[16:20]))
	if len(data) < 20+pathLen {
		return fmt.Errorf("discovery path work: truncated path in gob payload")
	}
	end := 20 + pathLen
	w.Path = slices.Clone(data[20:end])
	return nil
}
