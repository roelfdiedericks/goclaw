package a2a

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	a2aproto "github.com/a2aproject/a2a-go/v2/a2a"
	libp2ptransport "github.com/roelfdiedericks/goclaw/internal/a2a/transports/libp2p"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type Manager struct {
	mu       sync.RWMutex
	cfg      Config
	users    *user.Registry
	executor Executor
	adapter  ExecutionAdapter
	runtime  *libp2ptransport.Runtime

	peers        map[string]*PeerRecord
	tasks        map[string]*taskRuntime
	expiredTasks map[string]time.Time
	startedAt    *time.Time
	lastError    string
}

type taskRuntime struct {
	Key        string
	TaskID     string
	RemotePeer string
	LocalUser  string
	SessionKey string
	ContextID  string
	ArtifactID a2aproto.ArtifactID
	Direction  TaskDirection
	State      TaskState
	Snapshot   TaskSnapshot
	Cancel     context.CancelFunc
	Watchers   []chan TaskSnapshot
}

type TaskSnapshot struct {
	TaskID     string    `json:"taskId"`
	PeerID     string    `json:"peerId"`
	SessionKey string    `json:"sessionKey"`
	ContextID  string    `json:"contextId,omitempty"`
	State      TaskState `json:"state"`
	Content    string    `json:"content,omitempty"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func NewManager(cfg Config, users *user.Registry) *Manager {
	cfg.Normalize()
	m := &Manager{
		cfg:          cfg,
		users:        users,
		peers:        make(map[string]*PeerRecord),
		tasks:        make(map[string]*taskRuntime),
		expiredTasks: make(map[string]time.Time),
	}
	for _, trusted := range cfg.Libp2p.TrustedPeers {
		if strings.TrimSpace(trusted.PeerID) == "" {
			continue
		}
		state := PeerStateTrustedConfigured
		if !trusted.Enabled {
			state = PeerStateDisconnected
		}
		m.peers[trusted.PeerID] = &PeerRecord{
			PeerID:     trusted.PeerID,
			Alias:      trusted.Alias,
			LocalUser:  trusted.LocalUser,
			State:      state,
			Trusted:    trusted.Enabled,
			Authorized: trusted.Enabled && strings.TrimSpace(trusted.LocalUser) != "",
			Addrs:      slices.Clone(trusted.Addrs),
			Notes:      trusted.Notes,
		}
	}
	return m
}

func (m *Manager) SetExecutor(executor Executor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executor = executor
	m.adapter = NewGatewayAdapter(executor)
}

func (m *Manager) Start(ctx context.Context) error {
	return m.startWithMode(ctx, RuntimeModeNode)
}

func (m *Manager) StartInfra(ctx context.Context, mode RuntimeMode) error {
	if mode == RuntimeModeNode {
		return fmt.Errorf("infra runtime mode required")
	}
	return m.startWithMode(ctx, mode)
}

func (m *Manager) startWithMode(ctx context.Context, mode RuntimeMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.cfg.Enabled || !m.cfg.Libp2p.Enabled {
		L_info("a2a: startup skipped", "enabled", m.cfg.Enabled, "libp2pEnabled", m.cfg.Libp2p.Enabled)
		return nil
	}
	if m.cfg.DefaultTransport != DefaultTransportLibp2p {
		return fmt.Errorf("unsupported A2A transport: %s", m.cfg.DefaultTransport)
	}
	if m.runtime != nil {
		return nil
	}
	now := time.Now()
	rt := libp2ptransport.New(libp2ptransport.Config{
		IdentityKeyFile:      m.cfg.Libp2p.Identity.KeyFile,
		ListenAddrs:          m.cfg.Libp2p.ListenAddrs,
		BootstrapPeers:       m.cfg.Libp2p.BootstrapPeers,
		MDNSEnabled:          m.cfg.Libp2p.Discovery.MDNSEnabled,
		MDNSServiceName:      m.cfg.Libp2p.Discovery.ServiceName,
		RendezvousEnabled:    m.cfg.Libp2p.Discovery.RendezvousEnabled,
		RendezvousNamespace:  m.cfg.Libp2p.Discovery.RendezvousNamespace,
		RegisterIntervalSecs: m.cfg.Libp2p.Discovery.RegisterIntervalSecs,
		QueryIntervalSecs:    m.cfg.Libp2p.Discovery.QueryIntervalSecs,
		RelayClientEnabled:   m.cfg.Libp2p.Relay.EnableClient,
		RelayServerEnabled:   m.cfg.Libp2p.Relay.EnableServer,
		StaticRelays:         m.cfg.Libp2p.Relay.StaticRelays,
		RPCProtocolID:        m.cfg.Libp2p.Protocol.RPCProtocolID,
		RendezvousProtocolID: m.cfg.Libp2p.Protocol.RendezvousProtocolID,
	}, libp2ptransport.RuntimeMode(mode), libp2ptransport.Callbacks{
		OnPeerObserved:  m.observePeer,
		OnInboundSubmit: m.startInboundTaskFromTransport,
		OnInboundResume: m.resumeTaskFromTransport,
		OnInboundCancel: m.CancelTask,
	})
	if err := rt.Start(ctx); err != nil {
		m.lastError = err.Error()
		return err
	}
	m.runtime = rt
	m.startedAt = &now
	m.lastError = ""
	L_info("a2a: started", "mode", mode, "peerID", rt.LocalPeerID())
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredTasksLocked(time.Now())

	status := Status{
		Enabled:            m.cfg.Enabled && m.cfg.Libp2p.Enabled,
		ActiveTransport:    m.cfg.DefaultTransport,
		RuntimeMode:        RuntimeModeNode,
		BootstrapPeers:     len(m.cfg.Libp2p.BootstrapPeers),
		TrustedPeers:       len(m.cfg.Libp2p.TrustedPeers),
		KnownPeers:         len(m.peers),
		PeerStateCounts:    make(map[string]int),
		LastError:          m.lastError,
		StartedAt:          m.startedAt,
		RecentTaskCount:    len(m.tasks),
		StateRetentionSecs: m.cfg.Libp2p.Protocol.StateRetentionSecs,
	}
	status.RelayClientEnabled = m.cfg.Libp2p.Relay.EnableClient
	status.RelayServerEnabled = m.cfg.Libp2p.Relay.EnableServer
	status.RendezvousEnabled = m.cfg.Libp2p.Discovery.RendezvousEnabled
	status.RendezvousNamespace = m.cfg.Libp2p.Discovery.RendezvousNamespace
	for _, rec := range m.peers {
		status.PeerStateCounts[string(rec.State)]++
		if rec.Connected {
			status.ConnectedPeers++
		}
		if rec.State == PeerStateDiscoveredUntrusted {
			status.DiscoveredPeers++
		}
	}
	if m.runtime != nil {
		status.RuntimeMode = RuntimeMode(m.runtime.Mode())
		status.LocalPeerID = m.runtime.LocalPeerID()
		status.ListenAddrs = m.runtime.ListenAddrs()
		status.AdvertisedAddrs = m.runtime.AdvertisedAddrs()
	}
	return status
}

func (m *Manager) ListTasks(filter, peer string) []TaskSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredTasksLocked(time.Now())

	out := make([]TaskSummary, 0, len(m.tasks))
	for _, rt := range m.tasks {
		if rt == nil {
			continue
		}
		summary := TaskSummary{
			TaskID:     rt.TaskID,
			PeerID:     rt.RemotePeer,
			SessionKey: rt.SessionKey,
			ContextID:  rt.Snapshot.ContextID,
			State:      rt.State,
			Direction:  rt.Direction,
			Resumable:  !isTerminalTaskState(rt.State),
			LocalUser:  rt.LocalUser,
			LastError:  rt.Snapshot.Error,
			UpdatedAt:  rt.Snapshot.UpdatedAt,
		}
		if !matchesTaskFilter(summary, filter, peer) {
			continue
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			if out[i].PeerID == out[j].PeerID {
				return out[i].TaskID < out[j].TaskID
			}
			return out[i].PeerID < out[j].PeerID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func matchesTaskFilter(summary TaskSummary, filter, peer string) bool {
	filter = strings.TrimSpace(strings.ToLower(filter))
	peer = strings.TrimSpace(peer)
	if peer != "" && summary.PeerID != peer {
		return false
	}
	switch filter {
	case "", "all":
		return true
	case "active", "running":
		return !isTerminalTaskState(summary.State)
	case "resumable":
		return summary.Resumable
	case "failed":
		return summary.State == TaskStateFailed
	case "inbound":
		return summary.Direction == TaskDirectionInbound
	case "outbound", "remote":
		return summary.Direction == TaskDirectionOutbound
	default:
		return true
	}
}

func (m *Manager) PairingPayload() PairingPayload {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.runtime == nil {
		return PairingPayload{}
	}
	return PairingPayload{
		PeerID: m.runtime.LocalPeerID(),
		Addrs:  m.runtime.AdvertisedAddrs(),
	}
}

func (m *Manager) ListPeers(filter string) []PeerRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]PeerRecord, 0, len(m.peers))
	for _, rec := range m.peers {
		if !matchesPeerFilter(rec, filter) {
			continue
		}
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].State == out[j].State {
			return out[i].PeerID < out[j].PeerID
		}
		return out[i].State < out[j].State
	})
	return out
}

func matchesPeerFilter(rec *PeerRecord, filter string) bool {
	switch strings.TrimSpace(strings.ToLower(filter)) {
	case "", "all":
		return true
	case "connected":
		return rec.Connected
	case "discovered":
		return rec.State == PeerStateDiscoveredUntrusted
	case "trusted":
		return rec.Trusted
	case "authorised", "authorized", "mapped", "configured":
		return rec.Authorized
	case "relayed":
		return rec.Relayed
	case "disconnected":
		return rec.State == PeerStateDisconnected
	default:
		return true
	}
}

func (m *Manager) PingPeer(ctx context.Context, target string) (PingResult, error) {
	m.mu.RLock()
	rt := m.runtime
	m.mu.RUnlock()
	if rt == nil {
		return PingResult{}, fmt.Errorf("A2A runtime not started")
	}
	peerID, latency, message, err := rt.PingPeer(ctx, target, m.transportPeerCandidates())
	if err != nil {
		return PingResult{}, err
	}
	return PingResult{
		PeerID:  peerID,
		Success: true,
		Latency: latency,
		Message: message,
	}, nil
}

func (m *Manager) UpsertPeer(record PeerRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.peers[record.PeerID]
	if !ok {
		copyRecord := record
		m.peers[record.PeerID] = &copyRecord
		return
	}
	if record.Alias != "" {
		existing.Alias = record.Alias
	}
	if record.LocalUser != "" {
		existing.LocalUser = record.LocalUser
	}
	if len(record.Addrs) > 0 {
		existing.Addrs = slices.Clone(record.Addrs)
	}
	if record.State != "" {
		existing.State = record.State
	}
	existing.Connected = record.Connected
	existing.Relayed = record.Relayed
	existing.Trusted = record.Trusted || existing.Trusted
	existing.Authorized = record.Authorized || existing.Authorized
	if !record.LastSeen.IsZero() {
		existing.LastSeen = record.LastSeen
	}
	if !record.LastConnectedAt.IsZero() {
		existing.LastConnectedAt = record.LastConnectedAt
	}
	if !record.LastDisconnectAt.IsZero() {
		existing.LastDisconnectAt = record.LastDisconnectAt
	}
	if record.Notes != "" {
		existing.Notes = record.Notes
	}
}

func (m *Manager) BuildPeerRecord(peerID string, addrs []string, connected, relayed bool) PeerRecord {
	record := PeerRecord{
		PeerID:    peerID,
		Addrs:     slices.Clone(addrs),
		Connected: connected,
		Relayed:   relayed,
		LastSeen:  time.Now(),
	}
	trusted, _, err := m.resolveTrustedPeer(peerID)
	if err == nil && trusted != nil {
		record.Trusted = true
		record.Authorized = strings.TrimSpace(trusted.LocalUser) != ""
		record.LocalUser = trusted.LocalUser
		record.Alias = trusted.Alias
		record.Notes = trusted.Notes
	}
	switch {
	case connected && relayed:
		record.State = PeerStateConnectedRelayed
	case connected && record.Trusted:
		record.State = PeerStateConnectedAuthorized
	case connected:
		record.State = PeerStateDiscoveredUntrusted
	case record.Trusted:
		record.State = PeerStateTrustedConfigured
	default:
		record.State = PeerStateDiscoveredUntrusted
	}
	return record
}

func (m *Manager) resolveTrustedPeer(peerID string) (*TrustedPeerConfig, *user.User, error) {
	for _, trusted := range m.cfg.Libp2p.TrustedPeers {
		if trusted.PeerID != peerID || !trusted.Enabled {
			continue
		}
		if strings.TrimSpace(trusted.LocalUser) == "" {
			return nil, nil, fmt.Errorf("trusted peer %s has no local user mapping", peerID)
		}
		if m.users == nil {
			return nil, nil, fmt.Errorf("user registry unavailable")
		}
		localUser := m.users.Get(trusted.LocalUser)
		if localUser == nil {
			return nil, nil, fmt.Errorf("trusted peer %s maps to unknown user %s", peerID, trusted.LocalUser)
		}
		return &trusted, localUser, nil
	}
	return nil, nil, fmt.Errorf("peer %s is not trusted", peerID)
}

func taskKey(peerID, taskID string) string {
	return peerID + ":" + taskID
}

func (m *Manager) observePeer(observation libp2ptransport.PeerObservation) {
	record := m.BuildPeerRecord(observation.PeerID, observation.Addrs, observation.Connected, observation.Relayed)
	if !observation.LastSeen.IsZero() {
		record.LastSeen = observation.LastSeen
	}
	if !observation.LastConnectedAt.IsZero() {
		record.LastConnectedAt = observation.LastConnectedAt
	}
	if !observation.LastDisconnectAt.IsZero() {
		record.LastDisconnectAt = observation.LastDisconnectAt
		if record.Trusted {
			record.State = PeerStateDisconnected
		}
	}
	m.UpsertPeer(record)
}

func (m *Manager) transportPeerCandidates() []libp2ptransport.PeerCandidate {
	peers := m.ListPeers("all")
	out := make([]libp2ptransport.PeerCandidate, 0, len(peers))
	for _, record := range peers {
		out = append(out, libp2ptransport.PeerCandidate{
			PeerID: record.PeerID,
			Alias:  record.Alias,
			Addrs:  slices.Clone(record.Addrs),
		})
	}
	return out
}
