package files

import (
	"errors"
	"io/fs"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
)

// DiscoveryWalkOutcome is the walk-time catalog classification for one image file.
type DiscoveryWalkOutcome int

const (
	// DiscoveryWalkProcess means the file must be enqueued for worker processing.
	DiscoveryWalkProcess DiscoveryWalkOutcome = iota
	// DiscoveryWalkSkipUnchangedValid means the file matches an unchanged files row.
	DiscoveryWalkSkipUnchangedValid
	// DiscoveryWalkSkipUnchangedInvalid means the file matches an unchanged invalid_files row.
	DiscoveryWalkSkipUnchangedInvalid
)

// DiscoveryCompareInput holds walk metadata and catalog state for the shared
// discovery modification decision (mirrors parallelwalkdir.DirEntState validity).
type DiscoveryCompareInput struct {
	InCatalogMap bool
	WalkMtime    int64
	WalkSize     int64

	InvalidPathValid bool
	InvalidMtime     int64
	InvalidSize      int64

	FileIDValid    bool
	FileMtimeValid bool
	FileMtime      int64
	FileSizeValid  bool
	FileSize       int64
	FileMD5Valid   bool
}

// discoveryModificationDecision classifies a cataloged image file for discovery.
// Evaluation order and outcomes match the former parallelwalkdir DirEntModified gate.
func discoveryModificationDecision(in DiscoveryCompareInput) (DiscoveryWalkOutcome, error) {
	if !in.InCatalogMap {
		return DiscoveryWalkProcess, nil
	}

	if in.InvalidPathValid {
		if in.InvalidMtime == in.WalkMtime && in.InvalidSize == in.WalkSize {
			return DiscoveryWalkSkipUnchangedInvalid, nil
		}
	}

	if in.FileIDValid {
		if in.FileMtimeValid && in.FileMtime == in.WalkMtime &&
			in.FileSizeValid && in.FileSize == in.WalkSize &&
			in.FileMD5Valid {
			return DiscoveryWalkSkipUnchangedValid, nil
		}
	}

	return DiscoveryWalkProcess, nil
}

func discoveryDirEntModifiedCore(stats *ProcessingStats, info fs.FileInfo, state parallelwalkdir.DirEntState, inMap bool) (bool, error) {
	if info == nil {
		return false, errors.New("files: DiscoveryDirEntModified requires non-nil FileInfo")
	}
	in := DiscoveryCompareInput{
		InCatalogMap:     inMap,
		WalkMtime:        info.ModTime().Unix(),
		WalkSize:         info.Size(),
		InvalidPathValid: state.InvalidPathValid,
		InvalidMtime:     state.InvalidMtime,
		InvalidSize:      state.InvalidSize,
		FileIDValid:      state.FileIDValid,
		FileMtimeValid:   state.FileMtimeValid,
		FileMtime:        state.FileMtime,
		FileSizeValid:    state.FileSizeValid,
		FileSize:         state.FileSize,
		FileMD5Valid:     state.FileMD5Valid,
	}
	outcome, err := discoveryModificationDecision(in)
	if err != nil {
		return false, err
	}
	if stats != nil {
		stats.TotalFound.Add(1)
		switch outcome {
		case DiscoveryWalkSkipUnchangedValid:
			stats.AlreadyExisting.Add(1)
		case DiscoveryWalkSkipUnchangedInvalid:
			stats.SkippedInvalid.Add(1)
		}
	}
	return outcome == DiscoveryWalkProcess, nil
}

// DiscoveryDirEntModifiedWithStats returns a parallelwalkdir.CheckIfFileModifiedFunc for discovery walks with walk-time processing counters.
func DiscoveryDirEntModifiedWithStats(stats *ProcessingStats) parallelwalkdir.CheckIfFileModifiedFunc {
	return func(_ []byte, info fs.FileInfo, state parallelwalkdir.DirEntState, inMap bool) (bool, error) {
		return discoveryDirEntModifiedCore(stats, info, state, inMap)
	}
}
