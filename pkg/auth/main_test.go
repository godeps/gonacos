package auth

import (
	"os"
	"testing"
)

// TestMain lowers the bcrypt cost for the auth test suite. The default
// cost (12) makes each hash ~250ms; with many tests creating users,
// the suite takes ~25s. Lowering to bcrypt.MinCost (4) brings it back
// to ~5s without affecting the validity of the tests (they verify
// format and migration, not cost). Production binaries are unaffected.
func TestMain(m *testing.M) {
	os.Setenv("GONACOS_BCRYPT_COST", "4")
	os.Exit(m.Run())
}
