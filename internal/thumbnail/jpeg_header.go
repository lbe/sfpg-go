package thumbnail

import "math"

// maxJPEGComponents is the largest component count parseJPEGSOF accepts.
// Frames with more components are rejected, even though T.81 allows more.
const maxJPEGComponents = 4

// jpegBlockCoeffBytes is the size of one 8x8 block of int32 coefficients.
const jpegBlockCoeffBytes = 64 * 4

// jpegFrame holds the SOF fields that size progressive coefficient storage.
type jpegFrame struct {
	progressive   bool
	width, height int
	components    int
	h, v          [maxJPEGComponents]int
}

// parseJPEGFrame walks the marker segments of an in-memory JPEG up to the
// first frame header (SOFn). It stops at SOS or EOI without a frame and fails
// on truncated or malformed segments.
func parseJPEGFrame(data []byte) (jpegFrame, bool) {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return jpegFrame{}, false
	}
	i := 2
	for {
		if i >= len(data) || data[i] != 0xff {
			return jpegFrame{}, false
		}
		for i < len(data) && data[i] == 0xff {
			i++
		}
		if i >= len(data) {
			return jpegFrame{}, false
		}
		m := data[i]
		i++
		switch {
		case m == 0x00 || m == 0xd9 || m == 0xda:
			// Stuffed byte, EOI or SOS before any frame header.
			return jpegFrame{}, false
		case m == 0x01 || m == 0xd8 || (m >= 0xd0 && m <= 0xd7):
			// Standalone markers carry no length.
			continue
		}
		if i+2 > len(data) {
			return jpegFrame{}, false
		}
		segLen := int(data[i])<<8 | int(data[i+1])
		if segLen < 2 || i+segLen > len(data) {
			return jpegFrame{}, false
		}
		seg := data[i+2 : i+segLen]
		if isJPEGFrameMarker(m) {
			return parseJPEGSOF(m, seg)
		}
		i += segLen
	}
}

// isJPEGFrameMarker reports whether m is a SOFn marker. DHT (C4), JPG (C8)
// and DAC (CC) share the C0-CF range but are not frame headers.
func isJPEGFrameMarker(m byte) bool {
	return m >= 0xc0 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc
}

// parseJPEGSOF reads the dimensions, component count and sampling factors from
// a SOFn segment. The sample precision byte is not checked.
func parseJPEGSOF(m byte, seg []byte) (jpegFrame, bool) {
	if len(seg) < 6 {
		return jpegFrame{}, false
	}
	f := jpegFrame{
		progressive: m == 0xc2 || m == 0xc6 || m == 0xca || m == 0xce,
		height:      int(seg[1])<<8 | int(seg[2]),
		width:       int(seg[3])<<8 | int(seg[4]),
		components:  int(seg[5]),
	}
	if f.width < 1 || f.height < 1 || f.components < 1 || f.components > maxJPEGComponents || len(seg) < 6+3*f.components {
		return jpegFrame{}, false
	}
	for c := range f.components {
		hv := seg[6+3*c+1]
		f.h[c], f.v[c] = int(hv>>4), int(hv&0x0f)
		if f.h[c] < 1 || f.v[c] < 1 || f.h[c] > 4 || f.v[c] > 4 {
			return jpegFrame{}, false
		}
	}
	return f, true
}

// progressiveCoefficientBytes returns the full-resolution coefficient storage
// that go-scaled-jpeg keeps between scans of a progressive frame, independent
// of the DCT scale. ok is false if the byte count would overflow int.
func progressiveCoefficientBytes(f jpegFrame) (int64, bool) {
	hMax, vMax := 1, 1
	for c := range f.components {
		hMax = max(hMax, f.h[c])
		vMax = max(vMax, f.v[c])
	}
	mcusX := int64((f.width + 8*hMax - 1) / (8 * hMax))
	mcusY := int64((f.height + 8*vMax - 1) / (8 * vMax))
	var blocks int64
	for c := range f.components {
		blocks += mcusX * int64(f.h[c]) * mcusY * int64(f.v[c])
	}
	if blocks > int64(math.MaxInt)/jpegBlockCoeffBytes {
		return 0, false
	}
	return blocks * jpegBlockCoeffBytes, true
}
