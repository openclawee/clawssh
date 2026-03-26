package adapter

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// Registry routes tasks to concrete providers based on task.Source.
type Registry struct {
	mu            sync.RWMutex
	providers     map[string]ToolProvider
	defaultSource string
}

func NewRegistry(defaultSource string) *Registry {
	if strings.TrimSpace(defaultSource) == "" {
		defaultSource = "ssh"
	}
	return &Registry{
		providers:     make(map[string]ToolProvider),
		defaultSource: strings.ToLower(strings.TrimSpace(defaultSource)),
	}
}

func (r *Registry) Register(source string, p ToolProvider) error {
	if p == nil {
		return fmt.Errorf("register source %q: nil provider", source)
	}
	s := strings.ToLower(strings.TrimSpace(source))
	if s == "" {
		return fmt.Errorf("register source: empty source")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[s] = p
	return nil
}

func (r *Registry) Execute(task *dsl.Task) (*dsl.Result, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}
	src := strings.ToLower(strings.TrimSpace(task.Source))
	if src == "" {
		src = r.defaultSource
	}
	r.mu.RLock()
	p := r.providers[src]
	r.mu.RUnlock()
	if p == nil {
		msg := fmt.Sprintf("no provider registered for source %q", src)
		return &dsl.Result{OK: false, ExitCode: -1, Message: msg}, newExecError(ErrCodeUnsupportedSource, msg, nil)
	}
	return p.Execute(task)
}

// ExecuteStream routes streaming execution when provider supports it; otherwise falls back to Execute.
func (r *Registry) ExecuteStream(task *dsl.Task, stdout, stderr io.Writer) (*dsl.Result, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}
	src := strings.ToLower(strings.TrimSpace(task.Source))
	if src == "" {
		src = r.defaultSource
	}
	r.mu.RLock()
	p := r.providers[src]
	r.mu.RUnlock()
	if p == nil {
		msg := fmt.Sprintf("no provider registered for source %q", src)
		return &dsl.Result{OK: false, ExitCode: -1, Message: msg}, newExecError(ErrCodeUnsupportedSource, msg, nil)
	}
	if sp, ok := p.(StreamToolProvider); ok {
		return sp.ExecuteStream(task, stdout, stderr)
	}
	return p.Execute(task)
}

func (r *Registry) ConnectionPoolStats() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]any{
		"type":      "registry",
		"providers": len(r.providers),
	}
	pools := map[string]any{}
	for source, p := range r.providers {
		if ps, ok := p.(PoolStatsProvider); ok {
			pools[source] = ps.ConnectionPoolStats()
		}
	}
	out["pool_stats"] = pools
	return out
}
