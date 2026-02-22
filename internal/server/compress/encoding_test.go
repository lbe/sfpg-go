package compress

import "testing"

func TestNegotiateEncoding(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty header",
			input: "",
			want:  "identity",
		},
		{
			name:  "gzip only",
			input: "gzip",
			want:  "gzip",
		},
		{
			name:  "brotli only",
			input: "br",
			want:  "br",
		},
		{
			name:  "brotli and gzip",
			input: "br, gzip",
			want:  "br",
		},
		{
			name:  "gzip and brotli",
			input: "gzip, br",
			want:  "gzip", // Returns first match (gzip), not the preferred one (br)
		},
		{
			name:  "wildcard",
			input: "*",
			want:  "br",
		},
		{
			name:  "with quality factor",
			input: "gzip;q=0.8, br;q=1.0",
			want:  "gzip", // Returns first match, ignores quality params
		},
		{
			name:  "gzip with quality",
			input: "gzip;q=0.5",
			want:  "gzip",
		},
		{
			name:  "unknown encoding",
			input: "deflate",
			want:  "identity",
		},
		{
			name:  "mixed with unknown",
			input: "deflate, gzip",
			want:  "gzip",
		},
		{
			name:  "whitespace",
			input: " br , gzip ",
			want:  "br",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NegotiateEncoding(tt.input)
			if got != tt.want {
				t.Errorf("NegotiateEncoding(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNegotiateEncoding_Priority(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"brotli preferred over gzip", "br, gzip", "br"},
		{"gzip preferred over deflate", "gzip, deflate", "gzip"},
		{"wildcard returns brotli", "*", "br"},
		{"complex quality params ignored", "gzip;q=0.8, br;q=0.5", "gzip"},
		{"only deflate returns identity", "deflate", "identity"},
		{"empty returns identity", "", "identity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NegotiateEncoding(tt.input)
			if got != tt.expected {
				t.Errorf("NegotiateEncoding(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestShouldCompressContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{
			name:        "empty content type (default to compressible)",
			contentType: "",
			want:        true,
		},
		{
			name:        "text/html",
			contentType: "text/html",
			want:        true,
		},
		{
			name:        "application/json",
			contentType: "application/json",
			want:        true,
		},
		{
			name:        "application/javascript",
			contentType: "application/javascript",
			want:        true,
		},
		{
			name:        "text/css",
			contentType: "text/css",
			want:        true,
		},
		{
			name:        "application/xml",
			contentType: "application/xml",
			want:        true,
		},
		{
			name:        "application/x-www-form-urlencoded",
			contentType: "application/x-www-form-urlencoded",
			want:        true,
		},
		{
			name:        "image/jpeg (not compressible)",
			contentType: "image/jpeg",
			want:        false,
		},
		{
			name:        "image/png (not compressible)",
			contentType: "image/png",
			want:        false,
		},
		{
			name:        "video/mp4 (not compressible)",
			contentType: "video/mp4",
			want:        false,
		},
		{
			name:        "case insensitive",
			contentType: "Text/HTML",
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldCompressContentType(tt.contentType)
			if got != tt.want {
				t.Errorf("ShouldCompressContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestShouldCompressPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "html file",
			path: "/page.html",
			want: true,
		},
		{
			name: "css file",
			path: "/styles/main.css",
			want: true,
		},
		{
			name: "javascript file",
			path: "/app.js",
			want: true,
		},
		{
			name: "json file",
			path: "/api/data.json",
			want: true,
		},
		{
			name: "jpeg file (not compressible)",
			path: "/gallery/image.jpg",
			want: false,
		},
		{
			name: "jpeg uppercase (not compressible)",
			path: "/gallery/image.JPG",
			want: false,
		},
		{
			name: "png file (not compressible)",
			path: "/gallery/image.png",
			want: false,
		},
		{
			name: "gif file (not compressible)",
			path: "/gallery/image.gif",
			want: false,
		},
		{
			name: "webp file (not compressible)",
			path: "/gallery/image.webp",
			want: false,
		},
		{
			name: "svg file (not compressible)",
			path: "/icon.svg",
			want: false,
		},
		{
			name: "ico file (not compressible)",
			path: "/favicon.ico",
			want: false,
		},
		{
			name: "mp4 file (not compressible)",
			path: "/video.mp4",
			want: false,
		},
		{
			name: "webm file (not compressible)",
			path: "/video.webm",
			want: false,
		},
		{
			name: "ogg file (not compressible)",
			path: "/audio.ogg",
			want: false,
		},
		{
			name: "mp3 file (not compressible)",
			path: "/audio.mp3",
			want: false,
		},
		{
			name: "wav file (not compressible)",
			path: "/audio.wav",
			want: false,
		},
		{
			name: "zip file (not compressible)",
			path: "/download.zip",
			want: false,
		},
		{
			name: "tar file (not compressible)",
			path: "/archive.tar",
			want: false,
		},
		{
			name: "gz file (not compressible)",
			path: "/file.tar.gz",
			want: false,
		},
		{
			name: "rar file (not compressible)",
			path: "/archive.rar",
			want: false,
		},
		{
			name: "exe file (not compressible)",
			path: "/setup.exe",
			want: false,
		},
		{
			name: "dll file (not compressible)",
			path: "/library.dll",
			want: false,
		},
		{
			name: "so file (not compressible)",
			path: "/lib.so",
			want: false,
		},
		{
			name: "woff file (not compressible)",
			path: "/font.woff",
			want: false,
		},
		{
			name: "woff2 file (not compressible)",
			path: "/font.woff2",
			want: false,
		},
		{
			name: "ttf file (not compressible)",
			path: "/font.ttf",
			want: false,
		},
		{
			name: "otf file (not compressible)",
			path: "/font.otf",
			want: false,
		},
		{
			name: "no extension",
			path: "/api/endpoint",
			want: true,
		},
		{
			name: "case insensitive",
			path: "/IMAGE.JPG",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldCompressPath(tt.path)
			if got != tt.want {
				t.Errorf("ShouldCompressPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
