//go:build !linux && !darwin && !windows && !freebsd

package memlimit

// totalRAM reports unknown RAM on platforms without a reader.
func totalRAM() uint64 { return 0 }
