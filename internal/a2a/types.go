package a2a

import (
	"context"
	"time"

	a2aproto "github.com/a2aproject/a2a-go/v2/a2a"
)

type PeerState string

const (
	PeerStateDiscoveredUntrusted PeerState = "discovered-untrusted"
	PeerStateTrustedConfigured   PeerState = "trusted-configured"
	PeerStateConnectedAuthorized PeerState = "connected-authorized"
	PeerStateConnectedRelayed    PeerState = "connected-relayed"
	PeerStateDisconnected        PeerState = "disconnected"
)

type RuntimeMode string

const (
	RuntimeModeNode      RuntimeMode = "node"
	RuntimeModeBootstrap RuntimeMode = "bootstrap"
	RuntimeModeRelay     RuntimeMode = "relay"
	RuntimeModeBoth      RuntimeMode = "both"
)

type LifecycleState string

const (
	LifecycleStateIdle      LifecycleState = "idle"
	LifecycleStateDisabled  LifecycleState = "disabled"
	LifecycleStateStarting  LifecycleState = "starting"
	LifecycleStateRunning   LifecycleState = "running"
	LifecycleStateDegraded  LifecycleState = "degraded"
	LifecycleStateFailed    LifecycleState = "failed"
)

type TaskState string

const (
	TaskStateSubmitted   TaskState = "submitted"
	TaskStateRunning     TaskState = "running"
	TaskStateInterrupted TaskState = "interrupted"
	TaskStateCompleted   TaskState = "completed"
	TaskStateFailed      TaskState = "failed"
	TaskStateCancelled   TaskState = "cancelled"
)

type TaskDirection string

const (
	TaskDirectionInbound  TaskDirection = "inbound"
	TaskDirectionOutbound TaskDirection = "outbound"
)

type PeerRecord struct {
	PeerID           string    `json:"peerId"`
	Alias            string    `json:"alias,omitempty"`
	LocalUser        string    `json:"localUser,omitempty"`
	State            PeerState `json:"state"`
	Connected        bool      `json:"connected"`
	Relayed          bool      `json:"relayed"`
	Trusted          bool      `json:"trusted"`
	Authorized       bool      `json:"authorized"`
	LastSeen         time.Time `json:"lastSeen,omitempty"`
	LastConnectedAt  time.Time `json:"lastConnectedAt,omitempty"`
	LastDisconnectAt time.Time `json:"lastDisconnectAt,omitempty"`
	Addrs            []string  `json:"addrs,omitempty"`
	Notes            string    `json:"notes,omitempty"`
}

type Status struct {
	Enabled             bool           `json:"enabled"`
	ActiveTransport     string         `json:"activeTransport"`
	LifecycleState      LifecycleState `json:"lifecycleState"`
	Ready               bool           `json:"ready"`
	WarmupComplete      bool           `json:"warmupComplete"`
	RuntimeMode         RuntimeMode    `json:"runtimeMode"`
	LocalPeerID         string         `json:"localPeerId,omitempty"`
	ListenAddrs         []string       `json:"listenAddrs,omitempty"`
	AdvertisedAddrs     []string       `json:"advertisedAddrs,omitempty"`
	RelayAddrs          []string       `json:"relayAddrs,omitempty"`
	BootstrapPeers      int            `json:"bootstrapPeers"`
	TrustedPeers        int            `json:"trustedPeers"`
	KnownPeers          int            `json:"knownPeers"`
	ConnectedPeers      int            `json:"connectedPeers"`
	DiscoveredPeers     int            `json:"discoveredPeers"`
	RelayClientEnabled  bool           `json:"relayClientEnabled"`
	RelayServerEnabled  bool           `json:"relayServerEnabled"`
	AutoRelayEnabled    bool           `json:"autoRelayEnabled"`
	HolePunchEnabled    bool           `json:"holePunchEnabled"`
	NATPortMapEnabled   bool           `json:"natPortMapEnabled"`
	AutoNATv2Enabled    bool           `json:"autoNATv2Enabled"`
	NATServiceEnabled   bool           `json:"natServiceEnabled"`
	AnnouncePrivate     bool           `json:"announcePrivate"`
	RendezvousEnabled   bool           `json:"rendezvousEnabled"`
	RendezvousNamespace string         `json:"rendezvousNamespace,omitempty"`
	RendezvousAdmissionMode string     `json:"rendezvousAdmissionMode,omitempty"`
	RendezvousAcceptsPrivate bool      `json:"rendezvousAcceptsPrivate"`
	Reachability        string         `json:"reachability,omitempty"`
	PeerStateCounts     map[string]int `json:"peerStateCounts,omitempty"`
	LastError           string         `json:"lastError,omitempty"`
	StartedAt           *time.Time     `json:"startedAt,omitempty"`
	RecentTaskCount     int            `json:"recentTaskCount"`
	StateRetentionSecs  int            `json:"stateRetentionSecs"`
}

type PairingPayload struct {
	PeerID string   `json:"peerId"`
	Addrs  []string `json:"addrs,omitempty"`
}

type PingResult struct {
	PeerID     string        `json:"peerId"`
	Success    bool          `json:"success"`
	Latency    time.Duration `json:"latency"`
	Relayed    bool          `json:"relayed"`
	Message    string        `json:"message"`
	RemoteMode string        `json:"remoteMode,omitempty"`
}

type TaskSummary struct {
	TaskID     string        `json:"taskId"`
	PeerID     string        `json:"peerId"`
	SessionKey string        `json:"sessionKey"`
	ContextID  string        `json:"contextId,omitempty"`
	State      TaskState     `json:"state"`
	Direction  TaskDirection `json:"direction"`
	Resumable  bool          `json:"resumable"`
	LocalUser  string        `json:"localUser,omitempty"`
	LastError  string        `json:"lastError,omitempty"`
	UpdatedAt  time.Time     `json:"updatedAt"`
}

type ExecutionRequest struct {
	TaskID      a2aproto.TaskID
	ContextID   string
	ArtifactID  a2aproto.ArtifactID
	TransportID string
	RemotePeer  string
	LocalUser   string
	SessionKey  string
	Message     *a2aproto.Message
}

type Executor interface {
	ExecuteTask(ctx context.Context, req ExecutionRequest, emit func(a2aproto.Event)) error
}
