package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/clawssh/clawssh/configs"
	"github.com/clawssh/clawssh/internal/knowledge"
	"github.com/clawssh/clawssh/internal/monitor"
	"github.com/clawssh/clawssh/pkg/dsl"
)

// LLMIntentEngine implements IntentEngine using an LLM and JSON schema validation.
type LLMIntentEngine struct {
	client LLMClient
	system string
	parser *TaskParser
	log    *slog.Logger
	kb     *knowledge.Base
	mon    *monitor.Reporter
}

// LLMIntentConfig wires the model client and embedded assets.
type LLMIntentConfig struct {
	Client       LLMClient
	SystemPrompt string
	SchemaJSON   []byte
	Log          *slog.Logger
	Knowledge    *knowledge.Base
	Monitor      *monitor.Reporter
}

// NewLLMIntentEngine constructs an LLM-backed parser.
func NewLLMIntentEngine(cfg LLMIntentConfig) (*LLMIntentEngine, error) {
	if cfg.Client == nil {
		return nil, errors.New("LLM client is nil")
	}
	if cfg.SystemPrompt == "" {
		return nil, errors.New("system prompt is empty")
	}
	p, err := NewTaskParser(cfg.SchemaJSON)
	if err != nil {
		return nil, err
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &LLMIntentEngine{
		client: cfg.Client,
		system: cfg.SystemPrompt,
		parser: p,
		log:    log,
		kb:     cfg.Knowledge,
		mon:    cfg.Monitor,
	}, nil
}

// NewLLMIntentEngineFromEnv builds the engine using CLAWSSH_* / CLAW_AI_API_KEY and embedded configs.
func NewLLMIntentEngineFromEnv(ctx context.Context, log *slog.Logger) (*LLMIntentEngine, error) {
	c, err := NewLLMClientFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	opsRoot := strings.TrimSpace(os.Getenv("CLAWSSH_KNOWLEDGE_PATH"))
	if opsRoot == "" {
		opsRoot = filepath.Join("docs", "ops")
	}
	kb, err := knowledge.LoadLocal(opsRoot)
	if err != nil {
		return nil, err
	}
	return NewLLMIntentEngine(LLMIntentConfig{
		Client:       c,
		SystemPrompt: configs.IntentSystemPrompt,
		SchemaJSON:   configs.TaskSchemaJSON,
		Log:          log,
		Knowledge:    kb,
		Monitor:      monitor.Default(),
	})
}

// ShowProgress implements ProgressReporter.
func (e *LLMIntentEngine) ShowProgress() bool { return true }

// Parse calls the LLM, validates JSON, and retries once with validation errors (self-correction).
func (e *LLMIntentEngine) Parse(ctx context.Context, input string, ses *Context, progress io.Writer) (*dsl.Task, error) {
	if progress == nil {
		progress = io.Discard
	}
	user := buildIntentUserPayload(ses, input)

	if _, err := io.WriteString(progress, "[ClawSSH]: 正在解析意图...\n"); err != nil {
		e.log.Debug("progress write failed", "err", err)
	}

	systemPrompt := e.system
	if e.kb != nil {
		if ref := e.kb.ReferenceSOP(input); ref != "" {
			systemPrompt = systemPrompt + "\n\n" + ref
		}
	}

	start := time.Now()
	out, err := e.client.Generate(ctx, systemPrompt, user)
	if e.mon != nil {
		e.mon.RecordLLM(time.Since(start), err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAIUnavailable, err)
	}

	jsonStr := ExtractJSONFromModelOutput(out)
	task, vErr := e.parser.ValidateAndUnmarshal(jsonStr)
	if vErr == nil {
		return task, nil
	}

	e.log.Warn("intent validation failed, retrying with error hint", "err", vErr)
	retryUser := user + fmt.Sprintf(
		"\n\nYour previous answer failed validation: %v\nRaw output:\n%s\nReturn a single corrected JSON object only.",
		vErr, truncateForLog(out, 4000),
	)
	start = time.Now()
	out2, err2 := e.client.Generate(ctx, systemPrompt, retryUser)
	if e.mon != nil {
		e.mon.RecordLLM(time.Since(start), err2)
	}
	if err2 != nil {
		return nil, fmt.Errorf("%w: %w", ErrAIUnavailable, err2)
	}
	json2 := ExtractJSONFromModelOutput(out2)
	task2, vErr2 := e.parser.ValidateAndUnmarshal(json2)
	if vErr2 != nil {
		return nil, fmt.Errorf("intent: invalid json after retry: %w (first: %v)", vErr2, vErr)
	}
	return task2, nil
}

func buildIntentUserPayload(ses *Context, utterance string) string {
	env := map[string]any{}
	if ses != nil {
		env["ssh_user"] = ses.Username
		env["remote_addr"] = ses.RemoteAddr
		env["session_id"] = ses.SessionID
		if ses.Env != nil {
			env["os"] = ses.Env.OS
			env["arch"] = ses.Env.Arch
			env["hostname"] = ses.Env.Hostname
			if len(ses.Env.Infra) > 0 {
				env["infra_probe"] = ses.Env.Infra
			}
		}
	}
	jb, _ := json.MarshalIndent(env, "", "  ")
	return "env_context:\n" + string(jb) + "\n\nuser_request:\n" + utterance
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
