//go:build darwin && amd64

package stat

import _ "unsafe" // for go:cgo_import_dynamic

// libcFstatatTrampolineAddr is set by fstatat_darwin_amd64.s to the address of
// the trampoline that jumps to libSystem's fstatat64. On darwin/amd64 the plain
// fstatat symbol carries the legacy 32-bit inode layout; only the 64 variant
// matches syscall.Stat_t.
var libcFstatatTrampolineAddr uintptr

//go:cgo_import_dynamic libc_fstatat64 fstatat64 "/usr/lib/libSystem.B.dylib"
