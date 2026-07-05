package app

import (
	"os"
	"testing"
)

// TestMain lowers the bcrypt cost for the entire app test suite. Many
// app tests bootstrap an admin user via NewHandler, which hashes the
// admin password. At the default bcrypt cost (12), each hash takes
// ~250ms — with dozens of tests creating fresh handlers, the suite
// ballooned from ~5s to ~220s under the race detector. Lowering the
// cost to bcrypt.MinCost (4) restores the fast iteration loop without
// affecting production security (the cost is only read here, not in
// production binaries).
func TestMain(m *testing.M) {
	os.Setenv("GONACOS_BCRYPT_COST", "4")
	os.Exit(m.Run())
}
