//go:build windows

package readdir

import "io/fs"

// Info returns FileInfo populated during ReadDir.
func (e *Entry) Info(_ []byte) (fs.FileInfo, error) {
	if !e.hasMeta {
		return nil, fs.ErrInvalid
	}
	return &e.meta, nil
}
