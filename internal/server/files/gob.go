package files

import (
	"bytes"
	"encoding/gob"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// fileWire is the gob-safe wire format for File. It mirrors File's fields
// exactly but replaces *bytes.Buffer with []byte, because bytes.Buffer has
// no exported fields and cannot be gob-encoded — even when the pointer is nil,
// gob still tries to resolve the pointed-to type for type info.
type fileWire struct {
	Ok                  bool
	Exists              bool
	ImagesDir           string
	Path                string
	File                gallerydb.File
	ThumbnailBytes      []byte // nil if no thumbnail (replaces *bytes.Buffer)
	Exif                gallerydb.UpsertExifParams
	Itpc                gallerydb.UpsertIPTCParams
	XmpProps            []gallerydb.UpsertXMPPropertyParams
	XmpRaw              gallerydb.UpsertXMPRawParams
	HasValidJpegMarkers bool
}

// GobEncode serializes File into a gob-safe wire format. The *bytes.Buffer
// Thumbnail field is extracted as raw bytes; the caller's object is not mutated.
func (f File) GobEncode() ([]byte, error) {
	w := fileWire{
		Ok:                  f.Ok,
		Exists:              f.Exists,
		ImagesDir:           f.ImagesDir,
		Path:                f.Path,
		File:                f.File,
		Exif:                f.Exif,
		Itpc:                f.Itpc,
		XmpProps:            append([]gallerydb.UpsertXMPPropertyParams(nil), f.XmpProps...),
		XmpRaw:              f.XmpRaw,
		HasValidJpegMarkers: f.HasValidJpegMarkers,
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
// When cleanupBatchedWriteResources later returns it to the pool via
// thumbnail.PutBytesBuffer, the pool accepts any *bytes.Buffer.
func (f *File) GobDecode(data []byte) error {
	var w fileWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return err
	}

	f.Ok = w.Ok
	f.Exists = w.Exists
	f.ImagesDir = w.ImagesDir
	f.Path = w.Path
	f.File = w.File
	f.Thumbnail = nil
	if len(w.ThumbnailBytes) > 0 {
		f.Thumbnail = bytes.NewBuffer(w.ThumbnailBytes)
	}
	f.Exif = w.Exif
	f.Itpc = w.Itpc
	f.XmpProps = append([]gallerydb.UpsertXMPPropertyParams(nil), w.XmpProps...)
	f.XmpRaw = w.XmpRaw
	f.HasValidJpegMarkers = w.HasValidJpegMarkers

	return nil
}
