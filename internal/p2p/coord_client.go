package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CoordClient is used by node and client to talk to the coordinator HTTP API.
type CoordClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	udpAddr    *net.UDPAddr
}

func NewCoordClient(baseURL, token, udpAddr string, hc *http.Client) *CoordClient {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	var resolvedUDP *net.UDPAddr
	if strings.TrimSpace(udpAddr) != "" {
		if ua, err := net.ResolveUDPAddr("udp", strings.TrimSpace(udpAddr)); err == nil {
			resolvedUDP = ua
		}
	}
	return &CoordClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:      strings.TrimSpace(token),
		httpClient: hc,
		udpAddr:    resolvedUDP,
	}
}

func (c *CoordClient) Connect(ctx context.Context, reqBody ConnectRequest) (*ConnectResponse, error) {
	if strings.TrimSpace(reqBody.Token) == "" {
		reqBody.Token = c.token
	}
	var resp ConnectResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/p2p/connect", reqBody, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *CoordClient) Events(ctx context.Context, sessionID, role string) ([]Event, error) {
	values := url.Values{}
	values.Set("session_id", strings.TrimSpace(sessionID))
	values.Set("role", role)
	var out struct {
		OK     bool    `json:"ok"`
		Events []Event `json:"events"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/p2p/events?"+values.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

func (c *CoordClient) ValidateToken(ctx context.Context, token string) (bool, error) {
	// Local shared secret check. Keep ctx for future remote auth extension.
	_ = ctx
	return strings.TrimSpace(token) != "" && strings.TrimSpace(token) == c.token, nil
}

func (c *CoordClient) UDPAddr() *net.UDPAddr {
	return c.udpAddr
}

func (c *CoordClient) doJSON(ctx context.Context, method, path string, in any, out any) error {
	fullURL := c.baseURL + path
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("coordinator %s %s failed: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
