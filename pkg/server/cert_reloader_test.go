package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
)

// writeTestCert generates a self-signed ECDSA cert+key pair and writes it
// to the named files. Returns the cert's NotAfter so tests can assert the
// reloader picks up a new cert after rotation.
func writeTestCert(t *testing.T, certPath, keyPath string) time.Time {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gonacos-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pemEncode("CERTIFICATE", der)
	keyPEM, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEMBlock := pemEncode("EC PRIVATE KEY", keyPEM)
	if err := os.WriteFile(certPath, []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(keyPEMBlock), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return template.NotAfter
}

// pemEncode wraps a DER block in PEM headers.
func pemEncode(blockType string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}

// base64Encode is unused now — retained as a no-op to keep imports minimal
// if future tests need raw base64.
var _ = base64.StdEncoding

// TestCertReloader_InitialLoad verifies the reloader loads the cert pair at
// construction time and GetCertificate returns it.
func TestCertReloader_InitialLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeTestCert(t, certPath, keyPath)

	r, err := NewCertReloader(certPath, keyPath)
	if err != nil {
		t.Fatalf("new reloader: %v", err)
	}
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("get cert: %v", err)
	}
	if cert == nil {
		t.Fatalf("cert = nil")
	}
	if len(cert.Certificate) == 0 {
		t.Fatalf("cert chain empty")
	}
}

// TestCertReloader_ReloadOnFileChange verifies the reloader picks up a new
// cert when the file mtime changes. This is the hot-reload path: operators
// replace the cert file on disk and the next TLS handshake uses the new cert.
func TestCertReloader_ReloadOnFileChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeTestCert(t, certPath, keyPath)

	r, err := NewCertReloader(certPath, keyPath)
	if err != nil {
		t.Fatalf("new reloader: %v", err)
	}
	origCert, _ := r.GetCertificate(nil)

	// Wait briefly so the mtime is guaranteed to differ when we rewrite.
	time.Sleep(50 * time.Millisecond)
	writeTestCert(t, certPath, keyPath)

	// GetCertificate should reload and return a new cert.
	var newCert *tls.Certificate
	for i := 0; i < 20; i++ {
		newCert, _ = r.GetCertificate(nil)
		if newCert != origCert {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if newCert == origCert {
		t.Fatalf("cert not reloaded after file change")
	}
}

// TestCertReloader_ReloadFailureKeepsOldCert verifies that when the cert file
// is deleted mid-rotation, GetCertificate returns the previously cached cert
// rather than erroring. This keeps active connections working.
func TestCertReloader_ReloadFailureKeepsOldCert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeTestCert(t, certPath, keyPath)

	r, err := NewCertReloader(certPath, keyPath)
	if err != nil {
		t.Fatalf("new reloader: %v", err)
	}
	origCert, _ := r.GetCertificate(nil)

	// Simulate mid-rotation: truncate the cert file (invalid PEM).
	// mtime changes, reload fails, cached cert stays.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(certPath, []byte("truncated"), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("get cert after truncate: %v", err)
	}
	if cert != origCert {
		t.Fatalf("cert changed after reload failure; expected cached cert")
	}
}

// TestCertReloader_ConcurrentAccess verifies the reloader is safe for
// concurrent GetCertificate calls.
func TestCertReloader_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeTestCert(t, certPath, keyPath)

	r, err := NewCertReloader(certPath, keyPath)
	if err != nil {
		t.Fatalf("new reloader: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.GetCertificate(nil)
		}()
	}
	wg.Wait()
}

// TestCertReloader_MetricsExpiryGauges verifies that
// NewCertReloaderWithMetrics wires the not_before/not_after gauges into
// the registry and that the gauges reflect the leaf cert's validity
// window. Operators alert on not_after < 30 days from now to catch
// rotation failures before they cause an outage.
func TestCertReloader_MetricsExpiryGauges(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	notAfter := writeTestCert(t, certPath, keyPath)

	registry := observability.NewRegistry()
	r, err := NewCertReloaderWithMetrics(certPath, keyPath, registry)
	if err != nil {
		t.Fatalf("NewCertReloaderWithMetrics: %v", err)
	}

	naGauge := registry.Gauge("gonacos_tls_cert_not_after_timestamp", nil).Value()
	if naGauge == 0 {
		t.Fatal("not_after gauge not set after construction")
	}
	wantNA := notAfter.Unix()
	if naGauge != wantNA {
		t.Fatalf("not_after gauge = %d, want %d", naGauge, wantNA)
	}

	nbGauge := registry.Gauge("gonacos_tls_cert_not_before_timestamp", nil).Value()
	if nbGauge == 0 {
		t.Fatal("not_before gauge not set after construction")
	}
	// NotBefore is time.Now().Add(-time.Hour) in writeTestCert; we
	// can't assert the exact value (clock skew), but it should be
	// within a small window of "about an hour ago".
	wantNB := time.Now().Add(-time.Hour).Unix()
	delta := nbGauge - wantNB
	if delta < -60 || delta > 60 {
		t.Fatalf("not_before gauge = %d, want ~%d (delta %d)", nbGauge, wantNB, delta)
	}

	// Sanity: the leaf cert is the one we wrote.
	if _, _, ok := r.certExpiry(); !ok {
		t.Fatal("certExpiry() returned ok=false, want true")
	}
}

// TestCertReloader_MetricsGaugesUpdateOnReload verifies that the expiry
// gauges are updated when the cert is reloaded — a rotation that lands
// a new cert with a later NotAfter is reflected immediately. This is
// the property operators rely on: after rotation, the gauge shows the
// new expiry so alerts clear.
func TestCertReloader_MetricsGaugesUpdateOnReload(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	firstNotAfter := writeTestCert(t, certPath, keyPath)

	registry := observability.NewRegistry()
	r, err := NewCertReloaderWithMetrics(certPath, keyPath, registry)
	if err != nil {
		t.Fatalf("NewCertReloaderWithMetrics: %v", err)
	}

	// Sanity: initial gauge matches first cert.
	gotFirst := registry.Gauge("gonacos_tls_cert_not_after_timestamp", nil).Value()
	if gotFirst != firstNotAfter.Unix() {
		t.Fatalf("initial not_after = %d, want %d", gotFirst, firstNotAfter.Unix())
	}

	// Write a new cert with a different NotAfter (writeTestCert uses
	// time.Now().Add(24h), so a second call lands a different value
	// after the file mtime changes). Sleep 1.1s to guarantee the
	// file mtime advances — many filesystems have second-level mtime
	// precision, so sub-second writes can be missed.
	time.Sleep(1100 * time.Millisecond)
	secondNotAfter := writeTestCert(t, certPath, keyPath)

	// Trigger a reload via GetCertificate (which checks mtime).
	if _, err := r.GetCertificate(nil); err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	gotSecond := registry.Gauge("gonacos_tls_cert_not_after_timestamp", nil).Value()
	if gotSecond != secondNotAfter.Unix() {
		t.Fatalf("post-reload not_after = %d, want %d", gotSecond, secondNotAfter.Unix())
	}
	if gotSecond == gotFirst {
		t.Fatal("not_after gauge did not change after reload — rotation not detected")
	}
}

// TestCertReloader_NilRegistryNoop verifies that constructing the reloader
// with a nil registry does not panic and still loads the cert. Production
// callers that opt out of metrics must not crash.
func TestCertReloader_NilRegistryNoop(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	writeTestCert(t, certPath, keyPath)

	r, err := NewCertReloaderWithMetrics(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloaderWithMetrics(nil): %v", err)
	}
	if _, err := r.GetCertificate(nil); err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
}
