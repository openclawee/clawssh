package adapter

import (
	"errors"
	"testing"

	"github.com/clawssh/clawssh/pkg/dsl"
)

type fakeProvider struct {
	name string
}

func (f fakeProvider) Execute(task *dsl.Task) (*dsl.Result, error) {
	if task == nil {
		return nil, errors.New("nil")
	}
	return &dsl.Result{OK: true, ExitCode: 0, Stdout: f.name}, nil
}

func TestRegistry_RoutesBySource(t *testing.T) {
	r := NewRegistry("ssh")
	if err := r.Register("ssh", fakeProvider{name: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("ansible", fakeProvider{name: "ansible"}); err != nil {
		t.Fatal(err)
	}

	res, err := r.Execute(&dsl.Task{Source: "ansible", Action: dsl.ActionCheckCPU})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "ansible" {
		t.Fatalf("expected ansible provider, got %q", res.Stdout)
	}
}

func TestRegistry_DefaultSource(t *testing.T) {
	r := NewRegistry("ssh")
	_ = r.Register("ssh", fakeProvider{name: "local"})
	res, err := r.Execute(&dsl.Task{Action: dsl.ActionCheckCPU})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "local" {
		t.Fatalf("expected default ssh provider, got %q", res.Stdout)
	}
}
