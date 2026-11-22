package thumbnail

import "testing"

// TestFitInsideBoxDims verifies fitInsideBoxDims geometry for the width-limited,
// height-limited, and already-fitting cases, including upscaling of small
// sources.
func TestFitInsideBoxDims(t *testing.T) {
	tests := []struct {
		name         string
		maxW, maxH   int
		srcW, srcH   int
		wantW, wantH int
	}{
		{
			name: "small landscape fills box",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH, srcW: 40, srcH: 30,
			wantW: galleryThumbMaxW, wantH: galleryThumbMaxH,
		},
		{
			name: "tall portrait is height-limited",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH, srcW: 10, srcH: 200,
			wantW: 7, wantH: galleryThumbMaxH,
		},
		{
			name: "already fitting is unchanged",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH, srcW: 200, srcH: 100,
			wantW: galleryThumbMaxW, wantH: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotW, gotH := fitInsideBoxDims(tt.maxW, tt.maxH, tt.srcW, tt.srcH)
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Fatalf("fitInsideBoxDims(%d, %d, %d, %d) = (%d, %d), want (%d, %d)", tt.maxW, tt.maxH, tt.srcW, tt.srcH, gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

// TestChooseJPEGDCTSize verifies the adaptive DCT-scale picker: the chosen
// dct decodes the srcW×srcH JPEG to at least the 200×150 gallery-thumb fit
// size (ceil(need*8/src) clamped to [1,8]).
func TestChooseJPEGDCTSize(t *testing.T) {
	tests := []struct {
		name    string
		srcW    int
		srcH    int
		wantDCT int
	}{
		{name: "12MP stays at 1/8", srcW: 4000, srcH: 3000, wantDCT: 1},
		{name: "800x600 decodes at 1/4", srcW: 800, srcH: 600, wantDCT: 2},
		{name: "400x300 decodes at 1/2", srcW: 400, srcH: 300, wantDCT: 4},
		{name: "320x240 decodes near 1:1", srcW: 320, srcH: 240, wantDCT: 5},
		{name: "zero width guards to 1:1", srcW: 0, srcH: 600, wantDCT: 8},
		{name: "zero height guards to 1:1", srcW: 800, srcH: 0, wantDCT: 8},
		{name: "negative dims guard to 1:1", srcW: -10, srcH: -20, wantDCT: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chooseJPEGDCTSize(tt.srcW, tt.srcH); got != tt.wantDCT {
				t.Fatalf("chooseJPEGDCTSize(%d, %d) = %d, want %d", tt.srcW, tt.srcH, got, tt.wantDCT)
			}
		})
	}
}
