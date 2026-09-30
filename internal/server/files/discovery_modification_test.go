package files

import (
	"io/fs"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
)

type discoveryModTestFileInfo struct {
	mtimeUnix int64
	size      int64
}

func (f *discoveryModTestFileInfo) Name() string       { return "x" }
func (f *discoveryModTestFileInfo) Size() int64        { return f.size }
func (f *discoveryModTestFileInfo) Mode() fs.FileMode  { return 0 }
func (f *discoveryModTestFileInfo) ModTime() time.Time { return time.Unix(f.mtimeUnix, 0) }
func (f *discoveryModTestFileInfo) IsDir() bool        { return false }
func (f *discoveryModTestFileInfo) Sys() any           { return nil }

func TestDiscoveryModificationDecision(t *testing.T) {
	const mtime = int64(1_700_000_000)
	const size = int64(8192)

	unchangedFile := DiscoveryCompareInput{
		InCatalogMap:   true,
		WalkMtime:      mtime,
		WalkSize:       size,
		FileIDValid:    true,
		FileMtimeValid: true,
		FileMtime:      mtime,
		FileSizeValid:  true,
		FileSize:       size,
		FileMD5Valid:   true,
	}
	unchangedInvalid := DiscoveryCompareInput{
		InCatalogMap:     true,
		WalkMtime:        mtime,
		WalkSize:         size,
		InvalidPathValid: true,
		InvalidMtime:     mtime,
		InvalidSize:      size,
	}

	tests := []struct {
		name        string
		in          DiscoveryCompareInput
		wantOutcome DiscoveryWalkOutcome
		wantMod     bool
		wantErr     bool
		nilInfo     bool
		dirEntSt    parallelwalkdir.DirEntState
		inMap       bool
	}{
		{
			name: "notInMap",
			in: DiscoveryCompareInput{
				WalkMtime: mtime,
				WalkSize:  size,
			},
			wantOutcome: DiscoveryWalkProcess,
			wantMod:     true,
		},
		{
			name:        "fileUnchanged",
			in:          unchangedFile,
			wantOutcome: DiscoveryWalkSkipUnchangedValid,
			wantMod:     false,
		},
		{
			name: "fileMtimeMismatch",
			in: func() DiscoveryCompareInput {
				in := unchangedFile
				in.FileMtime = mtime - 1
				return in
			}(),
			wantOutcome: DiscoveryWalkProcess,
			wantMod:     true,
		},
		{
			name: "fileSizeMismatch",
			in: func() DiscoveryCompareInput {
				in := unchangedFile
				in.FileSize = size - 1
				return in
			}(),
			wantOutcome: DiscoveryWalkProcess,
			wantMod:     true,
		},
		{
			name: "fileMissingMD5",
			in: func() DiscoveryCompareInput {
				in := unchangedFile
				in.FileMD5Valid = false
				return in
			}(),
			wantOutcome: DiscoveryWalkProcess,
			wantMod:     true,
		},
		{
			name:        "invalidUnchanged",
			in:          unchangedInvalid,
			wantOutcome: DiscoveryWalkSkipUnchangedInvalid,
			wantMod:     false,
		},
		{
			name: "invalidStale",
			in: func() DiscoveryCompareInput {
				in := unchangedInvalid
				in.InvalidMtime = mtime - 100
				return in
			}(),
			wantOutcome: DiscoveryWalkProcess,
			wantMod:     true,
		},
		{
			name: "invalidAndFileBothValid_staleInvalidMatchingFileUnchanged",
			in: func() DiscoveryCompareInput {
				in := unchangedFile
				in.InvalidPathValid = true
				in.InvalidMtime = mtime - 100
				in.InvalidSize = size - 1
				return in
			}(),
			wantOutcome: DiscoveryWalkSkipUnchangedValid,
			wantMod:     false,
		},
		{
			name:    "nilInfo",
			nilInfo: true,
			dirEntSt: parallelwalkdir.DirEntState{
				FileIDValid:    true,
				FileMtimeValid: true,
				FileMtime:      mtime,
				FileSizeValid:  true,
				FileSize:       size,
				FileMD5Valid:   true,
			},
			inMap:   true,
			wantMod: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkModified := DiscoveryDirEntModifiedWithStats(nil)
			if tt.nilInfo {
				modified, err := checkModified(nil, nil, tt.dirEntSt, tt.inMap)
				if (err != nil) != tt.wantErr || modified != tt.wantMod {
					t.Fatalf("modified=%v err=%v wantMod=%v wantErr=%v", modified, err, tt.wantMod, tt.wantErr)
				}
				return
			}

			outcome, err := discoveryModificationDecision(tt.in)
			if err != nil {
				t.Fatalf("DiscoveryModificationOutcome: %v", err)
			}
			if outcome != tt.wantOutcome {
				t.Fatalf("outcome=%v want %v", outcome, tt.wantOutcome)
			}
			if (outcome == DiscoveryWalkProcess) != tt.wantMod {
				t.Fatalf("modified=%v want %v", outcome == DiscoveryWalkProcess, tt.wantMod)
			}

			info := &discoveryModTestFileInfo{mtimeUnix: tt.in.WalkMtime, size: tt.in.WalkSize}
			st := parallelwalkdir.DirEntState{
				InvalidPathValid: tt.in.InvalidPathValid,
				InvalidMtime:     tt.in.InvalidMtime,
				InvalidSize:      tt.in.InvalidSize,
				FileIDValid:      tt.in.FileIDValid,
				FileMtimeValid:   tt.in.FileMtimeValid,
				FileMtime:        tt.in.FileMtime,
				FileSizeValid:    tt.in.FileSizeValid,
				FileSize:         tt.in.FileSize,
				FileMD5Valid:     tt.in.FileMD5Valid,
			}
			modified, err := checkModified(nil, info, st, tt.in.InCatalogMap)
			if err != nil {
				t.Fatalf("DiscoveryDirEntModifiedWithStats: %v", err)
			}
			if modified != tt.wantMod {
				t.Fatalf("DiscoveryDirEntModifiedWithStats modified=%v want %v", modified, tt.wantMod)
			}
		})
	}
}

func assertDiscoveryStatsConservation(t *testing.T, stats *ProcessingStats) {
	t.Helper()
	total := stats.TotalFound.Load()
	sum := stats.AlreadyExisting.Load() + stats.SkippedInvalid.Load() + stats.NewlyInserted.Load()
	if total != sum {
		t.Errorf("conservation: TotalFound=%d != AlreadyExisting(%d)+SkippedInvalid(%d)+NewlyInserted(%d)=%d",
			total, stats.AlreadyExisting.Load(), stats.SkippedInvalid.Load(), stats.NewlyInserted.Load(), sum)
	}
}

func TestDiscoveryDirEntModifiedWithStats(t *testing.T) {
	const mtime = int64(1_700_000_000)
	const size = int64(8192)
	info := &discoveryModTestFileInfo{mtimeUnix: mtime, size: size}
	st := parallelwalkdir.DirEntState{
		FileIDValid:    true,
		FileMtimeValid: true,
		FileMtime:      mtime,
		FileSizeValid:  true,
		FileSize:       size,
		FileMD5Valid:   true,
	}

	stats := &ProcessingStats{}
	check := DiscoveryDirEntModifiedWithStats(stats)
	modified, err := check(nil, info, st, true)
	if err != nil || modified {
		t.Fatalf("check: modified=%v err=%v", modified, err)
	}
	if stats.TotalFound.Load() != 1 {
		t.Fatalf("TotalFound=%d want 1", stats.TotalFound.Load())
	}
	if stats.AlreadyExisting.Load() != 1 {
		t.Fatalf("AlreadyExisting=%d want 1", stats.AlreadyExisting.Load())
	}
	assertDiscoveryStatsConservation(t, stats)
}
