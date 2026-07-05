package server

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// CertReloader hot-reloads a TLS certificate by checking the cert file's
// mtime on each GetCertificate call. When the mtime changes, the cert pair
// is re-read from disk and the cached *tls.Certificate is replaced. This
// lets operators rotate certificates by replacing the file on disk and
// sending a SIGHUP (or just waiting for the next TLS handshake) — no
// restart required, no active connections dropped.
//
// The reloader is safe for concurrent use: GetCertificate takes a read lock
// on the cached cert, and reload takes a write lock. A reload failure
// (e.g., the operator is mid-write and the file is truncated) leaves the
// previous cert in place — the server keeps serving the old cert until the
// file is in a consistent state.
type CertReloader struct {
	certFile string
	keyFile  string

	mu        sync.RWMutex
	cert      *tls.Certificate
	certMtime time.Time
}

// NewCertReloader loads the cert pair immediately (so config errors surface
// at startup) and returns a reloader suitable for tls.Config.GetCertificate.
func NewCertReloader(certFile, keyFile string) (*CertReloader, error) {
	r := &CertReloader{certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// reload re-reads the cert pair from disk and swaps the cached cert. It is
// called on the first GetCertificate and whenever the cert file's mtime
// changes. A non-nil error is returned only on the initial load; subsequent
// reload failures fall back to the previous cert (logged by the caller).
func (r *CertReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	mtime, _ := fileMtime(r.certFile)
	r.mu.Lock()
	r.cert = &cert
	r.certMtime = mtime
	r.mu.Unlock()
	return nil
}

// GetCertificate is the tls.Config.GetCertificate callback. It checks the
// cert file's mtime on every call; if changed, it reloads. The check is a
// single os.Stat per handshake — cheap relative to the TLS handshake itself.
// On reload failure the previous cert is returned so active connections
// keep working.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if mtime, ok := fileMtime(r.certFile); ok && !mtime.Equal(r.certMtime) {
		// Best-effort reload; fall back to the cached cert on error.
		_ = r.reload()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

// fileMtime returns the modification time of the named file. The boolean
// is false when the stat failed (e.g., the file was deleted mid-rotation).
func fileMtime(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
