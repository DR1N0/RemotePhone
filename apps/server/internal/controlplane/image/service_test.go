package image

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/persistence/repo"

	_ "modernc.org/sqlite"
)

// fakeWorker backs an httptest.Server implementing the one worker route
// this package calls — mirrors instance/service_test.go's fakeWorkerClient
// pattern (a real HTTP server, not a hand-written interface fake), since
// Service's own captureOnWorker is a plain HTTP client, not an injectable
// interface.
type capturedRequest struct {
	RuntimeClass  string `json:"runtimeClass"`
	BaseImageID   string `json:"baseImageId"`
	SourceDataDir string `json:"sourceDataDir"`
}

type fakeWorker struct {
	capturedRequests []capturedRequest
	artifactPath     string
	failCapture      bool
}

func (f *fakeWorker) httpServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/base-images/capture", func(w http.ResponseWriter, r *http.Request) {
		var req capturedRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.capturedRequests = append(f.capturedRequests, req)
		if f.failCapture {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"artifactPath": f.artifactPath})
	})
	return httptest.NewServer(mux)
}

type fakePlacementSelector struct {
	worker  *fakeWorker
	server  *httptest.Server
	nextPID int
}

func (s *fakePlacementSelector) AcquireWorker(ctx context.Context, instanceID string) (string, int, error) {
	if s.server == nil {
		s.server = s.worker.httpServer()
	}
	s.nextPID++
	return s.server.URL, s.nextPID, nil
}

func (s *fakePlacementSelector) ReleaseWorker(ctx context.Context, pid int) error {
	return nil
}

func newTestService(t *testing.T, worker *fakeWorker) (*Service, *sql.DB) {
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
	if worker == nil {
		worker = &fakeWorker{artifactPath: "/mako/base-images/default"}
	}
	selector := &fakePlacementSelector{worker: worker}
	t.Cleanup(func() {
		if selector.server != nil {
			selector.server.Close()
		}
	})
	return NewService(db, selector), db
}

func createTestUser(t *testing.T, db *sql.DB, id, userType, status string) {
	t.Helper()
	q := repo.New(db)
	if _, err := q.CreateUser(context.Background(), repo.CreateUserParams{
		ID: id, DisplayName: id, UserType: userType, Status: status,
	}); err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
}

// createTestInstance inserts an instance row directly via repo (this
// package deliberately has no dependency on instance.Service — see
// Service's doc comment on why), optionally updating desired_state and
// state_data_ref to simulate "has been started and stopped at least once."
func createTestInstance(t *testing.T, db *sql.DB, id, ownerID, templateID, runtimeClass string, stateDataRef *string) {
	t.Helper()
	q := repo.New(db)
	if _, err := q.CreateInstance(context.Background(), repo.CreateInstanceParams{
		ID: id, Name: id, RuntimeClass: runtimeClass, TemplateID: templateID,
		PlacementPolicyJson: "{}", ResourceProfileJson: "{}", LaunchConfigJson: "{}",
		OwnerID: ownerID,
	}); err != nil {
		t.Fatalf("createTestInstance: %v", err)
	}
	if stateDataRef != nil {
		ref := sql.NullString{String: *stateDataRef, Valid: true}
		if _, err := db.Exec(`UPDATE instances SET state_data_ref = ? WHERE id = ?`, ref.String, id); err != nil {
			t.Fatalf("set state_data_ref: %v", err)
		}
	}
}

func strPtr(s string) *string { return &s }

func testActor(userID string) audit.Context {
	return audit.NewContext(&userID, nil, "req-1")
}

func TestCaptureCandidateImageSuccess(t *testing.T) {
	worker := &fakeWorker{artifactPath: "/mako/base-images/img-1"}
	svc, db := newTestService(t, worker)
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))

	got, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if err != nil {
		t.Fatalf("CaptureCandidateImage: %v", err)
	}
	if got.TemplateID != "tmpl-A" || got.SourceInstanceID != "inst-1" || got.ArtifactPath != "/mako/base-images/img-1" {
		t.Errorf("CaptureCandidateImage() = %+v, want templateId=tmpl-A sourceInstanceId=inst-1 artifactPath=/mako/base-images/img-1", got)
	}
	if len(worker.capturedRequests) != 1 || worker.capturedRequests[0].SourceDataDir != "/mako/instance-data/inst-1.avd" {
		t.Errorf("worker.capturedRequests = %+v, want one call with sourceDataDir=/mako/instance-data/inst-1.avd", worker.capturedRequests)
	}

	// A templates row must have been lazily created.
	q := repo.New(db)
	tmpl, err := q.GetTemplateByID(context.Background(), "tmpl-A")
	if err != nil {
		t.Fatalf("expected templates row to be lazily created: %v", err)
	}
	if tmpl.CurrentBaseImageID.Valid {
		t.Error("capture must not set current_base_image_id — only promote does")
	}
}

func TestCaptureCandidateImageRejectsRunningInstance(t *testing.T) {
	svc, db := newTestService(t, nil)
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))
	if _, err := db.Exec(`UPDATE instances SET desired_state = 'running' WHERE id = ?`, "inst-1"); err != nil {
		t.Fatalf("set running: %v", err)
	}

	_, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CaptureCandidateImage() error = %v, want ErrInvalidRequest", err)
	}
}

func TestCaptureCandidateImageRejectsMissingStateDataRef(t *testing.T) {
	svc, db := newTestService(t, nil)
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", nil)

	_, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CaptureCandidateImage() error = %v, want ErrInvalidRequest", err)
	}
}

func TestCaptureCandidateImageRejectsNonOwner(t *testing.T) {
	svc, db := newTestService(t, nil)
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestUser(t, db, "other-user", "registered", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))

	_, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "other-user", testActor("other-user"))
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("CaptureCandidateImage() error = %v, want ErrForbidden", err)
	}
}

func TestPromoteBaseImageUpdatesTemplateCurrentPointer(t *testing.T) {
	svc, db := newTestService(t, &fakeWorker{artifactPath: "/mako/base-images/img-1"})
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))
	captured, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if err != nil {
		t.Fatalf("CaptureCandidateImage: %v", err)
	}

	tmpl, err := svc.PromoteBaseImage(context.Background(), captured.ID, "admin-1", testActor("admin-1"))
	if err != nil {
		t.Fatalf("PromoteBaseImage: %v", err)
	}
	if tmpl.CurrentBaseImageID == nil || *tmpl.CurrentBaseImageID != captured.ID {
		t.Errorf("PromoteBaseImage() template.CurrentBaseImageID = %v, want %s", tmpl.CurrentBaseImageID, captured.ID)
	}

	path, ok, err := svc.ResolveCurrentBaseImage(context.Background(), "tmpl-A")
	if err != nil || !ok || path != "/mako/base-images/img-1" {
		t.Errorf("ResolveCurrentBaseImage() = (%q, %v, %v), want (/mako/base-images/img-1, true, nil)", path, ok, err)
	}
}

func TestPromoteBaseImageRejectsNonAdmin(t *testing.T) {
	svc, db := newTestService(t, nil)
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))
	captured, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if err != nil {
		t.Fatalf("CaptureCandidateImage: %v", err)
	}

	_, err = svc.PromoteBaseImage(context.Background(), captured.ID, "owner-1", testActor("owner-1"))
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("PromoteBaseImage() error = %v, want ErrForbidden", err)
	}
}

func TestPromoteBaseImageRollbackIsJustPromotingAnOlderCandidate(t *testing.T) {
	svc, db := newTestService(t, &fakeWorker{artifactPath: "/mako/base-images/img-1"})
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))

	old, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if err != nil {
		t.Fatalf("capture old: %v", err)
	}
	if _, err := svc.PromoteBaseImage(context.Background(), old.ID, "admin-1", testActor("admin-1")); err != nil {
		t.Fatalf("promote old: %v", err)
	}

	newer, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1"))
	if err != nil {
		t.Fatalf("capture newer: %v", err)
	}
	if _, err := svc.PromoteBaseImage(context.Background(), newer.ID, "admin-1", testActor("admin-1")); err != nil {
		t.Fatalf("promote newer: %v", err)
	}

	// Rollback: promote the OLD candidate again — same primitive, no
	// separate code path.
	tmpl, err := svc.PromoteBaseImage(context.Background(), old.ID, "admin-1", testActor("admin-1"))
	if err != nil {
		t.Fatalf("rollback promote: %v", err)
	}
	if tmpl.CurrentBaseImageID == nil || *tmpl.CurrentBaseImageID != old.ID {
		t.Errorf("after rollback, CurrentBaseImageID = %v, want %s", tmpl.CurrentBaseImageID, old.ID)
	}
}

func TestResolveCurrentBaseImageNotOKWhenNeverCaptured(t *testing.T) {
	svc, _ := newTestService(t, nil)
	path, ok, err := svc.ResolveCurrentBaseImage(context.Background(), "tmpl-never-captured")
	if err != nil {
		t.Fatalf("ResolveCurrentBaseImage: %v", err)
	}
	if ok || path != "" {
		t.Errorf("ResolveCurrentBaseImage() = (%q, %v), want (\"\", false) for an unknown template", path, ok)
	}
}

func TestResolveCurrentBaseImageNotOKWhenCapturedButNotPromoted(t *testing.T) {
	svc, db := newTestService(t, &fakeWorker{artifactPath: "/mako/base-images/img-1"})
	createTestUser(t, db, "owner-1", "registered", "active")
	createTestInstance(t, db, "inst-1", "owner-1", "tmpl-A", "android-studio-emulator", strPtr("/mako/instance-data/inst-1.avd"))
	if _, err := svc.CaptureCandidateImage(context.Background(), "inst-1", "owner-1", testActor("owner-1")); err != nil {
		t.Fatalf("CaptureCandidateImage: %v", err)
	}

	path, ok, err := svc.ResolveCurrentBaseImage(context.Background(), "tmpl-A")
	if err != nil || ok || path != "" {
		t.Errorf("ResolveCurrentBaseImage() = (%q, %v, %v), want (\"\", false, nil) — captured but never promoted", path, ok, err)
	}
}
