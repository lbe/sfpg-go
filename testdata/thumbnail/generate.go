//go:build ignore

// Command generate creates reproducible thumbnail test fixtures for
// internal/thumbnail. It uses exiftool for the JPEG fixture and builds
// TIFF/WebP/edge-case fixtures manually so every byte is controlled.
//
// Run: go run generate.go
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	// Source images.
	mainImg := drawImage(800, 600, color.RGBA{R: 0, G: 0, B: 255, A: 255})
	thumbImg := drawImage(160, 120, color.RGBA{R: 0, G: 255, B: 255, A: 255})

	mainJPEG := mustEncodeJPEG(mainImg)
	thumbJPEG := mustEncodeJPEG(thumbImg)

	// 1) Plain JPEG without EXIF thumbnail.
	if err := os.WriteFile("no-exif-thumb.jpg", mainJPEG, 0o644); err != nil {
		return fmt.Errorf("write no-exif-thumb.jpg: %w", err)
	}

	// 2) JPEG with embedded EXIF thumbnail (via exiftool).
	if err := exiftoolJPEGWithThumb("exif-thumb.jpg", mainJPEG, thumbJPEG); err != nil {
		return fmt.Errorf("exif-thumb.jpg: %w", err)
	}

	// 3) TIFF with embedded EXIF thumbnail. We write a minimal TIFF blob
	//    directly: IFD0 is empty and points at IFD1, which holds the
	//    JPEGInterchangeFormat/Length tags for thumbJPEG. This exercises the
	//    TIFF path of the extractor without needing a full valid image.
	tiffBlob := makeThumbnailTIFF(thumbJPEG)
	if err := os.WriteFile("exif-thumb.tiff", tiffBlob, 0o644); err != nil {
		return fmt.Errorf("write exif-thumb.tiff: %w", err)
	}

	// 4) WebP with EXIF chunk containing the standard "Exif\x00\x00" prefix.
	if err := writeWebPWithEXIF("exif-thumb.webp", tiffBlob, true); err != nil {
		return fmt.Errorf("exif-thumb.webp: %w", err)
	}

	// 5) WebP with EXIF chunk but no "Exif\x00\x00" prefix.
	if err := writeWebPWithEXIF("exif-thumb-no-prefix.webp", tiffBlob, false); err != nil {
		return fmt.Errorf("exif-thumb-no-prefix.webp: %w", err)
	}

	// 6) Truncated APP1 segment: SOI + APP1 marker claiming 16 bytes but only
	//    10 bytes of payload follow.
	truncated := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x10}
	truncated = append(truncated, []byte("0123456789")...)
	if err := os.WriteFile("truncated-app1.jpg", truncated, 0o644); err != nil {
		return fmt.Errorf("write truncated-app1.jpg: %w", err)
	}

	fmt.Println("Generated thumbnail test fixtures:")
	for _, name := range []string{"no-exif-thumb.jpg", "exif-thumb.jpg", "exif-thumb.tiff", "exif-thumb.webp", "exif-thumb-no-prefix.webp", "truncated-app1.jpg"} {
		fi, err := os.Stat(name)
		if err != nil {
			return err
		}
		fmt.Printf("  %s %d bytes\n", name, fi.Size())
	}
	return nil
}

func drawImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func mustEncodeJPEG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func exiftoolJPEGWithThumb(dst string, mainJPEG, thumbJPEG []byte) error {
	mainFile, err := os.CreateTemp("", "main-*.jpg")
	if err != nil {
		return err
	}
	defer os.Remove(mainFile.Name())
	mainFile.Close()
	if err := os.WriteFile(mainFile.Name(), mainJPEG, 0o644); err != nil {
		return err
	}

	thumbFile, err := os.CreateTemp("", "thumb-*.jpg")
	if err != nil {
		return err
	}
	defer os.Remove(thumbFile.Name())
	thumbFile.Close()
	if err := os.WriteFile(thumbFile.Name(), thumbJPEG, 0o644); err != nil {
		return err
	}

	cmd := exec.Command("exiftool", "-q", "-overwrite_original", "-Artist=Test", "-ThumbnailImage<="+thumbFile.Name(), "-o", dst, mainFile.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("exiftool: %w\n%s", err, out)
	}
	return nil
}

// makeThumbnailTIFF builds a minimal little-endian TIFF containing only IFD0,
// IFD1, and the JPEGInterchangeFormat/Length tags pointing at thumbJPEG.
func makeThumbnailTIFF(thumbJPEG []byte) []byte {
	var buf bytes.Buffer

	// TIFF header: little-endian, magic 42, IFD0 at offset 8.
	buf.WriteString("II")
	binary.Write(&buf, binary.LittleEndian, uint16(0x002a))
	binary.Write(&buf, binary.LittleEndian, uint32(8))

	// IFD0: 0 entries, next IFD at offset 14.
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint32(14))

	// IFD1 offset = 14. 2 entries, then next-IFD pointer (0).
	ifd1Start := buf.Len()
	binary.Write(&buf, binary.LittleEndian, uint16(2))

	thumbOffset := ifd1Start + 2 + 2*12 + 4 // after IFD1 header + entries + next-IFD

	// Entry 0x0201 JPEGInterchangeFormat (LONG, count 1).
	buf.Write([]byte{0x01, 0x02}) // tag
	buf.Write([]byte{0x03, 0x00}) // type LONG
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	binary.Write(&buf, binary.LittleEndian, uint32(thumbOffset))

	// Entry 0x0202 JPEGInterchangeFormatLength (LONG, count 1).
	buf.Write([]byte{0x02, 0x02}) // tag
	buf.Write([]byte{0x03, 0x00}) // type LONG
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	binary.Write(&buf, binary.LittleEndian, uint32(len(thumbJPEG)))

	// Next IFD pointer = 0.
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	// Thumbnail bytes.
	buf.Write(thumbJPEG)
	return buf.Bytes()
}

// writeWebPWithEXIF writes a RIFF/WebP container containing a single EXIF chunk.
// If withPrefix is true, the EXIF chunk data is prefixed with "Exif\x00\x00".
func writeWebPWithEXIF(dst string, tiffBlob []byte, withPrefix bool) error {
	var exifData bytes.Buffer
	if withPrefix {
		exifData.WriteString("Exif\x00\x00")
	}
	exifData.Write(tiffBlob)

	var riff bytes.Buffer
	riff.WriteString("RIFF")
	// RIFF size placeholder.
	riff.Write([]byte{0, 0, 0, 0})
	riff.WriteString("WEBP")

	riff.WriteString("EXIF")
	binary.Write(&riff, binary.LittleEndian, uint32(exifData.Len()))
	riff.Write(exifData.Bytes())
	// Pad to even size.
	if riff.Len()%2 != 0 {
		riff.WriteByte(0)
	}

	data := riff.Bytes()
	// RIFF chunk size = file size - 8.
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))

	return os.WriteFile(dst, data, 0o644)
}
