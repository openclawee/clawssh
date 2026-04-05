package p2p

const (
	quicALPN = "clawssh-p2p-ssh-v1"

	RoleServer = "server"
	RoleClient = "client"

	EventPeerReady = "peer_ready"

	udpTypeAnnounce = "announce"
	udpTypeAck      = "ack"
)

// ConnectRequest registers a server node or creates a client session.
type ConnectRequest struct {
	Role         string `json:"role"`                     // server|client
	NodeID       string `json:"node_id"`                  // server node id or client id
	TargetNodeID string `json:"target_node_id,omitempty"` // required for client
	SessionID    string `json:"session_id,omitempty"`     // optional for client
	Token        string `json:"token,omitempty"`
}

type ConnectResponse struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Peer      *Peer  `json:"peer,omitempty"`
}

type Peer struct {
	Role    string `json:"role"`
	NodeID  string `json:"node_id,omitempty"`
	UDPAddr string `json:"udp_addr,omitempty"`
}

type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Peer      *Peer  `json:"peer,omitempty"`
	TimeUnix  int64  `json:"time_unix,omitempty"`
}

// Announce is the UDP hole-punch announcement to coordinator.
type Announce struct {
	Type      string `json:"type"`
	Role      string `json:"role"` // server|client
	NodeID    string `json:"node_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Token     string `json:"token,omitempty"`
	TimeUnix  int64  `json:"time_unix"`
}

type udpAck struct {
	Type string `json:"type"`
	OK   bool   `json:"ok"`
}

type ValidateRequest struct {
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	Token     string `json:"token"`
}

type ValidateResponse struct {
	OK bool `json:"ok"`
}
