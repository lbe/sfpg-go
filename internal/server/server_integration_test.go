//go:build integration

package server

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestAppRouterOnRealListener(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	router := app.getRouter()

	// Start a real listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: router}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Create a folder in the test database that we can request
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	pathID, err := cpc.Queries.UpsertFolderPathReturningID(app.ctx, "/test-listener-folder")
	if err != nil {
		t.Fatalf("failed to insert folder path: %v", err)
	}
	folder, err := cpc.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Valid: false},
		PathID:    pathID,
		Name:      "listener-folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to insert folder: %v", err)
	}

	// Create an authenticated cookie using the same helper as tests
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	session, _ := app.store.Get(req, "session-name")
	session.Values["authenticated"] = true
	err = session.Save(req, rr)
	if err != nil {
		t.Fatalf("failed to save session: %v", err)
	}
	cookie := rr.Result().Cookies()[0]

	url := "http://" + ln.Addr().String() + fmt.Sprintf("/gallery/%d", folder.ID)
	client := &http.Client{}
	req2, _ := http.NewRequest("GET", url, nil)
	req2.AddCookie(cookie)

	resp, err := client.Do(req2)
	if err != nil {
		t.Fatalf("http GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 OK from app router, got %d", resp.StatusCode)
	}
}
