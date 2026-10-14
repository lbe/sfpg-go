package dbconnpool

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/migrations"
)

func TestTestDBCopyPathsLowercase(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "migration", "main.db")
	thumbsPath := filepath.Join(dir, "migration", "thumbs.db")
	schemaKey, err := migrations.LatestSchemaKey()
	if err != nil {
		t.Fatalf("schema key: %v", err)
	}
	_ = schemaKey
	if err := CopyMainTestDatabaseTo(mainPath); err != nil {
		t.Fatalf("CopyMainTestDatabaseTo: %v", err)
	}
	if err := CopyThumbsTestDatabaseTo(thumbsPath); err != nil {
		t.Fatalf("CopyThumbsTestDatabaseTo: %v", err)
	}
	for _, p := range []string{mainPath, thumbsPath} {
		db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(p)+"?_txlock=immediate&mode=rwc")
		if err != nil {
			t.Fatalf("open copy: %v", err)
		}
		if err := db.Ping(); err != nil {
			db.Close()
			t.Fatalf("ping copy: %v", err)
		}
		db.Close()
	}
}

func TestCopyMainTestDatabaseTo(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "copy.db")
	if err := CopyMainTestDatabaseTo(dest); err != nil {
		t.Fatalf("CopyMainTestDatabaseTo: %v", err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dest)+"?_txlock=immediate&mode=rwc")
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping copy: %v", err)
	}
}

func TestCopyThumbsTestDatabaseTo(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "thumbs.db")
	if err := CopyThumbsTestDatabaseTo(dest); err != nil {
		t.Fatalf("CopyThumbsTestDatabaseTo: %v", err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dest)+"?_txlock=immediate&mode=rwc")
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping copy: %v", err)
	}
}
