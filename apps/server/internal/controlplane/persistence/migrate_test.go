package persistence

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master for %s: %v", name, err)
	}
	return count == 1
}

func TestApplyMigrationsDropsWorkersAndSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if err := ApplyMigrations(db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	if tableExists(t, db, "workers") {
		t.Error("workers table still exists after migrations, want it dropped (0011)")
	}
	if tableExists(t, db, "settings") {
		t.Error("settings table still exists after migrations, want it dropped (0011)")
	}
	if !tableExists(t, db, "instances") {
		t.Error("instances table missing after migrations")
	}
}
