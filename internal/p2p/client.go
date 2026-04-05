package p2p

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	quic "github.com/quic-go/quic-go"
)

// ClientConfig configures local TCP entry and remote P2P node connection.
type ClientConfig struct {
	LocalListenAddr string
	ClientID        string
	TargetNodeID    string
	SharedToken     string
	CoordinatorHTTP string
	ConnectTimeout  time.Duration
}

func (c ClientConfig) Validate() error {
	if strings.TrimSpace(c.TargetNodeID) == "" {
		return errors.New("target node id is required")
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return errors.New("client id is required")
	}
	if strings.TrimSpace(c.SharedToken) == "" {
		return errors.New("shared token is required")
	}
	if strings.TrimSpace(c.CoordinatorHTTP) == "" {
		return errors.New("coordinator http url is required")
	}
	if strings.TrimSpace(c.LocalListenAddr) == "" {
		return errors.New("local listen addr is required")
	}
	return nil
}

// Client exposes a local TCP port for standard SSH tools and tunnels traffic over P2P.
type Client struct {
	cfg  ClientConfig
	log  *slog.Logger
	udp  *net.UDPConn
	tran *quic.Transport
}

// NewClient validates configuration and creates a client tunnel.
func NewClient(cfg ClientConfig, log *slog.Logger) (*Client, error) {
	if cfg.LocalListenAddr == "" {
		cfg.LocalListenAddr = "127.0.0.1:2222"
	}
	if cfg.CoordinatorHTTP == "" {
		cfg.CoordinatorHTTP = "http://127.0.0.1:8080"
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 15 * time.Second
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		cfg: cfg,
		log: log.With("component", "p2p-client"),
	}, nil
}

// Run starts local listener and handles tunnel sessions until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("listen udp for p2p client: %w", err)
	}
	defer udpConn.Close()
	c.udp = udpConn

	c.tran = &quic.Transport{Conn: udpConn}
	defer c.tran.Close()

	listener, err := net.Listen("tcp", c.cfg.LocalListenAddr)
	if err != nil {
		return fmt.Errorf("listen local tcp %s: %w", c.cfg.LocalListenAddr, err)
	}
	defer listener.Close()

	c.log.Info("p2p client ready",
		"local_listen", c.cfg.LocalListenAddr,
		"target_node", c.cfg.TargetNodeID,
		"client_id", c.cfg.ClientID,
	)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("accept local tcp: %w", err)
		}
		go c.handleLocalConn(ctx, conn)
	}
}

func (c *Client) handleLocalConn(ctx context.Context, localConn net.Conn) {
	defer localConn.Close()

	cc, cancel := context.WithTimeout(ctx, c.cfg.ConnectTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("%s-%d", c.cfg.TargetNodeID, time.Now().UnixNano())
	api := NewCoordClient(c.cfg.CoordinatorHTTP, c.cfg.SharedToken, "", nil)
	if _, err := api.Connect(cc, ConnectRequest{
		Role:      RoleClient,
		SessionID: sessionID,
		NodeID:    c.cfg.ClientID,
		Token:     c.cfg.SharedToken,
	}); err != nil {
		c.log.Warn("register connect session failed", "err", err)
		return
	}

	peerAddr, err := c.waitServerPeer(cc, api, sessionID)
	if err != nil {
		c.log.Warn("wait server peer failed", "err", err)
		return
	}
	addr, err := net.ResolveUDPAddr("udp", peerAddr)
	if err != nil {
		c.log.Warn("invalid server addr", "addr", peerAddr, "err", err)
		return
	}

	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{quicALPN},
		ServerName:         c.cfg.TargetNodeID,
	}
	qconn, err := c.tran.Dial(cc, addr, tlsConf, &quic.Config{
		HandshakeIdleTimeout: 5 * time.Second,
		KeepAlivePeriod:      20 * time.Second,
	})
	if err != nil {
		c.log.Warn("dial p2p quic failed", "peer", addr.String(), "err", err)
		return
	}
	defer qconn.CloseWithError(0, "")

	stream, err := qconn.OpenStreamSync(cc)
	if err != nil {
		c.log.Warn("open quic stream failed", "err", err)
		return
	}
	defer stream.Close()

	if _, err := fmt.Fprintf(stream, "%s\n", c.cfg.SharedToken); err != nil {
		c.log.Warn("write auth token failed", "err", err)
		return
	}
	ack := readLine(stream)
	if ack != "OK" {
		c.log.Warn("auth rejected by node", "ack", ack)
		return
	}

	if err := bidirectionalCopy(stream, localConn); err != nil && !errors.Is(err, net.ErrClosed) {
		c.log.Debug("relay closed", "err", err)
	}
}

func (c *Client) waitServerPeer(ctx context.Context, api *CoordClient, sessionID string) (string, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	for {
		if time.Now().After(deadline) {
			return "", context.DeadlineExceeded
		}
		events, err := api.Events(ctx, sessionID, RoleClient)
		if err == nil {
			for _, ev := range events {
				if ev.Type == EventPeerReady && ev.Peer != nil && ev.Peer.Role == RoleServer && ev.Peer.UDPAddr != "" {
					return ev.Peer.UDPAddr, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(600 * time.Millisecond):
		}
	}
}
