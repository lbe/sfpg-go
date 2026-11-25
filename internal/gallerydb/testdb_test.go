package gallerydb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lbe/sfpg-go/internal/testdb/gallerytest"
)

func setupTestDB(t *testing.T) (*sql.DB, *CustomQueries, context.Context) {
	t.Helper()
	db := gallerytest.SharedDB(t)
	ctx := context.Background()
	q, err := PrepareCustomQueries(ctx, db)
	if err != nil {
		t.Fatalf("PrepareCustomQueries: %v", err)
	}
	return db, q, ctx
}
