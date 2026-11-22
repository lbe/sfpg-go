package files

import (
	"errors"
	"testing"
)

func TestInvalidReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "unknown"},
		{"non-image", errors.New("non-image file: text/plain"), "non-image"},
		{"decode", errors.New("failed to decode image config: invalid"), "decode"},
		{"DecodeConfig", errors.New("DecodeConfig failed"), "decode"},
		{"thumbnail", errors.New("failed to generate thumbnail"), "thumbnail"},
		{"exif lowercase", errors.New("exif read failed"), "exif"},
		{"EXIF uppercase", errors.New("EXIF parse error"), "exif"},
		{"open", errors.New("failed to open file"), "open"},
		{"Open", errors.New("Open: no such file"), "open"},
		{"unknown", errors.New("something else"), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InvalidReason(tt.err); got != tt.want {
				t.Errorf("InvalidReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
