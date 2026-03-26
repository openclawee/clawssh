package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/clawssh/clawssh/pkg/dsl"
)

func TestKeywordRouter_Parse_CheckCPU(t *testing.T) {
	r := NewKeywordRouter()
	ctx := &Context{Username: "alice", RemoteAddr: "127.0.0.1:1", SessionID: "sess-1"}
	task, err := r.Parse(context.Background(), "check cpu", ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Action != dsl.ActionCheckCPU || task.Target != dsl.TargetLocal {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestKeywordRouter_Parse_CheckDisk(t *testing.T) {
	r := NewKeywordRouter()
	task, err := r.Parse(context.Background(), "  CHECK DISK  ", &Context{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Action != dsl.ActionCheckDisk {
		t.Fatalf("unexpected action: %q", task.Action)
	}
}

func TestKeywordRouter_Parse_Unknown(t *testing.T) {
	r := NewKeywordRouter()
	_, err := r.Parse(context.Background(), "reboot everything", &Context{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnknownIntent) {
		t.Fatalf("expected ErrUnknownIntent, got %v", err)
	}
}
