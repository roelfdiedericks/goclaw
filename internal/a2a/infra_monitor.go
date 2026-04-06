package a2a

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type InfraSummary struct {
	ConnectedPeers          int            `json:"connectedPeers"`
	ConnectedDirectPeers    int            `json:"connectedDirectPeers"`
	ConnectedRelayedPeers   int            `json:"connectedRelayedPeers"`
	ConnectedPeerStateCount map[string]int `json:"connectedPeerStateCount,omitempty"`
	RendezvousEntries       int            `json:"rendezvousEntries"`
	RendezvousNamespaces    int            `json:"rendezvousNamespaces"`
	RendezvousByNamespace   map[string]int `json:"rendezvousByNamespace,omitempty"`
}

type InfraConnectedPeer struct {
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

type InfraRendezvousEntry struct {
	Namespace string    `json:"namespace"`
	PeerID    string    `json:"peerId"`
	Addrs     []string  `json:"addrs,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

type InfraRendezvousNamespace struct {
	Namespace string                `json:"namespace"`
	Entries   []InfraRendezvousEntry `json:"entries,omitempty"`
}

type InfraSnapshot struct {
	Status      Status                    `json:"status"`
	Summary     InfraSummary              `json:"summary"`
	Peers       []InfraConnectedPeer      `json:"peers,omitempty"`
	Rendezvous  []InfraRendezvousNamespace `json:"rendezvous,omitempty"`
	CapturedAt  time.Time                 `json:"capturedAt"`
}

func (m *Manager) InfraSnapshot() InfraSnapshot {
	m.mu.RLock()
	status := m.statusLocked()
	peers := make([]InfraConnectedPeer, 0, len(m.peers))
	for _, rec := range m.peers {
		if rec == nil || !rec.Connected {
			continue
		}
		peers = append(peers, InfraConnectedPeer{
			PeerID:           rec.PeerID,
			Alias:            rec.Alias,
			LocalUser:        rec.LocalUser,
			State:            rec.State,
			Connected:        rec.Connected,
			Relayed:          rec.Relayed,
			Trusted:          rec.Trusted,
			Authorized:       rec.Authorized,
			LastSeen:         rec.LastSeen,
			LastConnectedAt:  rec.LastConnectedAt,
			LastDisconnectAt: rec.LastDisconnectAt,
			Addrs:            cloneStrings(rec.Addrs),
			Notes:            rec.Notes,
		})
	}
	rt := m.runtime
	m.mu.RUnlock()

	sort.Slice(peers, func(i, j int) bool {
		if peers[i].State == peers[j].State {
			return peers[i].PeerID < peers[j].PeerID
		}
		return peers[i].State < peers[j].State
	})

	var rendezvous []InfraRendezvousNamespace
	if rt != nil {
		namespaces := rt.RendezvousSnapshot()
		rendezvous = make([]InfraRendezvousNamespace, 0, len(namespaces))
		for _, ns := range namespaces {
			out := InfraRendezvousNamespace{
				Namespace: ns.Namespace,
				Entries:   make([]InfraRendezvousEntry, 0, len(ns.Entries)),
			}
			for _, entry := range ns.Entries {
				out.Entries = append(out.Entries, InfraRendezvousEntry{
					Namespace: ns.Namespace,
					PeerID:    entry.PeerID,
					Addrs:     cloneStrings(entry.Addrs),
					ExpiresAt: entry.ExpiresAt,
				})
			}
			rendezvous = append(rendezvous, out)
		}
	}

	summary := InfraSummary{
		ConnectedPeerStateCount: map[string]int{},
		RendezvousByNamespace:   map[string]int{},
	}
	for _, peer := range peers {
		summary.ConnectedPeers++
		if peer.Relayed {
			summary.ConnectedRelayedPeers++
		} else {
			summary.ConnectedDirectPeers++
		}
		summary.ConnectedPeerStateCount[string(peer.State)]++
	}
	for _, ns := range rendezvous {
		summary.RendezvousNamespaces++
		summary.RendezvousByNamespace[ns.Namespace] = len(ns.Entries)
		summary.RendezvousEntries += len(ns.Entries)
	}

	return InfraSnapshot{
		Status:     status,
		Summary:    summary,
		Peers:      peers,
		Rendezvous: rendezvous,
		CapturedAt: time.Now(),
	}
}

func (s InfraSnapshot) Fingerprint() string {
	copySnapshot := s
	copySnapshot.CapturedAt = time.Time{}
	data, err := json.Marshal(copySnapshot)
	if err != nil {
		return ""
	}
	return string(data)
}

func (m *Manager) statusLocked() Status {
	status := Status{
		Enabled:            m.cfg.Enabled && m.cfg.Libp2p.Enabled,
		ActiveTransport:    m.cfg.DefaultTransport,
		LifecycleState:     m.currentLifecycleStateLocked(),
		Ready:              m.runtime != nil && (m.lifecycleState == LifecycleStateRunning || m.lifecycleState == LifecycleStateDegraded),
		WarmupComplete:     m.warmupComplete,
		RuntimeMode:        m.runtimeMode,
		BootstrapPeers:     len(m.cfg.Libp2p.BootstrapPeers),
		TrustedPeers:       m.trustedPeerCountLocked(),
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

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
