package audit

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/persistence/repo"

	_ "modernc.org/sqlite"
)

func newTestQueries(t *testing.T) (*repo.Queries, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := persistence.ApplyMigrations(db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return repo.New(db), db
}

// insertEventAt writes an audit_events row with an explicit id/created_at,
// bypassing Record/CURRENT_TIMESTAMP, so pagination tests can control
// ordering deterministically instead of racing SQLite's second-resolution
// CURRENT_TIMESTAMP across fast successive inserts.
func insertEventAt(t *testing.T, db *sql.DB, id, action, createdAt string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO audit_events (id, action, target_type, target_id, request_id, outcome, created_at)
		 VALUES (?, ?, 'test', 'target-1', ?, 'success', ?)`,
		id, action, "req-"+id, createdAt,
	)
	if err != nil {
		t.Fatalf("insertEventAt(%s): %v", id, err)
	}
}

func TestRecordThenListRoundTrips(t *testing.T) {
	q, _ := newTestQueries(t)
	ctx := NewContext(nil, nil, "req-1")
	if err := Record(context.Background(), q, ctx, "user.ban", "user", "u-1", OutcomeSuccess); err != nil {
		t.Fatalf("Record: %v", err)
	}

	items, nextCursor, err := List(context.Background(), q, 10, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if nextCursor != nil {
		t.Errorf("nextCursor = %v, want nil", *nextCursor)
	}
	if len(items) != 1 || items[0].Action != "user.ban" || items[0].TargetID != "u-1" || items[0].RequestID != "req-1" {
		t.Fatalf("List() = %+v, want one user.ban/u-1/req-1 event", items)
	}
	if items[0].ActorUserID != nil {
		t.Errorf("ActorUserID = %v, want nil (unauthenticated actor)", *items[0].ActorUserID)
	}
}

func TestListReturnsNewestFirst(t *testing.T) {
	q, db := newTestQueries(t)
	insertEventAt(t, db, "e1", "first", "2026-01-01T00:00:00Z")
	insertEventAt(t, db, "e2", "second", "2026-01-01T00:00:01Z")

	items, nextCursor, err := List(context.Background(), q, 10, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if nextCursor != nil {
		t.Errorf("nextCursor = %v, want nil", *nextCursor)
	}
	if len(items) != 2 || items[0].Action != "second" || items[1].Action != "first" {
		t.Fatalf("List() = %+v, want [second first]", items)
	}
}

func TestListPaginatesByCursor(t *testing.T) {
	q, db := newTestQueries(t)
	insertEventAt(t, db, "e1", "a", "2026-01-01T00:00:00Z")
	insertEventAt(t, db, "e2", "b", "2026-01-01T00:00:01Z")
	insertEventAt(t, db, "e3", "c", "2026-01-01T00:00:02Z")

	page1, cursor1, err := List(context.Background(), q, 2, nil)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page1) != 2 || page1[0].Action != "c" || page1[1].Action != "b" || cursor1 == nil {
		t.Fatalf("List page 1 = %+v, cursor=%v, want [c b] with a cursor", page1, cursor1)
	}

	page2, cursor2, err := List(context.Background(), q, 2, cursor1)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page2) != 1 || page2[0].Action != "a" {
		t.Fatalf("List page 2 = %+v, want [a]", page2)
	}
	if cursor2 != nil {
		t.Errorf("cursor2 = %v, want nil (no more pages)", *cursor2)
	}
}

func TestListRejectsInvalidCursor(t *testing.T) {
	q, db := newTestQueries(t)
	insertEventAt(t, db, "e1", "only", "2026-01-01T00:00:00Z")

	bogus := "does-not-exist"
	_, _, err := List(context.Background(), q, 10, &bogus)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("List() error = %v, want ErrInvalidCursor", err)
	}
}

func TestListRejectsInvalidLimit(t *testing.T) {
	q, _ := newTestQueries(t)

	for _, limit := range []int{0, -1, 101} {
		if _, _, err := List(context.Background(), q, limit, nil); !errors.Is(err, ErrInvalidLimit) {
			t.Errorf("List(limit=%d) error = %v, want ErrInvalidLimit", limit, err)
		}
	}
}
