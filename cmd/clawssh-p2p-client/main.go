// Command clawssh-p2p-client creates a local TCP port for standard SSH clients
// and bridges traffic to a remote ClawSSH node via UDP P2P + QUIC.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/clawssh/clawssh/internal/p2p"
)

func main() {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "clawssh-p2p-client: %v\n", err)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	log := slog.Default().With("component", "p2p_client")

	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := p2p.NewClient(cfg, log)
	if err != nil {
		log.Error("configure p2p client failed", "err", err)
		os.Exit(1)
	}
	log.Info("starting p2p client tunnel", "local_addr", cfg.LocalListenAddr, "target_node", cfg.TargetNodeID)
	if err := client.Run(ctx); err != nil {
		log.Error("client tunnel exited", "err", err)
		os.Exit(1)
	}
}

func loadConfigFromEnv() (p2p.ClientConfig, error) {
	cfg := p2p.ClientConfig{
		LocalListenAddr: envOr("CLAWSSH_P2P_CLIENT_LISTEN", "127.0.0.1:2222"),
		ClientID:        envOr("CLAWSSH_P2P_CLIENT_ID", ""),
		TargetNodeID:    envOr("CLAWSSH_P2P_TARGET_NODE_ID", ""),
		SharedToken:     envOr("CLAWSSH_P2P_SHARED_TOKEN", ""),
		CoordinatorHTTP: envOr("CLAWSSH_P2P_COORDINATOR", "http://127.0.0.1:18080"),
		ConnectTimeout:  durationSecondsEnv("CLAWSSH_P2P_CONNECT_TIMEOUT_SECONDS", 15),
	}
	return cfg, cfg.Validate()
}

func durationSecondsEnv(key string, fallback int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return time.Duration(fallback) * time.Second
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return time.Duration(fallback) * time.Second
	}
	return time.Duration(n) * time.Second
}

func envOr(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func loadDotEnv() error {
	path := strings.TrimSpace(os.Getenv("CLAWSSH_DOTENV"))
	if path == "" {
		path = ".env"
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return godotenv.Load(path)
}
