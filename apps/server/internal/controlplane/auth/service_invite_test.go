package auth

import (
	"context"
	"testing"
	"time"
)

func TestIsValidUsername(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"Admin_One", "admin_one", true},
		{"  spaced_out  ", "spaced_out", true},
		{"ab", "", false},                                 // too short (min 3)
		{"this_name_is_absolutely_too_long_x", "", false}, // over 32 chars
		{"has space", "", false},
		{"has-dash", "", false},
	}
	for _, c := range cases {
		got, ok := isValidUsername(c.input)
		if ok != c.ok {
			t.Errorf("isValidUsername(%q) ok = %v, want %v", c.input, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("isValidUsername(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestIsValidPassword(t *testing.T) {
	if isValidPassword("7chars!") {
		t.Error("isValidPassword accepted a 7-char password (below the 8-char minimum)")
	}
	if !isValidPassword("8charsss") {
		t.Error("isValidPassword rejected an 8-char password (the minimum, should be accepted)")
	}
}

func TestCreateRegistrationInviteRequiresActiveAdmin(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "regular1", "registered", "active")

	_, _, err := svc.CreateRegistrationInvite(context.Background(), "regular1", nil, "req1")
	if err == nil {
		t.Fatal("CreateRegistrationInvite succeeded for a non-admin user")
	}
}

func TestCreateRegistrationInviteIsAFourDigitCodeValidFiveMinutes(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")

	code, expiresAt, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1")
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	if len(code) != 4 {
		t.Errorf("invite code = %q, want exactly 4 digits", code)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Errorf("invite code = %q, want all-numeric", code)
			break
		}
	}
	wantAround := svc.now().Add(5 * time.Minute)
	if diff := expiresAt.Sub(wantAround); diff < -time.Second || diff > time.Second {
		t.Errorf("expiresAt = %v, want approximately now+5m (%v)", expiresAt, wantAround)
	}
}

func TestCreateRegistrationInviteWritesAuditEventAtomically(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")

	_, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req-audit-1")
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}

	var count int
	row := svc.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'registration.invite.issue' AND request_id = 'req-audit-1'`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	if count != 1 {
		t.Errorf("found %d audit_events rows for the issued invite, want exactly 1", count)
	}
}
