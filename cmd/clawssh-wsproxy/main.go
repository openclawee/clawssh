// Command clawssh-wsproxy is a standalone WSS (TLS) proxy for SSH traffic.
// It is designed as a sidecar/edge process and does not modify ClawSSH logic.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

type config struct {
	ListenAddr      string
	Path            string
	TargetAddr      string
	TLSCertFile     string
	TLSKeyFile      string
	AllowOrigins    []string
	AllowAllOrigins bool
	AllowNoOrigin   bool

	ReadBufferBytes  int
	WriteBufferBytes int
	CopyBufferBytes  int
	MaxFrameBytes    int64
	MaxConns         int

	HandshakeTimeout time.Duration
	DialTimeout      time.Duration
	WSIdleTimeout    time.Duration
	WriteTimeout     time.Duration
	PingInterval     time.Duration
	TCPKeepAlive     time.Duration
	ShutdownTimeout  time.Duration
}

type proxy struct {
	cfg      config
	log      *slog.Logger
	upgrader websocket.Upgrader

	connSeq atomic.Uint64
	slots   chan struct{}
}

func main() {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "clawssh-wsproxy: %v\n", err)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	log := slog.Default().With("component", "wsproxy")

	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	p := newProxy(cfg, log)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           p.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting ws proxy",
			"addr", cfg.ListenAddr,
			"path", cfg.Path,
			"target", cfg.TargetAddr,
			"tls_cert", cfg.TLSCertFile,
		)
		errCh <- srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
		log.Info("ws proxy stopped")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("ws proxy exited", "err", err)
			os.Exit(1)
		}
	}
}

func newProxy(cfg config, log *slog.Logger) *proxy {
	p := &proxy{
		cfg: cfg,
		log: log,
	}
	if cfg.MaxConns > 0 {
		p.slots = make(chan struct{}, cfg.MaxConns)
	}

	p.upgrader = websocket.Upgrader{
		ReadBufferSize:    cfg.ReadBufferBytes,
		WriteBufferSize:   cfg.WriteBufferBytes,
		HandshakeTimeout:  cfg.HandshakeTimeout,
		EnableCompression: false,
		CheckOrigin: func(r *http.Request) bool {
			return p.originAllowed(r)
		},
	}

	return p
}

func (p *proxy) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", p.handleHealth)
	mux.HandleFunc(p.cfg.Path, p.handleWS)
	return mux
}

func (p *proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (p *proxy) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.acquireSlot() {
		http.Error(w, "proxy busy", http.StatusServiceUnavailable)
		return
	}
	defer p.releaseSlot()

	connID := p.connSeq.Add(1)
	log := p.log.With("conn_id", connID, "remote", r.RemoteAddr)

	wsConn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn("websocket upgrade failed", "err", err)
		return
	}
	defer wsConn.Close()

	if err := p.proxyStream(r.Context(), log, wsConn); err != nil && !isExpectedClose(err) {
		log.Warn("proxy stream ended with error", "err", err)
		return
	}
	log.Info("connection closed")
}

func (p *proxy) proxyStream(ctx context.Context, log *slog.Logger, wsConn *websocket.Conn) error {
	dialer := net.Dialer{
		Timeout:   p.cfg.DialTimeout,
		KeepAlive: p.cfg.TCPKeepAlive,
	}
	tcpConn, err := dialer.DialContext(ctx, "tcp", p.cfg.TargetAddr)
	if err != nil {
		return fmt.Errorf("dial target %s: %w", p.cfg.TargetAddr, err)
	}
	defer tcpConn.Close()

	if tc, ok := tcpConn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(p.cfg.TCPKeepAlive)
		_ = tc.SetNoDelay(true)
	}

	log.Debug("target connected", "target", p.cfg.TargetAddr)
	wsConn.SetReadLimit(p.cfg.MaxFrameBytes)
	_ = wsConn.SetReadDeadline(time.Now().Add(p.cfg.WSIdleTimeout))
	wsConn.SetPongHandler(func(string) error {
		return wsConn.SetReadDeadline(time.Now().Add(p.cfg.WSIdleTimeout))
	})

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 3)
	var writeMu sync.Mutex

	go func() {
		errCh <- p.forwardWSToTCP(wsConn, tcpConn)
	}()
	go func() {
		errCh <- p.forwardTCPToWS(wsConn, tcpConn, &writeMu)
	}()
	if p.cfg.PingInterval > 0 {
		go func() {
			errCh <- p.pingLoop(streamCtx, wsConn, &writeMu)
		}()
	}

	err = <-errCh
	cancel()
	_ = tcpConn.Close()
	_ = wsConn.Close()
	return err
}

func (p *proxy) forwardWSToTCP(wsConn *websocket.Conn, tcpConn net.Conn) error {
	buf := make([]byte, p.cfg.CopyBufferBytes)
	for {
		msgType, reader, err := wsConn.NextReader()
		if err != nil {
			return err
		}
		if msgType != websocket.BinaryMessage && msgType != websocket.TextMessage {
			continue
		}
		if _, err := io.CopyBuffer(tcpConn, reader, buf); err != nil {
			return err
		}
	}
}

func (p *proxy) forwardTCPToWS(wsConn *websocket.Conn, tcpConn net.Conn, writeMu *sync.Mutex) error {
	buf := make([]byte, p.cfg.CopyBufferBytes)
	for {
		n, err := tcpConn.Read(buf)
		if n > 0 {
			writeMu.Lock()
			_ = wsConn.SetWriteDeadline(time.Now().Add(p.cfg.WriteTimeout))
			writer, werr := wsConn.NextWriter(websocket.BinaryMessage)
			if werr != nil {
				writeMu.Unlock()
				return werr
			}
			_, werr = writer.Write(buf[:n])
			cerr := writer.Close()
			writeMu.Unlock()

			if werr != nil {
				return werr
			}
			if cerr != nil {
				return cerr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (p *proxy) pingLoop(ctx context.Context, wsConn *websocket.Conn, writeMu *sync.Mutex) error {
	ticker := time.NewTicker(p.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			writeMu.Lock()
			_ = wsConn.SetWriteDeadline(time.Now().Add(p.cfg.WriteTimeout))
			err := wsConn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func (p *proxy) originAllowed(r *http.Request) bool {
	if p.cfg.AllowAllOrigins {
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return p.cfg.AllowNoOrigin
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	host := strings.ToLower(u.Hostname())
	hostWithPort := strings.ToLower(u.Host)
	originLower := strings.ToLower(origin)

	for _, allowed := range p.cfg.AllowOrigins {
		a := strings.ToLower(strings.TrimSpace(allowed))
		if a == "" {
			continue
		}
		if a == originLower || a == host || a == hostWithPort {
			return true
		}
		if strings.HasPrefix(a, "*.") {
			suffix := strings.TrimPrefix(a, "*.")
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
		}
	}
	return false
}

func (p *proxy) acquireSlot() bool {
	if p.slots == nil {
		return true
	}
	select {
	case p.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *proxy) releaseSlot() {
	if p.slots == nil {
		return
	}
	select {
	case <-p.slots:
	default:
	}
}

func loadConfigFromEnv() (config, error) {
	cfg := config{
		ListenAddr:       envOr("CLAWSSH_WSPROXY_ADDR", ":443"),
		Path:             normalizePath(envOr("CLAWSSH_WSPROXY_PATH", "/ws")),
		TargetAddr:       envOr("CLAWSSH_WSPROXY_TARGET_ADDR", "127.0.0.1:22"),
		TLSCertFile:      strings.TrimSpace(os.Getenv("CLAWSSH_WSPROXY_TLS_CERT_FILE")),
		TLSKeyFile:       strings.TrimSpace(os.Getenv("CLAWSSH_WSPROXY_TLS_KEY_FILE")),
		AllowOrigins:     splitCSVEnv("CLAWSSH_WSPROXY_ALLOW_ORIGINS"),
		AllowNoOrigin:    envBoolOr("CLAWSSH_WSPROXY_ALLOW_NO_ORIGIN", true),
		ReadBufferBytes:  envIntOr("CLAWSSH_WSPROXY_READ_BUFFER_BYTES", 32*1024),
		WriteBufferBytes: envIntOr("CLAWSSH_WSPROXY_WRITE_BUFFER_BYTES", 32*1024),
		CopyBufferBytes:  envIntOr("CLAWSSH_WSPROXY_COPY_BUFFER_BYTES", 32*1024),
		MaxFrameBytes:    int64(envIntOr("CLAWSSH_WSPROXY_MAX_FRAME_BYTES", 1024*1024)),
		MaxConns:         envIntOr("CLAWSSH_WSPROXY_MAX_CONNS", 0),
		HandshakeTimeout: envDurationSecondsOr("CLAWSSH_WSPROXY_HANDSHAKE_TIMEOUT_SECONDS", 10*time.Second),
		DialTimeout:      envDurationSecondsOr("CLAWSSH_WSPROXY_DIAL_TIMEOUT_SECONDS", 10*time.Second),
		WSIdleTimeout:    envDurationSecondsOr("CLAWSSH_WSPROXY_WS_IDLE_TIMEOUT_SECONDS", 90*time.Second),
		WriteTimeout:     envDurationSecondsOr("CLAWSSH_WSPROXY_WRITE_TIMEOUT_SECONDS", 15*time.Second),
		PingInterval:     envDurationSecondsOr("CLAWSSH_WSPROXY_PING_INTERVAL_SECONDS", 25*time.Second),
		TCPKeepAlive:     envDurationSecondsOr("CLAWSSH_WSPROXY_TCP_KEEPALIVE_SECONDS", 30*time.Second),
		ShutdownTimeout:  envDurationSecondsOr("CLAWSSH_WSPROXY_SHUTDOWN_TIMEOUT_SECONDS", 15*time.Second),
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return config{}, errors.New("CLAWSSH_WSPROXY_TLS_CERT_FILE and CLAWSSH_WSPROXY_TLS_KEY_FILE are required")
	}
	if cfg.ReadBufferBytes <= 0 || cfg.WriteBufferBytes <= 0 || cfg.CopyBufferBytes <= 0 {
		return config{}, errors.New("buffer sizes must be > 0")
	}
	if cfg.MaxFrameBytes <= 0 {
		return config{}, errors.New("CLAWSSH_WSPROXY_MAX_FRAME_BYTES must be > 0")
	}
	if cfg.MaxConns < 0 {
		return config{}, errors.New("CLAWSSH_WSPROXY_MAX_CONNS must be >= 0")
	}
	if len(cfg.AllowOrigins) == 0 {
		cfg.AllowAllOrigins = true
	}
	for _, origin := range cfg.AllowOrigins {
		if strings.TrimSpace(origin) == "*" {
			cfg.AllowAllOrigins = true
			cfg.AllowOrigins = nil
			break
		}
	}
	return cfg, nil
}

func normalizePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "/ws"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func envOr(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func envIntOr(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
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

func envBoolOr(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
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

func isExpectedClose(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return true
	}
	if websocket.IsCloseError(
		err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
		websocket.CloseAbnormalClosure,
	) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed network connection")
}
