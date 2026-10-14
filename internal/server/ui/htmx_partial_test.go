package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTMXPartial_IsPartialRequest(t *testing.T) {
	tests := []struct {
		name       string
		rule       PartialRule
		wantTarget string
		hxRequest  string
		hxTarget   string
		want       bool
	}{
		{
			name:      "dashboard_hx_true",
			rule:      PartialRuleHXRequestOnly,
			hxRequest: "true",
			want:      true,
		},
		{
			name:      "dashboard_hx_true_ignores_target",
			rule:      PartialRuleHXRequestOnly,
			hxRequest: "true",
			hxTarget:  "gallery-content",
			want:      true,
		},
		{
			name:      "dashboard_hx_false",
			rule:      PartialRuleHXRequestOnly,
			hxRequest: "false",
			want:      false,
		},
		{
			name: "dashboard_hx_missing",
			rule: PartialRuleHXRequestOnly,
			want: false,
		},
		{
			name:      "dashboard_hx_True_wrong_case",
			rule:      PartialRuleHXRequestOnly,
			hxRequest: "True",
			want:      false,
		},
		{
			name:       "gallery_match",
			rule:       PartialRuleHXRequestTarget,
			wantTarget: HXTargetGalleryContent,
			hxRequest:  "true",
			hxTarget:   HXTargetGalleryContent,
			want:       true,
		},
		{
			name:       "gallery_wrong_target",
			rule:       PartialRuleHXRequestTarget,
			wantTarget: HXTargetGalleryContent,
			hxRequest:  "true",
			hxTarget:   "not-gallery-content",
			want:       false,
		},
		{
			name:       "gallery_missing_target",
			rule:       PartialRuleHXRequestTarget,
			wantTarget: HXTargetGalleryContent,
			hxRequest:  "true",
			want:       false,
		},
		{
			name:       "gallery_no_hx_request",
			rule:       PartialRuleHXRequestTarget,
			wantTarget: HXTargetGalleryContent,
			hxTarget:   HXTargetGalleryContent,
			want:       false,
		},
		{
			name:       "gallery_target_wrong_case",
			rule:       PartialRuleHXRequestTarget,
			wantTarget: HXTargetGalleryContent,
			hxRequest:  "true",
			hxTarget:   "Gallery-Content",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.hxRequest != "" {
				req.Header.Set("HX-Request", tt.hxRequest)
			}
			if tt.hxTarget != "" {
				req.Header.Set("HX-Target", tt.hxTarget)
			}
			got := IsPartialRequest(req, tt.rule, tt.wantTarget)
			if got != tt.want {
				t.Errorf("IsPartialRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTMXPartial_HXRequest(t *testing.T) {
	tests := []struct {
		name      string
		hxRequest string
		want      bool
	}{
		{name: "true", hxRequest: "true", want: true},
		{name: "True", hxRequest: "True", want: false},
		{name: "missing", want: false},
		{name: "false", hxRequest: "false", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.hxRequest != "" {
				req.Header.Set("HX-Request", tt.hxRequest)
			}
			if got := HXRequest(req); got != tt.want {
				t.Errorf("HXRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTMXPartial_SetLayoutPartialVary(t *testing.T) {
	tests := []struct {
		name       string
		varyTarget bool
		wantVary   []string
	}{
		{
			name:       "dashboard_only_hx_request",
			varyTarget: false,
			wantVary:   []string{"HX-Request"},
		},
		{
			name:       "gallery_hx_request_and_target",
			varyTarget: true,
			wantVary:   []string{"HX-Request", "HX-Target"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			SetLayoutPartialVary(rec, tt.varyTarget)
			got := rec.Header().Values("Vary")
			if len(got) != len(tt.wantVary) {
				t.Fatalf("Vary count = %d, want %d; got %v", len(got), len(tt.wantVary), got)
			}
			for i, want := range tt.wantVary {
				if got[i] != want {
					t.Errorf("Vary[%d] = %q, want %q; full %v", i, got[i], want, got)
				}
			}
		})
	}
}

func TestHTMXPartial_SetPartialNoStore(t *testing.T) {
	tests := []struct {
		name          string
		priorCacheCtl string
		wantCacheCtl  string
	}{
		{
			name:         "sets_no_store",
			wantCacheCtl: "no-store",
		},
		{
			name:          "overwrites_gallery_cache_headers",
			priorCacheCtl: "public, max-age=3600",
			wantCacheCtl:  "no-store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if tt.priorCacheCtl != "" {
				rec.Header().Set("Cache-Control", tt.priorCacheCtl)
			}
			SetPartialNoStore(rec)
			got := rec.Header().Get("Cache-Control")
			if got != tt.wantCacheCtl {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantCacheCtl)
			}
		})
	}
}
