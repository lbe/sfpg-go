//go:build linux && (386 || arm || mips || mipsle)

package stat

import "syscall"

const fstatatTrap = syscall.SYS_FSTATAT64
