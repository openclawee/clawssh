package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// UserInfo captures operator/session identity.
type UserInfo struct {
	Username   string `json:"username"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
}

// PolicyResult captures policy gate outcome for replay.
type PolicyResult struct {
	Allowed      bool   `json:"allowed"`
	NeedConfirm  bool   `json:"need_confirm"`
	Reason       string `json:"reason,omitempty"`
	ConfirmInput string `json:"confirm_input,omitempty"`
	Confirmed    bool   `json:"confirmed,omitempty"`
}

// ExecutionOutput stores adapter execution result.
type ExecutionOutput struct {
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
	Streamed bool   `json:"streamed,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

// Entry records one operation trail item for replay.
type Entry struct {
	Timestamp       time.Time         `json:"timestamp"`
	Stage           string            `json:"stage"` // parse_error/policy_denied/cancelled/executed/...
	User            UserInfo          `json:"user"`
	RawInput        string            `json:"raw_input"`
	GeneratedDSL    *dsl.Task         `json:"generated_dsl,omitempty"`
	Policy          *PolicyResult     `json:"policy_result,omitempty"`
	ExecutionOutput *ExecutionOutput  `json:"execution_output,omitempty"`
	Extra           map[string]string `json:"extra,omitempty"`
}

// Logger writes audit entries asynchronously to slog and daily JSONL files.
type Logger struct {
	log       *slog.Logger
	baseDir   string
	queue     chan Entry
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// New creates an audit logger using the given slog.Logger.
func New(log *slog.Logger) *Logger {
	if log == nil {
		log = slog.Default()
	}
	l := &Logger{
		log:     log,
		baseDir: filepath.Join("logs", "audit"),
		queue:   make(chan Entry, 512),
	}
	l.wg.Add(1)
	go l.worker()
	return l
}

// Log records an entry asynchronously. When queue is full, it drops oldest behavior
// by rejecting this write and logs a warning to avoid blocking request path.
func (l *Logger) Log(e Entry) {
	if l == nil {
		return
	}
	e.Timestamp = time.Now().UTC()
	select {
	case l.queue <- e:
	default:
		l.log.Warn("audit queue full; dropping entry", "stage", e.Stage, "session_id", e.User.SessionID)
	}
}

// Close flushes queue (best-effort).
func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.closeOnce.Do(func() {
		close(l.queue)
		l.wg.Wait()
	})
}

func (l *Logger) worker() {
	defer l.wg.Done()
	for e := range l.queue {
		l.writeSlog(e)
		if err := l.appendJSONL(e); err != nil {
			l.log.Error("audit persist failed", "err", err, "stage", e.Stage)
		}
	}
}

func (l *Logger) writeSlog(e Entry) {
	args := []any{
		"timestamp", e.Timestamp.Format(time.RFC3339),
		"stage", e.Stage,
		"user", e.User.Username,
		"remote_addr", e.User.RemoteAddr,
		"session_id", e.User.SessionID,
		"raw_input", e.RawInput,
	}
	if e.GeneratedDSL != nil {
		args = append(args, "action", e.GeneratedDSL.Action, "target", e.GeneratedDSL.Target, "is_group", e.GeneratedDSL.IsGroup, "source", e.GeneratedDSL.Source)
	}
	if e.Policy != nil {
		args = append(args, "policy_allowed", e.Policy.Allowed, "policy_need_confirm", e.Policy.NeedConfirm, "policy_reason", e.Policy.Reason, "policy_confirmed", e.Policy.Confirmed)
	}
	if e.ExecutionOutput != nil {
		args = append(args, "exec_ok", e.ExecutionOutput.OK, "exec_exit_code", e.ExecutionOutput.ExitCode, "exec_streamed", e.ExecutionOutput.Streamed)
	}
	l.log.Info("audit", args...)
}

func (l *Logger) appendJSONL(e Entry) error {
	if err := os.MkdirAll(l.baseDir, 0o750); err != nil {
		return err
	}
	fileName := e.Timestamp.Format("2006-01-02") + ".jsonl"
	path := filepath.Join(l.baseDir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}
