package dque

import (
	"os"

	"github.com/lbe/sfpg-go/internal/flock"
)

var (
	// osReadDir is a testable hook for os.ReadDir used by DQue.load.
	osReadDir = os.ReadDir

	// osRemove is a testable hook for os.Remove used by qSegment.delete.
	osRemove = os.Remove

	// segmentFileWrite is a testable hook for (*os.File).Write used by qSegment.add.
	segmentFileWrite = (*os.File).Write

	// segmentFileSync is a testable hook for (*os.File).Sync used by qSegment._sync.
	segmentFileSync = (*os.File).Sync

	// segmentFileClose is a testable hook for (*os.File).Close used by qSegment.delete.
	segmentFileClose = (*os.File).Close

	// flockClose is a testable hook for (*flock.Flock).Close used by DQue.initQueue.
	flockClose = (*flock.Flock).Close
)
