package libp2p

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	golibp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	autorelay "github.com/libp2p/go-libp2p/p2p/host/autorelay"
	relayclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/libp2p/go-libp2p/p2p/protocol/holepunch"
	pingproto "github.com/libp2p/go-libp2p/p2p/protocol/ping"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

type RuntimeMode string

const (
	RuntimeModeNode      RuntimeMode = "node"
	RuntimeModeBootstrap RuntimeMode = "bootstrap"
	RuntimeModeRelay     RuntimeMode = "relay"
	RuntimeModeBoth      RuntimeMode = "both"
)

const (
	bootstrapConnectedRefreshEvery = 60 * time.Minute
	rendezvousHealthyQueryEvery    = 5 * time.Minute
	rendezvousTriggerMinInterval   = 2 * time.Second
)

const (
	rendezvousEntrySourceSelf  = "self"
	rendezvousEntrySourceInfra = "infra"
)

type Config struct {
	IdentityKeyFile             string
	ListenAddrs                 []string
	AnnounceAddrs               []string
	AnnouncePrivateAddrs        bool
	DisableIdentifyAddrDiscovery bool
	NATPortMap                  bool
	BootstrapPeers              []string
	BootstrapSeedTXT            string
	MDNSEnabled                 bool
	MDNSServiceName             string
	RendezvousEnabled           bool
	RendezvousNamespace         string
	RendezvousAdmissionMode     string
	RegisterIntervalSecs        int
	QueryIntervalSecs           int
	RelayClientEnabled          bool
	RelayServerEnabled          bool
	AutoRelayEnabled            bool
	HolePunchEnabled            bool
	BackgroundDirectUpgradeEnabled bool
	DirectUpgradeTimeoutSecs    int
	DirectUpgradeCooldownSecs   int
	RPCProtocolID               string
	RendezvousProtocolID        string
}

type PeerObservation struct {
	PeerID           string
	Addrs            []string
	Connected        bool
	Relayed          bool
	LastSeen         time.Time
	LastConnectedAt  time.Time
	LastDisconnectAt time.Time
}

type TaskUpdate struct {
	TaskID    string
	ContextID string
	State     string
	Content   string
	Error     string
	UpdatedAt time.Time
}

type PeerCandidate struct {
	PeerID string
	Alias  string
	Addrs  []string
}

type Callbacks struct {
	OnPeerObserved  func(PeerObservation)
	OnInboundSubmit func(ctx context.Context, peerID, taskID, input string) (<-chan TaskUpdate, error)
	OnInboundResume func(peerID, taskID string) (<-chan TaskUpdate, error)
	OnInboundCancel func(peerID, taskID string) error
}

type peerPathSnapshot struct {
	Mode      string
	Signature string
}

type peerConnectionSummary struct {
	Total      int
	Direct     int
	Relayed    int
	Transports map[string]int
	Addrs      []string
}

type Runtime struct {
	cfg       Config
	mode      RuntimeMode
	callbacks Callbacks

	host      host.Host
	ping      *pingproto.PingService
	relay     *relayv2.Relay
	startedAt time.Time

	rendezvousMu   sync.Mutex
	rendezvousData map[string]map[string]rendezvousEntry

	bootstrapMu         sync.RWMutex
	bootstrapEntries    []string
	bootstrapSource     string
	bootstrapResolvedAt time.Time
	bootstrapPeerIDs    map[peer.ID]struct{}
	lastRendezvousRegister time.Time
	lastRendezvousQuery time.Time

	stateMu           sync.RWMutex
	localReachability network.Reachability
	relayAddrs        []string
	peerPathState     map[string]peerPathSnapshot

	advertiseEvalMu      sync.Mutex
	lastAdvertiseEvalSig string

	// Background direct-upgrade scheduling: relay-first outbound traffic stays immediate;
	// bounded direct Connect() attempts run asynchronously for future sessions.
	directUpgradeMu       sync.Mutex
	directUpgradeLast     map[peer.ID]time.Time
	directUpgradeInflight map[peer.ID]struct{}
}

type rendezvousEntry struct {
	PeerID    string    `json:"peerId"`
	Addrs     []string  `json:"addrs"`
	ExpiresAt time.Time `json:"expiresAt"`
	Source    string    `json:"-"`
}

type rendezvousRequest struct {
	Action    string   `json:"action"`
	Namespace string   `json:"namespace"`
	PeerID    string   `json:"peerId,omitempty"`
	Addrs     []string `json:"addrs,omitempty"`
}

type rendezvousResponse struct {
	Entries []rendezvousEntry `json:"entries,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type rpcEnvelope struct {
	Kind    string      `json:"kind"`
	TaskID  string      `json:"taskId,omitempty"`
	Input   string      `json:"input,omitempty"`
	Update  *TaskUpdate `json:"update,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

func New(cfg Config, mode RuntimeMode, callbacks Callbacks) *Runtime {
	return &Runtime{
		cfg:                   cfg,
		mode:                  mode,
		callbacks:             callbacks,
		rendezvousData:        make(map[string]map[string]rendezvousEntry),
		bootstrapPeerIDs:      make(map[peer.ID]struct{}),
		localReachability:     network.ReachabilityUnknown,
		peerPathState:         make(map[string]peerPathSnapshot),
		directUpgradeLast:     make(map[peer.ID]time.Time),
		directUpgradeInflight: make(map[peer.ID]struct{}),
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	priv, err := loadOrCreateIdentity(r.cfg.IdentityKeyFile)
	if err != nil {
		return fmt.Errorf("load libp2p identity: %w", err)
	}
	listenAddrs, err := parseMultiaddrs(r.cfg.ListenAddrs)
	if err != nil {
		return fmt.Errorf("parse listen addrs: %w", err)
	}
	announceAddrs, err := parseAdvertiseMultiaddrs(r.cfg.AnnounceAddrs)
	if err != nil {
		return fmt.Errorf("parse announce addrs: %w", err)
	}
	options := []golibp2p.Option{
		golibp2p.Identity(priv),
		golibp2p.ListenAddrs(listenAddrs...),
		golibp2p.EnableRelay(),
		golibp2p.AddrsFactory(r.addrFactory(announceAddrs)),
		golibp2p.EnableAutoNATv2(),
	}
	if r.natServiceEnabled() {
		options = append(options, golibp2p.EnableNATService())
	}
	if r.cfg.NATPortMap {
		options = append(options, golibp2p.NATPortMap())
	}
	if r.cfg.DisableIdentifyAddrDiscovery {
		options = append(options, golibp2p.DisableIdentifyAddressDiscovery())
	}
	if r.mode == RuntimeModeNode && r.cfg.RelayClientEnabled && r.cfg.AutoRelayEnabled {
		options = append(options, golibp2p.EnableAutoRelayWithPeerSource(
			r.bootstrapRelayPeerSource(),
			autorelay.WithBootDelay(0),
			autorelay.WithMinCandidates(1),
			autorelay.WithMaxCandidates(1),
			autorelay.WithNumRelays(1),
			autorelay.WithMinInterval(1*time.Second),
			autorelay.WithBackoff(15*time.Second),
		))
	}
	if r.mode == RuntimeModeNode && r.cfg.RelayClientEnabled && r.cfg.HolePunchEnabled {
		options = append(options, golibp2p.EnableHolePunching(holepunch.WithTracer(r)))
	}

	h, err := golibp2p.New(options...)
	if err != nil {
		return fmt.Errorf("create libp2p host: %w", err)
	}
	r.host = h
	r.startedAt = time.Now()
	if r.cfg.DirectUpgradeTimeoutSecs <= 0 {
		r.cfg.DirectUpgradeTimeoutSecs = 3
	}
	if r.cfg.DirectUpgradeCooldownSecs <= 0 {
		r.cfg.DirectUpgradeCooldownSecs = 30
	}
	r.ping = pingproto.NewPingService(h)
	h.Network().Notify(&notifiee{runtime: r})
	r.startEventWatchers(ctx)

	L_info("a2a libp2p: host created",
		"mode", r.mode,
		"listenAddrs", len(listenAddrs),
		"explicitAnnounceAddrs", len(announceAddrs),
		"natPortMap", r.cfg.NATPortMap,
		"relayClient", r.cfg.RelayClientEnabled,
		"relayServer", r.cfg.RelayServerEnabled,
		"rendezvousAdmissionMode", r.cfg.RendezvousAdmissionMode,
		"autoNATv2", true,
		"natService", r.natServiceEnabled(),
		"autoRelay", r.cfg.AutoRelayEnabled,
		"autoRelayBootDelay", "0s",
		"autoRelayMinCandidates", 1,
		"autoRelayMaxCandidates", 1,
		"autoRelayDesiredRelays", 1,
		"holePunch", r.cfg.HolePunchEnabled,
		"backgroundDirectUpgrade", r.cfg.BackgroundDirectUpgradeEnabled,
		"directUpgradeTimeoutSecs", r.cfg.DirectUpgradeTimeoutSecs,
		"directUpgradeCooldownSecs", r.cfg.DirectUpgradeCooldownSecs,
		"autoRelayCandidateSource", "bootstrap",
	)

	if r.mode == RuntimeModeBootstrap || r.mode == RuntimeModeBoth {
		h.SetStreamHandler(protocol.ID(r.cfg.RendezvousProtocolID), r.handleRendezvousStream)
	}
	if r.mode == RuntimeModeNode {
		h.SetStreamHandler(protocol.ID(r.cfg.RPCProtocolID), r.handleRPCStream)
	}
	if r.mode == RuntimeModeRelay || r.mode == RuntimeModeBoth || r.cfg.RelayServerEnabled {
		relaySvc, err := relayv2.New(h)
		if err != nil {
			return fmt.Errorf("start relay service: %w", err)
		}
		r.relay = relaySvc
	}
	return nil
}

func (r *Runtime) natServiceEnabled() bool {
	return r.mode == RuntimeModeBootstrap || r.mode == RuntimeModeRelay || r.mode == RuntimeModeBoth || r.cfg.RelayServerEnabled
}

func (r *Runtime) Warmup(ctx context.Context) error {
	if r.host == nil {
		return fmt.Errorf("host not started")
	}
	var errs []string
	if r.bootstrapDialEnabled() {
		if err := r.connectBootstrapPeers(ctx, true); err != nil {
			L_warn("a2a libp2p: bootstrap connect pass failed", "error", err)
			errs = append(errs, err.Error())
		}
	} else {
		L_info("a2a libp2p: bootstrap connect pass skipped", "mode", r.mode)
	}
	if r.mode == RuntimeModeNode && r.cfg.RelayClientEnabled {
		if err := r.reserveBootstrapRelays(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if r.mode == RuntimeModeNode && r.cfg.MDNSEnabled {
		if err := r.startMDNS(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	r.runImmediateRendezvousPass(ctx, "warmup")
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) startEventWatchers(ctx context.Context) {
	if r.host == nil {
		return
	}
	reachabilitySub, err := r.host.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
	if err != nil {
		L_warn("a2a libp2p: reachability subscription failed", "error", err)
	} else {
		go r.watchReachability(ctx, reachabilitySub)
	}
	relaySub, err := r.host.EventBus().Subscribe(new(event.EvtAutoRelayAddrsUpdated))
	if err != nil {
		L_warn("a2a libp2p: auto relay subscription failed", "error", err)
	} else {
		go r.watchAutoRelayAddrs(ctx, relaySub)
	}
	identifySub, err := r.host.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted))
	if err != nil {
		L_warn("a2a libp2p: identify completion subscription failed", "error", err)
	} else {
		go r.watchPeerIdentificationCompleted(ctx, identifySub)
	}
	identifyFailedSub, err := r.host.EventBus().Subscribe(new(event.EvtPeerIdentificationFailed))
	if err != nil {
		L_warn("a2a libp2p: identify failure subscription failed", "error", err)
	} else {
		go r.watchPeerIdentificationFailed(ctx, identifyFailedSub)
	}
	connectednessSub, err := r.host.EventBus().Subscribe(new(event.EvtPeerConnectednessChanged))
	if err != nil {
		L_warn("a2a libp2p: connectedness subscription failed", "error", err)
	} else {
		go r.watchPeerConnectedness(ctx, connectednessSub)
	}
	localAddrsSub, err := r.host.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		L_warn("a2a libp2p: local address subscription failed", "error", err)
	} else {
		go r.watchLocalAddresses(ctx, localAddrsSub)
	}
	protocolsSub, err := r.host.EventBus().Subscribe(new(event.EvtPeerProtocolsUpdated))
	if err != nil {
		L_warn("a2a libp2p: peer protocol subscription failed", "error", err)
	} else {
		go r.watchPeerProtocols(ctx, protocolsSub)
	}
}

type eventSubscription interface {
	Out() <-chan interface{}
	Close() error
}

func (r *Runtime) watchReachability(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			reachability := evt.(event.EvtLocalReachabilityChanged).Reachability
			r.stateMu.Lock()
			changed := r.localReachability != reachability
			r.localReachability = reachability
			r.stateMu.Unlock()
			if changed {
				L_info("a2a libp2p: reachability changed", "reachability", strings.ToLower(reachability.String()))
				if reachability != network.ReachabilityUnknown {
					// Reachability alone is not a good registration edge: private often arrives
					// before relay/direct advertised addrs are actually ready. Query immediately,
					// but only register here once the node believes it is public.
					if reachability == network.ReachabilityPublic {
						go r.maybeRegisterRendezvous(ctx, "reachability-public", 0)
					}
					go r.maybeQueryRendezvous(ctx, "reachability-changed", 0, true)
				}
			}
		}
	}
}

func (r *Runtime) watchAutoRelayAddrs(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			relayAddrs := multiaddrsWithPeerIDToStrings(evt.(event.EvtAutoRelayAddrsUpdated).RelayAddrs, r.host.ID())
			r.stateMu.Lock()
			prevRelayAddrs := cloneStrings(r.relayAddrs)
			r.relayAddrs = relayAddrs
			r.stateMu.Unlock()
			L_info("a2a libp2p: relay addresses updated", "count", len(relayAddrs))
			for _, addr := range relayAddrs {
				L_trace("a2a libp2p: relay advertised address", "addr", addr)
			}
			if !sameStrings(prevRelayAddrs, relayAddrs) && len(relayAddrs) > 0 {
				// Relay addrs are the real "ready now" edge for private nodes. Register
				// immediately so rendezvous stops advertising an empty entry.
				go r.maybeRegisterRendezvous(ctx, "relay-addrs-updated", 0)
				go r.maybeQueryRendezvous(ctx, "relay-addrs-updated", 0, true)
			}
		}
	}
}

func (r *Runtime) watchPeerIdentificationCompleted(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			completed := evt.(event.EvtPeerIdentificationCompleted)
			remoteAddr := ""
			localAddr := ""
			observedAddr := ""
			relayed := false
			transport := "unknown"
			security := ""
			muxer := ""
			if completed.Conn != nil {
				if completed.Conn.RemoteMultiaddr() != nil {
					remoteAddr = completed.Conn.RemoteMultiaddr().String()
				}
				if completed.Conn.LocalMultiaddr() != nil {
					localAddr = completed.Conn.LocalMultiaddr().String()
				}
				relayed = isRelayedConn(completed.Conn)
				state := completed.Conn.ConnState()
				transport = connTransportLabel(completed.Conn)
				security = string(state.Security)
				muxer = string(state.StreamMultiplexer)
			}
			if completed.ObservedAddr != nil {
				observedAddr = completed.ObservedAddr.String()
			}
			L_info("a2a libp2p: peer identified",
				"peerID", completed.Peer,
				"remoteAddr", remoteAddr,
				"localAddr", localAddr,
				"observedAddr", observedAddr,
				"listenAddrs", len(completed.ListenAddrs),
				"protocols", len(completed.Protocols),
				"agentVersion", completed.AgentVersion,
				"protocolVersion", completed.ProtocolVersion,
				"relayed", relayed,
				"transport", transport,
				"security", security,
				"muxer", muxer,
			)
			if len(completed.ListenAddrs) > 0 {
				L_trace("a2a libp2p: peer identify listen addrs", "peerID", completed.Peer, "addrs", strings.Join(multiaddrsToStrings(completed.ListenAddrs), ", "))
			}
			if len(completed.Protocols) > 0 {
				L_trace("a2a libp2p: peer identify protocols", "peerID", completed.Peer, "protocols", strings.Join(protocolIDsToStrings(completed.Protocols), ", "))
			}
			r.logPeerPathState(completed.Peer, "identify-complete")
		}
	}
}

func (r *Runtime) watchPeerIdentificationFailed(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			failed := evt.(event.EvtPeerIdentificationFailed)
			L_warn("a2a libp2p: peer identification failed", "peerID", failed.Peer, "error", failed.Reason)
			r.logPeerPathState(failed.Peer, "identify-failed")
		}
	}
}

func (r *Runtime) watchPeerConnectedness(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			changed := evt.(event.EvtPeerConnectednessChanged)
			L_info("a2a libp2p: peer connectedness changed",
				"peerID", changed.Peer,
				"connectedness", strings.ToLower(changed.Connectedness.String()),
				"activeConnections", len(r.host.Network().ConnsToPeer(changed.Peer)),
			)
			r.logPeerPathState(changed.Peer, "connectedness")
		}
	}
}

func (r *Runtime) watchLocalAddresses(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			updated := evt.(event.EvtLocalAddressesUpdated)
			added := 0
			maintained := 0
			unknown := 0
			for _, current := range updated.Current {
				switch current.Action {
				case event.Added:
					added++
				case event.Maintained:
					maintained++
				default:
					unknown++
				}
			}
			L_info("a2a libp2p: local addresses updated",
				"diffs", updated.Diffs,
				"current", len(updated.Current),
				"removed", len(updated.Removed),
				"added", added,
				"maintained", maintained,
				"unknown", unknown,
			)
			for _, current := range updated.Current {
				if current.Address == nil {
					continue
				}
				L_trace("a2a libp2p: local address current", "action", addrActionLabel(current.Action), "addr", current.Address.String())
			}
			for _, removed := range updated.Removed {
				if removed.Address == nil {
					continue
				}
				L_trace("a2a libp2p: local address removed", "action", addrActionLabel(removed.Action), "addr", removed.Address.String())
			}
			if len(updated.Current) > 0 {
				// Local address changes can mean new direct/public advertised addrs. Register
				// them immediately; direct upgrades matter more than suppressing duplicate passes.
				go r.maybeRegisterRendezvous(ctx, "local-addresses-updated", 0)
				go r.maybeQueryRendezvous(ctx, "local-addresses-updated", 0, true)
			}
		}
	}
}

func (r *Runtime) watchPeerProtocols(ctx context.Context, sub eventSubscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Out():
			if !ok {
				return
			}
			updated := evt.(event.EvtPeerProtocolsUpdated)
			L_debug("a2a libp2p: peer protocols updated",
				"peerID", updated.Peer,
				"added", len(updated.Added),
				"removed", len(updated.Removed),
			)
			if len(updated.Added) > 0 {
				L_trace("a2a libp2p: peer protocols added", "peerID", updated.Peer, "protocols", strings.Join(protocolIDsToStrings(updated.Added), ", "))
			}
			if len(updated.Removed) > 0 {
				L_trace("a2a libp2p: peer protocols removed", "peerID", updated.Peer, "protocols", strings.Join(protocolIDsToStrings(updated.Removed), ", "))
			}
		}
	}
}

func (r *Runtime) Run(ctx context.Context) error {
	r.runBackground(ctx)
	return nil
}

func (r *Runtime) Mode() RuntimeMode { return r.mode }

func (r *Runtime) LocalPeerID() string {
	if r.host == nil {
		return ""
	}
	return r.host.ID().String()
}

func (r *Runtime) ListenAddrs() []string {
	if r.host == nil {
		return nil
	}
	return multiaddrsToStrings(r.host.Network().ListenAddresses())
}

func (r *Runtime) AdvertisedAddrs() []string {
	if r.host == nil {
		return nil
	}
	return multiaddrsWithPeerIDToStrings(r.host.Addrs(), r.host.ID())
}

func (r *Runtime) RelayAddrs() []string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return cloneStrings(r.relayAddrs)
}

func (r *Runtime) Reachability() string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return strings.ToLower(r.localReachability.String())
}

func (r *Runtime) AutoNATv2Enabled() bool {
	return true
}

func (r *Runtime) NATServiceEnabled() bool {
	return r.natServiceEnabled()
}

func (r *Runtime) PingPeer(ctx context.Context, target string, knownPeers []PeerCandidate) (string, time.Duration, string, error) {
	if r.host == nil {
		return "", 0, "", fmt.Errorf("host not started")
	}
	info, sources, err := r.prepareTargetPeer(ctx, target, knownPeers, true)
	if err != nil {
		return "", 0, "", err
	}
	L_info("a2a libp2p: outbound ping starting",
		"target", target,
		"peerID", info.ID,
		"sources", strings.Join(sources, ", "),
		"addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "),
	)
	if err := r.ensureConnectedPeer(ctx, *info); err != nil {
		L_debug("a2a libp2p: ping connect best effort failed", "peerID", info.ID, "error", err)
	}
	if r.hasLivePeerConnection(info.ID) {
		r.afterOutboundPeerReady("ping", info.ID, info.Addrs)
	}
	results := r.ping.Ping(ctx, info.ID)
	select {
	case result := <-results:
		if result.Error != nil {
			L_warn("a2a libp2p: outbound ping failed", "peerID", info.ID, "error", result.Error)
			return "", 0, "", result.Error
		}
		L_info("a2a libp2p: outbound ping result", "peerID", info.ID, "latency", result.RTT)
		return info.ID.String(), result.RTT, "pong", nil
	case <-ctx.Done():
		L_warn("a2a libp2p: outbound ping context done", "peerID", info.ID, "error", ctx.Err())
		return "", 0, "", ctx.Err()
	}
}

func (r *Runtime) SubmitRemoteTask(ctx context.Context, target, taskID, input string, knownPeers []PeerCandidate) (<-chan TaskUpdate, error) {
	info, sources, err := r.prepareTargetPeer(ctx, target, knownPeers, true)
	if err != nil {
		return nil, err
	}
	L_info("a2a libp2p: outbound submit starting",
		"target", target,
		"peerID", info.ID,
		"taskID", taskID,
		"sources", strings.Join(sources, ", "),
		"addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "),
		"inputLength", len(input),
	)
	if err := r.ensureConnectedPeer(ctx, *info); err != nil {
		return nil, err
	}
	r.afterOutboundPeerReady("submit", info.ID, info.Addrs)
	stream, err := r.openRPCStream(ctx, info.ID, "submit", taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}
	writer := bufio.NewWriter(stream)
	if err := json.NewEncoder(writer).Encode(rpcEnvelope{Kind: "submit", TaskID: taskID, Input: input}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := writer.Flush(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	updates := make(chan TaskUpdate, 16)
	go readRemoteUpdates(stream, updates)
	return updates, nil
}

func (r *Runtime) ResumeRemoteTask(ctx context.Context, target, taskID string, knownPeers []PeerCandidate) (<-chan TaskUpdate, error) {
	info, sources, err := r.prepareTargetPeer(ctx, target, knownPeers, true)
	if err != nil {
		return nil, err
	}
	L_info("a2a libp2p: outbound resume starting",
		"target", target,
		"peerID", info.ID,
		"taskID", taskID,
		"sources", strings.Join(sources, ", "),
		"addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "),
	)
	if err := r.ensureConnectedPeer(ctx, *info); err != nil {
		return nil, err
	}
	r.afterOutboundPeerReady("resume", info.ID, info.Addrs)
	stream, err := r.openRPCStream(ctx, info.ID, "resume", taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}
	writer := bufio.NewWriter(stream)
	if err := json.NewEncoder(writer).Encode(rpcEnvelope{Kind: "resume", TaskID: taskID}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := writer.Flush(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	updates := make(chan TaskUpdate, 16)
	go readRemoteUpdates(stream, updates)
	return updates, nil
}

func (r *Runtime) CancelRemoteTask(ctx context.Context, target, taskID string, knownPeers []PeerCandidate) (TaskUpdate, error) {
	info, sources, err := r.prepareTargetPeer(ctx, target, knownPeers, true)
	if err != nil {
		return TaskUpdate{}, err
	}
	L_info("a2a libp2p: outbound cancel starting",
		"target", target,
		"peerID", info.ID,
		"taskID", taskID,
		"sources", strings.Join(sources, ", "),
		"addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "),
	)
	if err := r.ensureConnectedPeer(ctx, *info); err != nil {
		return TaskUpdate{}, err
	}
	r.afterOutboundPeerReady("cancel", info.ID, info.Addrs)
	stream, err := r.openRPCStream(ctx, info.ID, "cancel", taskID)
	if err != nil {
		return TaskUpdate{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	writer := bufio.NewWriter(stream)
	if err := json.NewEncoder(writer).Encode(rpcEnvelope{Kind: "cancel", TaskID: taskID}); err != nil {
		return TaskUpdate{}, err
	}
	if err := writer.Flush(); err != nil {
		return TaskUpdate{}, err
	}

	var env rpcEnvelope
	if err := json.NewDecoder(stream).Decode(&env); err != nil {
		return TaskUpdate{}, err
	}
	switch env.Kind {
	case "cancelled":
		return TaskUpdate{TaskID: taskID, State: "cancelled", UpdatedAt: time.Now()}, nil
	case "error":
		return TaskUpdate{}, fmt.Errorf(env.Error)
	default:
		return TaskUpdate{}, fmt.Errorf("unexpected cancel response kind %q", env.Kind)
	}
}

func (r *Runtime) runBackground(ctx context.Context) {
	var bootstrapTicker *time.Ticker
	var bootstrapCh <-chan time.Time
	if r.bootstrapDialEnabled() {
		bootstrapTicker = time.NewTicker(30 * time.Second)
		bootstrapCh = bootstrapTicker.C
		defer bootstrapTicker.Stop()
	}

	registerEvery := time.Duration(r.cfg.RegisterIntervalSecs) * time.Second
	if registerEvery <= 0 {
		registerEvery = 30 * time.Second
	}
	queryEvery := time.Duration(r.cfg.QueryIntervalSecs) * time.Second
	if queryEvery <= 0 {
		queryEvery = 30 * time.Second
	}
	registerTicker := time.NewTicker(registerEvery)
	defer registerTicker.Stop()
	queryTicker := time.NewTicker(queryEvery)
	defer queryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			if r.host != nil {
				_ = r.host.Close()
			}
			return
		case <-bootstrapCh:
			if ok, forceRefresh := r.shouldAttemptBootstrapRefresh(time.Now()); ok {
				_ = r.connectBootstrapPeers(ctx, forceRefresh)
			}
		case <-registerTicker.C:
			r.maybeRegisterRendezvous(ctx, "ticker", registerEvery)
		case <-queryTicker.C:
			r.maybeQueryRendezvous(ctx, "ticker", queryEvery, false)
		}
	}
}

func (r *Runtime) connectBootstrapPeers(ctx context.Context, forceRefresh bool) error {
	entries, source, resolutionErr := r.bootstrapPeerEntries(forceRefresh)
	if resolutionErr != nil {
		return resolutionErr
	}
	if len(entries) == 0 {
		L_info("a2a libp2p: no bootstrap peers resolved", "source", source)
		return nil
	}
	L_info("a2a libp2p: bootstrap candidates resolved", "source", source, "count", len(entries))
	var errs []string
	for _, entry := range entries {
		info, err := parseAddrInfo(entry)
		if err != nil {
			L_warn("a2a libp2p: invalid bootstrap multiaddr", "source", source, "addr", entry, "error", err)
			errs = append(errs, err.Error())
			continue
		}
		if r.host != nil && info.ID == r.host.ID() {
			L_info("a2a libp2p: skipping self bootstrap candidate", "source", source, "peerID", info.ID)
			continue
		}
		if err := r.connectAddrInfo(ctx, *info); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) bootstrapDialEnabled() bool {
	return r.mode == RuntimeModeNode
}

func (r *Runtime) bootstrapPeerEntries(forceRefresh bool) ([]string, string, error) {
	if !forceRefresh {
		if entries, source, ok := r.cachedBootstrapPeerEntries(); ok {
			L_trace("a2a libp2p: using cached bootstrap entries", "source", source, "count", len(entries))
			return entries, source, nil
		}
	}
	entries, source, err := r.resolveBootstrapPeerEntries()
	if err != nil {
		return nil, source, err
	}
	r.storeBootstrapPeerEntries(entries, source)
	return entries, source, nil
}

func (r *Runtime) resolveBootstrapPeerEntries() ([]string, string, error) {
	if len(r.cfg.BootstrapPeers) > 0 {
		normalized := normalizeBootstrapEntries(r.cfg.BootstrapPeers)
		if len(normalized) != len(r.cfg.BootstrapPeers) {
			L_info("a2a libp2p: bootstrap candidates normalized", "source", "config", "input", len(r.cfg.BootstrapPeers), "kept", len(normalized), "transportPreference", "quic>tcp")
		}
		return normalized, "config", nil
	}
	seed := strings.TrimSpace(r.cfg.BootstrapSeedTXT)
	if seed == "" {
		return nil, "dns_txt", nil
	}
	txts, err := net.LookupTXT(seed)
	if err != nil {
		return nil, "dns_txt", fmt.Errorf("resolve bootstrap TXT %s: %w", seed, err)
	}
	valid := make([]string, 0, len(txts))
	var invalid int
	for _, txt := range txts {
		entry := strings.TrimSpace(txt)
		if entry == "" {
			continue
		}
		if _, err := parseAddrInfo(entry); err != nil {
			L_warn("a2a libp2p: ignoring malformed bootstrap TXT entry", "seed", seed, "value", entry, "error", err)
			invalid++
			continue
		}
		valid = append(valid, entry)
	}
	L_info("a2a libp2p: bootstrap TXT resolved", "seed", seed, "records", len(txts), "valid", len(valid), "invalid", invalid)
	normalized := normalizeBootstrapEntries(valid)
	if len(normalized) != len(valid) {
		L_info("a2a libp2p: bootstrap candidates normalized", "source", "dns_txt", "input", len(valid), "kept", len(normalized), "transportPreference", "quic>tcp")
	}
	return normalized, "dns_txt", nil
}

func (r *Runtime) cachedBootstrapPeerEntries() ([]string, string, bool) {
	r.bootstrapMu.RLock()
	defer r.bootstrapMu.RUnlock()
	if len(r.bootstrapEntries) == 0 {
		return nil, "", false
	}
	return cloneStrings(r.bootstrapEntries), r.bootstrapSource, true
}

func (r *Runtime) storeBootstrapPeerEntries(entries []string, source string) {
	peerIDs := make(map[peer.ID]struct{})
	for _, entry := range entries {
		info, err := parseAddrInfo(entry)
		if err != nil {
			continue
		}
		peerIDs[info.ID] = struct{}{}
	}
	r.bootstrapMu.Lock()
	r.bootstrapEntries = cloneStrings(entries)
	r.bootstrapSource = source
	r.bootstrapResolvedAt = time.Now()
	r.bootstrapPeerIDs = peerIDs
	r.bootstrapMu.Unlock()
}

func (r *Runtime) shouldAttemptBootstrapRefresh(now time.Time) (bool, bool) {
	if !r.connectedToBootstrapPeer() {
		return true, true
	}
	r.bootstrapMu.RLock()
	resolvedAt := r.bootstrapResolvedAt
	r.bootstrapMu.RUnlock()
	if resolvedAt.IsZero() || now.Sub(resolvedAt) >= bootstrapConnectedRefreshEvery {
		return true, true
	}
	L_trace("a2a libp2p: bootstrap refresh skipped", "reason", "connected-bootstrap-peer", "nextRefreshIn", bootstrapConnectedRefreshEvery-now.Sub(resolvedAt))
	return false, false
}

func (r *Runtime) connectedToBootstrapPeer() bool {
	if r.host == nil {
		return false
	}
	r.bootstrapMu.RLock()
	defer r.bootstrapMu.RUnlock()
	for peerID := range r.bootstrapPeerIDs {
		if r.host.Network().Connectedness(peerID) == network.Connected {
			return true
		}
	}
	return false
}

func (r *Runtime) shouldRunRendezvousQuery(now time.Time) bool {
	healthy := r.connectedToBootstrapPeer()
	r.bootstrapMu.Lock()
	defer r.bootstrapMu.Unlock()
	if healthy && !r.lastRendezvousQuery.IsZero() && now.Sub(r.lastRendezvousQuery) < rendezvousHealthyQueryEvery {
		L_trace("a2a libp2p: rendezvous query skipped", "reason", "healthy-bootstrap-connection", "nextQueryIn", rendezvousHealthyQueryEvery-now.Sub(r.lastRendezvousQuery))
		return false
	}
	r.lastRendezvousQuery = now
	return true
}

func (r *Runtime) runImmediateRendezvousPass(ctx context.Context, reason string) {
	if !r.rendezvousEnabledForNode() {
		return
	}
	r.maybeRegisterRendezvous(ctx, reason, 0)
	r.maybeQueryRendezvous(ctx, reason, 0, true)
}

func (r *Runtime) triggerRendezvousRegister(ctx context.Context, reason string) {
	if !r.rendezvousEnabledForNode() {
		return
	}
	r.maybeRegisterRendezvous(ctx, reason, rendezvousTriggerMinInterval)
}

func (r *Runtime) rendezvousEnabledForNode() bool {
	return r.mode == RuntimeModeNode && r.cfg.RendezvousEnabled
}

func (r *Runtime) maybeRegisterRendezvous(ctx context.Context, trigger string, minInterval time.Duration) bool {
	if !r.rendezvousEnabledForNode() {
		return false
	}
	now := time.Now()
	if !r.reserveRendezvousRegisterSlot(now, minInterval, trigger) {
		return false
	}
	r.registerRendezvous(ctx)
	return true
}

func (r *Runtime) maybeQueryRendezvous(ctx context.Context, trigger string, minInterval time.Duration, bypassHealthyInterval bool) bool {
	if !r.rendezvousEnabledForNode() {
		return false
	}
	now := time.Now()
	if !r.reserveRendezvousQuerySlot(now, minInterval, trigger, bypassHealthyInterval) {
		return false
	}
	r.queryRendezvous(ctx)
	return true
}

func (r *Runtime) reserveRendezvousRegisterSlot(now time.Time, minInterval time.Duration, trigger string) bool {
	r.bootstrapMu.Lock()
	defer r.bootstrapMu.Unlock()
	if minInterval > 0 && !r.lastRendezvousRegister.IsZero() && now.Sub(r.lastRendezvousRegister) < minInterval {
		L_trace("a2a libp2p: rendezvous register skipped", "reason", "recent-register", "trigger", trigger, "nextRegisterIn", minInterval-now.Sub(r.lastRendezvousRegister))
		return false
	}
	r.lastRendezvousRegister = now
	return true
}

func (r *Runtime) reserveRendezvousQuerySlot(now time.Time, minInterval time.Duration, trigger string, bypassHealthyInterval bool) bool {
	healthy := r.connectedToBootstrapPeer()
	r.bootstrapMu.Lock()
	defer r.bootstrapMu.Unlock()

	effectiveInterval := minInterval
	if healthy && !bypassHealthyInterval && rendezvousHealthyQueryEvery > effectiveInterval {
		effectiveInterval = rendezvousHealthyQueryEvery
	}
	if effectiveInterval > 0 && !r.lastRendezvousQuery.IsZero() && now.Sub(r.lastRendezvousQuery) < effectiveInterval {
		reason := "recent-query"
		if healthy && !bypassHealthyInterval && effectiveInterval == rendezvousHealthyQueryEvery {
			reason = "healthy-bootstrap-connection"
		}
		L_trace("a2a libp2p: rendezvous query skipped", "reason", reason, "trigger", trigger, "nextQueryIn", effectiveInterval-now.Sub(r.lastRendezvousQuery))
		return false
	}
	r.lastRendezvousQuery = now
	return true
}

func (r *Runtime) prepareTargetPeer(ctx context.Context, target string, knownPeers []PeerCandidate, allowQuery bool) (*peer.AddrInfo, []string, error) {
	info, sources, err := r.resolveTargetPeer(target, knownPeers)
	if err != nil {
		return nil, nil, err
	}
	if r.hasLivePeerConnection(info.ID) {
		return info, sources, nil
	}
	if len(info.Addrs) == 0 && allowQuery {
		if r.maybeQueryRendezvous(ctx, "user-no-address", rendezvousTriggerMinInterval, true) {
			refreshed, refreshedSources, refreshErr := r.resolveTargetPeer(target, knownPeers)
			if refreshErr == nil {
				info = refreshed
				sources = refreshedSources
			}
		}
	}
	if len(info.Addrs) == 0 {
		synthesized := r.synthesizeRelayFallbackAddrs(info.ID)
		if len(synthesized) > 0 {
			info.Addrs = dedupeMultiaddrs(append(info.Addrs, synthesized...))
			sources = append(sources, "synthesized-relay")
			L_info("a2a libp2p: synthesized relay fallback candidates",
				"peerID", info.ID,
				"count", len(synthesized),
				"addrs", strings.Join(multiaddrsToStrings(synthesized), ", "),
			)
		}
	}
	sources = uniqueStrings(sources)
	if len(info.Addrs) == 0 {
		L_debug("a2a libp2p: target resolved without dial addresses", "target", target, "peerID", info.ID, "sources", strings.Join(sources, ", "))
	}
	return info, sources, nil
}

func (r *Runtime) ensureConnectedPeer(ctx context.Context, info peer.AddrInfo) error {
	if r.hasLivePeerConnection(info.ID) {
		return nil
	}
	return r.connectAddrInfo(ctx, info)
}

func (r *Runtime) openRPCStream(ctx context.Context, peerID peer.ID, kind, taskID string) (network.Stream, error) {
	if r.host == nil {
		return nil, fmt.Errorf("host not started")
	}
	streamCtx := network.WithAllowLimitedConn(ctx, "a2a-rpc-"+kind)
	started := time.Now()
	L_info("a2a libp2p: opening rpc stream", "peerID", peerID, "kind", kind, "taskID", taskID, "protocol", r.cfg.RPCProtocolID)
	stream, err := r.host.NewStream(streamCtx, peerID, protocol.ID(r.cfg.RPCProtocolID))
	if err != nil {
		L_warn("a2a libp2p: open rpc stream failed", "peerID", peerID, "kind", kind, "taskID", taskID, "elapsed", time.Since(started), "error", err)
		return nil, err
	}
	L_info("a2a libp2p: rpc stream opened", "peerID", peerID, "kind", kind, "taskID", taskID, "elapsed", time.Since(started), "protocol", stream.Protocol())
	return stream, nil
}

// nonRelayDialMultiaddrs returns multiaddrs that are not circuit-relay paths, for best-effort direct dials.
func nonRelayDialMultiaddrs(addrs []ma.Multiaddr) []ma.Multiaddr {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]ma.Multiaddr, 0, len(addrs))
	for _, a := range addrs {
		if a == nil {
			continue
		}
		if strings.Contains(a.String(), "/p2p-circuit") {
			continue
		}
		out = append(out, a)
	}
	return dedupeMultiaddrs(out)
}

// canAttemptBackgroundDirectUpgrade reports whether cooldown and inflight state allow another background attempt.
func canAttemptBackgroundDirectUpgrade(inflight bool, lastAttempt time.Time, now time.Time, cooldown time.Duration) bool {
	if inflight {
		return false
	}
	if !lastAttempt.IsZero() && now.Sub(lastAttempt) < cooldown {
		return false
	}
	return true
}

// afterOutboundPeerReady schedules a background direct upgrade when the peer is relay-only.
// Design: relay-backed traffic for the current request stays immediate; this never blocks the caller.
func (r *Runtime) afterOutboundPeerReady(trigger string, id peer.ID, resolvedAddrs []ma.Multiaddr) {
	if !r.hasLivePeerConnection(id) {
		return
	}
	r.maybeScheduleBackgroundDirectUpgrade(trigger, id, resolvedAddrs)
}

func (r *Runtime) maybeScheduleBackgroundDirectUpgrade(trigger string, target peer.ID, extra []ma.Multiaddr) {
	if r.host == nil || !r.cfg.BackgroundDirectUpgradeEnabled {
		return
	}
	if r.mode == RuntimeModeBootstrap || r.mode == RuntimeModeRelay {
		return
	}
	if !r.hasLivePeerConnection(target) {
		return
	}
	conns := r.host.Network().ConnsToPeer(target)
	summary := summarizePeerConnections(conns)
	mode := peerPathMode(summary.Total, summary.Direct, summary.Relayed)
	if mode != "relay-only" {
		L_trace("a2a libp2p: background direct upgrade skipped", "peerID", target, "trigger", trigger, "reason", "not-relay-only", "mode", mode)
		return
	}
	merged := dedupeMultiaddrs(append(append([]ma.Multiaddr(nil), extra...), r.host.Peerstore().Addrs(target)...))
	direct := nonRelayDialMultiaddrs(merged)
	if len(direct) == 0 {
		L_debug("a2a libp2p: background direct upgrade skipped", "peerID", target, "trigger", trigger, "reason", "no-direct-candidates")
		return
	}
	cooldown := time.Duration(r.cfg.DirectUpgradeCooldownSecs) * time.Second
	now := time.Now()
	r.directUpgradeMu.Lock()
	if _, inflight := r.directUpgradeInflight[target]; inflight {
		r.directUpgradeMu.Unlock()
		L_trace("a2a libp2p: background direct upgrade skipped", "peerID", target, "trigger", trigger, "reason", "inflight")
		return
	}
	last := r.directUpgradeLast[target]
	if !last.IsZero() && now.Sub(last) < cooldown {
		r.directUpgradeMu.Unlock()
		L_trace("a2a libp2p: background direct upgrade skipped", "peerID", target, "trigger", trigger, "reason", "cooldown")
		return
	}
	r.directUpgradeInflight[target] = struct{}{}
	r.directUpgradeMu.Unlock()

	timeout := time.Duration(r.cfg.DirectUpgradeTimeoutSecs) * time.Second
	L_info("a2a libp2p: background direct upgrade launched",
		"peerID", target,
		"trigger", trigger,
		"candidates", len(direct),
		"timeout", timeout,
	)
	go r.runBackgroundDirectConnect(target, direct, timeout, trigger)
}

func (r *Runtime) runBackgroundDirectConnect(target peer.ID, directAddrs []ma.Multiaddr, timeout time.Duration, trigger string) {
	defer func() {
		r.directUpgradeMu.Lock()
		delete(r.directUpgradeInflight, target)
		r.directUpgradeLast[target] = time.Now()
		r.directUpgradeMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	info := peer.AddrInfo{ID: target, Addrs: directAddrs}
	err := r.host.Connect(ctx, info)
	if err != nil {
		L_debug("a2a libp2p: background direct upgrade failed", "peerID", target, "trigger", trigger, "error", err)
		return
	}
	L_debug("a2a libp2p: background direct upgrade connect ok", "peerID", target, "trigger", trigger)
	r.logPeerPathState(target, "background-direct-upgrade")
}

func (r *Runtime) hasLivePeerConnection(peerID peer.ID) bool {
	return r.host != nil && len(r.host.Network().ConnsToPeer(peerID)) > 0
}

func (r *Runtime) connectAddrInfo(ctx context.Context, info peer.AddrInfo) error {
	if r.host == nil {
		return fmt.Errorf("host not started")
	}
	if r.hasLivePeerConnection(info.ID) {
		return nil
	}
	info.Addrs = dedupeMultiaddrs(append(info.Addrs, r.host.Peerstore().Addrs(info.ID)...))
	if len(info.Addrs) == 0 {
		return fmt.Errorf("connect %s: no addresses", info.ID)
	}
	L_info("a2a libp2p: dialing peer", "peerID", info.ID, "addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "), "preferredTransport", preferredTransportLabel(info.Addrs))
	r.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
	if err := r.host.Connect(ctx, info); err != nil {
		L_warn("a2a libp2p: dial failed", "peerID", info.ID, "preferredTransport", preferredTransportLabel(info.Addrs), "addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "), "error", err)
		return fmt.Errorf("connect %s: %w", info.ID, err)
	}
	L_info("a2a libp2p: dial established", "peerID", info.ID, "transport", preferredTransportLabel(info.Addrs), "addrs", strings.Join(multiaddrsToStrings(info.Addrs), ", "))
	r.observePeer(PeerObservation{
		PeerID:          info.ID.String(),
		Addrs:           multiaddrsToStrings(info.Addrs),
		Connected:       true,
		Relayed:         hasRelayedAddr(info.Addrs),
		LastSeen:        time.Now(),
		LastConnectedAt: time.Now(),
	})
	return nil
}

func (r *Runtime) reserveBootstrapRelays(ctx context.Context) error {
	entries, source, err := r.bootstrapPeerEntries(false)
	if err != nil {
		return err
	}
	var errs []string
	for _, relayAddr := range entries {
		info, err := parseAddrInfo(relayAddr)
		if err != nil {
			L_warn("a2a libp2p: invalid bootstrap relay candidate", "source", source, "addr", relayAddr, "error", err)
			errs = append(errs, err.Error())
			continue
		}
		if r.host != nil && info.ID == r.host.ID() {
			L_trace("a2a libp2p: skipping self relay candidate", "source", source, "peerID", info.ID)
			continue
		}
		if err := r.connectAddrInfo(ctx, *info); err != nil {
			L_warn("a2a libp2p: connect bootstrap relay failed", "source", source, "peerID", info.ID, "error", err)
			errs = append(errs, err.Error())
			continue
		}
		if _, err := relayclient.Reserve(ctx, r.host, *info); err != nil {
			L_warn("a2a libp2p: bootstrap relay reservation failed", "source", source, "peerID", info.ID, "error", err)
			errs = append(errs, err.Error())
			continue
		}
		L_info("a2a libp2p: bootstrap relay reserved", "source", source, "peerID", info.ID)
	}
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) bootstrapRelayPeerSource() autorelay.PeerSource {
	return func(ctx context.Context, numPeers int) <-chan peer.AddrInfo {
		bufSize := numPeers
		if bufSize < 1 {
			bufSize = 1
		}
		out := make(chan peer.AddrInfo, bufSize)
		go func() {
			defer close(out)
			entries, source, err := r.bootstrapPeerEntries(false)
			if err != nil {
				L_warn("a2a libp2p: bootstrap relay candidates unavailable", "error", err)
				return
			}
			sent := 0
			seen := make(map[peer.ID]struct{})
			for _, entry := range entries {
				if numPeers > 0 && sent >= numPeers {
					break
				}
				info, err := parseAddrInfo(entry)
				if err != nil {
					L_warn("a2a libp2p: invalid bootstrap relay candidate", "source", source, "addr", entry, "error", err)
					continue
				}
				if r.host != nil && info.ID == r.host.ID() {
					continue
				}
				if _, ok := seen[info.ID]; ok {
					continue
				}
				seen[info.ID] = struct{}{}
				select {
				case <-ctx.Done():
					return
				case out <- *info:
					sent++
				}
			}
			if sent > 0 {
				L_trace("a2a libp2p: bootstrap relay candidates supplied", "source", source, "count", sent)
			}
		}()
		return out
	}
}

func (r *Runtime) startMDNS() error {
	service := mdns.NewMdnsService(r.host, r.cfg.MDNSServiceName, &mdnsNotifee{runtime: r})
	if err := service.Start(); err != nil {
		L_warn("a2a libp2p: mdns start failed", "error", err)
		return err
	}
	L_info("a2a libp2p: mdns started", "service", r.cfg.MDNSServiceName)
	return nil
}

func (r *Runtime) observeDiscoveredPeer(info peer.AddrInfo) {
	if info.ID == r.host.ID() {
		return
	}
	L_trace("a2a libp2p: discovered peer candidate", "peerID", info.ID, "addrs", len(info.Addrs), "relayed", hasRelayedAddr(info.Addrs))
	r.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
	r.observePeer(PeerObservation{
		PeerID:    info.ID.String(),
		Addrs:     multiaddrsToStrings(info.Addrs),
		Connected: false,
		Relayed:   hasRelayedAddr(info.Addrs),
		LastSeen:  time.Now(),
	})
	if len(info.Addrs) == 0 {
		L_debug("a2a libp2p: discovered peer has no dial addresses yet", "peerID", info.ID)
		return
	}
	if err := r.host.Connect(context.Background(), info); err == nil {
		r.observePeer(PeerObservation{
			PeerID:          info.ID.String(),
			Addrs:           multiaddrsToStrings(info.Addrs),
			Connected:       true,
			Relayed:         hasRelayedAddr(info.Addrs),
			LastSeen:        time.Now(),
			LastConnectedAt: time.Now(),
		})
	} else {
		L_debug("a2a libp2p: connect discovered peer failed", "peerID", info.ID, "error", err)
	}
}

func (r *Runtime) observePeer(observation PeerObservation) {
	if r.callbacks.OnPeerObserved != nil {
		r.callbacks.OnPeerObserved(observation)
	}
}

func (r *Runtime) handlePeerConnected(conn network.Conn) {
	transport := connTransportLabel(conn)
	state := conn.ConnState()
	relayed := isRelayedConn(conn)
	L_info("a2a libp2p: peer connected",
		"peerID", conn.RemotePeer(),
		"connID", conn.ID(),
		"remoteAddr", conn.RemoteMultiaddr(),
		"localAddr", conn.LocalMultiaddr(),
		"path", connectionPathLabel(relayed),
		"relayed", relayed,
		"transport", transport,
		"security", state.Security,
		"muxer", state.StreamMultiplexer,
	)
	r.observePeer(PeerObservation{
		PeerID:          conn.RemotePeer().String(),
		Addrs:           multiaddrsToStrings([]ma.Multiaddr{conn.RemoteMultiaddr()}),
		Connected:       true,
		Relayed:         relayed,
		LastSeen:        time.Now(),
		LastConnectedAt: time.Now(),
	})
	if r.relayRendezvousSeedingEnabled() {
		L_info("a2a libp2p: infra relay rendezvous seed eligible",
			"peerID", conn.RemotePeer(),
			"namespace", r.normalizeRendezvousNamespace(r.cfg.RendezvousNamespace),
			"reason", "peer-connected-to-infra",
			"path", connectionPathLabel(relayed),
			"remoteAddr", conn.RemoteMultiaddr(),
		)
		r.seedInfraRelayRendezvousEntry(conn.RemotePeer())
	} else if r.rendezvousServerEnabled() {
		L_info("a2a libp2p: infra relay rendezvous seed skipped",
			"peerID", conn.RemotePeer(),
			"namespace", r.normalizeRendezvousNamespace(r.cfg.RendezvousNamespace),
			"reason", "relay-seeding-disabled",
			"path", connectionPathLabel(relayed),
			"remoteAddr", conn.RemoteMultiaddr(),
		)
	}
	r.logPeerPathState(conn.RemotePeer(), "conn-open")
}

func (r *Runtime) handlePeerDisconnected(conn network.Conn) {
	transport := connTransportLabel(conn)
	state := conn.ConnState()
	relayed := isRelayedConn(conn)
	L_info("a2a libp2p: peer disconnected",
		"peerID", conn.RemotePeer(),
		"connID", conn.ID(),
		"remoteAddr", conn.RemoteMultiaddr(),
		"localAddr", conn.LocalMultiaddr(),
		"path", connectionPathLabel(relayed),
		"relayed", relayed,
		"transport", transport,
		"security", state.Security,
		"muxer", state.StreamMultiplexer,
	)
	r.observePeer(PeerObservation{
		PeerID:           conn.RemotePeer().String(),
		Addrs:            multiaddrsToStrings([]ma.Multiaddr{conn.RemoteMultiaddr()}),
		Connected:        false,
		Relayed:          relayed,
		LastSeen:         time.Now(),
		LastDisconnectAt: time.Now(),
	})
	if !r.hasLivePeerConnection(conn.RemotePeer()) {
		if removed := r.removeInfraOwnedRendezvousEntry(r.cfg.RendezvousNamespace, conn.RemotePeer().String()); removed {
			L_info("a2a libp2p: infra provisional rendezvous entry removed",
				"peerID", conn.RemotePeer(),
				"namespace", r.normalizeRendezvousNamespace(r.cfg.RendezvousNamespace),
			)
		}
	}
	r.logPeerPathState(conn.RemotePeer(), "conn-close")
}

func (r *Runtime) registerRendezvous(ctx context.Context) {
	advertised := r.AdvertisedAddrs()
	if len(advertised) == 0 {
		L_info("a2a libp2p: rendezvous register proceeding with empty addresses", "namespace", r.cfg.RendezvousNamespace)
	} else {
		L_info("a2a libp2p: rendezvous register publishing addresses",
			"namespace", r.cfg.RendezvousNamespace,
			"count", len(advertised),
			"reachability", r.Reachability(),
			"addrs", strings.Join(advertised, ", "),
		)
	}
	payload := rendezvousRequest{
		Action:    "register",
		Namespace: r.cfg.RendezvousNamespace,
		PeerID:    r.host.ID().String(),
		Addrs:     advertised,
	}
	entries, _, err := r.bootstrapPeerEntries(false)
	if err != nil {
		L_warn("a2a libp2p: rendezvous bootstrap resolution failed", "error", err)
		return
	}
	successes := 0
	for _, addr := range entries {
		info, err := parseAddrInfo(addr)
		if err != nil {
			continue
		}
		L_trace("a2a libp2p: rendezvous register attempt", "peerID", info.ID, "namespace", payload.Namespace)
		if err := r.sendRendezvousRequest(ctx, *info, payload, nil); err != nil {
			L_warn("a2a libp2p: rendezvous register failed", "peerID", info.ID, "error", err)
			continue
		}
		successes++
	}
	L_info("a2a libp2p: rendezvous register pass complete", "namespace", payload.Namespace, "targets", len(entries), "successful", successes)
}

func (r *Runtime) queryRendezvous(ctx context.Context) {
	payload := rendezvousRequest{
		Action:    "list",
		Namespace: r.cfg.RendezvousNamespace,
		PeerID:    r.host.ID().String(),
	}
	entries, _, err := r.bootstrapPeerEntries(false)
	if err != nil {
		L_warn("a2a libp2p: rendezvous bootstrap resolution failed", "error", err)
		return
	}
	successes := 0
	returned := 0
	for _, addr := range entries {
		info, err := parseAddrInfo(addr)
		if err != nil {
			continue
		}
		L_trace("a2a libp2p: rendezvous query attempt", "peerID", info.ID, "namespace", payload.Namespace)
		var resp rendezvousResponse
		if err := r.sendRendezvousRequest(ctx, *info, payload, &resp); err != nil {
			L_warn("a2a libp2p: rendezvous query failed", "peerID", info.ID, "error", err)
			continue
		}
		successes++
		returned += len(resp.Entries)
		for _, entry := range resp.Entries {
			if entry.PeerID == r.host.ID().String() {
				continue
			}
			addrInfo, err := addrInfoFromStrings(entry.PeerID, entry.Addrs)
			if err != nil {
				L_warn("a2a libp2p: rendezvous peer parse failed", "peerID", entry.PeerID, "error", err)
				continue
			}
			r.observeDiscoveredPeer(*addrInfo)
		}
	}
	L_info("a2a libp2p: rendezvous query pass complete", "namespace", payload.Namespace, "targets", len(entries), "successful", successes, "entries", returned)
}

func (r *Runtime) sendRendezvousRequest(ctx context.Context, info peer.AddrInfo, payload rendezvousRequest, resp *rendezvousResponse) error {
	L_trace("a2a libp2p: opening rendezvous stream", "peerID", info.ID, "action", payload.Action, "namespace", payload.Namespace)
	if err := r.connectAddrInfo(ctx, info); err != nil {
		return err
	}
	stream, err := r.host.NewStream(ctx, info.ID, protocol.ID(r.cfg.RendezvousProtocolID))
	if err != nil {
		return fmt.Errorf("open rendezvous stream: %w", err)
	}
	defer stream.Close()

	writer := bufio.NewWriter(stream)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		return fmt.Errorf("encode rendezvous request: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush rendezvous request: %w", err)
	}
	if resp == nil {
		return nil
	}
	if err := json.NewDecoder(stream).Decode(resp); err != nil {
		return fmt.Errorf("decode rendezvous response: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf(resp.Error)
	}
	return nil
}

func (r *Runtime) handleRendezvousStream(stream network.Stream) {
	defer stream.Close()
	var req rendezvousRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		L_warn("a2a libp2p: rendezvous decode failed", "error", err)
		return
	}
	L_trace("a2a libp2p: rendezvous request received", "action", req.Action, "namespace", req.Namespace, "peerID", req.PeerID)
	resp := rendezvousResponse{}
	switch req.Action {
	case "register":
		namespace, effectiveAddrs, dropped, reasons, previousSource, preservedInfra := r.registerSelfRendezvousEntry(req.Namespace, req.PeerID, req.Addrs)
		if dropped > 0 {
			L_info("a2a libp2p: rendezvous registration sanitized",
				"peerID", req.PeerID,
				"namespace", namespace,
				"kept", len(effectiveAddrs),
				"dropped", dropped,
				"reasons", formatCounts(reasons),
				"mode", r.cfg.RendezvousAdmissionMode,
			)
		}
		if preservedInfra {
			L_info("a2a libp2p: self registration preserved infra provisional rendezvous entry",
				"peerID", req.PeerID,
				"namespace", namespace,
				"addrs", len(effectiveAddrs),
			)
		} else if previousSource == rendezvousEntrySourceInfra {
			L_info("a2a libp2p: self registration replaced infra provisional rendezvous entry",
				"peerID", req.PeerID,
				"namespace", namespace,
				"addrs", len(effectiveAddrs),
			)
		}
		L_trace("a2a libp2p: rendezvous peer registered", "peerID", req.PeerID, "namespace", namespace, "addrs", len(effectiveAddrs))
	case "list":
		resp.Entries = r.listRendezvous(req.Namespace, req.PeerID)
		L_info("a2a libp2p: rendezvous list served", "requester", req.PeerID, "namespace", req.Namespace, "entries", len(resp.Entries))
	default:
		resp.Error = "unknown rendezvous action"
	}
	if err := json.NewEncoder(stream).Encode(resp); err != nil && err != io.EOF {
		L_warn("a2a libp2p: rendezvous encode failed", "error", err)
	}
}

func (r *Runtime) listRendezvous(namespace, requester string) []rendezvousEntry {
	r.rendezvousMu.Lock()
	defer r.rendezvousMu.Unlock()
	namespace = r.normalizeRendezvousNamespace(namespace)
	now := time.Now()
	bucket := r.rendezvousData[namespace]
	if bucket == nil {
		return nil
	}
	out := make([]rendezvousEntry, 0, len(bucket))
	for peerID, entry := range bucket {
		if now.After(entry.ExpiresAt) {
			delete(bucket, peerID)
			continue
		}
		if peerID == requester {
			continue
		}
		sanitized, _, _ := r.sanitizeRemoteRegistrationAddrs(entry.PeerID, entry.Addrs)
		out = append(out, rendezvousEntry{
			PeerID:    entry.PeerID,
			Addrs:     sanitized,
			ExpiresAt: entry.ExpiresAt,
		})
	}
	return out
}

func (r *Runtime) normalizeRendezvousNamespace(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return r.cfg.RendezvousNamespace
	}
	return namespace
}

func (r *Runtime) rendezvousServerEnabled() bool {
	return r.mode == RuntimeModeBootstrap || r.mode == RuntimeModeBoth
}

func (r *Runtime) relayRendezvousSeedingEnabled() bool {
	return r.rendezvousServerEnabled() && (r.mode == RuntimeModeBoth || r.cfg.RelayServerEnabled || r.relay != nil)
}

func (r *Runtime) rendezvousBucketLocked(namespace string) map[string]rendezvousEntry {
	namespace = r.normalizeRendezvousNamespace(namespace)
	bucket := r.rendezvousData[namespace]
	if bucket == nil {
		bucket = make(map[string]rendezvousEntry)
		r.rendezvousData[namespace] = bucket
	}
	return bucket
}

func (r *Runtime) putRendezvousEntryLocked(namespace string, entry rendezvousEntry) (rendezvousEntry, bool) {
	bucket := r.rendezvousBucketLocked(namespace)
	prev, ok := bucket[entry.PeerID]
	bucket[entry.PeerID] = entry
	return prev, ok
}

func (r *Runtime) registerSelfRendezvousEntry(namespace, peerID string, addrs []string) (string, []string, int, map[string]int, string, bool) {
	namespace = r.normalizeRendezvousNamespace(namespace)
	sanitized, dropped, reasons := r.sanitizeRemoteRegistrationAddrs(peerID, addrs)
	r.rendezvousMu.Lock()
	prev, hadPrev := r.rendezvousBucketLocked(namespace)[peerID]
	if hadPrev && prev.Source == rendezvousEntrySourceInfra && len(prev.Addrs) > 0 && len(sanitized) == 0 {
		prev.ExpiresAt = time.Now().Add(2 * time.Minute)
		r.rendezvousBucketLocked(namespace)[peerID] = prev
		r.rendezvousMu.Unlock()
		return namespace, cloneStrings(prev.Addrs), dropped, reasons, prev.Source, true
	}
	entry := rendezvousEntry{
		PeerID:    peerID,
		Addrs:     sanitized,
		ExpiresAt: time.Now().Add(2 * time.Minute),
		Source:    rendezvousEntrySourceSelf,
	}
	prev, hadPrev = r.putRendezvousEntryLocked(namespace, entry)
	r.rendezvousMu.Unlock()
	if hadPrev {
		return namespace, sanitized, dropped, reasons, prev.Source, false
	}
	return namespace, sanitized, dropped, reasons, "", false
}

func (r *Runtime) localRelayCircuitAddrs(target peer.ID) []string {
	if r.host == nil || target == "" {
		return nil
	}
	relayBaseAddrs := dedupeMultiaddrs(r.host.Addrs())
	out := make([]ma.Multiaddr, 0, len(relayBaseAddrs))
	for _, relayBase := range relayBaseAddrs {
		if relayBase == nil || strings.Contains(relayBase.String(), "/p2p-circuit") {
			continue
		}
		out = append(out, buildRelayCircuitAddr(relayBase, r.host.ID(), target))
	}
	return multiaddrsToStrings(dedupeMultiaddrs(out))
}

func (r *Runtime) seedInfraRelayRendezvousEntry(peerID peer.ID) {
	if !r.rendezvousServerEnabled() {
		L_debug("a2a libp2p: infra relay rendezvous seed skipped", "peerID", peerID, "reason", "rendezvous-server-disabled", "mode", r.mode)
		return
	}
	if !r.relayRendezvousSeedingEnabled() {
		L_debug("a2a libp2p: infra relay rendezvous seed skipped", "peerID", peerID, "reason", "relay-seeding-disabled", "mode", r.mode, "relayServer", r.cfg.RelayServerEnabled, "relayReady", r.relay != nil)
		return
	}
	if r.host == nil {
		L_debug("a2a libp2p: infra relay rendezvous seed skipped", "peerID", peerID, "reason", "host-not-started")
		return
	}
	if peerID == "" {
		L_debug("a2a libp2p: infra relay rendezvous seed skipped", "reason", "empty-peer-id")
		return
	}
	addrs := r.localRelayCircuitAddrs(peerID)
	if len(addrs) == 0 {
		L_debug("a2a libp2p: infra relay rendezvous seed skipped", "peerID", peerID, "reason", "no-relay-addrs")
		return
	}
	namespace := r.normalizeRendezvousNamespace(r.cfg.RendezvousNamespace)
	L_info("a2a libp2p: infra relay rendezvous seed attempting",
		"peerID", peerID,
		"namespace", namespace,
		"candidateAddrs", strings.Join(addrs, ", "),
	)
	sanitized, dropped, reasons := r.sanitizeRemoteRegistrationAddrs(peerID.String(), addrs)
	entry := rendezvousEntry{
		PeerID:    peerID.String(),
		Addrs:     sanitized,
		ExpiresAt: time.Now().Add(2 * time.Minute),
		Source:    rendezvousEntrySourceInfra,
	}
	r.rendezvousMu.Lock()
	bucket := r.rendezvousBucketLocked(namespace)
	if existing, ok := bucket[peerID.String()]; ok && existing.Source == rendezvousEntrySourceSelf {
		r.rendezvousMu.Unlock()
		L_info("a2a libp2p: infra relay rendezvous seed skipped", "peerID", peerID, "namespace", namespace, "reason", "self-owned-entry")
		return
	}
	prev, hadPrev := r.putRendezvousEntryLocked(namespace, entry)
	r.rendezvousMu.Unlock()
	if dropped > 0 {
		L_info("a2a libp2p: infra relay rendezvous seed sanitized",
			"peerID", peerID,
			"namespace", namespace,
			"kept", len(sanitized),
			"dropped", dropped,
			"reasons", formatCounts(reasons),
		)
	}
	action := "created"
	if hadPrev {
		action = "refreshed"
	}
	L_info("a2a libp2p: infra relay rendezvous seeded",
		"peerID", peerID,
		"namespace", namespace,
		"action", action,
		"previousSource", prev.Source,
		"addrs", strings.Join(sanitized, ", "),
	)
}

func (r *Runtime) removeInfraOwnedRendezvousEntry(namespace, peerID string) bool {
	namespace = r.normalizeRendezvousNamespace(namespace)
	r.rendezvousMu.Lock()
	defer r.rendezvousMu.Unlock()
	bucket := r.rendezvousData[namespace]
	if bucket == nil {
		return false
	}
	entry, ok := bucket[peerID]
	if !ok || entry.Source != rendezvousEntrySourceInfra {
		return false
	}
	delete(bucket, peerID)
	if len(bucket) == 0 {
		delete(r.rendezvousData, namespace)
	}
	return true
}

func (r *Runtime) resolveTargetPeer(target string, knownPeers []PeerCandidate) (*peer.AddrInfo, []string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil, fmt.Errorf("peer target is required")
	}
	var (
		id            peer.ID
		candidateAddrs []string
		sources       []string
	)
	if decoded, err := peer.Decode(target); err == nil {
		id = decoded
		sources = append(sources, "peer-id")
	} else {
		found := false
		for _, candidate := range knownPeers {
			if candidate.PeerID != target && candidate.Alias != target {
				continue
			}
			decodedID, decodeErr := peer.Decode(candidate.PeerID)
			if decodeErr != nil {
				return nil, nil, decodeErr
			}
			id = decodedID
			candidateAddrs = append(candidateAddrs, candidate.Addrs...)
			if candidate.Alias == target && candidate.Alias != "" {
				sources = append(sources, "alias")
			}
			sources = append(sources, "known-peers")
			found = true
			break
		}
		if !found {
			return nil, nil, fmt.Errorf("peer %s not found", target)
		}
	}
	candidateMultiaddrs, invalid := parseMultiaddrsLenient(candidateAddrs)
	if invalid > 0 {
		L_warn("a2a libp2p: target candidate addresses ignored", "peerID", id, "invalid", invalid)
	}
	peerstoreAddrs := []ma.Multiaddr(nil)
	if r.host != nil {
		peerstoreAddrs = r.host.Peerstore().Addrs(id)
	}
	if len(peerstoreAddrs) > 0 {
		sources = append(sources, "peerstore")
	}
	return &peer.AddrInfo{
		ID:    id,
		Addrs: dedupeMultiaddrs(append(candidateMultiaddrs, peerstoreAddrs...)),
	}, uniqueStrings(sources), nil
}

func parseMultiaddrs(raw []string) ([]ma.Multiaddr, error) {
	out := make([]ma.Multiaddr, 0, len(raw))
	for _, item := range raw {
		if strings.TrimSpace(item) == "" {
			continue
		}
		addr, err := ma.NewMultiaddr(strings.TrimSpace(item))
		if err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	return out, nil
}

func parseAdvertiseMultiaddrs(raw []string) ([]ma.Multiaddr, error) {
	addrs, err := parseMultiaddrs(raw)
	if err != nil {
		return nil, err
	}
	out := make([]ma.Multiaddr, 0, len(addrs))
	for _, addr := range addrs {
		transport, _ := peer.SplitAddr(addr)
		if transport == nil {
			continue
		}
		out = append(out, transport)
	}
	return dedupeMultiaddrs(out), nil
}

func parseAddrInfo(raw string) (*peer.AddrInfo, error) {
	addr, err := ma.NewMultiaddr(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return peer.AddrInfoFromP2pAddr(addr)
}

func addrInfoFromStrings(peerID string, raw []string) (*peer.AddrInfo, error) {
	id, err := peer.Decode(peerID)
	if err != nil {
		return nil, err
	}
	addrs, err := parseMultiaddrs(raw)
	if err != nil {
		return nil, err
	}
	return &peer.AddrInfo{ID: id, Addrs: addrs}, nil
}

func parseMultiaddrsLenient(raw []string) ([]ma.Multiaddr, int) {
	out := make([]ma.Multiaddr, 0, len(raw))
	invalid := 0
	for _, item := range raw {
		if strings.TrimSpace(item) == "" {
			continue
		}
		addr, err := ma.NewMultiaddr(strings.TrimSpace(item))
		if err != nil {
			invalid++
			continue
		}
		out = append(out, addr)
	}
	return out, invalid
}

func (r *Runtime) synthesizeRelayFallbackAddrs(target peer.ID) []ma.Multiaddr {
	if r.host == nil || target == "" || target == r.host.ID() {
		return nil
	}
	r.bootstrapMu.RLock()
	bootstrapEntries := cloneStrings(r.bootstrapEntries)
	bootstrapPeerIDs := make([]peer.ID, 0, len(r.bootstrapPeerIDs))
	for peerID := range r.bootstrapPeerIDs {
		bootstrapPeerIDs = append(bootstrapPeerIDs, peerID)
	}
	r.bootstrapMu.RUnlock()

	baseByPeer := make(map[peer.ID][]ma.Multiaddr)
	for _, peerID := range bootstrapPeerIDs {
		if peerID == target || !r.hasLivePeerConnection(peerID) {
			continue
		}
		baseByPeer[peerID] = append(baseByPeer[peerID], r.host.Peerstore().Addrs(peerID)...)
	}
	for _, entry := range bootstrapEntries {
		info, err := parseAddrInfo(entry)
		if err != nil || info == nil || info.ID == target || !r.hasLivePeerConnection(info.ID) {
			continue
		}
		baseByPeer[info.ID] = append(baseByPeer[info.ID], info.Addrs...)
	}

	out := make([]ma.Multiaddr, 0)
	for relayPeerID, baseAddrs := range baseByPeer {
		for _, baseAddr := range dedupeMultiaddrs(baseAddrs) {
			if baseAddr == nil || strings.Contains(baseAddr.String(), "/p2p-circuit") {
				continue
			}
			out = append(out, buildRelayCircuitAddr(baseAddr, relayPeerID, target))
		}
	}
	return dedupeMultiaddrs(out)
}

func buildRelayCircuitAddr(relayBase ma.Multiaddr, relayPeerID, target peer.ID) ma.Multiaddr {
	return relayBase.
		Encapsulate(ma.StringCast("/p2p/" + relayPeerID.String())).
		Encapsulate(ma.StringCast("/p2p-circuit")).
		Encapsulate(ma.StringCast("/p2p/" + target.String()))
}

func normalizeBootstrapEntries(entries []string) []string {
	type candidate struct {
		entry string
		score int
	}
	byPeer := make(map[peer.ID]candidate)
	orderedPeerIDs := make([]peer.ID, 0)
	for _, entry := range entries {
		info, err := parseAddrInfo(entry)
		if err != nil || info == nil || len(info.Addrs) == 0 {
			continue
		}
		score := bootstrapCandidateScore(info.Addrs[0])
		current, exists := byPeer[info.ID]
		if !exists {
			orderedPeerIDs = append(orderedPeerIDs, info.ID)
			byPeer[info.ID] = candidate{entry: entry, score: score}
			continue
		}
		if score < current.score {
			byPeer[info.ID] = candidate{entry: entry, score: score}
		}
	}
	out := make([]string, 0, len(orderedPeerIDs))
	for _, peerID := range orderedPeerIDs {
		out = append(out, byPeer[peerID].entry)
	}
	return out
}

func bootstrapCandidateScore(addr ma.Multiaddr) int {
	if addr == nil {
		return 1 << 30
	}
	switch {
	case hasProtocol(addr, ma.P_QUIC_V1), hasProtocol(addr, ma.P_QUIC):
		return 0
	case hasProtocol(addr, ma.P_TCP):
		return 1
	default:
		return 2
	}
}

func preferredTransportLabel(addrs []ma.Multiaddr) string {
	best := "other"
	bestScore := 1 << 30
	for _, addr := range addrs {
		score := bootstrapCandidateScore(addr)
		if score >= bestScore {
			continue
		}
		bestScore = score
		switch score {
		case 0:
			best = "quic"
		case 1:
			best = "tcp"
		default:
			best = "other"
		}
	}
	return best
}

func protocolIDsToStrings(ids []protocol.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func hasProtocol(addr ma.Multiaddr, code int) bool {
	if addr == nil {
		return false
	}
	found := false
	ma.ForEach(addr, func(c ma.Component) bool {
		if c.Protocol().Code == code {
			found = true
			return false
		}
		return true
	})
	return found
}

func multiaddrsToStrings(addrs []ma.Multiaddr) []string {
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.String())
	}
	sort.Strings(out)
	return out
}

func multiaddrsWithPeerIDToStrings(addrs []ma.Multiaddr, peerID peer.ID) []string {
	out := make([]string, 0, len(addrs))
	for _, addr := range dedupeMultiaddrs(addrs) {
		out = append(out, addr.Encapsulate(ma.StringCast("/p2p/"+peerID.String())).String())
	}
	sort.Strings(out)
	return out
}

func dedupeMultiaddrs(addrs []ma.Multiaddr) []ma.Multiaddr {
	if len(addrs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(addrs))
	out := make([]ma.Multiaddr, 0, len(addrs))
	for _, addr := range addrs {
		if addr == nil {
			continue
		}
		key := addr.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func (r *Runtime) addrFactory(explicit []ma.Multiaddr) func([]ma.Multiaddr) []ma.Multiaddr {
	explicit = dedupeMultiaddrs(explicit)
	return func(addrs []ma.Multiaddr) []ma.Multiaddr {
		source := addrs
		sourceType := "derived"
		if len(explicit) > 0 {
			source = explicit
			sourceType = "explicit"
		}
		filtered := make([]ma.Multiaddr, 0, len(source))
		dropped := 0
		dropReasons := map[string]int{}
		for _, addr := range source {
			if !r.cfg.AnnouncePrivateAddrs && shouldFilterAdvertisedAddr(addr) {
				reason := advertisedAddrFilterReason(addr)
				dropped++
				dropReasons[reason]++
				continue
			}
			filtered = append(filtered, addr)
		}
		filtered = dedupeMultiaddrs(filtered)
		r.logAdvertisedAddressEvaluation(sourceType, source, filtered, dropped, dropReasons)
		return filtered
	}
}

func (r *Runtime) logAdvertisedAddressEvaluation(sourceType string, source, filtered []ma.Multiaddr, dropped int, dropReasons map[string]int) {
	signature := fmt.Sprintf("source=%s input=%d kept=%d dropped=%d announcePrivate=%t reasons=%s keptAddrs=%s",
		sourceType,
		len(source),
		len(filtered),
		dropped,
		r.cfg.AnnouncePrivateAddrs,
		formatCounts(dropReasons),
		strings.Join(multiaddrsToStrings(filtered), ","),
	)

	r.advertiseEvalMu.Lock()
	changed := signature != r.lastAdvertiseEvalSig
	if changed {
		r.lastAdvertiseEvalSig = signature
	}
	r.advertiseEvalMu.Unlock()

	if dropped == 0 {
		if !changed {
			return
		}
		L_debug("a2a libp2p: advertised address set evaluated",
			"source", sourceType,
			"input", len(source),
			"kept", len(filtered),
			"dropped", dropped,
			"announcePrivate", r.cfg.AnnouncePrivateAddrs,
		)
		return
	}

	if !changed {
		return
	}
	for reason, count := range dropReasons {
		L_trace("a2a libp2p: advertised addresses filtered", "reason", reason, "count", count, "source", sourceType)
	}
	L_debug("a2a libp2p: advertised address set evaluated",
		"source", sourceType,
		"input", len(source),
		"kept", len(filtered),
		"dropped", dropped,
		"reasons", formatCounts(dropReasons),
		"announcePrivate", r.cfg.AnnouncePrivateAddrs,
	)
}

func addrActionLabel(action event.AddrAction) string {
	switch action {
	case event.Added:
		return "added"
	case event.Maintained:
		return "maintained"
	case event.Removed:
		return "removed"
	default:
		return "unknown"
	}
}

func (r *Runtime) sanitizeRemoteRegistrationAddrs(peerID string, raw []string) ([]string, int, map[string]int) {
	reasons := map[string]int{}
	if strings.TrimSpace(peerID) == "" {
		peerID = "unknown"
	}
	parsed := make([]ma.Multiaddr, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		addr, err := ma.NewMultiaddr(item)
		if err != nil {
			reasons["invalid"]++
			L_warn("a2a libp2p: rendezvous registration address ignored", "peerID", peerID, "addr", item, "error", err)
			continue
		}
		transport, addrPeerID := peer.SplitAddr(addr)
		if transport == nil {
			reasons["missing-transport"]++
			continue
		}
		if addrPeerID != "" && addrPeerID.String() != peerID {
			L_warn("a2a libp2p: rendezvous registration peer mismatch", "peerID", peerID, "addrPeerID", addrPeerID.String(), "addr", item)
		}
		if reason := rendezvousAdmissionFilterReason(r.cfg.RendezvousAdmissionMode, transport); reason != "" {
			reasons[reason]++
			continue
		}
		parsed = append(parsed, transport.Encapsulate(ma.StringCast("/p2p/"+peerID)))
	}
	sanitized := multiaddrsToStrings(dedupeMultiaddrs(parsed))
	dropped := 0
	for _, count := range reasons {
		dropped += count
	}
	return sanitized, dropped, reasons
}

func rendezvousAdmissionFilterReason(mode string, addr ma.Multiaddr) string {
	reason := advertisedAddrFilterReason(addr)
	if reason == "" {
		return ""
	}
	switch normalizedRendezvousAdmissionMode(mode) {
	case "private-network":
		switch reason {
		case "private", "carrier-grade-nat":
			return ""
		}
	}
	return reason
}

func normalizedRendezvousAdmissionMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "public-safe"
	}
	return mode
}

func formatCounts(values map[string]int) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	return strings.Join(parts, ", ")
}

func shouldFilterAdvertisedAddr(addr ma.Multiaddr) bool {
	return advertisedAddrFilterReason(addr) != ""
}

func advertisedAddrFilterReason(addr ma.Multiaddr) string {
	if addr == nil {
		return "nil"
	}
	text := addr.String()
	if strings.Contains(text, "/p2p-circuit") {
		return ""
	}
	ip := ipFromMultiaddr(addr)
	if ip == nil {
		return ""
	}
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsUnspecified():
		return "unspecified"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return "link-local"
	case ip.IsPrivate():
		return "private"
	case isCarrierGradeNAT(ip):
		return "carrier-grade-nat"
	default:
		return ""
	}
}

func ipFromMultiaddr(addr ma.Multiaddr) net.IP {
	if value, err := addr.ValueForProtocol(ma.P_IP4); err == nil {
		return net.ParseIP(value)
	}
	if value, err := addr.ValueForProtocol(ma.P_IP6); err == nil {
		return net.ParseIP(value)
	}
	return nil
}

func isCarrierGradeNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

func hasRelayedAddr(addrs []ma.Multiaddr) bool {
	for _, addr := range addrs {
		if strings.Contains(addr.String(), "p2p-circuit") {
			return true
		}
	}
	return false
}

func isRelayedConn(conn network.Conn) bool {
	if conn == nil {
		return false
	}
	if conn.Stat().Limited {
		return true
	}
	if conn.RemoteMultiaddr() != nil && strings.Contains(conn.RemoteMultiaddr().String(), "/p2p-circuit") {
		return true
	}
	if conn.LocalMultiaddr() != nil && strings.Contains(conn.LocalMultiaddr().String(), "/p2p-circuit") {
		return true
	}
	return false
}

func connectionPathLabel(relayed bool) string {
	if relayed {
		return "relayed"
	}
	return "direct"
}

func connTransportLabel(conn network.Conn) string {
	if conn == nil {
		return "unknown"
	}
	transport := strings.TrimSpace(conn.ConnState().Transport)
	if transport != "" {
		return transport
	}
	switch {
	case hasProtocol(conn.RemoteMultiaddr(), ma.P_QUIC_V1), hasProtocol(conn.RemoteMultiaddr(), ma.P_QUIC):
		return "quic"
	case hasProtocol(conn.RemoteMultiaddr(), ma.P_TCP):
		return "tcp"
	default:
		return "other"
	}
}

func peerPathMode(total, direct, relayed int) string {
	switch {
	case total == 0:
		return "disconnected"
	case direct > 0 && relayed > 0:
		return "mixed"
	case direct > 0:
		return "direct-only"
	case relayed > 0:
		return "relay-only"
	default:
		return "unknown"
	}
}

func summarizePeerConnections(conns []network.Conn) peerConnectionSummary {
	summary := peerConnectionSummary{
		Transports: make(map[string]int),
	}
	if len(conns) == 0 {
		return summary
	}
	addrs := make([]string, 0, len(conns))
	for _, conn := range conns {
		if conn == nil {
			continue
		}
		summary.Total++
		if isRelayedConn(conn) {
			summary.Relayed++
		} else {
			summary.Direct++
		}
		summary.Transports[connTransportLabel(conn)]++
		if conn.RemoteMultiaddr() != nil {
			addrs = append(addrs, conn.RemoteMultiaddr().String())
		}
	}
	sort.Strings(addrs)
	summary.Addrs = addrs
	return summary
}

func (r *Runtime) logPeerPathState(peerID peer.ID, trigger string) {
	if r.host == nil || peerID == "" {
		return
	}
	summary := summarizePeerConnections(r.host.Network().ConnsToPeer(peerID))
	mode := peerPathMode(summary.Total, summary.Direct, summary.Relayed)
	signature := fmt.Sprintf("mode=%s total=%d direct=%d relayed=%d transports=%s addrs=%s",
		mode,
		summary.Total,
		summary.Direct,
		summary.Relayed,
		formatCounts(summary.Transports),
		strings.Join(summary.Addrs, ","),
	)

	r.stateMu.Lock()
	prev := r.peerPathState[peerID.String()]
	changed := prev.Signature != signature
	if changed {
		r.peerPathState[peerID.String()] = peerPathSnapshot{
			Mode:      mode,
			Signature: signature,
		}
	}
	r.stateMu.Unlock()

	if !changed {
		return
	}
	L_info("a2a libp2p: peer path state changed",
		"peerID", peerID,
		"trigger", trigger,
		"mode", mode,
		"total", summary.Total,
		"direct", summary.Direct,
		"relayed", summary.Relayed,
		"transports", formatCounts(summary.Transports),
		"addrs", strings.Join(summary.Addrs, ", "),
	)
	switch {
	case prev.Mode == "relay-only" && summary.Direct > 0:
		L_info("a2a libp2p: peer direct upgrade observed", "peerID", peerID, "trigger", trigger, "mode", mode)
	case prev.Mode != "" && (prev.Mode == "direct-only" || prev.Mode == "mixed") && mode == "relay-only":
		L_warn("a2a libp2p: peer direct path lost", "peerID", peerID, "trigger", trigger, "previousMode", prev.Mode, "mode", mode)
	}
}

func (r *Runtime) logHolePunchFailureContext(peerID peer.ID, trigger string) {
	if r.host == nil || peerID == "" {
		return
	}
	summary := summarizePeerConnections(r.host.Network().ConnsToPeer(peerID))
	mode := peerPathMode(summary.Total, summary.Direct, summary.Relayed)
	peerstoreAddrs := multiaddrsToStrings(r.host.Peerstore().Addrs(peerID))
	L_warn("a2a libp2p: hole punch failure context",
		"peerID", peerID,
		"trigger", trigger,
		"mode", mode,
		"total", summary.Total,
		"direct", summary.Direct,
		"relayed", summary.Relayed,
		"transports", formatCounts(summary.Transports),
		"connAddrs", strings.Join(summary.Addrs, ", "),
		"peerstoreAddrs", strings.Join(peerstoreAddrs, ", "),
	)
}

func (r *Runtime) Trace(evt *holepunch.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case holepunch.DirectDialEvtT:
		directDial, _ := evt.Evt.(*holepunch.DirectDialEvt)
		if directDial == nil {
			L_warn("a2a libp2p: hole punch direct dial event missing payload", "peerID", evt.Remote)
			return
		}
		if directDial.Success {
			L_info("a2a libp2p: hole punch direct dial succeeded", "peerID", evt.Remote, "elapsed", directDial.EllapsedTime)
		} else {
			L_warn("a2a libp2p: hole punch direct dial failed", "peerID", evt.Remote, "elapsed", directDial.EllapsedTime, "error", directDial.Error)
		}
		r.logPeerPathState(evt.Remote, "holepunch-direct-dial")
	case holepunch.ProtocolErrorEvtT:
		protocolErr, _ := evt.Evt.(*holepunch.ProtocolErrorEvt)
		if protocolErr == nil {
			L_warn("a2a libp2p: hole punch protocol error missing payload", "peerID", evt.Remote)
			return
		}
		L_warn("a2a libp2p: hole punch protocol error", "peerID", evt.Remote, "error", protocolErr.Error)
	case holepunch.StartHolePunchEvtT:
		start, _ := evt.Evt.(*holepunch.StartHolePunchEvt)
		if start == nil {
			L_warn("a2a libp2p: hole punch start missing payload", "peerID", evt.Remote)
			return
		}
		L_info("a2a libp2p: hole punch started", "peerID", evt.Remote, "rtt", start.RTT, "remoteAddrs", strings.Join(start.RemoteAddrs, ", "))
	case holepunch.EndHolePunchEvtT:
		end, _ := evt.Evt.(*holepunch.EndHolePunchEvt)
		if end == nil {
			L_warn("a2a libp2p: hole punch end missing payload", "peerID", evt.Remote)
			return
		}
		if end.Success {
			L_info("a2a libp2p: hole punch succeeded", "peerID", evt.Remote, "elapsed", end.EllapsedTime)
		} else {
			L_warn("a2a libp2p: hole punch failed", "peerID", evt.Remote, "elapsed", end.EllapsedTime, "error", end.Error)
			r.logHolePunchFailureContext(evt.Remote, "holepunch-end")
		}
		r.logPeerPathState(evt.Remote, "holepunch-end")
	case holepunch.HolePunchAttemptEvtT:
		attempt, _ := evt.Evt.(*holepunch.HolePunchAttemptEvt)
		if attempt == nil {
			L_warn("a2a libp2p: hole punch attempt missing payload", "peerID", evt.Remote)
			return
		}
		L_trace("a2a libp2p: hole punch attempt", "peerID", evt.Remote, "attempt", attempt.Attempt)
	default:
		L_debug("a2a libp2p: hole punch event", "peerID", evt.Remote, "type", evt.Type)
	}
}

func loadOrCreateIdentity(keyFile string) (crypto.PrivKey, error) {
	expanded, err := paths.ExpandTilde(keyFile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(expanded)
	if err == nil {
		return crypto.UnmarshalPrivateKey(data)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(expanded), 0o750); err != nil {
		return nil, err
	}
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(expanded, encoded, 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

type mdnsNotifee struct{ runtime *Runtime }

func (m *mdnsNotifee) HandlePeerFound(info peer.AddrInfo) {
	m.runtime.observeDiscoveredPeer(info)
}

type notifiee struct{ runtime *Runtime }

func (n *notifiee) Listen(network.Network, ma.Multiaddr)      {}
func (n *notifiee) ListenClose(network.Network, ma.Multiaddr) {}
func (n *notifiee) OpenedStream(network.Network, network.Stream) {
}
func (n *notifiee) ClosedStream(network.Network, network.Stream) {
}
func (n *notifiee) Connected(_ network.Network, conn network.Conn) {
	n.runtime.handlePeerConnected(conn)
}
func (n *notifiee) Disconnected(_ network.Network, conn network.Conn) {
	n.runtime.handlePeerDisconnected(conn)
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func readRemoteUpdates(stream network.Stream, updates chan<- TaskUpdate) {
	defer close(updates)
	defer stream.Close()

	dec := json.NewDecoder(stream)
	lastTaskID := ""
	lastContextID := ""
	terminalSeen := false
	for {
		var env rpcEnvelope
		if err := dec.Decode(&env); err != nil {
			if err == io.EOF {
				if !terminalSeen {
					updates <- TaskUpdate{
						TaskID:    lastTaskID,
						ContextID: lastContextID,
						State:     "interrupted",
						Error:     "task stream closed before terminal state; resume may be required",
						UpdatedAt: time.Now(),
					}
				}
				return
			}
			updates <- TaskUpdate{
				TaskID:    lastTaskID,
				ContextID: lastContextID,
				State:     "interrupted",
				Error:     err.Error(),
				UpdatedAt: time.Now(),
			}
			return
		}
		switch env.Kind {
		case "update":
			if env.Update != nil {
				lastTaskID = env.Update.TaskID
				lastContextID = env.Update.ContextID
				if env.Update.State == "completed" || env.Update.State == "failed" || env.Update.State == "cancelled" {
					terminalSeen = true
				}
				updates <- *env.Update
			}
		case "error":
			updates <- TaskUpdate{TaskID: env.TaskID, State: "failed", Error: env.Error, UpdatedAt: time.Now()}
			terminalSeen = true
			return
		case "cancelled":
			updates <- TaskUpdate{TaskID: env.TaskID, State: "cancelled", UpdatedAt: time.Now()}
			terminalSeen = true
			return
		}
	}
}
