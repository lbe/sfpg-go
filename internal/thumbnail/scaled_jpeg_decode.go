package thumbnail

import (
	"bufio"
	"bytes"
	"image"
	"io"

	jpegscaled "github.com/m8rge/go-scaled-jpeg"
)

// jpegMagic is the JPEG SOI marker prefix (FF D8 FF) used to sniff JPEG
// input before it is dispatched to the go-scaled-jpeg decoder.
var jpegMagic = []byte{0xFF, 0xD8, 0xFF}

// decodeJPEGScaled decodes a JPEG from r via go-scaled-jpeg at
// dctSizeScaled/8 of the source resolution (8 is 1:1, 1 is 1/8).
// go-scaled-jpeg is decode-only and does NOT call image.RegisterFormat, so it
// never becomes a stdlib image.Decode dispatch target.
func decodeJPEGScaled(r io.Reader, dctSizeScaled int) (image.Image, error) {
	return jpegscaled.Decode(r, jpegscaled.DecodeOptions{DCTSizeScaled: dctSizeScaled})
}

// decodeJPEGScaledHook is a testable hook for the go-scaled-jpeg decoder.
// Production default: decodeJPEGScaled. Tests may replace it; they must
// restore the default (e.g. via t.Cleanup).
var decodeJPEGScaledHook = decodeJPEGScaled

// isJPEG reports whether br starts with the JPEG SOI marker. The peek does
// not consume the reader, so the caller can hand br to the decoder with the
// stream still positioned at the start.
func isJPEG(br *bufio.Reader) bool {
	magic, err := br.Peek(len(jpegMagic))
	return err == nil && bytes.HasPrefix(magic, jpegMagic)
}

// decodeFullImage decodes a full image from r. It is the production default
// for fullImageDecodeHook — the single source-decode path (there is no
// embedded-EXIF-thumbnail shortcut). JPEG input is sniffed with a buffered
// peek (which does not consume the reader) and decoded via go-scaled-jpeg at
// an adaptive DCT scale chosen from srcW/srcH (chooseJPEGDCTSize) so the
// decoded JPEG is at least the 200×150 gallery-thumb fit size; the same
// buffered reader is handed to the decoder so the peeked bytes are never
// lost. Any other format is decoded with stdlib image.Decode on the same
// buffered reader (srcW/srcH are ignored for non-JPEG input). A JPEG
// scaled-decode error is returned as-is: there is no stdlib image/jpeg.Decode
// fallback (hard fail).
func decodeFullImage(r io.Reader, srcW, srcH int) (image.Image, string, error) {
	br := bufio.NewReader(r)
	if !isJPEG(br) {
		return image.Decode(br)
	}
	dct := chooseJPEGDCTSize(srcW, srcH)
	img, err := decodeJPEGScaledHook(br, dct)
	if err != nil {
		return nil, "", err
	}
	return img, "jpeg", nil
}
