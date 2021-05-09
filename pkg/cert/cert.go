package cert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// Reloader helps implementing TLS client/server that reloads
// certificates on a filesystem automatically.
type Reloader struct {
	dir string
	log logr.Logger

	mu           sync.RWMutex
	ca           *x509.CertPool
	cert         *tls.Certificate
	clientConfig *tls.Config
}

// NewReloader creates a Realoder that loads certificates from `dir`.
// The directory must contain these files.
//
// - ca.crt:  CA certificate bundle in PEM format
// - tls.crt: The TLS certificate to be used.
// - tls.key: The private key.
func NewReloader(dir string, log logr.Logger) (*Reloader, error) {
	r := &Reloader{dir: dir, log: log}
	if err := r.check(); err != nil {
		return nil, err
	}
	return r, nil
}

// Run checks updates of the certificate files at every `interval`
// until `ctx` is canceled.  This should be called as a goroutine.
func (r *Reloader) Run(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		if err := r.check(); err != nil {
			r.log.Error(err, "failed to reload certificates")
		}
	}
}

func (r *Reloader) check() error {
	data, err := os.ReadFile(filepath.Join(r.dir, "ca.crt"))
	if err != nil {
		return fmt.Errorf("failed to load ca.crt: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(data)

	cert, err := tls.LoadX509KeyPair(filepath.Join(r.dir, "tls.crt"), filepath.Join(r.dir, "tls.key"))
	if err != nil {
		return fmt.Errorf("failed to load cert/key pair: %w", err)
	}
	r.log.Info("certificate reloaded")

	r.mu.Lock()
	defer r.mu.Unlock()

	r.ca = pool
	r.cert = &cert
	r.clientConfig = &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
	}
	return nil
}

func (r *Reloader) TLSClientConfig() *tls.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clientConfig
}

func (r *Reloader) TLSServerConfig() *tls.Config {
	return &tls.Config{
		GetConfigForClient: r.getConfigForClient,
	}
}

func (r *Reloader) getConfigForClient(*tls.ClientHelloInfo) (*tls.Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return &tls.Config{
		Certificates:     []tls.Certificate{*r.cert},
		ClientAuth:       tls.RequireAndVerifyClientCert,
		ClientCAs:        r.ca,
		VerifyConnection: r.verifyConnection,
	}, nil
}

func (r *Reloader) verifyConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("no client cert")
	}
	cert := cs.PeerCertificates[0]
	if cert.Subject.CommonName != "moco-controller" {
		err := fmt.Errorf("invalid certificate: common name is not valid: %s", cert.Subject.CommonName)
		r.log.Error(err, "connection refused")
		return err
	}
	return nil
}
