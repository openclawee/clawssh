// Command clawssh is the ClawSSH gateway entrypoint: an SSH server that turns operator
// input into structured tasks executed through pluggable adapters.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/clawssh/clawssh/internal/adapter"
	"github.com/clawssh/clawssh/internal/engine"
	"github.com/clawssh/clawssh/internal/inventory"
	"github.com/clawssh/clawssh/internal/policy"
	"github.com/clawssh/clawssh/internal/server"
)

func main() {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "clawssh: %v\n", err)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	log := slog.Default().With("component", "main")

	hostKeyPath := os.Getenv("CLAWSSH_HOST_KEY")
	if hostKeyPath == "" {
		hostKeyPath = "host_key"
	}
	addr := os.Getenv("CLAWSSH_ADDR")
	if addr == "" {
		addr = ":2222"
	}
	password := os.Getenv("CLAWSSH_PASSWORD")
	authorizedKeys := os.Getenv("CLAWSSH_AUTHORIZED_KEYS")

	if err := server.EnsureHostKey(hostKeyPath); err != nil {
		log.Error("ensure host key", "err", err)
		os.Exit(1)
	}

	env := strings.TrimSpace(os.Getenv("CLAWSSH_ENV"))
	if env == "" {
		env = "development"
	}

	cfg := server.Config{
		Addr:                  addr,
		HostKeyPath:           hostKeyPath,
		Password:              password,
		AuthorizedKeysPath:    authorizedKeys,
		Environment:           env,
		ExplorerConfigPath:    strings.TrimSpace(os.Getenv("CLAWSSH_EXPLORER_CONFIG")),
		ExplorerProbeFilter:   splitCSVEnv("CLAWSSH_EXPLORER_PROBES"),
		ExplorerTimeout:       durationSecondsEnv("CLAWSSH_EXPLORER_TIMEOUT_SECONDS"),
		StreamActionAllowlist: splitCSVEnv("CLAWSSH_STREAM_ACTIONS"),
	}
	inv, err := buildInventory()
	if err != nil {
		log.Error("configure inventory", "err", err)
		os.Exit(1)
	}
	cfg.Inventory = inv

	intent, err := buildIntentEngine(context.Background(), log)
	if err != nil {
		log.Error("configure intent engine", "err", err)
		os.Exit(1)
	}

	pol, err := buildPolicyEngine()
	if err != nil {
		log.Error("configure policy engine", "err", err)
		os.Exit(1)
	}

	srv, err := server.New(
		cfg,
		intent,
		pol,
		nil, // audit: use default slog-based logger
		buildAdapterRegistry(log, inv),
		slog.Default(),
	)
	if err != nil {
		log.Error("configure server", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting ClawSSH", "addr", addr)
	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func splitCSVEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func durationSecondsEnv(key string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func buildAdapterRegistry(log *slog.Logger, inv *inventory.Inventory) adapter.ToolProvider {
	reg := adapter.NewRegistry("ssh")
	_ = reg.Register("ssh", adapter.NewLocalSSHAdapter())
	remote, err := adapter.NewRemoteSSHProviderFromEnv()
	if err != nil {
		log.Warn("remote ssh provider init failed; keep local ssh", "err", err)
		return reg
	}
	// "ssh_remote" allows explicit routing without changing the default local behavior.
	_ = reg.Register("ssh_remote", remote)
	return adapter.NewInventoryDispatch(reg, inv)
}

func buildInventory() (*inventory.Inventory, error) {
	path := strings.TrimSpace(os.Getenv("CLAWSSH_INVENTORY"))
	if path == "" {
		path = "configs/hosts"
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return inventory.LoadINIFromFile(path)
}

// loadDotEnv loads a dotenv file before reading os.Getenv.
// Does nothing if the file is missing. Existing environment variables are not overridden.
func loadDotEnv() error {
	path := strings.TrimSpace(os.Getenv("CLAWSSH_DOTENV"))
	if path == "" {
		path = ".env"
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return godotenv.Load(path)
}

func buildPolicyEngine() (*policy.PolicyEngine, error) {
	path := os.Getenv("CLAWSSH_POLICIES")
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return policy.NewPolicyEngineWithRules(data)
	}
	return policy.NewPolicyEngine(), nil
}

func buildIntentEngine(ctx context.Context, log *slog.Logger) (engine.IntentEngine, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CLAWSSH_INTENT_ENGINE")))
	if mode == "" {
		mode = "keyword"
	}
	switch mode {
	case "keyword", "legacy":
		return engine.NewKeywordRouter(), nil
	case "llm", "ai":
		return engine.NewLLMIntentEngineFromEnv(ctx, log)
	default:
		log.Warn("unknown CLAWSSH_INTENT_ENGINE, falling back to keyword", "value", mode)
		return engine.NewKeywordRouter(), nil
	}
}
