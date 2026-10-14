package dbconnpool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ncruces/go-sqlite3"

	"github.com/lbe/sfpg-go/internal/testdb"
	"github.com/lbe/sfpg-go/migrations"
)

// CopyMainTestDatabaseTo creates a file-backed copy of the main test
// database at destPath via a short-lived memory database pair.
// For tests and local dev only; not for production.
func CopyMainTestDatabaseTo(destPath string) error {
	return copyTestImageTo(destPath, false)
}

// CopyThumbsTestDatabaseTo creates a file-backed copy of the thumbs test
// database at destPath via a short-lived memory database pair.
// For tests and local dev only; not for production.
func CopyThumbsTestDatabaseTo(destPath string) error {
	return copyTestImageTo(destPath, true)
}

func copyTestImageTo(destPath string, thumbs bool) (err error) {
	ctx := context.Background()
	schemaKey, err := migrations.LatestSchemaKey()
	if err != nil {
		return fmt.Errorf("resolve schema key: %w", err)
	}
	md, err := testdb.NewMemoryDatabases(ctx, schemaKey)
	if err != nil {
		return fmt.Errorf("create test databases: %w", err)
	}
	defer func() {
		if closeErr := md.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close test databases: %w", closeErr))
		}
	}()

	var srcURI string
	if thumbs {
		srcURI, err = md.ThumbsURI(ctx, true)
		if err != nil {
			return fmt.Errorf("thumbs test database URI: %w", err)
		}
	} else {
		srcURI = md.MainURI(true)
	}

	if err := backupImageToFile(ctx, srcURI, destPath); err != nil {
		return err
	}
	return nil
}

// backupImageToFile copies the "main" database of the memory image at srcURI
// to a file-backed SQLite database at destPath using the SQLite backup API.
func backupImageToFile(ctx context.Context, srcURI, destPath string) (err error) {
	if mkdirErr := os.MkdirAll(filepath.Dir(destPath), 0o755); mkdirErr != nil {
		return fmt.Errorf("create test database dir: %w", mkdirErr)
	}
	_ = os.Remove(destPath)

	src, err := sqlite3.OpenContext(ctx, srcURI)
	if err != nil {
		return fmt.Errorf("open test database image: %w", err)
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close test database image: %w", closeErr))
		}
	}()

	dstURI := "file:" + filepath.ToSlash(destPath)
	if err := src.Backup("main", dstURI); err != nil {
		_ = os.Remove(destPath)
		return fmt.Errorf("backup test database to file: %w", err)
	}
	return nil
}
