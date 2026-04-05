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
		fmt.Fprintf(os.Stderr, "clawssh-p2p-coord: %v\n", err)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	log := slog.Default().With("component", "p2p-coord")

	cfg := p2p.CoordinatorConfig{
		HTTPAddr:   envOr("CLAWSSH_P2P_COORD_HTTP_ADDR", ":18080"),
		UDPAddr:    envOr("CLAWSSH_P2P_COORD_UDP_ADDR", ":3478"),
		Token:      strings.TrimSpace(os.Getenv("CLAWSSH_P2P_COORD_TOKEN")),
		SessionTTL: envDurationSecondsOr("CLAWSSH_P2P_SESSION_TTL_SECONDS", 120*time.Second),
	}
	coord := p2p.NewCoordinator(cfg, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := coord.Run(ctx); err != nil && err != context.Canceled {
		log.Error("coordinator exited", "err", err)
		os.Exit(1)
	}
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

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envDurationSecondsOr(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}
