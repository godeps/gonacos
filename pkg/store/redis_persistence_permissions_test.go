package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteDumpFileDirectoryPermissions verifies the dump directory is
// created with mode 0o700 — the dump file contains bcrypt password hashes,
// namespace configs, and arbitrary config values (which may include
// database URLs with credentials or API keys). Restricting the directory
// to the gonacos process user is defense in depth.
//
// 0o700 = rwx------ : owner can read/write/traverse, no group, no other.
// On a multi-user host, an attacker with another account cannot traverse
// into the data dir to read the dump.
func TestWriteDumpFileDirectoryPermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nested := filepath.Join(dir, "data", "gonacos")
	dumpPath := filepath.Join(nested, "snapshot.json")

	if err := writeDumpFile(dumpPath, []byte("{}")); err != nil {
		t.Fatalf("writeDumpFile: %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o700 {
		t.Errorf("nested dir mode = %o, want 0700", mode)
	}

	// The dump file itself should be 0o600 (os.CreateTemp's default,
	// preserved across os.Rename).
	dumpInfo, err := os.Stat(dumpPath)
	if err != nil {
		t.Fatalf("stat dump: %v", err)
	}
	dumpMode := dumpInfo.Mode().Perm()
	if dumpMode != 0o600 {
		t.Errorf("dump file mode = %o, want 0600", dumpMode)
	}
}

// TestWriteDumpFilePreExistingDirectoryModeUnchanged verifies MkdirAll
// only sets the mode on directories it creates — a pre-existing directory
// keeps its existing mode. This is the safety contract: an operator who
// pre-provisions the data dir with a specific mode (e.g., to share with a
// backup agent) is not surprised by gonacos changing it out from under
// them.
func TestWriteDumpFilePreExistingDirectoryModeUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Pre-create with 0o750 (group can read, others can't).
	nested := filepath.Join(dir, "data")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dumpPath := filepath.Join(nested, "snapshot.json")

	if err := writeDumpFile(dumpPath, []byte("{}")); err != nil {
		t.Fatalf("writeDumpFile: %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("pre-existing dir mode changed: got %o, want 0750", got)
	}
}
