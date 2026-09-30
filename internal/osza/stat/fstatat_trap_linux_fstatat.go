//go:build linux && (arm64 || riscv64)

package stat

import "syscall"

const fstatatTrap = syscall.SYS_FSTATAT
