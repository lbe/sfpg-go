package discovery

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/server/metrics"
)

const (
	testMtime = int64(1_700_000_000)
	testSize  = int64(4096)
)

// unchangedValid is a catalog entry for a valid row that matches the file.
func unchangedValid() parallelwalkdir.DirEntState {
	return parallelwalkdir.DirEntState{
		FileIDValid:    true,
		FileMtimeValid: true,
		FileMtime:      testMtime,
		FileSizeValid:  true,
		FileSize:       testSize,
		FileMD5Valid:   true,
	}
}

// unchangedInvalid is a catalog entry for an invalid record that matches the file.
func unchangedInvalid() parallelwalkdir.DirEntState {
	return parallelwalkdir.DirEntState{
		InvalidPathValid: true,
		InvalidMtime:     testMtime,
		InvalidSize:      testSize,
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name      string
		st        parallelwalkdir.DirEntState
		inCatalog bool
		mtime     int64
		size      int64
		want      outcome
	}{
		{
			name:      "not in catalog is processed even with matching rows",
			st:        unchangedValid(),
			inCatalog: false,
			mtime:     testMtime,
			size:      testSize,
			want:      process,
		},
		{
			name:      "unchanged valid row with md5 is skipped",
			st:        unchangedValid(),
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      skipUnchangedValid,
		},
		{
			name:      "unchanged invalid record is skipped",
			st:        unchangedInvalid(),
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      skipUnchangedInvalid,
		},
		{
			name: "unchanged invalid wins over a matching valid row",
			st: parallelwalkdir.DirEntState{
				InvalidPathValid: true,
				InvalidMtime:     testMtime,
				InvalidSize:      testSize,
				FileIDValid:      true,
				FileMtimeValid:   true,
				FileMtime:        testMtime,
				FileSizeValid:    true,
				FileSize:         testSize,
				FileMD5Valid:     true,
			},
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      skipUnchangedInvalid,
		},
		{
			name: "stale invalid record does not hide a matching valid row",
			st: parallelwalkdir.DirEntState{
				InvalidPathValid: true,
				InvalidMtime:     testMtime - 100,
				InvalidSize:      testSize - 1,
				FileIDValid:      true,
				FileMtimeValid:   true,
				FileMtime:        testMtime,
				FileSizeValid:    true,
				FileSize:         testSize,
				FileMD5Valid:     true,
			},
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      skipUnchangedValid,
		},
		{
			name:      "valid row without md5 is processed",
			st:        withoutMD5(unchangedValid()),
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      process,
		},
		{
			name:      "valid row with a changed mtime is processed",
			st:        unchangedValid(),
			inCatalog: true,
			mtime:     testMtime + 1,
			size:      testSize,
			want:      process,
		},
		{
			name:      "valid row with a changed size is processed",
			st:        unchangedValid(),
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize + 1,
			want:      process,
		},
		{
			name:      "valid row with no file id is processed",
			st:        withoutFileID(unchangedValid()),
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      process,
		},
		{
			name:      "invalid record with a changed mtime is processed",
			st:        unchangedInvalid(),
			inCatalog: true,
			mtime:     testMtime + 1,
			size:      testSize,
			want:      process,
		},
		{
			name:      "catalog entry with neither row is processed",
			st:        parallelwalkdir.DirEntState{},
			inCatalog: true,
			mtime:     testMtime,
			size:      testSize,
			want:      process,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decide(tt.st, tt.inCatalog, tt.mtime, tt.size); got != tt.want {
				t.Errorf("decide() = %d, want %d", got, tt.want)
			}
		})
	}
}

func withoutMD5(st parallelwalkdir.DirEntState) parallelwalkdir.DirEntState {
	st.FileMD5Valid = false
	return st
}

func withoutFileID(st parallelwalkdir.DirEntState) parallelwalkdir.DirEntState {
	st.FileIDValid = false
	return st
}

func TestModifiedUpdatesCounters(t *testing.T) {
	dir := t.TempDir()
	info := writeFile(t, dir, "a.jpg", make([]byte, testSize), testMtime)

	tests := []struct {
		name        string
		info        bool // pass the file's info; false passes nil
		st          parallelwalkdir.DirEntState
		inCatalog   bool
		wantProcess bool
		wantErr     bool
		want        metrics.FileProcessingMetrics
	}{
		{
			name:        "new file counts as found and is processed",
			info:        true,
			wantProcess: true,
			want:        metrics.FileProcessingMetrics{TotalFound: 1},
		},
		{
			name:      "unchanged valid file counts as already existing",
			info:      true,
			st:        unchangedValid(),
			inCatalog: true,
			want:      metrics.FileProcessingMetrics{TotalFound: 1, AlreadyExisting: 1},
		},
		{
			name:      "unchanged invalid file counts as skipped invalid",
			info:      true,
			st:        unchangedInvalid(),
			inCatalog: true,
			want:      metrics.FileProcessingMetrics{TotalFound: 1, SkippedInvalid: 1},
		},
		{
			name:        "missing file info is an error and counts nothing",
			info:        false,
			wantProcess: false,
			wantErr:     true,
			want:        metrics.FileProcessingMetrics{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(t, Config{}, Deps{})
			fi := info
			if !tt.info {
				fi = nil
			}
			process, err := s.modified(nil, fi, tt.st, tt.inCatalog)
			if (err != nil) != tt.wantErr {
				t.Fatalf("modified() error = %v, wantErr %v", err, tt.wantErr)
			}
			if process != tt.wantProcess {
				t.Errorf("modified() process = %v, want %v", process, tt.wantProcess)
			}
			if got := s.deps.Stats.GetStats(); got != tt.want {
				t.Errorf("counters = %+v, want %+v", got, tt.want)
			}
		})
	}
}
