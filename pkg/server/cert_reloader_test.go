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
