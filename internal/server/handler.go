package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	glider "github.com/gliderlabs/ssh"

	"github.com/clawssh/clawssh/internal/adapter"
	"github.com/clawssh/clawssh/internal/audit"
	"github.com/clawssh/clawssh/internal/engine"
	"github.com/clawssh/clawssh/internal/explorer"
	"github.com/clawssh/clawssh/internal/policy"
	"github.com/clawssh/clawssh/pkg/dsl"
)

// envForPolicy returns the environment name for policy context.
func (s *Server) envForPolicy() string {
	if s.cfg.Environment != "" {
		return strings.ToLower(strings.TrimSpace(s.cfg.Environment))
	}
	return policy.EnvDevelopment
}

// intentParseTimeout returns the per-line LLM budget (default 60s).
func (s *Server) intentParseTimeout() time.Duration {
	if s.cfg.IntentParseTimeout > 0 {
		return s.cfg.IntentParseTimeout
	}
	return 60 * time.Second
}

// handleUserLine runs intent → policy → (optional confirm) → audit → tool execution for one non-empty input line.
func (s *Server) handleUserLine(parent context.Context, sess glider.Session, br *bufio.Reader, log *slog.Logger, sid, line string, snap explorer.Snapshot) {
	ctx, cancel := context.WithTimeout(parent, s.intentParseTimeout())
	defer cancel()

	hostname, _ := os.Hostname()
	engCtx := &engine.Context{
		Username:   sess.User(),
		RemoteAddr: sess.RemoteAddr().String(),
		SessionID:  sid,
		Env: &engine.EnvContext{
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			Hostname: hostname,
			Infra:    snap.Values,
		},
	}
	if s.cfg.Inventory != nil {
		engCtx.Env.InventoryAliases = s.cfg.Inventory.Aliases()
		engCtx.Env.InventoryGroups = s.cfg.Inventory.Groups()
	}

	var progress io.Writer
	if pr, ok := s.intent.(engine.ProgressReporter); ok && pr.ShowProgress() {
		progress = sess
	}

	task, err := s.intent.Parse(ctx, line, engCtx, progress)
	if err != nil {
		s.audit.Log(audit.Entry{
			Stage:    "intent_parse_error",
			User:     audit.UserInfo{Username: engCtx.Username, RemoteAddr: engCtx.RemoteAddr, SessionID: sid},
			RawInput: line,
			ExecutionOutput: &audit.ExecutionOutput{
				OK:    false,
				Error: err.Error(),
			},
		})
		if errors.Is(err, context.DeadlineExceeded) {
			if _, werr := io.WriteString(sess, "intent parse timed out; try a shorter request or standard commands (check cpu / check disk).\n"); werr != nil {
				log.Error("write timeout msg", "err", werr)
			}
			return
		}
		if errors.Is(err, engine.ErrAIUnavailable) {
			log.Warn("ai engine unavailable", "input", line, "err", err)
			if _, werr := io.WriteString(sess, "AI 引擎暂时不可用，请尝试标准指令（如 check cpu、check disk）。\n"); werr != nil {
				log.Error("write ai unavailable", "err", werr)
			}
			if _, werr := io.WriteString(sess, "（服务端日志中有详细错误原因，请查看运行 clawssh 的终端。）\n"); werr != nil {
				log.Error("write ai hint", "err", werr)
			}
			return
		}
		log.Info("intent parse failed", "input", line, "err", err)
		if _, werr := fmt.Fprintf(sess, "parse error: %v\n", err); werr != nil {
			log.Error("write parse error", "err", werr)
		}
		return
	}
	s.maybeFixInventoryTargetByInput(line, task)

	log.Info("task planned", "action", task.Action, "target", task.Target)

	polCtx := &policy.Context{
		Environment:    s.envForPolicy(),
		Username:       engCtx.Username,
		RemoteAddr:     engCtx.RemoteAddr,
		AllowedTargets: s.inventoryTargets(),
	}
	polRes := s.policy.Evaluate(polCtx, task)
	if !polRes.Allowed {
		s.audit.Log(audit.Entry{
			Stage:        "policy_denied",
			User:         audit.UserInfo{Username: engCtx.Username, RemoteAddr: engCtx.RemoteAddr, SessionID: sid},
			RawInput:     line,
			GeneratedDSL: task,
			Policy: &audit.PolicyResult{
				Allowed:     polRes.Allowed,
				NeedConfirm: polRes.NeedConfirm,
				Reason:      polRes.Reason,
			},
		})
		log.Warn("policy denied", "action", task.Action, "reason", polRes.Reason)
		if _, werr := fmt.Fprintf(sess, "policy denied: %s\n", polRes.Reason); werr != nil {
			log.Error("write policy denial", "err", werr)
		}
		return
	}

	if polRes.NeedConfirm {
		warn := fmt.Sprintf("\n[!] %s\n确认执行？输入 yes 继续，其他任意输入取消: ", polRes.Reason)
		if _, werr := io.WriteString(sess, warn); werr != nil {
			log.Error("write confirm prompt", "err", werr)
			return
		}
		confirm, err := readLineWithEcho(br, sess, log)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			log.Error("read confirm", "err", err)
			return
		}
		if strings.TrimSpace(strings.ToLower(confirm)) != "yes" {
			s.audit.Log(audit.Entry{
				Stage:        "policy_cancelled",
				User:         audit.UserInfo{Username: engCtx.Username, RemoteAddr: engCtx.RemoteAddr, SessionID: sid},
				RawInput:     line,
				GeneratedDSL: task,
				Policy: &audit.PolicyResult{
					Allowed:      true,
					NeedConfirm:  true,
					Reason:       polRes.Reason,
					ConfirmInput: confirm,
					Confirmed:    false,
				},
			})
			if _, werr := io.WriteString(sess, "操作已取消。\n"); werr != nil {
				log.Error("write cancel msg", "err", werr)
			}
			return
		}
	}

	var (
		res      *dsl.Result
		execErr  error
		streamed bool
	)
	if sp, ok := s.tools.(adapter.StreamToolProvider); ok && s.shouldStreamAction(task.Action) {
		streamed = true
		if _, werr := io.WriteString(sess, "[ClawSSH]: executing...\n"); werr != nil {
			log.Error("write exec progress", "err", werr)
		}
		res, execErr = sp.ExecuteStream(task, sess, sess)
	} else {
		res, execErr = s.tools.Execute(task)
	}
	if execErr != nil {
		log.Error("tool execution failed", "action", task.Action, "err", execErr)
		if hint := adapterRecoveryHint(execErr); hint != "" {
			if _, werr := fmt.Fprintf(sess, "hint: %s\n", hint); werr != nil {
				log.Error("write recovery hint", "err", werr)
			}
		}
	}
	if res == nil {
		s.audit.Log(audit.Entry{
			Stage:        "execution_empty_result",
			User:         audit.UserInfo{Username: engCtx.Username, RemoteAddr: engCtx.RemoteAddr, SessionID: sid},
			RawInput:     line,
			GeneratedDSL: task,
			Policy: &audit.PolicyResult{
				Allowed:      true,
				NeedConfirm:  polRes.NeedConfirm,
				Reason:       polRes.Reason,
				ConfirmInput: "yes",
				Confirmed:    true,
			},
			ExecutionOutput: &audit.ExecutionOutput{
				OK:       false,
				ExitCode: -1,
				Error:    "internal error: empty result",
				Streamed: streamed,
			},
		})
		if _, werr := io.WriteString(sess, "internal error: empty result\n"); werr != nil {
			log.Error("write empty result", "err", werr)
		}
		return
	}
	if streamed {
		// Output already streamed live to session; keep only metadata/message in final formatter.
		res.Stdout = ""
		res.Stderr = ""
	}
	execOut := &audit.ExecutionOutput{
		OK:       res.OK,
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		Message:  res.Message,
		Streamed: streamed,
	}
	if execErr != nil {
		execOut.Error = execErr.Error()
	}
	// Best-effort one-line summary for long outputs.
	if s.summarizer != nil && s.summarizer.ShouldSummarize(execOut.Stdout, execOut.Stderr) {
		sum, err := s.summarizer.Summarize(parent, task, execOut.Stdout, execOut.Stderr)
		if err != nil {
			log.Debug("output summary failed", "err", err)
		} else if strings.TrimSpace(sum) != "" {
			execOut.Summary = sum
		}
	}
	s.audit.Log(audit.Entry{
		Stage:        "executed",
		User:         audit.UserInfo{Username: engCtx.Username, RemoteAddr: engCtx.RemoteAddr, SessionID: sid},
		RawInput:     line,
		GeneratedDSL: task,
		Policy: &audit.PolicyResult{
			Allowed:      true,
			NeedConfirm:  polRes.NeedConfirm,
			Reason:       polRes.Reason,
			ConfirmInput: "yes",
			Confirmed:    true,
		},
		ExecutionOutput: execOut,
	})

	writeTaskResult(sess, log, res)
	if execOut.Summary != "" {
		if _, werr := fmt.Fprintf(sess, "summary: %s\n", execOut.Summary); werr != nil {
			log.Error("write summary", "err", werr)
		}
	}
}

func (s *Server) maybeFixInventoryTargetByInput(line string, task *dsl.Task) {
	if s == nil || s.cfg.Inventory == nil || task == nil {
		return
	}
	lower := strings.ToLower(line)

	// If model already set a known non-local target, keep it.
	target := strings.TrimSpace(task.Target)
	if target != "" && target != dsl.TargetLocal {
		return
	}

	// Prefer explicit group mention first.
	for _, g := range s.cfg.Inventory.Groups() {
		if strings.Contains(lower, strings.ToLower(g)) {
			task.Target = g
			task.IsGroup = true
			return
		}
	}
	for _, a := range s.cfg.Inventory.Aliases() {
		if strings.Contains(lower, strings.ToLower(a)) {
			task.Target = a
			task.IsGroup = false
			return
		}
	}
}

func (s *Server) inventoryTargets() map[string]struct{} {
	if s == nil || s.cfg.Inventory == nil {
		return nil
	}
	out := map[string]struct{}{}
	for _, a := range s.cfg.Inventory.Aliases() {
		out[a] = struct{}{}
	}
	for _, g := range s.cfg.Inventory.Groups() {
		out[g] = struct{}{}
	}
	return out
}

func (s *Server) shouldStreamAction(action string) bool {
	if s == nil || len(s.streamActions) == 0 {
		return false
	}
	_, ok := s.streamActions[strings.ToLower(strings.TrimSpace(action))]
	return ok
}

func adapterRecoveryHint(err error) string {
	var ee *adapter.ExecError
	if !errors.As(err, &ee) || ee == nil {
		return ""
	}
	switch ee.Code {
	case adapter.ErrCodeConnectionFailed:
		return "远程连接失败，请检查 host/port 是否可达，或改用 source=ssh 本地执行。"
	case adapter.ErrCodeAuthFailed:
		return "远程认证失败，请检查私钥路径/内容与目标主机用户权限。"
	case adapter.ErrCodeTimeout:
		return "远程连接超时，请检查网络连通性、防火墙，或稍后重试。"
	case adapter.ErrCodeUnsupportedSource:
		return "未找到执行源，请在 DSL 中设置已注册 source（如 ssh 或 ssh_remote）。"
	case adapter.ErrCodeUnsupportedAction:
		return "当前执行器不支持该 action，可切换 source 或改用支持的动作。"
	case adapter.ErrCodeInvalidInput:
		return "DSL 参数不完整或格式错误，请检查 parameters 字段。"
	default:
		return ""
	}
}

func writeTaskResult(sess glider.Session, log *slog.Logger, res *dsl.Result) {
	if res.Message != "" {
		if _, err := fmt.Fprintf(sess, "message: %s\n", res.Message); err != nil {
			log.Error("write message", "err", err)
			return
		}
	}
	if _, err := fmt.Fprintf(sess, "exit=%d ok=%v\n", res.ExitCode, res.OK); err != nil {
		log.Error("write meta", "err", err)
		return
	}
	if res.Stdout != "" {
		if _, err := fmt.Fprintf(sess, "--- stdout ---\n%s\n", strings.TrimSuffix(res.Stdout, "\n")); err != nil {
			log.Error("write stdout", "err", err)
			return
		}
	}
	if res.Stderr != "" {
		if _, err := fmt.Fprintf(sess, "--- stderr ---\n%s\n", strings.TrimSuffix(res.Stderr, "\n")); err != nil {
			log.Error("write stderr", "err", err)
			return
		}
	}
}
