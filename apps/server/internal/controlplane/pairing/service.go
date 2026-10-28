// Package pairing implements PIN-gated trust bootstrap for the client API's
// self-signed TLS certificate. A client must know the PIN printed by the
// control plane to obtain the server certificate fingerprint, then pins that
// fingerprint for subsequent connections.
//
// This is PIN-gated TOFU, not a PAKE: an active man-in-the-middle on the very
// first pairing can still substitute its own certificate. It removes blind
// TOFU and raises the bar for passive attackers; a future revision can add a
// PAKE/SAS to make first contact resistant to an active MITM.
package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidPIN      = errors.New("invalid pairing PIN")
	ErrTooManyAttempts = errors.New("too many pairing attempts")
)

const (
	maxFailures   = 5
	failureWindow = time.Minute
)

// Service owns one control-plane startup's pairing PIN and the certificate
// fingerprint clients pin after a successful pairing.
type Service struct {
	pin      string
	certHash string

	mu       sync.Mutex
	failures []time.Time
	codes    map[string]time.Time
}

// New builds a pairing service for cert, using pin verbatim. pin is intended
// to be a short, human-entered code; callers normally use NewFromEnv.
func New(pin string, cert tls.Certificate) (*Service, error) {
	if strings.TrimSpace(pin) == "" {
		return nil, errors.New("pairing PIN must not be empty")
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("pairing certificate has no DER bytes")
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return &Service{
		pin:      pin,
		certHash: hex.EncodeToString(sum[:]),
		codes:    make(map[string]time.Time),
	}, nil
}

// NewFromEnv uses REMOTEPHONE_PAIRING_PIN when set, otherwise generates a fresh
// six-digit PIN for this process. The caller is responsible for showing the
// PIN to the host user (the control plane logs it at startup).
func NewFromEnv(cert tls.Certificate) (*Service, error) {
	pin := os.Getenv("REMOTEPHONE_PAIRING_PIN")
	if strings.TrimSpace(pin) == "" {
		generated, err := generatePIN()
		if err != nil {
			return nil, err
		}
		pin = generated
	}
	return New(pin, cert)
}

func generatePIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate pairing PIN: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// PIN returns the pairing code for the control plane to print. It is never
// returned over HTTP.
func (s *Service) PIN() string { return s.pin }

// CertSHA256 returns the lowercase hex SHA-256 of the server certificate DER.
// Clients pin this value after a successful pairing.
func (s *Service) CertSHA256() string { return s.certHash }

// IssueCode creates a short-lived, single-use pairing code for a remote
// administrator to send to a client. The static startup PIN remains valid for
// local/bootstrap use; these codes are the remote path.
func (s *Service) IssueCode(ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if ttl > time.Hour {
		ttl = time.Hour
	}
	code, err := generateCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for existing, expiry := range s.codes {
		if now.After(expiry) {
			delete(s.codes, existing)
		}
	}
	s.codes[code] = expiresAt
	return code, expiresAt, nil
}

func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	return fmt.Sprintf("%08d", n.Int64()), nil
}

// Verify checks pin and enforces a small failure-rate limit so a LAN attacker
// cannot brute-force the six-digit code at wire speed.
func (s *Service) Verify(pin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	kept := s.failures[:0]
	for _, failedAt := range s.failures {
		if now.Sub(failedAt) < failureWindow {
			kept = append(kept, failedAt)
		}
	}
	s.failures = kept
	if len(s.failures) >= maxFailures {
		return ErrTooManyAttempts
	}

	if subtle.ConstantTimeCompare([]byte(pin), []byte(s.pin)) == 1 {
		s.failures = nil
		return nil
	}
	if expiry, ok := s.codes[pin]; ok && now.Before(expiry) {
		delete(s.codes, pin)
		s.failures = nil
		return nil
	}
	s.failures = append(s.failures, now)
	return ErrInvalidPIN
}
