package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type CoordinatorConfig struct {
	HTTPAddr      string
	UDPAddr       string
	Token         string
	SessionTTL    time.Duration
	HeartbeatTTL  time.Duration
	LongPollDelay time.Duration
}

type Coordinator struct {
	cfg CoordinatorConfig
	log *slog.Logger

	mu       sync.Mutex
	sessions map[string]*coordSession
	udpConn  net.PacketConn
}

type coordSession struct {
	ID            string
	Token         string
	ServerNodeID  string
	ServerUDPAddr string
	ClientUDPAddr string
	UpdatedAt     time.Time
	ExpiresAt     time.Time
}

func NewCoordinator(cfg CoordinatorConfig, log *slog.Logger) *Coordinator {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 2 * time.Minute
	}
	if cfg.HeartbeatTTL <= 0 {
		cfg.HeartbeatTTL = 60 * time.Second
	}
	if cfg.LongPollDelay <= 0 {
		cfg.LongPollDelay = 2 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Coordinator{
		cfg:      cfg,
		log:      log.With("component", "p2p-coordinator"),
		sessions: make(map[string]*coordSession),
	}
}

func (c *Coordinator) Run(ctx context.Context) error {
	udpConn, err := net.ListenPacket("udp", c.cfg.UDPAddr)
	if err != nil {
		return fmt.Errorf("listen udp %s: %w", c.cfg.UDPAddr, err)
	}
	defer udpConn.Close()
	c.udpConn = udpConn

	srv := &http.Server{
		Addr:              c.cfg.HTTPAddr,
		Handler:           c.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- c.runUDP(ctx, udpConn) }()
	go func() {
		c.log.Info("p2p coordinator listening", "http_addr", c.cfg.HTTPAddr, "udp_addr", c.cfg.UDPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (c *Coordinator) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", c.handleHealth)
	mux.HandleFunc("/v1/p2p/connect", c.handleConnect)
	mux.HandleFunc("/v1/p2p/events", c.handleEvents)
	return mux
}

func (c *Coordinator) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *Coordinator) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.authorized(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	defer r.Body.Close()
	var req ConnectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Role = strings.TrimSpace(req.Role)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.Role != RoleServer && req.Role != RoleClient {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.NodeID == "" {
		http.Error(w, "session_id and node_id are required", http.StatusBadRequest)
		return
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gcLocked(now)
	sess := c.ensureSessionLocked(req.SessionID, req.Token, now)
	if req.Token != "" && sess.Token == "" {
		sess.Token = req.Token
	}
	if sess.Token != "" && req.Token != sess.Token {
		http.Error(w, "session token mismatch", http.StatusForbidden)
		return
	}
	if req.Role == RoleServer {
		sess.ServerNodeID = req.NodeID
	} else {
		if req.NodeID != sess.ServerNodeID && sess.ServerNodeID != "" {
			// client node id is independent; no overwrite needed.
		}
	}
	sess.UpdatedAt = now
	sess.ExpiresAt = now.Add(c.cfg.SessionTTL)

	resp := ConnectResponse{
		OK:        true,
		SessionID: sess.ID,
	}
	if req.Role == RoleServer {
		resp.Message = "server registered; wait for client"
		if sess.ClientUDPAddr != "" {
			resp.Peer = &Peer{
				Role:    RoleClient,
				UDPAddr: sess.ClientUDPAddr,
				NodeID:  "client",
			}
		}
	} else {
		resp.Message = "client registered; wait for server"
		if sess.ServerUDPAddr != "" {
			resp.Peer = &Peer{
				Role:    RoleServer,
				UDPAddr: sess.ServerUDPAddr,
				NodeID:  sess.ServerNodeID,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (c *Coordinator) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.authorized(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	role := strings.TrimSpace(r.URL.Query().Get("role"))
	if sessionID == "" || (role != RoleServer && role != RoleClient) {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}

	start := time.Now()
	timeout := 30 * time.Second
	if dl, ok := r.Context().Deadline(); ok {
		if d := time.Until(dl); d > 0 {
			timeout = d
		}
	}

	for {
		now := time.Now()
		c.mu.Lock()
		c.gcLocked(now)
		sess := c.sessions[sessionID]
		var ev *Event
		if sess != nil {
			sess.ExpiresAt = now.Add(c.cfg.SessionTTL)
			switch role {
			case RoleServer:
				if sess.ClientUDPAddr != "" {
					ev = &Event{
						Type:      EventPeerReady,
						SessionID: sessionID,
						Peer: &Peer{
							Role:    RoleClient,
							UDPAddr: sess.ClientUDPAddr,
							NodeID:  "client",
						},
						TimeUnix: now.Unix(),
					}
				}
			case RoleClient:
				if sess.ServerUDPAddr != "" {
					ev = &Event{
						Type:      EventPeerReady,
						SessionID: sessionID,
						Peer: &Peer{
							Role:    RoleServer,
							UDPAddr: sess.ServerUDPAddr,
							NodeID:  sess.ServerNodeID,
						},
						TimeUnix: now.Unix(),
					}
				}
			}
		}
		c.mu.Unlock()

		if ev != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": []Event{*ev}})
			return
		}
		if time.Since(start) >= timeout {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": []Event{}})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(c.cfg.LongPollDelay):
		}
	}
}

func (c *Coordinator) runUDP(ctx context.Context, conn net.PacketConn) error {
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					return nil
				default:
					continue
				}
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		var ann Announce
		if err := json.Unmarshal(buf[:n], &ann); err != nil {
			continue
		}
		sessionID := strings.TrimSpace(ann.SessionID)
		role := strings.TrimSpace(ann.Role)
		if sessionID == "" || (role != RoleServer && role != RoleClient) {
			continue
		}
		now := time.Now()
		c.mu.Lock()
		c.gcLocked(now)
		sess := c.ensureSessionLocked(sessionID, ann.Token, now)
		if sess.Token != "" && ann.Token != "" && ann.Token != sess.Token {
			c.mu.Unlock()
			continue
		}
		if sess.Token == "" && ann.Token != "" {
			sess.Token = ann.Token
		}
		switch role {
		case RoleServer:
			sess.ServerNodeID = ann.NodeID
			sess.ServerUDPAddr = addr.String()
		case RoleClient:
			sess.ClientUDPAddr = addr.String()
		}
		sess.UpdatedAt = now
		sess.ExpiresAt = now.Add(c.cfg.SessionTTL)
		peerAddr := ""
		if role == RoleServer {
			peerAddr = sess.ClientUDPAddr
		} else {
			peerAddr = sess.ServerUDPAddr
		}
		c.mu.Unlock()

		if peerAddr != "" {
			msg, _ := json.Marshal(map[string]string{
				"type":       "peer",
				"session_id": sessionID,
				"peer_addr":  peerAddr,
			})
			_, _ = conn.WriteTo(msg, addr)
		}
	}
}

func (c *Coordinator) ensureSessionLocked(sessionID, token string, now time.Time) *coordSession {
	s, ok := c.sessions[sessionID]
	if ok {
		return s
	}
	s = &coordSession{
		ID:        sessionID,
		Token:     token,
		UpdatedAt: now,
		ExpiresAt: now.Add(c.cfg.SessionTTL),
	}
	c.sessions[sessionID] = s
	return s
}

func (c *Coordinator) gcLocked(now time.Time) {
	for id, s := range c.sessions {
		if now.After(s.ExpiresAt) {
			delete(c.sessions, id)
		}
	}
}

func (c *Coordinator) authorized(authHeader string) bool {
	if c.cfg.Token == "" {
		return true
	}
	authHeader = strings.TrimSpace(authHeader)
	if authHeader == "" {
		return false
	}
	const bearer = "Bearer "
	if !strings.HasPrefix(authHeader, bearer) {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, bearer)) == c.cfg.Token
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
