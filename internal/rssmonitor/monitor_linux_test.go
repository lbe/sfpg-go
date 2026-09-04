//go:build linux

package rssmonitor

import "testing"

func TestParseVmRSSKB(t *testing.T) {
	t.Parallel()

	data := []byte("Name:\tmain\nVmRSS:\t  12345 kB\nVmSize:\t 99999 kB\n")
	kb, ok := parseVmRSSKB(data)
	if !ok {
		t.Fatal("parseVmRSSKB returned ok=false")
	}
	if kb != 12345 {
		t.Fatalf("VmRSS kb = %d, want 12345", kb)
	}
}

func TestReadProcessRSS(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	rss, ok := readProcessRSS()
	if !ok {
		t.Fatal("readProcessRSS returned ok=false")
	}
	if rss == 0 {
		t.Fatal("expected non-zero rss")
	}
}
