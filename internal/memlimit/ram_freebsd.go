package memlimit

import "golang.org/x/sys/unix"

// totalRAM returns hw.physmem, or 0 if unknown.
func totalRAM() uint64 {
	ram, err := unix.SysctlUint64("hw.physmem")
	if err != nil {
		return 0
	}
	return ram
}
