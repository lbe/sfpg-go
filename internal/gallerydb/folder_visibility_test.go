package gallerydb

import (
	"database/sql"
	"testing"
	"time"
)

func TestSubfoldersWithoutImagesAreHidden(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	rootID := ensureRootFolderID(t, q, ctx)
	photosID := ensureFolderAtGalleryPath(t, q, ctx, "photos", rootID)
	nestedID := ensureFolderAtGalleryPath(t, q, ctx, "photos/nested", photosID)
	macosxID := ensureFolderAtGalleryPath(t, q, ctx, "__MACOSX", rootID)

	now := time.Now().Unix()
	imgPathID, err := q.UpsertFilePathReturningID(ctx, "photos/a.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	imgFile, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID: sql.NullInt64{Int64: photosID, Valid: true}, PathID: imgPathID,
		Filename: "a.jpg", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}
	for _, id := range []int64{photosID, nestedID} {
		if err = q.UpdateFolderTileId(ctx, UpdateFolderTileIdParams{
			ID: id, TileID: sql.NullInt64{Int64: imgFile.ID, Valid: true},
		}); err != nil {
			t.Fatalf("UpdateFolderTileId %d: %v", id, err)
		}
	}
	if err = q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
		Path: "__MACOSX/._a.jpg", Mtime: now, Size: 1, FolderID: macosxID,
	}); err != nil {
		t.Fatalf("UpsertInvalidFile: %v", err)
	}

	rootChildren, err := q.GetGalleryFolderThumbRowsByParentID(ctx, sql.NullInt64{Int64: rootID, Valid: true})
	if err != nil {
		t.Fatalf("GetGalleryFolderThumbRowsByParentID(root): %v", err)
	}
	if len(rootChildren) != 1 {
		t.Fatalf("root children = %d rows, want 1: %+v", len(rootChildren), rootChildren)
	}
	if rootChildren[0].ID != photosID || rootChildren[0].Name != "photos" {
		t.Fatalf("root child = {ID:%d Name:%q}, want {ID:%d Name:%q}",
			rootChildren[0].ID, rootChildren[0].Name, photosID, "photos")
	}

	counts, err := q.GetFolderInfoCountsByID(ctx, rootID)
	if err != nil {
		t.Fatalf("GetFolderInfoCountsByID(root): %v", err)
	}
	if counts.DirCount != 1 {
		t.Fatalf("root DirCount = %d, want 1", counts.DirCount)
	}

	photosChildren, err := q.GetGalleryFolderThumbRowsByParentID(ctx, sql.NullInt64{Int64: photosID, Valid: true})
	if err != nil {
		t.Fatalf("GetGalleryFolderThumbRowsByParentID(photos): %v", err)
	}
	if len(photosChildren) != 1 {
		t.Fatalf("photos children = %d rows, want 1: %+v", len(photosChildren), photosChildren)
	}
	if photosChildren[0].ID != nestedID || photosChildren[0].Name != "nested" {
		t.Fatalf("photos child = {ID:%d Name:%q}, want {ID:%d Name:%q}",
			photosChildren[0].ID, photosChildren[0].Name, nestedID, "nested")
	}
}
