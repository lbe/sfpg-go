package files

import (
	"bytes"
	"encoding/gob"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
)

// fileWire is the gob-safe wire format for File. It mirrors File's fields
// exactly but replaces *bytes.Buffer with []byte, because bytes.Buffer has
// no exported fields and cannot be gob-encoded — even when the pointer is nil,
// gob still tries to resolve the pointed-to type for type info.
type fileWire struct {
	ImagesDir      string
	Path           string
	File           gallerydb.File
	ThumbnailBytes []byte // nil if no thumbnail (replaces *bytes.Buffer)
	Metadata       metadata.Info
}

// GobEncode serializes File into a gob-safe wire format. The *bytes.Buffer
// Thumbnail field is extracted as raw bytes; the caller's object is not mutated.
func (f File) GobEncode() ([]byte, error) {
	w := fileWire{
		ImagesDir: f.ImagesDir,
		Path:      f.Path,
		File:      f.File,
		Metadata:  f.Metadata,
	}
	if f.Thumbnail != nil {
		w.ThumbnailBytes = append([]byte(nil), f.Thumbnail.Bytes()...)
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&w); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GobDecode deserializes File from the gob-safe wire format, reconstructing
// the Thumbnail bytes.Buffer from the raw bytes. Note: the reconstructed
// buffer is created via bytes.NewBuffer, not from the thumbnail pool.
// The writer later returns it to the pool with thumbnail.PutBytesBuffer,
// which accepts any *bytes.Buffer.
func (f *File) GobDecode(data []byte) error {
	var w fileWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return err
	}

	f.ImagesDir = w.ImagesDir
	f.Path = w.Path
	f.File = w.File
	f.Thumbnail = nil
	if len(w.ThumbnailBytes) > 0 {
		f.Thumbnail = bytes.NewBuffer(w.ThumbnailBytes)
	}
	f.Metadata = w.Metadata

	return nil
}
