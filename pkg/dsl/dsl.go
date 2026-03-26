// Package dsl defines the cross-cutting task model for ClawSSH.
// It is intentionally free of transport or policy logic so adapters and engines
// can depend on a stable, versionable contract.
package dsl

// TargetLocal indicates execution on the gateway host itself (MVP default).
const TargetLocal = "local"

// Task is the structured unit of work produced by an IntentEngine and consumed by ToolProviders.
type Task struct {
	// Source selects which adapter backend should execute the task (e.g. "ssh", "ansible", "monitor").
	// When empty, the server's adapter registry fallback is used.
	Source string `json:"source,omitempty"`
	// IsGroup indicates whether Target is an inventory group name.
	IsGroup bool `json:"is_group,omitempty"`
	// Action names what to run (e.g. ActionCheckCPU). It must be a known, policy-approved value.
	Action string `json:"action"`
	// Target describes where the action should run. MVP supports TargetLocal only.
	Target string `json:"target,omitempty"`
	// Args carries optional positional parameters (legacy / simple adapters).
	Args []string `json:"args,omitempty"`
	// Parameters carries structured key-value arguments from the LLM (paths, service names, ports, etc.).
	Parameters map[string]any `json:"parameters,omitempty"`
}

// Result captures stdout/stderr and process metadata after a ToolProvider runs a Task.
type Result struct {
	// OK is true when the command exited with code 0 and no hard execution error occurred.
	OK bool `json:"ok"`
	// ExitCode is the OS exit code when available; -1 indicates the process did not start or was unknown.
	ExitCode int `json:"exit_code"`
	// Stdout contains captured standard output (may be empty).
	Stdout string `json:"stdout"`
	// Stderr contains captured standard error (may be empty).
	Stderr string `json:"stderr"`
	// Message carries a human-readable summary for operators (errors, policy text, etc.).
	Message string `json:"message,omitempty"`
}
