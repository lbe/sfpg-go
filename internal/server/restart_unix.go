//go:build unix

package server

import "syscall"

// execReplacement replaces the current process image with path. On success it
// never returns.
func execReplacement(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
