package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestRegisterSuccess(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")
	token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1")
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
	token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1")
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
	token1, _, _ := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1")
	if _, _, err := svc.Register(context.Background(), token1, "taken_name", "correct horse battery staple", "d1", "req2"); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	token2, _, _ := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req3")
	_, _, err := svc.Register(context.Background(), token2, "TAKEN_NAME", "correct horse battery staple", "d2", "req4")
	if err == nil {
		t.Fatal("Register succeeded with a username differing only in case from an existing user")
	}
}

func TestConcurrentRegisterSameInviteResolvesToOneWinner(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")
	token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req1")
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

// TestConcurrentRegisterSameUsernameResolvesToOneWinner covers a gap the
// existing TestConcurrentRegisterSameInviteResolvesToOneWinner test above
// cannot catch: that test uses distinct usernames, so it only ever exercises
// the registration-invite race (ConsumeRegistrationInvite's atomic UPDATE).
// Here, every goroutine redeems its OWN distinct, valid invite but asks for
// the IDENTICAL desired username, so the invite race never fires at all —
// this isolates the username-uniqueness race specifically. Exactly one
// Register call must succeed, and every loser's error must be
// ErrUsernameConflict, never a raw, unmapped SQLITE_BUSY/driver error
// surfaced by concurrent writers contending on the same connection pool.
//
// This test is a genuine regression test for NewService's db.SetMaxOpenConns(1):
// delete that call and this test starts intermittently failing with raw
// "database is locked (5) (SQLITE_BUSY)" errors instead of ErrUsernameConflict
// (confirmed by hand while fixing this). It is NOT, however, a regression
// test for the step 4c CreateUser/mapCreateUserError backstop in isolation:
// with the pool capped at one connection, Register's transactions fully
// serialize, so step 4b's existing GetUserByUsername pre-check already
// catches every loser here before step 4c's CreateUser ever runs. See
// TestMapCreateUserError below for a direct, deterministic test of that
// backstop's own logic.
func TestConcurrentRegisterSameUsernameResolvesToOneWinner(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "admin1", "admin", "active")

	const n = 5
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		token, _, err := svc.CreateRegistrationInvite(context.Background(), "admin1", nil, "req-invite")
		if err != nil {
			t.Fatalf("CreateRegistrationInvite[%d]: %v", i, err)
		}
		tokens[i] = token
	}

	const desiredUsername = "same_desired_name"
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = svc.Register(context.Background(), tokens[i],
				desiredUsername, "correct horse battery staple", "device", "req-register")
		}(i)
	}
	wg.Wait()

	successes := 0
	for i, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrUsernameConflict) {
			t.Errorf("loser[%d] error = %v, want errors.Is(err, ErrUsernameConflict)", i, err)
		}
	}
	if successes != 1 {
		t.Errorf("concurrent Register calls with the same desired username produced %d successes, want exactly 1", successes)
	}
}

// TestMapCreateUserError directly, deterministically exercises
// mapCreateUserError's two branches — independent of any real database
// connection-pool timing, which the above concurrency test cannot
// reliably force down the CreateUser-level backstop path (see its doc
// comment). This is the regression test for the step 4c backstop itself:
// revert mapCreateUserError to a bare `fmt.Errorf("create user: %w", err)`
// and this test fails.
func TestMapCreateUserError(t *testing.T) {
	uniqueErr := errors.New(`UNIQUE constraint failed: users.username`)
	if got := mapCreateUserError(uniqueErr); !errors.Is(got, ErrUsernameConflict) {
		t.Errorf("mapCreateUserError(%v) = %v, want errors.Is(got, ErrUsernameConflict)", uniqueErr, got)
	}

	otherErr := errors.New("database is locked (5) (SQLITE_BUSY)")
	got := mapCreateUserError(otherErr)
	if errors.Is(got, ErrUsernameConflict) {
		t.Errorf("mapCreateUserError(%v) = %v, want a wrapped error, not ErrUsernameConflict", otherErr, got)
	}
	if !errors.Is(got, otherErr) {
		t.Errorf("mapCreateUserError(%v) = %v, want it to wrap the original error", otherErr, got)
	}
}
