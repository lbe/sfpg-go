package memlimit

import (
	"os"
	"strconv"
	"strings"
)

const (
	meminfoPath   = "/proc/meminfo"
	cgroupMaxPath = "/sys/fs/cgroup/memory.max"
)

// totalRAM returns the smaller of physical memory and the cgroup v2 limit, or 0 if unknown.
func totalRAM() uint64 {
	return ramFromFiles(readOptional(meminfoPath), readOptional(cgroupMaxPath))
}

// readOptional returns the file contents, or nil if it cannot be read.
func readOptional(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// ramFromFiles combines /proc/meminfo and memory.max contents. A missing or
// "max" cgroup limit is ignored.
func ramFromFiles(meminfo, cgroupMax []byte) uint64 {
	ram, _ := parseMemTotal(meminfo)
	if limit, ok := parseCgroupMax(cgroupMax); ok && (ram == 0 || limit < ram) {
		ram = limit
	}
	return ram
}

// parseMemTotal returns MemTotal from /proc/meminfo content in bytes.
func parseMemTotal(data []byte) (uint64, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "MemTotal:" {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// parseCgroupMax returns the numeric cgroup v2 memory.max value; "max" or garbage reports false.
func parseCgroupMax(data []byte) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
