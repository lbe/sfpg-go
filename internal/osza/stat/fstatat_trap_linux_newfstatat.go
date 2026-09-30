//go:build linux && (amd64 || mips64 || mips64le || ppc64 || ppc64le || s390x)

package stat

import "syscall"

const fstatatTrap = syscall.SYS_NEWFSTATAT
