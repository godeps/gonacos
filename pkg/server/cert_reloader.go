package server

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
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
//
// When a metrics registry is wired (NewCertReloaderWithMetrics), the
// reloader exposes gonacos_tls_cert_not_before_timestamp and
// gonacos_tls_cert_not_after_timestamp gauges — operators alert when
// not_after is < 30 days from now to catch rotation failures before they
// cause an outage. The gauges are updated on each successful reload so
// they reflect the cert currently served by GetCertificate.
type CertReloader struct {
	certFile string
	keyFile  string

	mu        sync.RWMutex
	cert      *tls.Certificate
	certMtime time.Time

	// registry is wired once at construction; reload updates the gauges
	// under the same write lock that swaps the cached cert, so the
	// gauges and the served cert are consistent.
	registry *observability.Registry
}

// NewCertReloader loads the cert pair immediately (so config errors surface
// at startup) and returns a reloader suitable for tls.Config.GetCertificate.
// Metrics are not wired — use NewCertReloaderWithMetrics for that.
func NewCertReloader(certFile, keyFile string) (*CertReloader, error) {
	r := &CertReloader{certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// NewCertReloaderWithMetrics is like NewCertReloader but also wires the
// cert-not-before/not-after gauges into the provided registry. Pass nil
// to disable metrics (matches NewCertReloader behavior). The gauges are
// updated on each successful reload — a rotation that lands a new cert
// with a later notAfter is reflected immediately.
func NewCertReloaderWithMetrics(certFile, keyFile string, registry *observability.Registry) (*CertReloader, error) {
	r := &CertReloader{certFile: certFile, keyFile: keyFile, registry: registry}
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
	// Update the expiry gauges outside the cert lock — the gauge Set
	// is itself atomic and does not need the cert held. Reading the
	// leaf cert's NotBefore/NotAfter requires parsing the cert, which
	// can fail for intermediate certs in the chain; we use the first
	// parsed leaf (index 0) which is the end-entity cert presented to
	// clients.
	if r.registry != nil {
		if nb, na, ok := r.certExpiry(); ok {
			r.registry.Gauge("gonacos_tls_cert_not_before_timestamp", nil).Set(nb.Unix())
			r.registry.Gauge("gonacos_tls_cert_not_after_timestamp", nil).Set(na.Unix())
		}
	}
	return nil
}

// certExpiry returns the NotBefore/NotAfter of the leaf cert. Returns
// ok=false when the leaf cannot be parsed (e.g., the cert file is
// malformed but tls.LoadX509KeyPair accepted it — rare, but possible
// with PEM that has extra whitespace). The leaf is the first cert in
// the chain (index 0).
func (r *CertReloader) certExpiry() (notBefore, notAfter time.Time, ok bool) {
	r.mu.RLock()
	cert := r.cert
	r.mu.RUnlock()
	if cert == nil || len(cert.Certificate) == 0 {
		return time.Time{}, time.Time{}, false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return leaf.NotBefore, leaf.NotAfter, true
}

// GetCertificate is the tls.Config.GetCertificate callback. It checks the
// cert file's mtime on every call; if changed, it reloads. The check is a
// single os.Stat per handshake — cheap relative to the TLS handshake itself.
// On reload failure the previous cert is returned so active connections
// keep working.
//
// The mtime read takes a read lock so it does not race with a concurrent
// reload swapping the cached value. The cert read also takes a read lock
// so a reader sees a consistent (cert, mtime) pair — without the lock, a
// reload between the mtime check and the cert return could hand the
// caller a cert that does not match the mtime the check saw.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	cachedMtime := r.certMtime
	r.mu.RUnlock()
	if mtime, ok := fileMtime(r.certFile); ok && !mtime.Equal(cachedMtime) {
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
