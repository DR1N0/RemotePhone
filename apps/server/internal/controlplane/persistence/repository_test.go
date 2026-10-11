package persistence_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/persistence/repo"

	_ "modernc.org/sqlite"
)

func TestCreateAndGetWorker(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if err := persistence.ApplyMigrations(db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	q := repo.New(db)
	ctx := context.Background()
	if err := q.CreateWorker(ctx, repo.CreateWorkerParams{ID: "w-1", Kind: "local"}); err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	got, err := q.GetWorker(ctx, "w-1")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if got.ID != "w-1" || got.Kind != "local" || got.Phase != "pending" {
		t.Errorf("GetWorker() = %+v, want id=w-1 kind=local phase=pending", got)
	}
}
