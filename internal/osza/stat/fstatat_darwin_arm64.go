//go:build darwin && arm64

package stat

import _ "unsafe" // for go:cgo_import_dynamic

// libcFstatatTrampolineAddr is set by fstatat_darwin_arm64.s to the address of
// the trampoline that jumps to libSystem's fstatat. darwin/arm64 has no legacy
// 32-bit inode stat variants, so fstatat matches syscall.Stat_t.
var libcFstatatTrampolineAddr uintptr

//go:cgo_import_dynamic libc_fstatat fstatat "/usr/lib/libSystem.B.dylib"
