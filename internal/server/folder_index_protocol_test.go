package server

import (
	"context"
	"errors"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/server/files"
)

type fakeFolderIndexFlushQueries struct {
	exists bool
	err    error
}

func (f *fakeFolderIndexFlushQueries) FileFolderIndexNewExists(context.Context) (bool, error) {
	return f.exists, f.err
}

func folderIndexBW(gen, fileID int64) BatchedWrite {
	return BatchedWrite{FolderIndex: &files.FolderIndexRow{FileID: fileID, FolderID: 1, Generation: gen}}
}

func TestFolderIndexProtocol_IndexOnlyAndRebuildInactive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		rebuildActive bool
		batch         []BatchedWrite
		wantIndexOnly bool
	}{
		{
			name:          "index_only_detection_all_folder_rows_rebuild_off",
			rebuildActive: false,
			batch: []BatchedWrite{
				folderIndexBW(1, 1),
				folderIndexBW(1, 2),
			},
			wantIndexOnly: true,
		},
		{
			name:          "index_only_detection_false_when_rebuild_active",
			rebuildActive: true,
			batch:         []BatchedWrite{folderIndexBW(5, 1)},
			wantIndexOnly: false,
		},
		{
			name:          "index_only_detection_false_when_mixed_cache",
			rebuildActive: false,
			batch: []BatchedWrite{
				folderIndexBW(1, 1),
				{CacheEntry: &cachelite.HTTPCacheEntry{Key: "k"}},
			},
			wantIndexOnly: false,
		},
		{
			name:          "index_only_detection_false_when_empty_folder_slot",
			rebuildActive: false,
			batch:         []BatchedWrite{{}},
			wantIndexOnly: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p folderIndexProtocol
			p.setRebuildActive(tt.rebuildActive)
			got := p.indexOnlyAndRebuildInactive(tt.batch)
			if got != tt.wantIndexOnly {
				t.Errorf("indexOnlyAndRebuildInactive() = %v, want %v", got, tt.wantIndexOnly)
			}
		})
	}
}

func TestFolderIndexProtocol_ShouldDropIndexOnlyBatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		rebuildActive bool
		batch         []BatchedWrite
		wantDrop      bool
	}{
		{
			name:          "drop_when_rebuild_inactive_index_only_batch",
			rebuildActive: false,
			batch:         []BatchedWrite{folderIndexBW(1, 1)},
			wantDrop:      true,
		},
		{
			name:          "no_drop_when_rebuild_active_even_if_index_only",
			rebuildActive: true,
			batch:         []BatchedWrite{folderIndexBW(1, 1)},
			wantDrop:      false,
		},
		{
			name:          "no_drop_when_mixed_batch_needs_flush_path",
			rebuildActive: false,
			batch: []BatchedWrite{
				folderIndexBW(1, 1),
				{CacheEntry: &cachelite.HTTPCacheEntry{Key: "x"}},
			},
			wantDrop: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p folderIndexProtocol
			p.setRebuildActive(tt.rebuildActive)
			got := p.ShouldDropIndexOnlyBatch(tt.batch)
			if got != tt.wantDrop {
				t.Errorf("ShouldDropIndexOnlyBatch() = %v, want %v", got, tt.wantDrop)
			}
		})
	}
}

func TestFolderIndexProtocol_PrepareFolderIndexFlush(t *testing.T) {
	t.Parallel()
	const currentGen int64 = 42
	const staleGen int64 = 7

	tests := []struct {
		name          string
		rebuildActive bool
		destExists    bool
		destErr       error
		batch         []BatchedWrite
		wantLen       int
		wantFileIDs   []int64
		wantErr       bool
	}{
		{
			name:          "flush_clears_index_rows_same_as_drop_when_inactive_index_only",
			rebuildActive: false,
			destExists:    true,
			batch:         []BatchedWrite{folderIndexBW(currentGen, 10)},
			wantLen:       0,
		},
		{
			name:          "flush_inserts_when_rebuild_active_matching_generation",
			rebuildActive: true,
			destExists:    true,
			batch:         []BatchedWrite{folderIndexBW(currentGen, 10)},
			wantLen:       1,
			wantFileIDs:   []int64{10},
		},
		{
			name:          "flush_stale_generation_row_skipped_not_inserted",
			rebuildActive: true,
			destExists:    true,
			batch: []BatchedWrite{
				folderIndexBW(staleGen, 1),
				folderIndexBW(currentGen, 2),
			},
			wantLen:     1,
			wantFileIDs: []int64{2},
		},
		{
			name:          "flush_mixed_batch_skips_index_when_rebuild_inactive",
			rebuildActive: false,
			destExists:    true,
			batch: []BatchedWrite{
				folderIndexBW(currentGen, 99),
				{CacheEntry: &cachelite.HTTPCacheEntry{Key: "c"}},
			},
			wantLen: 0,
		},
		{
			name:          "flush_no_rows_when_dest_missing",
			rebuildActive: true,
			destExists:    false,
			batch:         []BatchedWrite{folderIndexBW(currentGen, 10)},
			wantLen:       0,
		},
		{
			name:          "flush_propagates_dest_exists_error",
			rebuildActive: true,
			destExists:    false,
			destErr:       errors.New("db down"),
			batch:         []BatchedWrite{folderIndexBW(currentGen, 10)},
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p folderIndexProtocol
			p.setRebuildActive(tt.rebuildActive)
			p.generation.Store(currentGen)
			q := &fakeFolderIndexFlushQueries{exists: tt.destExists, err: tt.destErr}
			params, err := p.PrepareFolderIndexFlush(context.Background(), tt.batch, q)
			if tt.wantErr {
				if err == nil {
					t.Fatal("PrepareFolderIndexFlush: expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("PrepareFolderIndexFlush: %v", err)
			}
			if len(params) != tt.wantLen {
				t.Fatalf("len(params) = %d, want %d", len(params), tt.wantLen)
			}
			if len(tt.wantFileIDs) == 0 {
				return
			}
			if len(params) != len(tt.wantFileIDs) {
				t.Fatalf("len(params) = %d, want %d file ids", len(params), len(tt.wantFileIDs))
			}
			for i, wantID := range tt.wantFileIDs {
				if params[i].FileID != wantID {
					t.Errorf("params[%d].FileID = %d, want %d", i, params[i].FileID, wantID)
				}
			}
		})
	}
}

func TestFolderIndexProtocol_OnFolderIndexBatchSuccess(t *testing.T) {
	t.Parallel()
	const currentGen int64 = 100
	const staleGen int64 = 1

	tests := []struct {
		name         string
		inflight     int64
		batch        []BatchedWrite
		wantInflight int64
	}{
		{
			name:     "on_success_decrements_inflight_for_matching_generation",
			inflight: 2,
			batch: []BatchedWrite{
				folderIndexBW(currentGen, 1),
				folderIndexBW(currentGen, 2),
			},
			wantInflight: 0,
		},
		{
			name:     "on_success_skips_stale_generation_no_inflight_change",
			inflight: 1,
			batch: []BatchedWrite{
				folderIndexBW(staleGen, 1),
			},
			wantInflight: 1,
		},
		{
			name:         "on_success_saturating_decrement_never_negative",
			inflight:     0,
			batch:        []BatchedWrite{folderIndexBW(currentGen, 1)},
			wantInflight: 0,
		},
		{
			name:     "on_success_mixed_generations_only_matching_rows_decrement",
			inflight: 3,
			batch: []BatchedWrite{
				folderIndexBW(staleGen, 1),
				folderIndexBW(currentGen, 2),
				folderIndexBW(currentGen, 3),
			},
			wantInflight: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p folderIndexProtocol
			p.generation.Store(currentGen)
			p.inflight.Store(tt.inflight)
			p.OnFolderIndexBatchSuccess(tt.batch)
			if got := p.inflightLoad(); got != tt.wantInflight {
				t.Errorf("inflight after OnFolderIndexBatchSuccess = %d, want %d", got, tt.wantInflight)
			}
		})
	}
}
