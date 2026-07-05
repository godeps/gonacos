package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveWritesHMACFile verifies that when an HMAC key is configured,
// Save writes a sibling .hmac file containing the hex-encoded
// HMAC-SHA256 of the dump bytes.
func TestSaveWritesHMACFile(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetHMACKey([]byte("test-key"))

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	hmacPath := dumpPath + ".hmac"
	hmacData, err := os.ReadFile(hmacPath)
	if err != nil {
		t.Fatalf("HMAC file not written: %v", err)
	}
	if len(hmacData) == 0 {
		t.Fatal("HMAC file empty")
	}
	// HMAC-SHA256 is 32 bytes -> 64 hex chars.
	if len(hmacData) != 64 {
		t.Errorf("HMAC file length = %d, want 64 (hex-encoded SHA-256)", len(hmacData))
	}
	// Must be valid hex.
	for _, c := range string(hmacData) {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("HMAC file contains non-hex char %q in %q", c, hmacData)
			break
		}
	}
}

// TestSaveWithoutHMACKeyDoesNotWriteHMACFile verifies that when no HMAC
// key is configured, Save does not write the .hmac file — preserving
// backward compatibility with pre-HMAC deployments.
func TestSaveWithoutHMACKeyDoesNotWriteHMACFile(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(dumpPath + ".hmac"); !os.IsNotExist(err) {
		t.Errorf("HMAC file should not exist without key configured; stat err = %v", err)
	}
}

// TestLoadAcceptsValidHMAC verifies that Load succeeds when the dump
// file and HMAC file are both present and the HMAC matches the dump.
func TestLoadAcceptsValidHMAC(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, c, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetHMACKey([]byte("test-key"))

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Flush Redis so Load falls through to the dump file path — the
	// only path that currently verifies the HMAC.
	c.FlushDB(context.Background())

	if err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load with valid HMAC: %v", err)
	}
}

// TestLoadRejectsTamperedDump verifies that Load returns
// ErrSnapshotTampered when the dump file has been modified after the
// HMAC was written — the canonical attack: an operator with filesystem
// write access replaces the dump to inject a malicious admin account.
// Without HMAC verification, the tampered dump would load silently.
func TestLoadRejectsTamperedDump(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, c, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetHMACKey([]byte("test-key"))

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c.FlushDB(context.Background())

	// Tamper with the dump file — flip a single byte. The HMAC file
	// still contains the original HMAC, so verification must fail.
	original, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	tampered := make([]byte, len(original))
	copy(tampered, original)
	tampered[len(tampered)-1] ^= 0xFF
	if err := os.WriteFile(dumpPath, tampered, 0o600); err != nil {
		t.Fatalf("write tampered dump: %v", err)
	}

	err = p.Load(context.Background())
	if !errors.Is(err, ErrSnapshotTampered) {
		t.Fatalf("Load with tampered dump: got %v, want ErrSnapshotTampered", err)
	}
}

// TestLoadNoHMACKeySkipsVerification verifies that when no HMAC key is
// configured, Load skips verification entirely. This is the backward-
// compatibility path: an operator who has not yet rolled out the key
// is not protected, but their existing deployment keeps working.
func TestLoadNoHMACKeySkipsVerification(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, c, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c.FlushDB(context.Background())

	// Tamper with the dump. Without a key configured, verification
	// is skipped — Load will NOT return ErrSnapshotTampered.
	original, _ := os.ReadFile(dumpPath)
	tampered := make([]byte, len(original))
	copy(tampered, original)
	tampered[len(tampered)-1] ^= 0xFF
	_ = os.WriteFile(dumpPath, tampered, 0o600)

	err := p.Load(context.Background())
	if errors.Is(err, ErrSnapshotTampered) {
		t.Fatalf("Load without key: got ErrSnapshotTampered, want no tamper error (verification should be skipped)")
	}
	// Other errors (e.g., JSON unmarshal) are acceptable — the point
	// is that HMAC verification was skipped, not that the load succeeds.
}

// TestLoadMissingHMACFileSkipsVerification verifies that when an HMAC
// key is configured but the .hmac file is missing (e.g., the dump
// predates the key being rolled out), Load skips verification and
// loads the dump as before.
func TestLoadMissingHMACFileSkipsVerification(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, c, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()

	// Save without a key — produces a dump with no .hmac file.
	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c.FlushDB(context.Background())

	// Now configure the key. The dump file exists but has no .hmac
	// file. Load must skip verification (not reject).
	p.SetHMACKey([]byte("rolled-out-later"))
	if err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load with missing .hmac file: %v (should skip verification)", err)
	}
}

// TestLoadRejectsTamperedHMACFile verifies that an attacker who
// modifies the .hmac file (rather than the dump) is also detected —
// the HMAC of the dump won't match the tampered HMAC file.
func TestLoadRejectsTamperedHMACFile(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, c, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetHMACKey([]byte("test-key"))

	if err := p.Save(context.Background()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c.FlushDB(context.Background())

	// Tamper with the .hmac file — flip a byte so it no longer
	// matches the dump.
	hmacPath := dumpPath + ".hmac"
	original, _ := os.ReadFile(hmacPath)
	tampered := make([]byte, len(original))
	copy(tampered, original)
	tampered[0] ^= 0xFF
	if err := os.WriteFile(hmacPath, tampered, 0o600); err != nil {
		t.Fatalf("write tampered hmac: %v", err)
	}

	err := p.Load(context.Background())
	if !errors.Is(err, ErrSnapshotTampered) {
		t.Fatalf("Load with tampered .hmac: got %v, want ErrSnapshotTampered", err)
	}
}

// TestSetHMACKeyCopiesKey verifies that SetHMACKey copies the key
// slice, so the caller can mutate their slice afterwards without
// affecting the persistence layer.
func TestSetHMACKeyCopiesKey(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	key := []byte("secret")
	p.SetHMACKey(key)
	key[0] = 'X' // mutate caller's slice
	if p.hmacKey[0] == 'X' {
		t.Errorf("SetHMACKey did not copy: caller mutation propagated")
	}
}

// TestSetHMACKeyEmptyClearsKey verifies that calling SetHMACKey with
// an empty key clears the configured key (skips verification).
func TestSetHMACKeyEmptyClearsKey(t *testing.T) {
	t.Parallel()
	dumpPath := filepath.Join(t.TempDir(), "snapshot.json")
	p, _, _, cleanup := newTestPersistence(t, dumpPath)
	defer cleanup()
	p.SetHMACKey([]byte("first"))
	p.SetHMACKey(nil)
	if p.hmacKey != nil {
		t.Errorf("SetHMACKey(nil) did not clear key: %v", p.hmacKey)
	}
}
