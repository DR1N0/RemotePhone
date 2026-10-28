package auth

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	"remotephone/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

// mustOpenTestDB opens a fresh, migrated, temp-file SQLite database.
func mustOpenTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := persistence.ApplyMigrations(db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return db
}

func TestSetupAdminSuccess(t *testing.T) {
	secretPath := t.TempDir() + "/bootstrap-secret"
	dbPath := t.TempDir() + "/test.db"
	db := mustOpenTestDB(t, dbPath)
	svc, err := NewService(db, secretPath)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret created at construction time: %v", err)
	}

	user, token, err := svc.SetupAdmin(context.Background(), SetupAdminInput{
		Username: "admin", Password: "correct horse battery staple", DeviceLabel: "setup-device",
		BootstrapSecret: string(secret),
	})
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	if user.Type != "admin" || token == "" {
		t.Errorf("SetupAdmin() = (%+v, %q), want type=admin and a non-empty token", user, token)
	}
	if _, err := os.ReadFile(secretPath); !os.IsNotExist(err) {
		t.Error("bootstrap secret file still exists after successful setup, want it invalidated/deleted")
	}

	claimed, err := svc.GetSetupStatus(context.Background())
	if err != nil || !claimed {
		t.Errorf("GetSetupStatus() = (%v, %v), want (true, nil) after setup", claimed, err)
	}
}

func TestSetupAdminRejectsWrongSecretWithoutCreatingUser(t *testing.T) {
	secretPath := t.TempDir() + "/bootstrap-secret"
	db := mustOpenTestDB(t, t.TempDir()+"/test.db")
	svc, err := NewService(db, secretPath)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, _, err = svc.SetupAdmin(context.Background(), SetupAdminInput{
		Username: "admin", Password: "correct horse battery staple", DeviceLabel: "d",
		BootstrapSecret: "definitely-the-wrong-secret-value-here-00000",
	})
	if err == nil {
		t.Fatal("SetupAdmin succeeded with a wrong bootstrap secret")
	}
	claimed, err := svc.GetSetupStatus(context.Background())
	if err != nil || claimed {
		t.Errorf("GetSetupStatus() = (%v, %v), want (false, nil) after a rejected setup attempt", claimed, err)
	}
	if _, err := os.ReadFile(secretPath); err != nil {
		t.Error("bootstrap secret file was removed after a FAILED setup attempt, want it untouched")
	}
}

func TestConcurrentSetupAdminResolvesToOneWinner(t *testing.T) {
	secretPath := t.TempDir() + "/bootstrap-secret"
	db := mustOpenTestDB(t, t.TempDir()+"/test.db")
	svc, err := NewService(db, secretPath)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}

	const n = 5
	results := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, results[i] = svc.SetupAdmin(context.Background(), SetupAdminInput{
				Username: "admin", Password: "correct horse battery staple",
				DeviceLabel: "d", BootstrapSecret: string(secret),
			})
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("concurrent SetupAdmin calls produced %d successes, want exactly 1", successes)
	}
}

func TestLoginAntiEnumeration(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	if _, err := svc.db.Exec(`UPDATE users SET username = 'realuser', password_hash = ? WHERE id = 'u1'`,
		mustHashPassword(t, "correct-password")); err != nil {
		t.Fatalf("set up real user password: %v", err)
	}

	_, _, errUnknownUser := svc.Login(context.Background(), LoginInput{Username: "nosuchuser", Password: "whatever", DeviceLabel: "d"})
	_, _, errWrongPassword := svc.Login(context.Background(), LoginInput{Username: "realuser", Password: "wrong-password", DeviceLabel: "d"})

	if errUnknownUser == nil || errWrongPassword == nil {
		t.Fatal("Login succeeded unexpectedly")
	}
	if errUnknownUser.Error() != errWrongPassword.Error() {
		t.Errorf("unknown-user error (%v) and wrong-password error (%v) differ — anti-enumeration broken", errUnknownUser, errWrongPassword)
	}
}

func TestLoginIssuesFreshTokenEachTime(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	hash := mustHashPassword(t, "correct-password")
	if _, err := svc.db.Exec(`UPDATE users SET username = 'realuser', password_hash = ? WHERE id = 'u1'`, hash); err != nil {
		t.Fatalf("set up user password: %v", err)
	}

	_, token1, err := svc.Login(context.Background(), LoginInput{Username: "realuser", Password: "correct-password", DeviceLabel: "d1"})
	if err != nil {
		t.Fatalf("Login (1st): %v", err)
	}
	_, token2, err := svc.Login(context.Background(), LoginInput{Username: "realuser", Password: "correct-password", DeviceLabel: "d2"})
	if err != nil {
		t.Fatalf("Login (2nd): %v", err)
	}
	if token1 == token2 {
		t.Error("two Login calls returned the same token")
	}
}

func mustHashPassword(t *testing.T, password string) string {
	t.Helper()
	h, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return h
}
