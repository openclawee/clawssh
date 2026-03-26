package dsl

import "testing"

func TestConstants(t *testing.T) {
	if ActionCheckCPU == "" || ActionCheckDisk == "" {
		t.Fatal("action constants must be non-empty")
	}
	if TargetLocal == "" {
		t.Fatal("TargetLocal must be non-empty")
	}
	if got := len(CoreActionIDs()); got != 20 {
		t.Fatalf("CoreActionIDs: want 20 entries, got %d", got)
	}
}
