// Package tlsmaterial keeps a *tls.Config in step with the TLS material on disk.
//
// A Kubernetes Secret volume is rewritten in place when cert-manager rotates the
// certificate it holds. A process that parsed the old bytes into a *tls.Config
// keeps presenting them until it exits, because crypto/tls copies neither the
// keypair nor the CA pool back from the filesystem. Measured on a live fleet:
// every long-lived operator process on a TLS cluster stopped being able to reach
// its Valkey once the certificate it had pinned at startup expired -- the sidecar
// labeler, the Sentinel cross-check and the drain promotion of ADR 0012, all
// silently, with valid material sitting in the mount.
//
// The Reloader is the answer for the processes this repo owns: it re-reads the
// files, rebuilds the config only when their bytes changed, and keeps the last
// config that worked when a read or a parse fails. Processes it does not own --
// valkey-server, valkey-sentinel and the third-party metrics exporter -- are
// covered the other way, by the operator replacing their pod when the material
// rotates.
package tlsmaterial

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Reloader builds a *tls.Config from files on disk and rebuilds it whenever
// their contents change. It is safe for concurrent use.
//
// The comparison is on bytes, not on modification time. The mount is a Secret
// volume without subPath, which kubelet updates by swapping the ..data symlink,
// so a modtime comparison would have to resolve the link; comparing the bytes
// has no such subtlety and costs nothing at the rates these processes dial at
// (the labeler polls once per second and the client holds no connection, so a
// rebuilt config is picked up on the very next command).
type Reloader struct {
	caFile   string
	certFile string
	keyFile  string

	mu   sync.Mutex
	ca   []byte
	cert []byte
	key  []byte
	cfg  *tls.Config

	// degraded remembers that the last reload failed, so the log records the
	// transition into and out of running on stale material rather than one line
	// per dial.
	degraded bool
}

// New reads the material once and returns a Reloader for it. Any file that is
// named must be readable and parsable, so a misconfigured process fails at
// startup exactly as it did when the config was built once and pinned.
//
// An empty caFile leaves RootCAs unset (system roots); empty certFile/keyFile
// leave the config without a client certificate, which is what the observer runs
// with unless mTLS was opted into.
//
// A Reloader that was constructed successfully always has a usable config
// afterwards, which is why Config returns no error.
func New(caFile, certFile, keyFile string) (*Reloader, error) {
	r := &Reloader{caFile: caFile, certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Config returns the TLS config to use for the next connection, re-reading the
// files first and rebuilding only if their bytes changed.
//
// On a read or parse failure it returns the last config that worked. Two things
// make that the right answer rather than a papered-over error: kubelet writes the
// new Secret contents by swapping a symlink, so a caller can observe tls.crt and
// tls.key from either side of a rotation and get a mismatched pair for one call;
// and material that is merely stale still works until it expires, whereas
// returning nothing turns a transient read into an immediate outage.
func (r *Reloader) Config() *tls.Config {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.reloadLocked(); err != nil {
		if !r.degraded {
			r.degraded = true
			ctrl.Log.WithName("tls-material").Error(err,
				"cannot reload TLS material, continuing with the last material that worked",
				"caFile", r.caFile, "certFile", r.certFile, "keyFile", r.keyFile)
		}
		return r.cfg
	}
	if r.degraded {
		r.degraded = false
		ctrl.Log.WithName("tls-material").Info("TLS material readable again",
			"caFile", r.caFile, "certFile", r.certFile, "keyFile", r.keyFile)
	}
	return r.cfg
}

// reload takes the lock and reloads. Used by New, where no caller can race yet
// but the lock keeps the invariant in one place.
func (r *Reloader) reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reloadLocked()
}

// reloadLocked re-reads the files and swaps in a new config if they changed.
// The caller holds r.mu.
func (r *Reloader) reloadLocked() error {
	ca, err := readIfNamed(r.caFile)
	if err != nil {
		return err
	}
	cert, err := readIfNamed(r.certFile)
	if err != nil {
		return err
	}
	key, err := readIfNamed(r.keyFile)
	if err != nil {
		return err
	}

	if r.cfg != nil && bytes.Equal(ca, r.ca) && bytes.Equal(cert, r.cert) && bytes.Equal(key, r.key) {
		return nil
	}

	cfg, err := buildConfig(ca, cert, key)
	if err != nil {
		return err
	}

	// Only committed once the new material parsed, so a half-written mount never
	// replaces a working config.
	r.ca, r.cert, r.key, r.cfg = ca, cert, key, cfg
	return nil
}

// readIfNamed reads path, or returns nil for an empty path.
func readIfNamed(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the operator-rendered pod spec, not from user input
	if err != nil {
		return nil, fmt.Errorf("reading TLS material %s: %w", path, err)
	}
	return data, nil
}

// buildConfig assembles a client TLS config from PEM bytes already in memory.
// The keypair is parsed from those bytes rather than re-read from disk, so the
// pair that is verified is the pair that was compared.
func buildConfig(ca, cert, key []byte) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if len(ca) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		cfg.RootCAs = pool
	}

	if len(cert) > 0 && len(key) > 0 {
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}

	return cfg, nil
}
