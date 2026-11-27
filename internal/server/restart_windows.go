//go:build windows

package server

import "os"

// execReplacement starts a replacement process for path and returns once it
// has started. Windows has no exec: the caller must exit afterwards. The child
// inherits stdin, stdout and stderr.
func execReplacement(path string, args, env []string) error {
	proc, err := os.StartProcess(path, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return err
	}
	return proc.Release()
}
