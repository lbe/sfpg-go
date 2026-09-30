// Example: grow buffers and retry osza.ReadDir until the listing is complete.
package main

import (
	"errors"
	"log"
	"os"

	"github.com/lbe/sfpg-go/internal/osza"
)

const maxEntries = 1 << 20
const maxNameBytes = 64 << 20
const maxJoinBytes = 64 << 20

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	entries := make([]osza.Entry, 16)
	dirPath := make([]byte, len(dir)+1)
	dirLen, err := osza.CopyDirPath(dirPath, dir)
	if err != nil {
		log.Fatal(err)
	}
	nameBuf := make([]byte, 4096)
	scratch := make([]byte, 8192)
	joinBuf := make([]byte, 64<<10)

	for {
		n, err := osza.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
		if err == nil {
			log.Printf("read %d entries from %s", n, dir)
			return
		}
		if errors.Is(err, osza.ErrOverflow) {
			if len(entries)*2 > maxEntries {
				log.Fatalf("entry cap %d exceeded (partial n=%d)", maxEntries, n)
			}
			if len(nameBuf)*2 > maxNameBytes {
				log.Fatalf("nameBuf cap %d exceeded (partial n=%d)", maxNameBytes, n)
			}
			entries = make([]osza.Entry, len(entries)*2)
			nameBuf = make([]byte, len(nameBuf)*2)
			continue
		}
		if errors.Is(err, osza.ErrPathBuffer) {
			if len(joinBuf)*2 > maxJoinBytes {
				log.Fatalf("joinBuf cap %d exceeded (partial n=%d)", maxJoinBytes, n)
			}
			joinBuf = make([]byte, len(joinBuf)*2)
			continue
		}
		log.Fatal(err)
	}
}
