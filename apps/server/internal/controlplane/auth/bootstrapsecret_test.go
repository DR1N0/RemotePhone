package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// bootstrapSecretPattern is defined in bootstrapsecret.go (same package).

func TestGetOrCreateCreatesFileWithExactPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows; the .exe build is covered by the functional tests")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "bootstrap-secret")
	bs := NewBootstrapSecret(path)

	secret, err := bs.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if !bootstrapSecretPattern.MatchString(secret) {
		t.Errorf("GetOrCreate() = %q, want 43-char base64url", secret)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestGetOrCreateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	bs := NewBootstrapSecret(path)
	first, err := bs.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate (1st): %v", err)
	}
	second, err := bs.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate (2nd): %v", err)
	}
	if first != second {
		t.Error("GetOrCreate returned different secrets on successive calls")
	}
}

func TestGetOrCreateRejectsWrongPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bs := NewBootstrapSecret(path)
	if _, err := bs.GetOrCreate(); err == nil {
		t.Error("GetOrCreate() succeeded on a file with 0640 permissions, want error")
	}
}

func TestGetOrCreateRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-secret")
	if err := os.WriteFile(real, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(dir, "linked-secret")
	if err := os.Symlink(real, link); err != nil {
		// Windows only grants SeCreateSymbolicLinkPrivilege to elevated /
		// developer-mode processes; the production code still refuses
		// reparse points via GetFileAttributes, this test just can't create
		// one here.
		t.Skipf("cannot create symlink on this host: %v", err)
	}
	bs := NewBootstrapSecret(link)
	if _, err := bs.GetOrCreate(); err == nil {
		t.Error("GetOrCreate() succeeded on a symlinked path, want error")
	}
}

func TestConcurrentGetOrCreateConvergesOnOneSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	const n = 8
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			bs := NewBootstrapSecret(path)
			results[i], errs[i] = bs.GetOrCreate()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: GetOrCreate: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Errorf("goroutine %d got a different secret than goroutine 0: concurrent creation did not converge", i)
		}
	}
}

func TestMatchesAndInvalidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	bs := NewBootstrapSecret(path)
	secret, err := bs.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	ok, err := bs.Matches(secret)
	if err != nil || !ok {
		t.Fatalf("Matches(correct) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = bs.Matches("wrong-value-entirely-00000000000000000000")
	if err != nil || ok {
		t.Fatalf("Matches(wrong) = (%v, %v), want (false, nil)", ok, err)
	}

	// Invalidate with the wrong secret must not delete the file.
	if err := bs.Invalidate("wrong-value-entirely-00000000000000000000"); err != nil {
		t.Fatalf("Invalidate(wrong): %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("file was deleted after Invalidate with the wrong secret: %v", err)
	}

	// Invalidate with the correct secret deletes the file.
	if err := bs.Invalidate(secret); err != nil {
		t.Fatalf("Invalidate(correct): %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists after Invalidate with the correct secret: err=%v", err)
	}

	// Invalidate on an already-gone file is a no-op, not an error.
	if err := bs.Invalidate(secret); err != nil {
		t.Fatalf("Invalidate on already-deleted file returned an error: %v", err)
	}
}
