//go:build linux || darwin

package readdir

import (
	"io/fs"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

// Info returns FileInfo for the entry via stat.LstatJoin into reused meta.
// joinBuf must satisfy stat join layout for the entry directory path and name.
// Call on the slice element so cached meta is stored: entries[i].Info(joinBuf).
// Do not call on a copied Entry. Do not retain the returned fs.FileInfo after
// the next ReadDir that reuses the same entries slot (it aliases entry storage).
func (e *Entry) Info(joinBuf []byte) (fs.FileInfo, error) {
	if e.hasMeta {
		return &e.meta, nil
	}
	parent := e.dirPath[:e.dirLen]
	parentLen := e.dirLen
	nameLen := len(e.Name)
	if err := packJoin(joinBuf, parent, e.Name); err != nil {
		return nil, err
	}
	if err := stat.LstatJoin(joinBuf, parentLen, nameLen, &e.meta); err != nil {
		return nil, err
	}
	e.hasMeta = true
	e.typ = e.meta.Mode().Type()
	return &e.meta, nil
}

func packJoin(joinBuf []byte, parent, name []byte) error {
	parentLen := len(parent)
	nameLen := len(name)
	if parentLen+1+nameLen+1 > len(joinBuf) {
		return stat.ErrPathBuffer
	}
	copy(joinBuf[:parentLen], parent)
	copy(joinBuf[parentLen:parentLen+nameLen], name)
	return nil
}
