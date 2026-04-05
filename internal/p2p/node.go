package p2p

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// NodeConfig configures the gateway-side P2P node.
type NodeConfig struct {
	NodeID           string
	NodeSecret       string
	CoordHTTP        string
	CoordUDP         string
	UDPListenAddr    string
	TargetAddr       string
	AnnounceInterval time.Duration
	PollInterval     time.Duration
}

// Node runs a QUIC endpoint that forwards streams to local SSH.
type Node struct {
	cfg       NodeConfig
	log       *slog.Logger
	coord     *CoordClient
	udpConn   net.PacketConn
	transport *quic.Transport
	listener  *quic.Listener
}

func NewNode(cfg NodeConfig, log *slog.Logger) (*Node, error) {
	if strings.TrimSpace(cfg.NodeID) == "" {
		return nil, errors.New("node id is required")
	}
	if strings.TrimSpace(cfg.NodeSecret) == "" {
		return nil, errors.New("node secret is required")
	}
	if strings.TrimSpace(cfg.CoordHTTP) == "" {
		return nil, errors.New("coordinator url is required")
	}
	if strings.TrimSpace(cfg.CoordUDP) == "" {
		return nil, errors.New("coordinator udp addr is required")
	}
	if strings.TrimSpace(cfg.UDPListenAddr) == "" {
		cfg.UDPListenAddr = ":40000"
	}
	if strings.TrimSpace(cfg.TargetAddr) == "" {
		cfg.TargetAddr = "127.0.0.1:22"
	}
	if cfg.AnnounceInterval <= 0 {
		cfg.AnnounceInterval = 15 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	u, err := url.Parse(strings.TrimSpace(cfg.CoordHTTP))
	if err != nil {
		return nil, fmt.Errorf("parse coordinator url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("coordinator url must be http or https")
	}
	return &Node{
		cfg:   cfg,
		log:   log.With("component", "p2p-node"),
		coord: NewCoordClient(cfg.CoordHTTP, cfg.NodeSecret, cfg.CoordUDP, nil),
	}, nil
}

func (n *Node) Run(ctx context.Context) error {
	udpConn, err := net.ListenPacket("udp", n.cfg.UDPListenAddr)
	if err != nil {
		return fmt.Errorf("listen udp %s: %w", n.cfg.UDPListenAddr, err)
	}
	n.udpConn = udpConn
	n.transport = &quic.Transport{Conn: udpConn}

	tlsConf, err := newNodeTLSConfig(n.cfg.NodeID)
	if err != nil {
		_ = udpConn.Close()
		return err
	}
	listener, err := n.transport.Listen(tlsConf, &quic.Config{
		MaxIdleTimeout:  90 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
		Allow0RTT:       false,
	})
	if err != nil {
		_ = udpConn.Close()
		return fmt.Errorf("quic listen: %w", err)
	}
	n.listener = listener

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errCh <- n.announceLoop(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		errCh <- n.acceptLoop(ctx)
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
			n.log.Error("p2p node stopped with error", "err", err)
		}
	}

	if n.listener != nil {
		_ = n.listener.Close()
	}
	if n.transport != nil {
		_ = n.transport.Close()
	}
	if n.udpConn != nil {
		_ = n.udpConn.Close()
	}
	wg.Wait()
	return nil
}

func (n *Node) announceLoop(ctx context.Context) error {
	ticker := time.NewTicker(n.cfg.AnnounceInterval)
	defer ticker.Stop()

	send := func() error {
		if err := sendUDPAnnounce(n.udpConn, n.coord.UDPAddr(), Announce{
			Type:      udpTypeAnnounce,
			Role:      RoleServer,
			NodeID:    n.cfg.NodeID,
			SessionID: n.cfg.NodeID,
			Token:     n.cfg.NodeSecret,
			TimeUnix:  time.Now().Unix(),
		}); err != nil {
			return err
		}
		_, err := n.coord.Connect(ctx, ConnectRequest{
			Role:      RoleServer,
			SessionID: n.cfg.NodeID,
			NodeID:    n.cfg.NodeID,
			Token:     n.cfg.NodeSecret,
		})
		return err
	}

	if err := send(); err != nil {
		n.log.Warn("initial p2p announce failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := send(); err != nil {
				n.log.Warn("announce failed", "err", err)
				continue
			}
			events, err := n.coord.Events(ctx, n.cfg.NodeID, RoleServer)
			if err != nil {
				n.log.Debug("poll events failed", "err", err)
				continue
			}
			for _, ev := range events {
				if ev.Type != EventPeerReady || ev.Peer == nil || ev.Peer.UDPAddr == "" {
					continue
				}
				peer, err := net.ResolveUDPAddr("udp", ev.Peer.UDPAddr)
				if err != nil {
					continue
				}
				// Burst a few packets to keep NAT mapping warm.
				for i := 0; i < 3; i++ {
					_, _ = n.udpConn.WriteTo([]byte("punch"), peer)
				}
			}
		}
	}
}

func (n *Node) acceptLoop(ctx context.Context) error {
	for {
		conn, err := n.listener.Accept(ctx)
		if err != nil {
			return err
		}
		go n.handleConn(ctx, conn)
	}
}

func (n *Node) handleConn(ctx context.Context, conn quic.Connection) {
	log := n.log.With("remote", conn.RemoteAddr().String())
	defer conn.CloseWithError(0, "")

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		log.Warn("accept stream", "err", err)
		return
	}

	token := strings.TrimSpace(readLine(stream))
	if token == "" {
		log.Warn("missing token")
		_ = stream.Close()
		return
	}
	ok, err := n.coord.ValidateToken(ctx, token)
	if err != nil {
		log.Warn("token validate failed", "err", err)
		_ = stream.Close()
		return
	}
	if !ok {
		log.Warn("token rejected")
		_ = stream.Close()
		return
	}

	if _, err := io.WriteString(stream, "OK\n"); err != nil {
		log.Warn("write handshake response", "err", err)
		_ = stream.Close()
		return
	}

	tcpConn, err := net.DialTimeout("tcp", n.cfg.TargetAddr, 10*time.Second)
	if err != nil {
		log.Warn("dial target failed", "target", n.cfg.TargetAddr, "err", err)
		_ = stream.Close()
		return
	}
	defer tcpConn.Close()

	if tc, ok := tcpConn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}

	if err := bidirectionalCopy(stream, tcpConn); err != nil && !errors.Is(err, io.EOF) {
		log.Debug("stream relay ended", "err", err)
	}
}

func newNodeTLSConfig(nodeID string) (*tls.Config, error) {
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tpl := x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{nodeID},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{quicALPN},
	}, nil
}

func readLine(r io.Reader) string {
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1)
	for len(buf) < 4096 {
		n, err := r.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				break
			}
			buf = append(buf, tmp[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(buf))
}

func bidirectionalCopy(a io.ReadWriteCloser, b io.ReadWriteCloser) error {
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(a, b)
		_ = a.Close()
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(b, a)
		_ = b.Close()
		errCh <- err
	}()
	err1 := <-errCh
	err2 := <-errCh
	if err1 != nil {
		return err1
	}
	return err2
}

func sendUDPAnnounce(conn net.PacketConn, addr net.Addr, ann Announce) error {
	if conn == nil {
		return errors.New("nil udp conn")
	}
	if addr == nil {
		return errors.New("nil udp addr")
	}
	data, err := json.Marshal(ann)
	if err != nil {
		return err
	}
	_, err = conn.WriteTo(data, addr)
	return err
}
