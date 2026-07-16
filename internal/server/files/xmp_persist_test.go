package files

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestXMPPropertyID(t *testing.T) {
	if got := xmpPropertyID(42, xmpPropGPSLatitude); got != 421 {
		t.Errorf("lat id = %d, want 421", got)
	}
	if got := xmpPropertyID(42, xmpPropGPSLongitude); got != 422 {
		t.Errorf("lon id = %d, want 422", got)
	}
}

type xmpUpsertSpy struct {
	called bool
}

func (s *xmpUpsertSpy) UpsertXMPRaw(context.Context, gallerydb.UpsertXMPRawParams) error {
	s.called = true
	return nil
}

func (s *xmpUpsertSpy) UpsertXMPProperty(context.Context, gallerydb.UpsertXMPPropertyParams) error {
	s.called = true
	return nil
}

func TestUpsertFileXMP_SkipsWhenEmpty(t *testing.T) {
	spy := &xmpUpsertSpy{}
	f := &File{Path: "x.jpg", File: gallerydb.File{ID: 1}}
	upsertFileXMP(context.Background(), spy, f)
	if spy.called {
		t.Error("upsertFileXMP should not call DB when XmpRaw.RawXml.Valid is false")
	}
}
