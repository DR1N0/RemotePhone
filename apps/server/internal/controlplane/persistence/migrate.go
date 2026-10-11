// Package persistence applies the SQL migrations in migrations/ in
// filename order and hosts the sqlc-generated repository code.
package persistence

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ApplyMigrations runs every .sql file in migrations/, in filename order,
// inside a single transaction. It is safe to call on an empty database;
// it is not (yet) safe to call twice, since there is no migration-tracking
// table in this phase — that is a Phase 2 concern once real data exists.
func ApplyMigrations(db *sql.DB) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, name := range names {
		b, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := tx.Exec(string(b)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return tx.Commit()
}
