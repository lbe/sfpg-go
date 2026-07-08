package thumbnail

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// maxThumbSize caps embedded thumbnail reads to guard against
// corrupt/malicious offset+length values.
const maxThumbSize = 1 << 22 // 4 MiB

const maxIFDEntries = 512

var errNoThumb = errors.New("thumbnail: no embedded thumbnail")

// extractEXIFThumbnail locates an embedded EXIF (IFD1) JPEG thumbnail in a
// JPEG, TIFF, or WebP stream and appends its bytes to buf. It reads only the
// container/EXIF structures required — never the full image data. The
// reader's position on return is unspecified. Returns errNoThumb (or another
// error) if no usable thumbnail exists; buf is only written to on success.
func extractEXIFThumbnail(r io.ReadSeeker, buf *bytes.Buffer) error {
	var sig [12]byte
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.ReadFull(r, sig[:]); err != nil {
		return errNoThumb
	}

	var tiffBase int64
	switch {
	case sig[0] == 0xFF && sig[1] == 0xD8: // JPEG
		base, err := findJPEGExif(r)
		if err != nil {
			return err
		}
		tiffBase = base
	case (sig[0] == 'I' && sig[1] == 'I' && sig[2] == 0x2A && sig[3] == 0) ||
		(sig[0] == 'M' && sig[1] == 'M' && sig[2] == 0 && sig[3] == 0x2A): // TIFF
		tiffBase = 0
	case string(sig[0:4]) == "RIFF" && string(sig[8:12]) == "WEBP": // WebP
		base, err := findWebPExif(r)
		if err != nil {
			return err
		}
		tiffBase = base
	default:
		return errNoThumb
	}

	off, length, err := findIFD1Thumb(r, tiffBase)
	if err != nil {
		return err
	}

	if _, err := r.Seek(tiffBase+off, io.SeekStart); err != nil {
		return err
	}
	start := buf.Len()
	if _, err := io.CopyN(buf, r, length); err != nil {
		buf.Truncate(start)
		return errNoThumb
	}
	// Sanity check: must be a JPEG (SOI marker).
	b := buf.Bytes()[start:]
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		buf.Truncate(start)
		return errNoThumb
	}
	return nil
}

// findJPEGExif scans JPEG marker segments for an APP1 "Exif\x00\x00" segment
// and returns the absolute file offset of the TIFF header within it.
func findJPEGExif(r io.ReadSeeker) (int64, error) {
	pos := int64(2) // past SOI
	var hdr [4]byte
	var exifID [6]byte
	for {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return 0, errNoThumb
		}
		if hdr[0] != 0xFF {
			return 0, errNoThumb
		}
		marker := hdr[1]
		// Standalone markers / start of scan: no EXIF beyond here.
		if marker == 0xD8 || marker == 0xDA || marker == 0xD9 ||
			(marker >= 0xD0 && marker <= 0xD7) {
			return 0, errNoThumb
		}
		segLen := int64(binary.BigEndian.Uint16(hdr[2:4]))
		if segLen < 2 {
			return 0, errNoThumb
		}
		if marker == 0xE1 && segLen >= 2+6+8 {
			if _, err := io.ReadFull(r, exifID[:]); err != nil {
				return 0, errNoThumb
			}
			if string(exifID[:]) == "Exif\x00\x00" {
				return pos + 4 + 6, nil
			}
		}
		pos += 2 + segLen
	}
}

// findWebPExif walks RIFF chunks looking for an "EXIF" chunk and returns the
// absolute file offset of the TIFF header within it.
func findWebPExif(r io.ReadSeeker) (int64, error) {
	pos := int64(12) // past RIFF header
	var hdr [8]byte
	var exifID [6]byte
	for {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return 0, errNoThumb
		}
		size := int64(binary.LittleEndian.Uint32(hdr[4:8]))
		if string(hdr[0:4]) == "EXIF" {
			base := pos + 8
			// Some writers include the "Exif\x00\x00" prefix; skip if present.
			if size >= 6 {
				if _, err := io.ReadFull(r, exifID[:]); err != nil {
					return 0, errNoThumb
				}
				if string(exifID[:]) == "Exif\x00\x00" {
					base += 6
				}
			}
			return base, nil
		}
		pos += 8 + size + (size & 1) // chunks are word-aligned
	}
}

// findIFD1Thumb parses the TIFF structure at base and returns the
// JPEGInterchangeFormat offset and length from IFD1 (tags 0x0201/0x0202).
// Offsets returned are relative to base.
func findIFD1Thumb(r io.ReadSeeker, base int64) (off, length int64, err error) {
	var b [8]byte
	if _, err = r.Seek(base, io.SeekStart); err != nil {
		return
	}
	if _, err = io.ReadFull(r, b[:]); err != nil {
		return 0, 0, errNoThumb
	}
	var bo binary.ByteOrder
	switch {
	case b[0] == 'I' && b[1] == 'I':
		bo = binary.LittleEndian
	case b[0] == 'M' && b[1] == 'M':
		bo = binary.BigEndian
	default:
		return 0, 0, errNoThumb
	}
	ifd0 := int64(bo.Uint32(b[4:8]))

	// Skip IFD0 entries to reach the next-IFD (IFD1) pointer.
	if _, err = r.Seek(base+ifd0, io.SeekStart); err != nil {
		return
	}
	if _, err = io.ReadFull(r, b[:2]); err != nil {
		return 0, 0, errNoThumb
	}
	n := int64(bo.Uint16(b[:2]))
	if n > maxIFDEntries {
		return 0, 0, errNoThumb
	}
	if _, err = r.Seek(base+ifd0+2+n*12, io.SeekStart); err != nil {
		return
	}
	if _, err = io.ReadFull(r, b[:4]); err != nil {
		return 0, 0, errNoThumb
	}
	ifd1 := int64(bo.Uint32(b[:4]))
	if ifd1 == 0 {
		return 0, 0, errNoThumb
	}

	// Scan IFD1 for thumbnail offset/length tags.
	if _, err = r.Seek(base+ifd1, io.SeekStart); err != nil {
		return
	}
	if _, err = io.ReadFull(r, b[:2]); err != nil {
		return 0, 0, errNoThumb
	}
	n = int64(bo.Uint16(b[:2]))
	if n > maxIFDEntries {
		return 0, 0, errNoThumb
	}
	var entry [12]byte
	for i := int64(0); i < n; i++ {
		if _, err = io.ReadFull(r, entry[:]); err != nil {
			return 0, 0, errNoThumb
		}
		switch bo.Uint16(entry[0:2]) {
		case 0x0201: // JPEGInterchangeFormat
			off = int64(bo.Uint32(entry[8:12]))
		case 0x0202: // JPEGInterchangeFormatLength
			length = int64(bo.Uint32(entry[8:12]))
		}
	}
	if off == 0 || length == 0 || length > maxThumbSize {
		return 0, 0, errNoThumb
	}
	return off, length, nil
}
