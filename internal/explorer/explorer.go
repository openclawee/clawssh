package explorer

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Snapshot is a structured environment probe result captured at session start.
type Snapshot struct {
	CollectedAt time.Time         `json:"collected_at"`
	Values      map[string]string `json:"values"`
}

// Explorer runs a lightweight probe command set.
type Explorer struct {
	timeout time.Duration
	probes  []Probe
}

type Probe struct {
	Key     string   `yaml:"key"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

type Config struct {
	TimeoutSeconds int     `yaml:"timeout_seconds"`
	Probes         []Probe `yaml:"probes"`
}

func New() *Explorer {
	return &Explorer{
		timeout: 4 * time.Second,
		probes:  defaultProbes(),
	}
}

// NewWithConfig builds an explorer with optional YAML + ENV overrides.
// - yamlPath: optional yaml config path.
// - probeFilter: optional probe key allowlist, from ENV (comma-separated).
// - timeoutOverride: optional timeout override from ENV.
func NewWithConfig(yamlPath string, probeFilter []string, timeoutOverride time.Duration) (*Explorer, error) {
	e := New()
	if strings.TrimSpace(yamlPath) != "" {
		cfg, err := loadConfigFile(yamlPath)
		if err != nil {
			return nil, err
		}
		if cfg.TimeoutSeconds > 0 {
			e.timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
		}
		if len(cfg.Probes) > 0 {
			e.probes = cfg.Probes
		}
	}
	if timeoutOverride > 0 {
		e.timeout = timeoutOverride
	}
	if len(probeFilter) > 0 {
		e.probes = filterProbes(e.probes, probeFilter)
	}
	return e, nil
}

func (e *Explorer) Probe(ctx context.Context) Snapshot {
	if e == nil {
		e = New()
	}
	if e.timeout <= 0 {
		e.timeout = 4 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	values := make(map[string]string, len(e.probes))
	for _, p := range e.probes {
		key := strings.TrimSpace(p.Key)
		cmd := strings.TrimSpace(p.Command)
		if key == "" || cmd == "" {
			continue
		}
		values[key] = runShort(probeCtx, cmd, p.Args...)
	}
	return Snapshot{
		CollectedAt: time.Now().UTC(),
		Values:      values,
	}
}

func loadConfigFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaultProbes() []Probe {
	return []Probe{
		{Key: "kernel", Command: "uname", Args: []string{"-a"}},
		{Key: "nginx_path", Command: "sh", Args: []string{"-c", "command -v nginx || echo not_found"}},
		{Key: "docker_path", Command: "sh", Args: []string{"-c", "command -v docker || echo not_found"}},
		{Key: "systemctl", Command: "sh", Args: []string{"-c", "command -v systemctl || echo not_found"}},
	}
}

func filterProbes(all []Probe, keys []string) []Probe {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		v := strings.ToLower(strings.TrimSpace(k))
		if v != "" {
			set[v] = struct{}{}
		}
	}
	if len(set) == 0 {
		return all
	}
	out := make([]Probe, 0, len(all))
	for _, p := range all {
		if _, ok := set[strings.ToLower(strings.TrimSpace(p.Key))]; ok {
			out = append(out, p)
		}
	}
	return out
}

func runShort(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "error: " + err.Error()
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return "unknown"
	}
	if len(s) > 240 {
		return s[:240] + "..."
	}
	return s
}
