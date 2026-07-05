package auth

import (
	"strings"
	"testing"
)

// TestHashPasswordUsesBcrypt verifies that new passwords are hashed with
// bcrypt (prefixed with "bcrypt$") and that the salt field is empty —
// bcrypt includes its own salt in the hash.
func TestHashPasswordUsesBcrypt(t *testing.T) {
	t.Parallel()
	salt, hash, err := hashPassword("secret123")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, hashPrefixBcrypt) {
		t.Fatalf("hash should start with %q, got %q", hashPrefixBcrypt, hash[:min(len(hash), len(hashPrefixBcrypt))])
	}
	if salt != "" {
		t.Fatalf("bcrypt salt should be empty (embedded in hash), got %q", salt)
	}
	if !verifyPassword("secret123", salt, hash) {
		t.Fatalf("verifyPassword rejected the password it just hashed")
	}
	if verifyPassword("wrong", salt, hash) {
		t.Fatalf("verifyPassword accepted wrong password")
	}
}

// TestVerifyPasswordLegacySHA256 verifies that hashes written by previous
// versions (single SHA-256 with salt, no prefix) still verify. This is
// the backward-compatibility path that lets existing snapshots log in.
func TestVerifyPasswordLegacySHA256(t *testing.T) {
	t.Parallel()
	// Reproduce the legacy format: random 16-byte salt hex + sha256(salt+pw).
	saltHex := "0123456789abcdef0123456789abcdef"
	hash := sha256Hex(saltHex + "legacy-pw")
	if !isLegacyHash(hash) {
		t.Fatalf("plain hex hash should be detected as legacy")
	}
	if !verifyPassword("legacy-pw", saltHex, hash) {
		t.Fatalf("legacy verify failed")
	}
	if verifyPassword("wrong", saltHex, hash) {
		t.Fatalf("legacy verify accepted wrong password")
	}
}

// TestLoginMigratesLegacyHash verifies that logging in with a legacy
// SHA-256 hash upgrades it to bcrypt. After migration, the in-memory user
// record has a bcrypt hash; the cleartext password still verifies.
func TestLoginMigratesLegacyHash(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin(""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Overwrite the admin's password with a legacy SHA-256 hash so we
	// can observe the migration.
	saltHex := "abcdef0123456789abcdef0123456789"
	legacyHash := sha256Hex(saltHex + "migrate-me")
	s.mu.Lock()
	admin := s.users[DefaultAdminUser]
	admin.Salt = saltHex
	admin.Password = legacyHash
	s.users[DefaultAdminUser] = admin
	s.mu.Unlock()

	// Login with the cleartext password. The legacy hash verifies, then
	// the migration kicks in and re-hashes with bcrypt.
	if _, err := s.Login(DefaultAdminUser, "migrate-me"); err != nil {
		t.Fatalf("login with legacy hash: %v", err)
	}

	s.mu.RLock()
	migrated := s.users[DefaultAdminUser]
	s.mu.RUnlock()

	if !strings.HasPrefix(migrated.Password, hashPrefixBcrypt) {
		t.Fatalf("after migration, hash should be bcrypt, got %q", migrated.Password)
	}
	if migrated.Salt != "" {
		t.Fatalf("after migration, salt should be empty (bcrypt embeds it), got %q", migrated.Salt)
	}
	// The cleartext password still verifies against the migrated hash.
	if !verifyPassword("migrate-me", migrated.Salt, migrated.Password) {
		t.Fatalf("verify after migration failed")
	}
}

// TestLoginDoesNotMigrateBcryptHash verifies that logging in with an
// already-bcrypt hash does NOT re-hash. Idempotent migration — otherwise
// every login would burn a bcrypt hash needlessly.
func TestLoginDoesNotMigrateBcryptHash(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin(""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	s.mu.RLock()
	original := s.users[DefaultAdminUser]
	s.mu.RUnlock()

	if _, err := s.Login(DefaultAdminUser, DefaultAdminPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	s.mu.RLock()
	after := s.users[DefaultAdminUser]
	s.mu.RUnlock()

	if original.Password != after.Password {
		t.Fatalf("bcrypt hash changed after login — migration should be idempotent")
	}
}

// TestIsLegacyHash covers the prefix-based detection.
func TestIsLegacyHash(t *testing.T) {
	t.Parallel()
	if isLegacyHash("bcrypt$abc") {
		t.Fatalf("bcrypt$-prefixed should not be legacy")
	}
	if !isLegacyHash("abcdef0123456789") {
		t.Fatalf("plain hex should be legacy")
	}
	if !isLegacyHash("") {
		t.Fatalf("empty hash should be legacy (no prefix)")
	}
}
