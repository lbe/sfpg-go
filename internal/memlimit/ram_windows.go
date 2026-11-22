package memlimit

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// totalRAM returns total physical memory, or 0 if unknown.
func totalRAM() uint64 {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	if ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 0
	}
	return status.TotalPhys
}
