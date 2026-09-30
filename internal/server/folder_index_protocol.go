package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
)

// folderIndexProtocol owns atomics for folder-index rebuild lifecycle: active
// window, RO scan-held WAL gate, generation, and submit/flush inflight count.
// InfrastructureService holds one instance shared with fileBatcher and flush paths.
type folderIndexProtocol struct {
	// rebuildActive gates flushing of FolderIndex rows to file_folder_index_new.
	rebuildActive atomic.Bool
	// rebuildScanHeld is true only while RebuildFileFolderIndex holds the RO scan
	// cursor open; gates WAL TRUNCATE checkpoints.
	rebuildScanHeld atomic.Bool
	// generation is the current in-process rebuild generation; stale rows skip.
	generation atomic.Int64
	// inflight counts FolderIndex rows submitted but not yet completed for the
	// current rebuild wait (SubmitFolderIndex before OnSuccess decrement).
	inflight atomic.Int64
}

func (p *folderIndexProtocol) setRebuildActive(active bool) {
	if p == nil {
		return
	}
	p.rebuildActive.Store(active)
}

func (p *folderIndexProtocol) rebuildActiveLoad() bool {
	if p == nil {
		return false
	}
	return p.rebuildActive.Load()
}

func (p *folderIndexProtocol) setRebuildScanHeld(held bool) {
	if p == nil {
		return
	}
	p.rebuildScanHeld.Store(held)
}

func (p *folderIndexProtocol) rebuildScanHeldLoad() bool {
	if p == nil {
		return false
	}
	return p.rebuildScanHeld.Load()
}

func (p *folderIndexProtocol) generationLoad() int64 {
	if p == nil {
		return 0
	}
	return p.generation.Load()
}

// bumpGeneration sets the rebuild generation to time.Now().UnixNano() (falling
// back to prev+1 when that is 0 or equal to the previous value) and returns
// the stored generation. It never Add(1)s from 0.
func (p *folderIndexProtocol) bumpGeneration() int64 {
	if p == nil {
		return 0
	}
	prev := p.generation.Load()
	next := time.Now().UnixNano()
	if next == 0 || next == prev {
		next = prev + 1
	}
	p.generation.Store(next)
	return next
}

func (p *folderIndexProtocol) inflightLoad() int64 {
	if p == nil {
		return 0
	}
	return p.inflight.Load()
}

func (p *folderIndexProtocol) inflightAdd(delta int64) {
	if p == nil {
		return
	}
	p.inflight.Add(delta)
}

// inflightSaturatingDecrement decrements inflight by one when it is above zero.
func (p *folderIndexProtocol) inflightSaturatingDecrement() {
	if p == nil {
		return
	}
	if p.inflight.Load() > 0 {
		p.inflight.Add(-1)
	}
}

// RebuildScanHeld reports whether the RO folder-index rebuild scan cursor is open.
func (p *folderIndexProtocol) RebuildScanHeld() bool {
	return p.rebuildScanHeldLoad()
}

// indexOnlyAndRebuildInactive is the shared skip/drop classifier: every batch
// item is FolderIndex-only and rebuild is inactive. Dest-exists is checked in
// PrepareFolderIndexFlush after BeginTx.
func (p *folderIndexProtocol) indexOnlyAndRebuildInactive(batch []BatchedWrite) bool {
	if p.rebuildActiveLoad() {
		return false
	}
	for _, bw := range batch {
		if bw.FolderIndex == nil || bw.File != nil || bw.CacheEntry != nil {
			return false
		}
	}
	return true
}

// ShouldDropIndexOnlyBatch is the DropWithoutFlush classifier: drop before
// BeginTx when indexOnlyAndRebuildInactive is true.
func (p *folderIndexProtocol) ShouldDropIndexOnlyBatch(batch []BatchedWrite) bool {
	if !p.indexOnlyAndRebuildInactive(batch) {
		return false
	}
	slog.Info("writebatcher: dropping folder-index batch before BeginTx",
		"count", len(batch), "reason", "rebuild inactive")
	return true
}

type folderIndexFlushQueries interface {
	FileFolderIndexNewExists(ctx context.Context) (bool, error)
}

// PrepareFolderIndexFlush collects FolderIndex rows from the batch and applies
// rebuild-active, dest-exists, and generation filters for INSERT. It does not
// decrement inflight.
func (p *folderIndexProtocol) PrepareFolderIndexFlush(
	ctx context.Context,
	batch []BatchedWrite,
	queries folderIndexFlushQueries,
) ([]gallerydb.InsertFileFolderIndexNewParams, error) {
	folderIndexWrites := make([]*files.FolderIndexRow, 0, len(batch))
	for _, bw := range batch {
		if bw.FolderIndex != nil {
			folderIndexWrites = append(folderIndexWrites, bw.FolderIndex)
		}
	}
	if p.indexOnlyAndRebuildInactive(batch) {
		folderIndexWrites = nil
	}
	rebuildActive := p.rebuildActiveLoad()
	destExists := false
	if len(folderIndexWrites) > 0 && rebuildActive {
		var existsErr error
		destExists, existsErr = queries.FileFolderIndexNewExists(ctx)
		if existsErr != nil {
			return nil, fmt.Errorf("check file_folder_index_new existence: %w", existsErr)
		}
	}
	if len(folderIndexWrites) > 0 && (!rebuildActive || !destExists) {
		reason := "rebuild inactive"
		if rebuildActive {
			reason = "dest table missing"
		}
		slog.Error("skipping folder index writes: "+reason,
			"count", len(folderIndexWrites), "rebuild_active", rebuildActive, "dest_exists", destExists)
		return nil, nil
	}
	if !rebuildActive || !destExists || len(folderIndexWrites) == 0 {
		return nil, nil
	}
	currentGen := p.generationLoad()
	indexParams := make([]gallerydb.InsertFileFolderIndexNewParams, 0, len(folderIndexWrites))
	for _, row := range folderIndexWrites {
		if row.Generation != currentGen {
			slog.Error("skipping stale folder index write",
				"file_id", row.FileID, "row_generation", row.Generation, "current_generation", currentGen)
			continue
		}
		indexParams = append(indexParams, gallerydb.InsertFileFolderIndexNewParams{
			FileID:     row.FileID,
			FolderID:   row.FolderID,
			ImageIndex: row.ImageIndex,
			ImageCount: row.ImageCount,
			PrevID:     row.PrevID,
			NextID:     row.NextID,
			FirstID:    row.FirstID,
			LastID:     row.LastID,
		})
	}
	return indexParams, nil
}

// OnFolderIndexBatchSuccess decrements inflight for FolderIndex rows matching
// the current rebuild generation. Call before cleanup nils FolderIndex pointers.
func (p *folderIndexProtocol) OnFolderIndexBatchSuccess(batch []BatchedWrite) {
	currentGen := p.generationLoad()
	for _, bw := range batch {
		if bw.FolderIndex != nil && bw.FolderIndex.Generation == currentGen {
			p.inflightSaturatingDecrement()
		}
	}
}
