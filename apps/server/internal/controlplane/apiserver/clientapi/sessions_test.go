package clientapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mako/internal/controlplane/apiserver/clientapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

type sessionTestSetup struct {
	mux         *http.ServeMux
	authSvc     *auth.Service
	instanceSvc *instance.Service
	sessionSvc  *session.Service
	secretPath  string
}

func newTestSessionSetup(t *testing.T) sessionTestSetup {
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

	secretPath := filepath.Join(t.TempDir(), "bootstrap-secret")
	authSvc, err := auth.NewService(db, secretPath)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator", DisplayName: "T1"}}}
	t.Cleanup(func() {
		if worker.server != nil {
			worker.server.Close()
		}
	})
	instanceSvc := instance.NewService(db, &fakePlacementSelector{worker: worker})
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, authSvc, keyedMutex, time.Minute)
	transportSvc := transport.NewService(db, sessionSvc, instanceSvc, keyedMutex)

	mux := http.NewServeMux()
	clientapi.RegisterInstanceRoutes(mux, instanceSvc, authSvc)
	clientapi.RegisterSessionRoutes(mux, sessionSvc, transportSvc, authSvc)
	clientapi.RegisterSessionInviteRoutes(mux, sessionSvc, authSvc)
	clientapi.RegisterSignalingRoutes(mux, sessionSvc, instanceSvc, transportSvc, authSvc)

	return sessionTestSetup{mux: mux, authSvc: authSvc, instanceSvc: instanceSvc, sessionSvc: sessionSvc, secretPath: secretPath}
}

// createRunningInstance drives the real HTTP routes to create and start an
// instance owned by whoever token belongs to, returning its ID.
func createRunningInstance(t *testing.T, mux *http.ServeMux, token string) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/instances", bytes.NewReader(createInstanceBody()))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create instance status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Instance instance.ManagedInstance `json:"instance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create instance response: %v", err)
	}

	req = httptest.NewRequest("POST", "/api/v1/instances/"+created.Instance.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start instance status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	return created.Instance.ID
}

func createSessionViaHTTP(t *testing.T, mux *http.ServeMux, token, instanceID string) (session.SessionRecord, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"instanceId": instanceID, "ownerClientId": "owner-client"})
	req := httptest.NewRequest("POST", "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Session         session.SessionRecord `json:"session"`
		AttachmentToken string                `json:"attachmentToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create session response: %v", err)
	}
	return created.Session, created.AttachmentToken
}

func TestSessionCreateGetDisconnect(t *testing.T) {
	setup := newTestSessionSetup(t)
	ownerToken := mustCreateAccount(t, setup.authSvc, setup.secretPath, "owner1")
	instanceID := createRunningInstance(t, setup.mux, ownerToken)

	record, attachmentToken := createSessionViaHTTP(t, setup.mux, ownerToken, instanceID)
	if record.Status != "active" || len(record.Attachments) != 1 {
		t.Fatalf("created session = %+v, want one active attachment", record)
	}
	ownerAttachmentID := record.Attachments[0].ID

	// Owner can read it back.
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+record.ID, nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get session (owner) status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// The bound attachment credential can also read it.
	req = httptest.NewRequest("GET", "/api/v1/sessions/"+record.ID, nil)
	req.Header.Set("Authorization", "Bearer "+attachmentToken)
	rec = httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get session (attachment) status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Disconnect by the attachment itself.
	req = httptest.NewRequest("POST", "/api/v1/sessions/"+record.ID+"/attachments/"+ownerAttachmentID+"/disconnect", nil)
	req.Header.Set("Authorization", "Bearer "+attachmentToken)
	rec = httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSessionRoutesRequireAuth(t *testing.T) {
	setup := newTestSessionSetup(t)
	req := httptest.NewRequest("POST", "/api/v1/sessions", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestGetSessionForbiddenForUnrelatedAccountIs404(t *testing.T) {
	setup := newTestSessionSetup(t)
	ownerToken := mustCreateAccount(t, setup.authSvc, setup.secretPath, "owner1")
	instanceID := createRunningInstance(t, setup.mux, ownerToken)
	record, _ := createSessionViaHTTP(t, setup.mux, ownerToken, instanceID)

	inviteToken, _, err := setup.authSvc.CreateRegistrationInvite(context.Background(), mustUserID(t, setup.authSvc, ownerToken), nil, "req-1")
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	_, otherToken, err := setup.authSvc.Register(context.Background(), inviteToken, "other_user", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/sessions/"+record.ID, nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (existence concealed), body=%s", rec.Code, rec.Body.String())
	}
}

func TestPromoteAndDemoteAttachmentViaInvite(t *testing.T) {
	setup := newTestSessionSetup(t)
	ownerToken := mustCreateAccount(t, setup.authSvc, setup.secretPath, "owner1")
	instanceID := createRunningInstance(t, setup.mux, ownerToken)
	record, _ := createSessionViaHTTP(t, setup.mux, ownerToken, instanceID)

	pin := createInviteViaHTTP(t, setup.mux, ownerToken, record.ID)
	guestAttachmentID, _ := redeemGuestInviteViaHTTP(t, setup.mux, pin, "A Friend")

	req := httptest.NewRequest("POST", "/api/v1/sessions/"+record.ID+"/attachments/"+guestAttachmentID+"/promote", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var promoted struct {
		Attachment session.AttachmentRecord `json:"attachment"`
	}
	json.Unmarshal(rec.Body.Bytes(), &promoted)
	if promoted.Attachment.Role != "participant" {
		t.Errorf("after promote, role = %q, want participant", promoted.Attachment.Role)
	}

	req = httptest.NewRequest("POST", "/api/v1/sessions/"+record.ID+"/attachments/"+guestAttachmentID+"/demote", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec = httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var demoted struct {
		Attachment session.AttachmentRecord `json:"attachment"`
	}
	json.Unmarshal(rec.Body.Bytes(), &demoted)
	if demoted.Attachment.Role != "observer" {
		t.Errorf("after demote, role = %q, want observer", demoted.Attachment.Role)
	}
}

func createInviteViaHTTP(t *testing.T, mux *http.ServeMux, ownerToken, sessionID string) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sessionID+"/invites", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invite status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Pin string `json:"pin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create invite response: %v", err)
	}
	return created.Pin
}

func redeemGuestInviteViaHTTP(t *testing.T, mux *http.ServeMux, pin, displayName string) (attachmentID, attachmentToken string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"pin": pin, "mode": "guest", "displayName": displayName})
	req := httptest.NewRequest("POST", "/api/v1/session-invites/redeem", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("redeem invite status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var redeemed struct {
		Attachment      session.AttachmentRecord `json:"attachment"`
		AttachmentToken string                   `json:"attachmentToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &redeemed); err != nil {
		t.Fatalf("decode redeem response: %v", err)
	}
	return redeemed.Attachment.ID, redeemed.AttachmentToken
}

func TestRedeemInviteRejectsGuestWithAuthorizationHeader(t *testing.T) {
	setup := newTestSessionSetup(t)
	ownerToken := mustCreateAccount(t, setup.authSvc, setup.secretPath, "owner1")
	instanceID := createRunningInstance(t, setup.mux, ownerToken)
	record, _ := createSessionViaHTTP(t, setup.mux, ownerToken, instanceID)
	pin := createInviteViaHTTP(t, setup.mux, ownerToken, record.ID)

	body, _ := json.Marshal(map[string]string{"pin": pin, "mode": "guest"})
	req := httptest.NewRequest("POST", "/api/v1/session-invites/redeem", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSignalingOfferAndICECandidate(t *testing.T) {
	setup := newTestSessionSetup(t)
	ownerToken := mustCreateAccount(t, setup.authSvc, setup.secretPath, "owner1")
	instanceID := createRunningInstance(t, setup.mux, ownerToken)
	record, attachmentToken := createSessionViaHTTP(t, setup.mux, ownerToken, instanceID)
	ownerAttachmentID := record.Attachments[0].ID

	offerBody, _ := json.Marshal(map[string]string{
		"sessionId": record.ID, "attachmentId": ownerAttachmentID, "sdpOffer": "fake-offer",
	})
	req := httptest.NewRequest("POST", "/api/v1/signaling/session", bytes.NewReader(offerBody))
	req.Header.Set("Authorization", "Bearer "+attachmentToken)
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signaling/session status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	iceBody, _ := json.Marshal(map[string]string{
		"sessionId": record.ID, "attachmentId": ownerAttachmentID, "candidate": "fake-candidate",
	})
	req = httptest.NewRequest("POST", "/api/v1/signaling/ice-candidate", bytes.NewReader(iceBody))
	req.Header.Set("Authorization", "Bearer "+attachmentToken)
	rec = httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signaling/ice-candidate status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignalingRejectsMismatchedAttachment(t *testing.T) {
	setup := newTestSessionSetup(t)
	ownerToken := mustCreateAccount(t, setup.authSvc, setup.secretPath, "owner1")
	instanceID := createRunningInstance(t, setup.mux, ownerToken)
	record, attachmentToken := createSessionViaHTTP(t, setup.mux, ownerToken, instanceID)

	offerBody, _ := json.Marshal(map[string]string{
		"sessionId": record.ID, "attachmentId": "some-other-attachment", "sdpOffer": "fake-offer",
	})
	req := httptest.NewRequest("POST", "/api/v1/signaling/session", bytes.NewReader(offerBody))
	req.Header.Set("Authorization", "Bearer "+attachmentToken)
	rec := httptest.NewRecorder()
	setup.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestRetiredSessionRoutesAreGone(t *testing.T) {
	setup := newTestSessionSetup(t)
	for _, path := range []string{"/api/v1/sessions/x/join", "/api/v1/sessions/x/resume"} {
		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		setup.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusGone {
			t.Errorf("%s status = %d, want 410", path, rec.Code)
		}
	}
}
