package thumbnail

import "testing"

// jpegSegment returns a length-prefixed JPEG segment with marker m.
func jpegSegment(m byte, payload ...byte) []byte {
	n := len(payload) + 2
	return append([]byte{0xff, m, byte(n >> 8), byte(n)}, payload...)
}

// jpegSOF returns a frame header segment with 8-bit precision. Each component
// is {id, horizontal<<4|vertical, quant table}.
func jpegSOF(m byte, width, height int, comps ...[3]byte) []byte {
	payload := []byte{8, byte(height >> 8), byte(height), byte(width >> 8), byte(width), byte(len(comps))}
	for _, c := range comps {
		payload = append(payload, c[0], c[1], c[2])
	}
	return jpegSegment(m, payload...)
}

func jpegSOI() []byte  { return []byte{0xff, 0xd8} }
func jpegEOI() []byte  { return []byte{0xff, 0xd9} }
func jpegAPP0() []byte { return jpegSegment(0xe0, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0) }

// jpegSOS() is a single-component scan header (Ns=1, Ss=0, Se=63, Ah/Al=0).
func jpegSOS() []byte { return jpegSegment(0xda, 1, 1, 0, 0, 63, 0) }

func comps420() [][3]byte { return [][3]byte{{1, 0x22, 0}, {2, 0x11, 1}, {3, 0x11, 1}} }
func comps444() [][3]byte { return [][3]byte{{1, 0x11, 0}, {2, 0x11, 1}, {3, 0x11, 1}} }

// jpeg420Header returns SOI, APP0 and a 4:2:0 frame header with marker m.
func jpeg420Header(m byte, width, height int) []byte {
	b := append(append([]byte{}, jpegSOI()...), jpegAPP0()...)
	return append(b, jpegSOF(m, width, height, comps420()...)...)
}

func TestJPEGFrameHeader(t *testing.T) {
	cases := []struct {
		name         string
		data         []byte
		wantOK       bool
		wantFrame    jpegFrame
		wantCoeffLen int64
	}{
		{
			// 4:2:0 at 12000x9000, MCU 16x16: mcusX=750, mcusY=ceil(9000/16)=563.
			// Y: 1500*1126 = 1,689,000 blocks; Cb, Cr: 750*563 = 422,250 each.
			// Total 2,533,500 blocks * 256 B = 648,576,000 bytes.
			name:         "420_progressive_12000x9000",
			data:         jpeg420Header(0xc2, 12000, 9000),
			wantOK:       true,
			wantFrame:    jpegFrame{progressive: true, width: 12000, height: 9000, components: 3, h: [4]int{2, 1, 1}, v: [4]int{2, 1, 1}},
			wantCoeffLen: 648_576_000,
		},
		{
			// 4:4:4 at 100x50, MCU 8x8: mcusX=13, mcusY=7. Three components of 91 blocks
			// each = 273 blocks * 256 B = 69,888 bytes.
			name:         "444_progressive_100x50",
			data:         append(append(append([]byte{}, jpegSOI()...), jpegAPP0()...), jpegSOF(0xc2, 100, 50, comps444()[0], comps444()[1], comps444()[2])...),
			wantOK:       true,
			wantFrame:    jpegFrame{progressive: true, width: 100, height: 50, components: 3, h: [4]int{1, 1, 1}, v: [4]int{1, 1, 1}},
			wantCoeffLen: 69_888,
		},
		{
			// Grayscale 9x9, one 1x1 component: mcusX=mcusY=2, 4 blocks * 256 B = 1024 bytes.
			name:         "gray_progressive_9x9",
			data:         append(append([]byte{}, jpegSOI()...), jpegSOF(0xc2, 9, 9, [3]byte{1, 0x11, 0})...),
			wantOK:       true,
			wantFrame:    jpegFrame{progressive: true, width: 9, height: 9, components: 1, h: [4]int{1}, v: [4]int{1}},
			wantCoeffLen: 1024,
		},
		{
			// SOF10 (arithmetic progressive) is progressive too; 16x16 gray = 4 blocks.
			name:         "progressive_sof10_16x16",
			data:         append(append([]byte{}, jpegSOI()...), jpegSOF(0xca, 16, 16, [3]byte{1, 0x11, 0})...),
			wantOK:       true,
			wantFrame:    jpegFrame{progressive: true, width: 16, height: 16, components: 1, h: [4]int{1}, v: [4]int{1}},
			wantCoeffLen: 4 * 256,
		},
		{
			name:      "baseline_sof0_not_progressive",
			data:      append(jpeg420Header(0xc0, 200, 100), jpegSOS()...),
			wantOK:    true,
			wantFrame: jpegFrame{progressive: false, width: 200, height: 100, components: 3, h: [4]int{2, 1, 1}, v: [4]int{2, 1, 1}},
		},
		{
			name:         "fill_bytes_before_marker",
			data:         append(append([]byte{}, jpegSOI()...), append([]byte{0xff, 0xff, 0xff}, jpegSOF(0xc2, 16, 16, [3]byte{1, 0x11, 0})[1:]...)...),
			wantOK:       true,
			wantFrame:    jpegFrame{progressive: true, width: 16, height: 16, components: 1, h: [4]int{1}, v: [4]int{1}},
			wantCoeffLen: 4 * 256,
		},
		{
			name:         "standalone_rst_before_sof",
			data:         append(append([]byte{}, jpegSOI()...), append([]byte{0xff, 0xd0}, jpegSOF(0xc2, 16, 16, [3]byte{1, 0x11, 0})...)...),
			wantOK:       true,
			wantFrame:    jpegFrame{progressive: true, width: 16, height: 16, components: 1, h: [4]int{1}, v: [4]int{1}},
			wantCoeffLen: 4 * 256,
		},
		{
			name:   "truncated_sof",
			data:   jpeg420Header(0xc2, 12000, 9000)[:len(jpeg420Header(0xc2, 12000, 9000))-3],
			wantOK: false,
		},
		{
			name:   "missing_sof_before_sos",
			data:   append(append(append(append([]byte{}, jpegSOI()...), jpegAPP0()...), jpegSOS()...), jpegSOF(0xc2, 16, 16, [3]byte{1, 0x11, 0})...),
			wantOK: false,
		},
		{
			name:   "eoi_before_sof",
			data:   append(append(append([]byte{}, jpegSOI()...), jpegAPP0()...), append(jpegEOI(), jpegSOF(0xc2, 16, 16, [3]byte{1, 0x11, 0})...)...),
			wantOK: false,
		},
		{
			name:   "missing_soi",
			data:   jpegSOF(0xc2, 16, 16, [3]byte{1, 0x11, 0}),
			wantOK: false,
		},
		{
			name:   "garbage_between_segments",
			data:   append(append([]byte{}, jpegSOI()...), append([]byte{0x12, 0x34}, jpegSOF(0xc2, 16, 16, [3]byte{1, 0x11, 0})...)...),
			wantOK: false,
		},
		{
			name:   "zero_components",
			data:   append(append([]byte{}, jpegSOI()...), jpegSOF(0xc2, 16, 16)...),
			wantOK: false,
		},
		{
			name:   "zero_sampling_factor",
			data:   append(append([]byte{}, jpegSOI()...), jpegSOF(0xc2, 16, 16, [3]byte{1, 0x01, 0})...),
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame, ok := parseJPEGFrame(tc.data)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if frame != tc.wantFrame {
				t.Fatalf("frame=%+v want %+v", frame, tc.wantFrame)
			}
			if !frame.progressive {
				return
			}
			n, ok := progressiveCoefficientBytes(frame)
			if !ok || n != tc.wantCoeffLen {
				t.Fatalf("coefficient bytes=%d,%v want %d", n, ok, tc.wantCoeffLen)
			}
		})
	}
}
