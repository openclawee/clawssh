package adapter

import (
	"testing"

	"github.com/clawssh/clawssh/pkg/dsl"
)

func TestLocalSSHAdapter_Execute_CheckDisk(t *testing.T) {
	a := NewLocalSSHAdapter()
	res, err := a.Execute(&dsl.Task{Action: dsl.ActionCheckDisk, Target: dsl.TargetLocal})
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Stdout == "" && res.Stderr == "" {
		t.Fatalf("expected output from df -h, got %+v", res)
	}
}

func TestLocalSSHAdapter_Execute_UnsupportedAction(t *testing.T) {
	a := NewLocalSSHAdapter()
	res, err := a.Execute(&dsl.Task{Action: "reboot", Target: dsl.TargetLocal})
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil || res.OK {
		t.Fatalf("expected failing result, got %+v", res)
	}
}

func TestLocalSSHAdapter_Execute_NilTask(t *testing.T) {
	a := NewLocalSSHAdapter()
	_, err := a.Execute(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
