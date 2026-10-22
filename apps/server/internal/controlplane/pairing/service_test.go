package pairing

import (
	"crypto/tls"
	"path/filepath"
	"testing"
	"time"

	"mako/internal/controlplane/tlsidentity"
)

func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	dir := t.TempDir()
	cert, err := tlsidentity.NewIdentity(
		filepath.Join(dir, "cert.pem"),
		filepath.Join(dir, "key.pem"),
	).GetOrCreate()
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	return cert
}

func TestVerifyAndFingerprint(t *testing.T) {
	cert := testCert(t)
	svc, err := New("123456", cert)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(svc.CertSHA256()); got != 64 {
		t.Fatalf("CertSHA256 length = %d, want 64 hex chars", got)
	}
	if err := svc.Verify("123456"); err != nil {
		t.Fatalf("Verify correct PIN: %v", err)
	}
	if err := svc.Verify("000000"); err != ErrInvalidPIN {
		t.Fatalf("Verify wrong PIN = %v, want ErrInvalidPIN", err)
	}
}

func TestVerifyRateLimitsFailures(t *testing.T) {
	cert := testCert(t)
	svc, err := New("123456", cert)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFailures; i++ {
		if err := svc.Verify("bad"); err != ErrInvalidPIN {
			t.Fatalf("attempt %d = %v, want ErrInvalidPIN", i, err)
		}
	}
	if err := svc.Verify("123456"); err != ErrTooManyAttempts {
		t.Fatalf("after %d failures = %v, want ErrTooManyAttempts", maxFailures, err)
	}

	svc.mu.Lock()
	for i := range svc.failures {
		svc.failures[i] = time.Now().Add(-2 * failureWindow)
	}
	svc.mu.Unlock()
	if err := svc.Verify("123456"); err != nil {
		t.Fatalf("after window expiry = %v, want success", err)
	}
}

func TestNewRejectsEmptyPIN(t *testing.T) {
	if _, err := New("", testCert(t)); err == nil {
		t.Fatal("New accepted empty PIN")
	}
}
