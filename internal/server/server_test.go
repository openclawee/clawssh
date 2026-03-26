package server

import (
	"log/slog"
	"os"
	"testing"

	"github.com/clawssh/clawssh/internal/adapter"
	"github.com/clawssh/clawssh/internal/engine"
	"github.com/clawssh/clawssh/internal/policy"
)

func TestNew_ValidatesDependencies(t *testing.T) {
	cfg := Config{Addr: ":0", HostKeyPath: "/tmp/will-not-be-used", Password: "x"}
	_, err := New(cfg, nil, policy.NewPolicyEngine(), nil, adapter.NewLocalSSHAdapter(), slog.Default())
	if err == nil {
		t.Fatal("expected error for nil intent")
	}
	_, err = New(cfg, engine.NewKeywordRouter(), nil, nil, adapter.NewLocalSSHAdapter(), slog.Default())
	if err == nil {
		t.Fatal("expected error for nil policy")
	}
	_, err = New(cfg, engine.NewKeywordRouter(), policy.NewPolicyEngine(), nil, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for nil tools")
	}
	_, err = New(cfg, engine.NewKeywordRouter(), policy.NewPolicyEngine(), nil, adapter.NewLocalSSHAdapter(), nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestNew_RequiresAuth(t *testing.T) {
	cfg := Config{Addr: ":0", HostKeyPath: "/tmp/x"}
	_, err := New(cfg, engine.NewKeywordRouter(), policy.NewPolicyEngine(), nil, adapter.NewLocalSSHAdapter(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err == nil {
		t.Fatal("expected error when no auth configured")
	}
}

func TestStreamActionSet_Defaults(t *testing.T) {
	got := streamActionSet(nil)
	if _, ok := got["tail_log"]; !ok {
		t.Fatal("expected tail_log in default stream action set")
	}
	if _, ok := got["service_control"]; !ok {
		t.Fatal("expected service_control in default stream action set")
	}
}
