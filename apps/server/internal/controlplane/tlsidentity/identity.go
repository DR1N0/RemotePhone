// Package tlsidentity manages the control plane's server-side TLS
// certificate and private key: generate a self-signed pair on first use,
// reuse it on every run after. This is HTTPS trust bootstrap only — it
// makes the client surface serve TLS at all. PIN-confirmed pairing and
// SPKI-fingerprint pinning (docs/architecture/security-model.md) are a
// separate, not-yet-built trust layer on top of whatever certificate this
// package serves; deferred until a real native client exists to pair
// with and verify the handshake against (see docs/plans/tls-identity.md).
package tlsidentity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// certValidity is deliberately long (this is a self-signed, trusted-LAN
// certificate with no CA-issued renewal process yet) — regenerating it
// isn't free: a future pairing layer will pin the old key's SPKI hash, so
// an unexpected regeneration would break every already-paired client.
const certValidity = 10 * 365 * 24 * time.Hour

// Identity manages one self-signed certificate/key pair persisted at
// certPath/keyPath.
type Identity struct {
	certPath string
	keyPath  string
}

func NewIdentity(certPath, keyPath string) *Identity {
	return &Identity{certPath: certPath, keyPath: keyPath}
}

// GetOrCreate loads the certificate/key pair at certPath/keyPath, or
// generates and persists a fresh self-signed pair if either file is
// missing. Safe to call every process start: once the files exist, this
// is just a load.
func (id *Identity) GetOrCreate() (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(id.certPath, id.keyPath)
	if err == nil {
		return cert, nil
	}
	if !os.IsNotExist(err) {
		return tls.Certificate{}, fmt.Errorf("load tls identity: %w", err)
	}
	if err := id.create(); err != nil {
		return tls.Certificate{}, err
	}
	cert, err = tls.LoadX509KeyPair(id.certPath, id.keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load freshly generated tls identity: %w", err)
	}
	return cert, nil
}

// create generates a fresh ECDSA P-256 self-signed certificate/key pair
// and writes them to certPath/keyPath. The certificate is public (0644);
// the private key is not (0600, matching auth.BootstrapSecret's file-mode
// convention for every other on-disk secret in this codebase).
func (id *Identity) create() error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate tls private key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate tls serial number: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "remotephone"},
		NotBefore:    now.Add(-time.Hour), // tolerate modest clock skew
		NotAfter:     now.Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	derCert, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create tls certificate: %w", err)
	}
	derKey, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal tls private key: %w", err)
	}

	dir := filepath.Dir(id.certPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create tls identity dir: %w", err)
	}
	keyDir := filepath.Dir(id.keyPath)
	if keyDir != dir {
		if err := os.MkdirAll(keyDir, 0700); err != nil {
			return fmt.Errorf("create tls identity key dir: %w", err)
		}
	}

	if err := writePEMFile(id.certPath, "CERTIFICATE", derCert, 0644); err != nil {
		return fmt.Errorf("write tls certificate: %w", err)
	}
	if err := writePEMFile(id.keyPath, "PRIVATE KEY", derKey, 0600); err != nil {
		return fmt.Errorf("write tls private key: %w", err)
	}
	return nil
}

func writePEMFile(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(mode); err != nil { // defensive: umask may have affected O_CREATE's mode
		return err
	}
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}
