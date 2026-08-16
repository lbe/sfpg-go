package thumbnail

// nfnt/resize is used by characterization and Phase 2 alternative-resize
// benches only. Production thumbnail generation no longer imports nfnt.

import (
	"image"

	"github.com/nfnt/resize"
)

// thumbnail scales img to fit inside maxWidth x maxHeight while preserving
// aspect ratio. Unlike resize.Thumbnail, it also upscales images that are
// smaller than the target box so they fill the requested thumbnail
// dimensions.
func thumbnail(maxWidth, maxHeight uint, img image.Image, interp resize.InterpolationFunction) image.Image {
	w, h := fitInsideBox(maxWidth, maxHeight, img)
	return resize.Resize(uint(w), uint(h), img, interp)
}
