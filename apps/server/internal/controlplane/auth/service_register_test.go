package auth

import (
	"context"
	"sync"
	"testing"
)

func TestRegisterSuccess(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")
	token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}

	user, credToken, err := svc.Register(context.Background(), token, "New_User", "correct horse battery staple", "device1", "req2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Type != "registered" || credToken == "" {
		t.Errorf("Register() user = %+v, credToken = %q", user, credToken)
	}
	if *user.Username != "new_user" {
		t.Errorf("Register() normalized username = %q, want new_user", *user.Username)
	}
}

func TestRegisterRejectsInvalidShapeBeforeTouchingInvite(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")
	token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}

	_, _, err = svc.Register(context.Background(), token, "ab", "short", "device1", "req2")
	if err == nil {
		t.Fatal("Register succeeded with an invalid username/password shape")
	}

	// The invite must still be usable — an invalid-shape attempt must not
	// have consumed it.
	_, _, err = svc.Register(context.Background(), token, "valid_user", "correct horse battery staple", "device1", "req3")
	if err != nil {
		t.Fatalf("Register with a valid shape failed after a prior invalid-shape attempt: %v", err)
	}
}

func TestRegisterRejectsUsernameConflict(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")
	token1, _, _ := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1", nil)
	if _, _, err := svc.Register(context.Background(), token1, "taken_name", "correct horse battery staple", "d1", "req2"); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	token2, _, _ := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req3", nil)
	_, _, err := svc.Register(context.Background(), token2, "TAKEN_NAME", "correct horse battery staple", "d2", "req4")
	if err == nil {
		t.Fatal("Register succeeded with a username differing only in case from an existing user")
	}
}

func TestConcurrentRegisterSameInviteResolvesToOneWinner(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")
	token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}

	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = svc.Register(context.Background(), token,
				usernameFor(i), "correct horse battery staple", "device", "req")
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("concurrent Register calls on the same invite produced %d successes, want exactly 1", successes)
	}
}

func usernameFor(i int) string {
	names := []string{"user_a", "user_b", "user_c", "user_d", "user_e"}
	return names[i]
}
