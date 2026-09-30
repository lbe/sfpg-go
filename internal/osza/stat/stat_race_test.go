//go:build linux || darwin

package stat

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

const (
	statRaceWorkers = 24
	statRaceRounds  = 40
)

func repackJoinName(joinBuf []byte, parentLen, nameLen int) {
	if nameLen > 0 {
		copy(joinBuf[parentLen:parentLen+nameLen], joinBuf[parentLen+1:parentLen+1+nameLen])
	}
}

func TestStatConcurrentAtJoin(t *testing.T) {
	runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(0)

	dir := t.TempDir()
	fileName := "data.bin"
	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	subName := "subdir"
	dirPath := filepath.Join(dir, subName)
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	linkName := "link"
	linkPath := filepath.Join(dir, linkName)
	if err := os.Symlink(filePath, linkPath); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(statRaceWorkers)
	for worker := range statRaceWorkers {
		go func(id int) {
			defer wg.Done()

			var meta FileMeta
			pathFile := copyPathBuf(filePath)
			pathDir := copyPathBuf(dirPath)
			pathLink := copyPathBuf(linkPath)
			joinFile, parentLen, nameFileLen := packJoinBuf(dir, fileName)
			joinDir, _, nameDirLen := packJoinBuf(dir, subName)
			joinLink, _, nameLinkLen := packJoinBuf(dir, linkName)

			for round := range statRaceRounds {
				kind := (id + round) % 3
				op := (id + round) % 4

				switch op {
				case 0:
					pathBuf, pathLen := pathForKind(kind, pathFile, pathDir, pathLink, len(filePath), len(dirPath), len(linkPath))
					if err := LstatAt(pathBuf, pathLen, &meta); err != nil {
						t.Errorf("worker %d round %d LstatAt: %v", id, round, err)
						continue
					}
					assertRaceLstatKind(t, id, round, kind, &meta)
				case 1:
					pathBuf, pathLen := pathForKind(kind, pathFile, pathDir, pathLink, len(filePath), len(dirPath), len(linkPath))
					if err := StatAt(pathBuf, pathLen, &meta); err != nil {
						t.Errorf("worker %d round %d StatAt: %v", id, round, err)
						continue
					}
					assertRaceStatKind(t, id, round, kind, &meta)
				case 2:
					joinBuf, nameLen := joinForKind(kind, joinFile, joinDir, joinLink, nameFileLen, nameDirLen, nameLinkLen)
					if err := LstatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
						t.Errorf("worker %d round %d LstatJoin: %v", id, round, err)
						continue
					}
					repackJoinName(joinBuf, parentLen, nameLen)
					assertRaceLstatKind(t, id, round, kind, &meta)
				case 3:
					joinBuf, nameLen := joinForKind(kind, joinFile, joinDir, joinLink, nameFileLen, nameDirLen, nameLinkLen)
					if err := StatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
						t.Errorf("worker %d round %d StatJoin: %v", id, round, err)
						continue
					}
					repackJoinName(joinBuf, parentLen, nameLen)
					assertRaceStatKind(t, id, round, kind, &meta)
				}
			}
		}(worker)
	}
	wg.Wait()
}

func pathForKind(kind int, pathFile, pathDir, pathLink []byte, fileLen, dirLen, linkLen int) ([]byte, int) {
	switch kind {
	case 0:
		return pathFile, fileLen
	case 1:
		return pathDir, dirLen
	default:
		return pathLink, linkLen
	}
}

func joinForKind(kind int, joinFile, joinDir, joinLink []byte, fileLen, dirLen, linkLen int) ([]byte, int) {
	switch kind {
	case 0:
		return joinFile, fileLen
	case 1:
		return joinDir, dirLen
	default:
		return joinLink, linkLen
	}
}

func assertRaceLstatKind(t *testing.T, worker, round, kind int, meta *FileMeta) {
	t.Helper()
	switch kind {
	case 0:
		if meta.Size() != 5 {
			t.Errorf("worker %d round %d file LstatAt size = %d", worker, round, meta.Size())
		}
	case 1:
		if !meta.IsDir() {
			t.Errorf("worker %d round %d dir LstatAt IsDir false", worker, round)
		}
	default:
		if meta.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("worker %d round %d symlink LstatAt missing ModeSymlink", worker, round)
		}
	}
}

func assertRaceStatKind(t *testing.T, worker, round, kind int, meta *FileMeta) {
	t.Helper()
	switch kind {
	case 0:
		if meta.Size() != 5 {
			t.Errorf("worker %d round %d file StatAt size = %d", worker, round, meta.Size())
		}
	case 1:
		if !meta.IsDir() {
			t.Errorf("worker %d round %d dir StatAt IsDir false", worker, round)
		}
	default:
		if meta.Mode()&fs.ModeSymlink != 0 {
			t.Errorf("worker %d round %d symlink StatAt still ModeSymlink", worker, round)
		}
		if meta.Size() != 5 {
			t.Errorf("worker %d round %d symlink StatAt size = %d", worker, round, meta.Size())
		}
	}
}
