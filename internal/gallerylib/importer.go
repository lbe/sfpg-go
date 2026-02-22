// Package gallerylib provides the business logic for importing and managing
// gallery content in the database.
package gallerylib

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.local/sfpg/internal/gallerydb"
)

// Importer provides methods for adding gallery content to the database.
type Importer struct {
	Conn *sql.Conn
	Q    *gallerydb.CustomQueries
}

// CreateRootFolderEntry creates or retrieves the root folder record in the database.
// It is idempotent and safe for concurrent calls.
// mtime is the filesystem modification time of the root directory (unix seconds).
// It returns the ID of the root folder.
func (imp *Importer) CreateRootFolderEntry(ctx context.Context, mtime int64) (int64, error) {
	// Try to find an existing root folder first to be race-safe / idempotent
	if id, err := imp.Q.GetFolderIDByPath(ctx, ""); err == nil {
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, fmt.Errorf("error checking for existing root folder: %w", err)
	}

	// Ensure the folder path row exists (UPSERT so concurrent calls are safe)
	rootFolderPathID, err := imp.Q.UpsertFolderPathReturningID(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("error upserting root folder path: %w", err)
	}

	// Upsert the folder row so repeated/concurrent calls return the same folder
	rootFolder, err := imp.Q.UpsertFolderReturningFolder(ctx,
		gallerydb.UpsertFolderReturningFolderParams{
			ParentID:  sql.NullInt64{Valid: false},
			PathID:    rootFolderPathID,
			Name:      "Gallery",
			Mtime:     sql.NullInt64{Int64: mtime, Valid: true},
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
	if err != nil {
		return 0, fmt.Errorf("error upserting root folder: %w", err)
	}

	// Defensive: if the upsert didn't return an ID for some reason, select it
	if rootFolder.ID == 0 {
		id, err := imp.Q.GetFolderIDByPath(ctx, "")
		if err != nil {
			return 0, fmt.Errorf("failed to obtain root folder id after upsert: %w", err)
		}
		return id, nil
	}

	return rootFolder.ID, nil
}

// UpsertPathChain ensures that all intermediate folders and the file record exist.
// It returns the final file record.
func (imp *Importer) UpsertPathChain(ctx context.Context, path string, mtime, size int64, md5 string, phash, width, height int64, mimeType string) (gallerydb.File, error) {
	// Normalize to forward-slash form for consistent DB storage across platforms
	path = filepath.ToSlash(filepath.Clean(path))

	// Insert path record for the file path itself
	fpID, err := imp.Q.UpsertFilePathReturningID(ctx, path)
	if err != nil {
		slog.Error("error upserting file path", "path", path, "err", err)
		return gallerydb.File{}, err
	}

	// Get or create the root folder.
	rootFolderID, err := imp.Q.GetFolderIDByPath(ctx, "")
	if err != nil {
		if err == sql.ErrNoRows {
			// No root folder exists; create it with current time as mtime
			rootFolderID, err = imp.CreateRootFolderEntry(ctx, time.Now().Unix())
			if err != nil {
				return gallerydb.File{}, fmt.Errorf("failed to create root folder entries: %w", err)
			}
		} else {
			// A different error occurred
			return gallerydb.File{}, fmt.Errorf("error getting root folder ID: %w", err)
		}
	}

	// Now, for any other folder, its parent will be the root folder (ID 1) if it's a top-level folder.
	currentParentID := sql.NullInt64{Int64: rootFolderID, Valid: true}

	dir := filepath.Dir(path)
	// Handle root-level files (e.g., "image.jpg")
	if dir != "." && dir != "/" {
		// Process the actual path segments for subfolders
		parts := strings.Split(filepath.ToSlash(filepath.Clean(dir)), "/")

		pathAccum := ""
		if strings.HasPrefix(filepath.ToSlash(filepath.Clean(dir)), "/") {
			pathAccum = "/"
		}

		for _, p := range parts {
			if p == "" { // Skip empty segments (e.g., from leading / or //)
				continue
			}
			// Use forward slashes for consistent path storage and querying across platforms
			if pathAccum == "" || pathAccum == "/" {
				pathAccum += p
			} else {
				pathAccum += "/" + p
			}

			folder, folderErr := imp.Q.GetFolderByPath(ctx, pathAccum)
			if folderErr != nil && folderErr != sql.ErrNoRows {
				return gallerydb.File{}, fmt.Errorf("error checking if folder exists: %w", folderErr)
			}

			if folderErr == sql.ErrNoRows { // Folder does not exist, create it
				pathID, upsertErr := imp.Q.UpsertFolderPathReturningID(ctx, pathAccum)
				if upsertErr != nil {
					return gallerydb.File{}, fmt.Errorf("error upserting folder path: %w", upsertErr)
				}

				// Extract folder mtime from filesystem
				folderMtime := time.Now().Unix()
				if stat, statErr := os.Stat(pathAccum); statErr == nil {
					folderMtime = stat.ModTime().Unix()
				}

				folder, err = imp.Q.UpsertFolderReturningFolder(ctx,
					gallerydb.UpsertFolderReturningFolderParams{
						ParentID:  currentParentID, // Use the parent from the previous iteration
						PathID:    pathID,
						Name:      p,
						Mtime:     sql.NullInt64{Int64: folderMtime, Valid: true},
						CreatedAt: time.Now().Unix(),
						UpdatedAt: time.Now().Unix(),
					})
				if err != nil {
					return gallerydb.File{}, fmt.Errorf("error upserting folder: %w", err)
				}
			}
			currentParentID = sql.NullInt64{Int64: folder.ID, Valid: true} // Update for the next iteration
		}
	}

	filename := filepath.Base(path)
	file, err := imp.Q.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  currentParentID, // Use the final currentParentID for the file's folder
		PathID:    fpID,            // Use the fpID for the file itself
		Filename:  filename,
		Mtime:     sql.NullInt64{Int64: mtime, Valid: true},
		SizeBytes: sql.NullInt64{Int64: size, Valid: true},
		Md5:       sql.NullString{String: md5, Valid: true},
		Phash:     sql.NullInt64{Int64: phash, Valid: true},
		MimeType:  sql.NullString{String: mimeType, Valid: true},
		Width:     sql.NullInt64{Int64: width, Valid: true},
		Height:    sql.NullInt64{Int64: height, Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		slog.Error("error upserting file", "path", path, "err", err)
		return gallerydb.File{}, err
	}
	return file, nil
}

// UpdateFolderTileChain updates the tile_id for a given folder and recursively
// ascends the folder hierarchy. It sets the `tile_id` for each parent folder
// to the provided `tileFileID` until it reaches a folder that already has a
// tile or it reaches the root of the gallery.
// The `tileFileID` parameter must be an ID from the `files` table.
func (imp *Importer) UpdateFolderTileChain(ctx context.Context, folderID, tileFileID int64) error {
	currentFolderID := folderID
	for {
		// Retrieve the current folder
		folder, err := imp.Q.GetFolderByID(ctx, currentFolderID)
		if err != nil {
			// If the folder is not found, or any other error occurs, stop the chain.
			// This might happen if a folder was deleted concurrently.
			if err == sql.ErrNoRows {
				return nil
			}
			slog.Error("error retrieving folder", "folder_id", currentFolderID, "err", err)
			return err
		}

		// If the current folder already has a tile_id, stop the chain.
		if folder.TileID.Valid {
			return nil
		}

		// Update the tile_id for the current folder
		err = imp.Q.UpdateFolderTileId(ctx, gallerydb.UpdateFolderTileIdParams{
			TileID: sql.NullInt64{Int64: tileFileID, Valid: true},
			ID:     currentFolderID,
		})
		if err != nil {
			slog.Error("error updating folder tile_id", "folder_id", currentFolderID, "thumbnail_id", tileFileID, "err", err)
			return err
		}

		// If there is no parent, we've reached the root, so stop the chain.
		if !folder.ParentID.Valid {
			return nil
		}

		// Move up to the parent folder
		currentFolderID = folder.ParentID.Int64
	}
}
