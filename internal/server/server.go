package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	glider "github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/clawssh/clawssh/internal/adapter"
	"github.com/clawssh/clawssh/internal/audit"
	"github.com/clawssh/clawssh/internal/engine"
	"github.com/clawssh/clawssh/internal/explorer"
	"github.com/clawssh/clawssh/internal/inventory"
	"github.com/clawssh/clawssh/internal/monitor"
	"github.com/clawssh/clawssh/internal/policy"
)

// Config controls listener, authentication, and host key material for the SSH gateway.
type Config struct {
	// Addr is the listen address (for example ":2222").
	Addr string
	// HostKeyPath points to an on-disk OpenSSH PEM private key used as the server host key.
	HostKeyPath string
	// Password, when non-empty, enables password authentication with this shared secret.
	// For production deployments prefer public keys via AuthorizedKeysPath.
	Password string
	// AuthorizedKeysPath, when non-empty, enables public-key authentication using an OpenSSH authorized_keys file.
	AuthorizedKeysPath string
	// IntentParseTimeout bounds a single LLM intent parse (default 60s when zero).
	IntentParseTimeout time.Duration
	// Environment for policy context: "production", "staging", "development".
	Environment string
	// ExplorerConfigPath points to optional YAML file describing probe list.
	ExplorerConfigPath string
	// ExplorerProbeFilter limits probes by keys when set (ENV override).
	ExplorerProbeFilter []string
	// ExplorerTimeout overrides probe timeout when > 0.
	ExplorerTimeout time.Duration
	// StreamActionAllowlist controls which actions use streaming output.
	// Empty means use server defaults.
	StreamActionAllowlist []string
	// Inventory holds parsed alias/group indexes for target resolution.
	Inventory *inventory.Inventory
}

// Server wires transport (gliderlabs/ssh), intent parsing, policy, audit, and tool execution.
type Server struct {
	cfg           Config
	intent        engine.IntentEngine
	policy        *policy.PolicyEngine
	audit         *audit.Logger
	tools         adapter.ToolProvider
	expl          *explorer.Explorer
	streamActions map[string]struct{}
	log           *slog.Logger
	mon           *monitor.Reporter
	summarizer    *engine.OutputSummarizer

	sessionCounter uint64
}

// New constructs a Server. Callers must supply non-nil dependencies.
func New(cfg Config, intent engine.IntentEngine, pol *policy.PolicyEngine, aud *audit.Logger, tools adapter.ToolProvider, log *slog.Logger) (*Server, error) {
	if intent == nil {
		return nil, errors.New("intent engine is nil")
	}
	if pol == nil {
		return nil, errors.New("policy engine is nil")
	}
	if tools == nil {
		return nil, errors.New("tool provider is nil")
	}
	if log == nil {
		return nil, errors.New("logger is nil")
	}
	if cfg.Addr == "" {
		return nil, errors.New("listen addr is empty")
	}
	if cfg.HostKeyPath == "" {
		return nil, errors.New("host key path is empty")
	}
	if cfg.Password == "" && cfg.AuthorizedKeysPath == "" {
		return nil, errors.New("must configure Password and/or AuthorizedKeysPath")
	}
	if aud == nil {
		aud = audit.New(log)
	}
	expl, err := explorer.NewWithConfig(cfg.ExplorerConfigPath, cfg.ExplorerProbeFilter, cfg.ExplorerTimeout)
	if err != nil {
		return nil, fmt.Errorf("explorer config: %w", err)
	}
	summarizer, _ := engine.NewOutputSummarizerFromEnv(context.Background())
	return &Server{
		cfg:           cfg,
		intent:        intent,
		policy:        pol,
		audit:         aud,
		tools:         tools,
		expl:          expl,
		streamActions: streamActionSet(cfg.StreamActionAllowlist),
		log:           log,
		mon:           monitor.Default(),
		summarizer:    summarizer,
	}, nil
}

func streamActionSet(allowlist []string) map[string]struct{} {
	actions := allowlist
	if len(actions) == 0 {
		actions = []string{
			"install_package",
			"service_control",
			"tail_log",
			"package_list",
		}
	}
	out := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		v := strings.ToLower(strings.TrimSpace(a))
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

// Run starts the SSH listener and blocks until the context is cancelled or ListenAndServe returns.
func (s *Server) Run(ctx context.Context) error {
	srv := &glider.Server{
		Addr:    s.cfg.Addr,
		Handler: s.handleSession,
		// Version string helps operators identify the gateway in SSH banners.
		Version: "ClawSSH",
	}

	if err := srv.SetOption(glider.HostKeyFile(s.cfg.HostKeyPath)); err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	if s.cfg.Password != "" {
		if err := srv.SetOption(glider.PasswordAuth(s.passwordHandler)); err != nil {
			return fmt.Errorf("password auth: %w", err)
		}
	}
	if s.cfg.AuthorizedKeysPath != "" {
		authorized, err := loadAuthorizedKeys(s.cfg.AuthorizedKeysPath)
		if err != nil {
			return err
		}
		pkAuth := func(_ glider.Context, key glider.PublicKey) bool {
			_, ok := authorized[string(key.Marshal())]
			return ok
		}
		if err := srv.SetOption(glider.PublicKeyAuth(pkAuth)); err != nil {
			return fmt.Errorf("public key auth: %w", err)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.log.Warn("ssh shutdown", "err", err)
		}
		<-errCh
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("ssh server: %w", err)
		}
		return nil
	}
}

func (s *Server) passwordHandler(_ glider.Context, password string) bool {
	return password == s.cfg.Password
}

func (s *Server) handleSession(sess glider.Session) {
	sid := strconv.FormatUint(atomic.AddUint64(&s.sessionCounter, 1), 10)
	log := s.log.With(
		"component", "ssh",
		"session_id", sid,
		"user", sess.User(),
		"remote", sess.RemoteAddr().String(),
	)

	intro := "ClawSSH — AI operations gateway.\n" +
		"Keyword mode: try 'check cpu' or 'check disk'. LLM mode: natural language is translated to JSON DSL.\n" +
		"Type 'exit' or 'quit' to disconnect.\n\n"
	if _, err := io.WriteString(sess, intro); err != nil {
		log.Error("write intro", "err", err)
		return
	}

	// One bufio.Reader for the whole session so CRLF peek/discard stays consistent across lines.
	br := bufio.NewReader(sess)
	snap := s.expl.Probe(sess.Context())
	prompt := func() {
		if _, err := io.WriteString(sess, "claw> "); err != nil {
			log.Error("write prompt", "err", err)
		}
	}
	prompt()

	for {
		line, err := readLineWithEcho(br, sess, log)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			if errors.Is(err, errInterrupt) {
				prompt()
				continue
			}
			log.Error("read line", "err", err)
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			prompt()
			continue
		}
		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			if _, err := io.WriteString(sess, "goodbye\n"); err != nil {
				log.Error("write goodbye", "err", err)
			}
			return
		}
		if strings.EqualFold(line, "status") {
			if _, err := io.WriteString(sess, s.mon.StatusText(s.tools)+"\n"); err != nil {
				log.Error("write status", "err", err)
			}
			prompt()
			continue
		}

		s.handleUserLine(sess.Context(), sess, br, log, sid, line, snap)
		prompt()
	}
}

func loadAuthorizedKeys(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read authorized keys: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	allowed := make(map[string]struct{})
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pub, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("parse authorized key line: %w", err)
		}
		allowed[string(pub.Marshal())] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("authorized keys file %q contained no valid keys", path)
	}
	return allowed, nil
}
