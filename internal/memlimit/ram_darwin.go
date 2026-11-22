package memlimit

import "golang.org/x/sys/unix"

// totalRAM returns hw.memsize, or 0 if unknown.
func totalRAM() uint64 {
	ram, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return ram
}
