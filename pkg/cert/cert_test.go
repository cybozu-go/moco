package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func writeCerts(t *testing.T, dir string, caPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "moco-controller"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if caPEM == nil {
		caPEM = certPEM
	}

	files := map[string][]byte{
		"ca.crt":  caPEM,
		"tls.crt": certPEM,
		"tls.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewReloaderInvalidCA(t *testing.T) {
	for name, ca := range map[string][]byte{
		"empty":   {},
		"not PEM": []byte("not a certificate\n"),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeCerts(t, dir, ca)

			if _, err := NewReloader(dir, logr.Discard()); err == nil {
				t.Fatal("NewReloader succeeded with an invalid ca.crt")
			}
		})
	}
}

func TestReloaderKeepsCertificatesOnInvalidCA(t *testing.T) {
	dir := t.TempDir()
	writeCerts(t, dir, nil)

	r, err := NewReloader(dir, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	before := r.TLSClientConfig()

	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.check(); err == nil {
		t.Fatal("check succeeded with an empty ca.crt")
	}

	if after := r.TLSClientConfig(); after != before {
		t.Error("client TLS config was replaced after a failed reload")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ca.Equal(before.RootCAs) {
		t.Error("CA pool was replaced after a failed reload")
	}
}
