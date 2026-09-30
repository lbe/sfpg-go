package files

import (
	"context"
	"database/sql"
	"errors"
	"path"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
)

func galleryDirPrefixFromReported(
	reportedDir []byte,
	normalizedImagesDir string,
	removePrefix func(string, string) (string, error),
) (string, error) {
	full := string(reportedDir)
	rel, err := removePrefix(normalizedImagesDir, full)
	if err != nil {
		return "", err
	}
	return rel, nil
}

func discoveryFileRowToDirEnt(row gallerydb.ListDiscoveryFilesByFolderIDRow) parallelwalkdir.DirEntState {
	var st parallelwalkdir.DirEntState
	st.FileIDValid = true
	st.FileMtimeValid = row.FileMtime.Valid
	if row.FileMtime.Valid {
		st.FileMtime = row.FileMtime.Int64
	}
	st.FileSizeValid = row.FileSizeBytes.Valid
	if row.FileSizeBytes.Valid {
		st.FileSize = row.FileSizeBytes.Int64
	}
	st.FileMD5Valid = row.FileMd5.Valid
	return st
}

func mergeInvalidIntoDirEnt(st parallelwalkdir.DirEntState, row gallerydb.ListDiscoveryInvalidByFolderIDRow) parallelwalkdir.DirEntState {
	st.InvalidPathValid = true
	st.InvalidMtime = row.InvalidMtime
	st.InvalidSize = row.InvalidSize
	return st
}

func discoveryInvalidRowToDirEnt(row gallerydb.ListDiscoveryInvalidByFolderIDRow) parallelwalkdir.DirEntState {
	var st parallelwalkdir.DirEntState
	return mergeInvalidIntoDirEnt(st, row)
}

func buildDirEntMapFromRows(
	fileRows []gallerydb.ListDiscoveryFilesByFolderIDRow,
	invalidRows []gallerydb.ListDiscoveryInvalidByFolderIDRow,
) map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState {
	m := make(map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, len(fileRows)+len(invalidRows))
	for _, row := range fileRows {
		key := parallelwalkdir.HashPathBytes([]byte(row.FileFilename))
		m[key] = discoveryFileRowToDirEnt(row)
	}
	for _, row := range invalidRows {
		basename := path.Base(row.InvalidPath)
		key := parallelwalkdir.HashPathBytes([]byte(basename))
		if existing, ok := m[key]; ok {
			m[key] = mergeInvalidIntoDirEnt(existing, row)
			continue
		}
		m[key] = discoveryInvalidRowToDirEnt(row)
	}
	return m
}

// NewDiscoveryDirEntMapFunc returns a per-directory catalog loader for discovery walks.
func NewDiscoveryDirEntMapFunc(
	roPool *dbconnpool.DbSQLConnPool,
	normalizedImagesDir string,
	removePrefix func(string, string) (string, error),
) parallelwalkdir.GetDirEntMapFunc {
	return func(ctx context.Context, reportedDir []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		galleryDir, err := galleryDirPrefixFromReported(reportedDir, normalizedImagesDir, removePrefix)
		if err != nil {
			return nil, err
		}
		cpc, err := roPool.Get()
		if err != nil {
			return nil, err
		}
		defer roPool.Put(cpc)

		folderID, err := cpc.Queries.GetFolderIDByPath(ctx, galleryDir)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{}, nil
			}
			return nil, err
		}

		folderIDParam := sql.NullInt64{Int64: folderID, Valid: true}
		fileRows, err := cpc.Queries.ListDiscoveryFilesByFolderID(ctx, folderIDParam)
		if err != nil {
			return nil, err
		}
		invalidRows, err := cpc.Queries.ListDiscoveryInvalidByFolderID(ctx, folderID)
		if err != nil {
			return nil, err
		}
		return buildDirEntMapFromRows(fileRows, invalidRows), nil
	}
}
