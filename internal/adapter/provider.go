package adapter

import (
	"errors"
	"fmt"
	"io"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// ToolProvider executes structured tasks using a concrete backend (SSH, Ansible, HTTP, etc.).
// The interface is intentionally narrow to encourage composable, testable adapters.
type ToolProvider interface {
	// Execute runs the given Task and returns a Result. Implementations must not mutate Task
	// in place; callers own the Task lifecycle.
	Execute(task *dsl.Task) (*dsl.Result, error)
}

// StreamToolProvider can stream stdout/stderr during execution.
// Implementations should still return a final Result for consistency.
type StreamToolProvider interface {
	ExecuteStream(task *dsl.Task, stdout, stderr io.Writer) (*dsl.Result, error)
}

// PoolStatsProvider exposes lightweight connection pool stats for monitoring.
type PoolStatsProvider interface {
	ConnectionPoolStats() map[string]any
}

const (
	ErrCodeUnsupportedSource = "unsupported_source"
	ErrCodeConnectionFailed  = "connection_failed"
	ErrCodeAuthFailed        = "auth_failed"
	ErrCodeTimeout           = "timeout"
	ErrCodeInvalidInput      = "invalid_input"
	ErrCodeUnsupportedAction = "unsupported_action"
	ErrCodeExecFailed        = "execution_failed"
)

// ExecError carries a stable error code for higher-level recovery logic.
type ExecError struct {
	Code    string
	Message string
	Cause   error
}

func (e *ExecError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
}

func (e *ExecError) Unwrap() error { return e.Cause }

func newExecError(code, message string, cause error) error {
	return &ExecError{Code: code, Message: message, Cause: cause}
}

func paramString(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, ok := m[k]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	case int:
		return fmt.Sprintf("%d", t)
	default:
		return fmt.Sprint(t)
	}
}

func notImplemented(msg string) (*dsl.Result, error) {
	return &dsl.Result{OK: false, ExitCode: -1, Message: msg}, newExecError(ErrCodeUnsupportedAction, msg, errors.New(msg))
}
