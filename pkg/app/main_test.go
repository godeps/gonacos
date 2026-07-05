package app

import (
	"os"
	"testing"
)

// TestMain configures env vars for the entire app test suite:
//
//   - GONACOS_BCRYPT_COST=4 lowers the bcrypt cost. Many app tests bootstrap
//     an admin user via NewHandler, which hashes the admin password. At the
//     default bcrypt cost (12), each hash takes ~250ms — with dozens of
//     tests creating fresh handlers, the suite ballooned from ~5s to ~220s
//     under the race detector. Lowering the cost to bcrypt.MinCost (4)
//     restores the fast iteration loop without affecting production
//     security (the cost is only read here, not in production binaries).
//
//   - GONACOS_APITOMCP_ALLOW_PRIVATE=true permits the apitomcp SSRF-safe
//     transport to dial loopback IPs. App tests stand up mock MCP servers
//     via httptest.NewServer (which binds 127.0.0.1) and exercise the full
//     handler stack — without this flag, the SSRF protection would block
//     every test's outbound call. Production binaries do not set this var.
func TestMain(m *testing.M) {
	os.Setenv("GONACOS_BCRYPT_COST", "4")
	os.Setenv("GONACOS_APITOMCP_ALLOW_PRIVATE", "true")
	os.Exit(m.Run())
}
