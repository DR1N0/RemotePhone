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
	if isValidPassword("short") {
		t.Error("isValidPassword accepted an 11-char password (below the 12-char minimum)")
	}
	if !isValidPassword("exactly12chr") {
		t.Error("isValidPassword rejected a 12-char password (the minimum, should be accepted)")
	}
}

func TestCreateRegistrationInviteRequiresActiveAdmin(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "regular1", "registered", "active")

	_, _, err := svc.CreateRegistrationInvite(context.Background(), "regular1", nil, "req1", nil)
	if err == nil {
		t.Fatal("CreateRegistrationInvite succeeded for a non-admin user")
	}
}

func TestCreateRegistrationInviteDefaultsAndValidatesTTL(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")

	_, expiresAt, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite (default ttl): %v", err)
	}
	wantAround := svc.now().Add(24 * time.Hour)
	if diff := expiresAt.Sub(wantAround); diff < -time.Second || diff > time.Second {
		t.Errorf("default ttl expiresAt = %v, want approximately now+24h (%v)", expiresAt, wantAround)
	}

	tooShort := int64(4 * time.Minute / time.Millisecond)
	if _, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req2", &tooShort); err == nil {
		t.Error("CreateRegistrationInvite accepted a ttl under 5 minutes")
	}

	exactlyFiveMin := int64(5 * time.Minute / time.Millisecond)
	if _, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req3", &exactlyFiveMin); err != nil {
		t.Errorf("CreateRegistrationInvite rejected exactly 5 minutes (the inclusive minimum): %v", err)
	}

	exactlySevenDays := int64(7 * 24 * time.Hour / time.Millisecond)
	if _, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req4", &exactlySevenDays); err != nil {
		t.Errorf("CreateRegistrationInvite rejected exactly 7 days (the inclusive maximum): %v", err)
	}

	tooLong := int64(7*24*time.Hour/time.Millisecond) + 1
	if _, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req5", &tooLong); err == nil {
		t.Error("CreateRegistrationInvite accepted a ttl over 7 days")
	}
}

func TestCreateRegistrationInviteWritesAuditEventAtomically(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")

	_, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req-audit-1", nil)
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
