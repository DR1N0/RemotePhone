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

func TestCreateAndGetInstance(t *testing.T) {
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
	created, err := q.CreateInstance(ctx, repo.CreateInstanceParams{
		ID:                  "i-1",
		Name:                "my-instance",
		RuntimeClass:        "android-studio-emulator",
		TemplateID:          "t-1",
		PlacementPolicyJson: `{"kind":"local-only"}`,
		ResourceProfileJson: `{}`,
		LaunchConfigJson:    `{}`,
		OwnerID:             "u-1",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if created.DesiredState != "stopped" || created.ObservedState != "stopped" {
		t.Errorf("CreateInstance() = %+v, want desired_state=stopped observed_state=stopped", created)
	}

	got, err := q.GetInstanceByID(ctx, "i-1")
	if err != nil {
		t.Fatalf("GetInstanceByID: %v", err)
	}
	if got.ID != "i-1" || got.OwnerID != "u-1" {
		t.Errorf("GetInstanceByID() = %+v, want id=i-1 owner_id=u-1", got)
	}

	if _, err := q.CreateInstance(ctx, repo.CreateInstanceParams{
		ID:                  "i-2",
		Name:                "second-instance",
		RuntimeClass:        "android-studio-emulator",
		TemplateID:          "t-1",
		PlacementPolicyJson: `{"kind":"local-only"}`,
		ResourceProfileJson: `{}`,
		LaunchConfigJson:    `{}`,
		OwnerID:             "u-1",
	}); err == nil {
		t.Error("CreateInstance() with a second instance for the same owner succeeded, want unique-owner constraint violation")
	}
}
