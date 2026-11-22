package memlimit

import "testing"

func TestParseMemTotal(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   uint64
		wantOK bool
	}{
		{"typical", "MemTotal:       16384000 kB\nMemFree:  1 kB\n", 16384000 * 1024, true},
		{"missing", "MemFree: 5 kB\n", 0, false},
		{"empty", "", 0, false},
		{"garbage", "MemTotal: abc kB\n", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseMemTotal([]byte(tt.in))
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseMemTotal = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestParseCgroupMax(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   uint64
		wantOK bool
	}{
		{"max is unlimited", "max\n", 0, false},
		{"number", "536870912\n", 536870912, true},
		{"empty", "", 0, false},
		{"garbage", "not-a-number\n", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCgroupMax([]byte(tt.in))
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseCgroupMax = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRAMFromFiles(t *testing.T) {
	meminfo := []byte("MemTotal:       16384000 kB\n")
	memTotal := uint64(16384000 * 1024)
	tests := []struct {
		name    string
		meminfo []byte
		cgroup  []byte
		want    uint64
	}{
		{"cgroup limit below meminfo", meminfo, []byte("536870912\n"), 536870912},
		{"cgroup max ignored", meminfo, []byte("max\n"), memTotal},
		{"cgroup above meminfo", meminfo, []byte("99999999999999\n"), memTotal},
		{"no cgroup file", meminfo, nil, memTotal},
		{"no meminfo, cgroup only", nil, []byte("536870912\n"), 536870912},
		{"neither readable", nil, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ramFromFiles(tt.meminfo, tt.cgroup); got != tt.want {
				t.Errorf("ramFromFiles = %d, want %d", got, tt.want)
			}
		})
	}
}
