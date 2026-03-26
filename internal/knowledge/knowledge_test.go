package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceSOP_MatchKeyword(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "mysql.md")
	if err := os.WriteFile(p, []byte("MySQL backup SOP: use xtrabackup and verify restore."), 0o644); err != nil {
		t.Fatal(err)
	}
	kb, err := LoadLocal(tmp)
	if err != nil {
		t.Fatal(err)
	}
	ref := kb.ReferenceSOP("how to backup mysql safely")
	if !strings.Contains(ref, "Reference SOP") || !strings.Contains(strings.ToLower(ref), "mysql") {
		t.Fatalf("unexpected reference block: %q", ref)
	}
}
