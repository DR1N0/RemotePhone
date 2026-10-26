package auth

import "testing"

func TestSetupStatusIgnoresSystemPrincipal(t *testing.T) {
	svc := newTestService(t)
	claimed, err := svc.GetSetupStatus(t.Context())
	if err != nil {
		t.Fatalf("GetSetupStatus: %v", err)
	}
	if claimed {
		t.Fatal("server with only the seeded _SYSTEM principal must not look claimed")
	}
}

func TestUsernameCannotStartWithUnderscore(t *testing.T) {
	if _, ok := isValidUsername("_admin"); ok {
		t.Fatal("leading underscore username was accepted")
	}
	if _, ok := isValidUsername("admin_user"); !ok {
		t.Fatal("interior underscore username was rejected")
	}
}
