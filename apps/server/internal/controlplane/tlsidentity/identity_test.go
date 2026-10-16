package tlsidentity

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGetOrCreateGeneratesAndLoadsAValidCertificate(t *testing.T) {
	dir := t.TempDir()
	id := NewIdentity(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))

	cert, err := id.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("GetOrCreate returned a certificate with no DER bytes")
	}

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("x509.ParseCertificate: %v", err)
	}
	if parsed.Subject.CommonName != "mako" {
		t.Errorf("CommonName = %q, want mako", parsed.Subject.CommonName)
	}
	if !contains(parsed.DNSNames, "localhost") {
		t.Errorf("DNSNames = %v, want to contain localhost", parsed.DNSNames)
	}
	if time.Until(parsed.NotAfter) < 365*24*time.Hour {
		t.Errorf("NotAfter = %v, want at least a year of validity remaining", parsed.NotAfter)
	}
	if parsed.NotBefore.After(time.Now()) {
		t.Errorf("NotBefore = %v, want it to already be valid", parsed.NotBefore)
	}
}

func TestGetOrCreateReusesAnExistingPair(t *testing.T) {
	dir := t.TempDir()
	id := NewIdentity(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))

	first, err := id.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate (first): %v", err)
	}
	second, err := id.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate (second): %v", err)
	}

	firstParsed, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatalf("parse first: %v", err)
	}
	secondParsed, err := x509.ParseCertificate(second.Certificate[0])
	if err != nil {
		t.Fatalf("parse second: %v", err)
	}
	if firstParsed.SerialNumber.Cmp(secondParsed.SerialNumber) != 0 {
		t.Error("GetOrCreate generated a new certificate on the second call, want the same persisted one reused")
	}
}

func TestKeyFileHasPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-mode semantics")
	}
	dir := t.TempDir()
	id := NewIdentity(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if _, err := id.GetOrCreate(); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	keyInfo := mustStat(t, filepath.Join(dir, "key.pem"))
	if keyInfo.Mode().Perm() != 0600 {
		t.Errorf("key.pem mode = %o, want 0600", keyInfo.Mode().Perm())
	}
	certInfo := mustStat(t, filepath.Join(dir, "cert.pem"))
	if certInfo.Mode().Perm() != 0644 {
		t.Errorf("cert.pem mode = %o, want 0644", certInfo.Mode().Perm())
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", path, err)
	}
	return info
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
