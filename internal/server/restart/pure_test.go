package restart

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestIsHTTPOnlyRestart(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{
			name: "nil config returns false",
			cfg:  nil,
			want: false,
		},
		{
			name: "valid config returns true",
			cfg:  config.DefaultConfig(),
			want: true,
		},
		{
			name: "modified port still returns true",
			cfg: func() *config.Config {
				c := config.DefaultConfig()
				c.ListenerPort = 9999
				return c
			}(),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsHTTPOnlyRestart(tt.cfg)
			if got != tt.want {
				t.Errorf("IsHTTPOnlyRestart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetRestartType(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "nil config returns full restart",
			cfg:  nil,
			want: "full",
		},
		{
			name: "valid config returns HTTP-only",
			cfg:  config.DefaultConfig(),
			want: "HTTP-only",
		},
		{
			name: "modified config returns HTTP-only",
			cfg: func() *config.Config {
				c := config.DefaultConfig()
				c.ListenerPort = 9999
				return c
			}(),
			want: "HTTP-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetRestartType(tt.cfg)
			if got != tt.want {
				t.Errorf("GetRestartType() = %v, want %v", got, tt.want)
			}
		})
	}
}
