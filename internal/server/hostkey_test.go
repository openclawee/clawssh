package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureHostKey_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host_key")
	if err := EnsureHostKey(path); err != nil {
		t.Fatalf("EnsureHostKey: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 perms, got %v", st.Mode().Perm())
	}
	// Second call should be a no-op.
	if err := EnsureHostKey(path); err != nil {
		t.Fatalf("EnsureHostKey second: %v", err)
	}
}

func TestEnsureHostKey_EmptyPath(t *testing.T) {
	if err := EnsureHostKey(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}
