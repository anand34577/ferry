package lan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Identity is this device's LAN identity: a self-signed certificate generated once. Its SHA-256 is the
// LocalSend "fingerprint" peers use to recognise (and pin) us.
type Identity struct {
	Cert        tls.Certificate
	Fingerprint string
}

// LoadIdentity reads the certificate from dir, creating it on first use.
func LoadIdentity(dir string) (*Identity, error) {
	id, err := loadIdentity(dir)
	if err == nil {
		clientCert.Store(&id.Cert)
	}
	return id, err
}

func loadIdentity(dir string) (*Identity, error) {
	certPath, keyPath := filepath.Join(dir, "lan-cert.pem"), filepath.Join(dir, "lan-key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return &Identity{Cert: c, Fingerprint: FingerprintOf(c.Certificate[0])}, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Ferry"}, NotBefore: now.Add(-24 * time.Hour),
		NotAfter: now.AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return &Identity{Cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, Fingerprint: FingerprintOf(der)}, nil
}

// FingerprintOf is the uppercase hex SHA-256 of a DER certificate (the same format as LocalSend and Ferry Android).
func FingerprintOf(der []byte) string {
	h := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(h[:]))
}
