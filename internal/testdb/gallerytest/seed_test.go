package gallerytest

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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
