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
	lastRendezvousQuery time.Time

	stateMu           sync.RWMutex
	localReachability network.Reachability
	relayAddrs        []string

	advertiseEvalMu      sync.Mutex
	lastAdvertiseEvalSig string
}

type rendezvousEntry struct {
	PeerID    string    `json:"peerId"`
	Addrs     []string  `json:"addrs"`
	ExpiresAt time.Time `json:"expiresAt"`
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
		cfg:               cfg,
		mode:              mode,
		callbacks:         callbacks,
		rendezvousData:    make(map[string]map[string]rendezvousEntry),
		localReachability: network.ReachabilityUnknown,
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
		options = append(options, golibp2p.EnableHolePunching())
	}

	h, err := golibp2p.New(options...)
	if err != nil {
		return fmt.Errorf("create libp2p host: %w", err)
	}
	r.host = h
	r.startedAt = time.Now()
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
}

func (r *Runtime) watchReachability(ctx context.Context, sub interface {
	Out() <-chan interface{}
	Close() error
}) {
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
			}
		}
	}
}

func (r *Runtime) watchAutoRelayAddrs(ctx context.Context, sub interface {
	Out() <-chan interface{}
	Close() error
}) {
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
			r.relayAddrs = relayAddrs
			r.stateMu.Unlock()
			L_info("a2a libp2p: relay addresses updated", "count", len(relayAddrs))
			for _, addr := range relayAddrs {
				L_trace("a2a libp2p: relay advertised address", "addr", addr)
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
	info, err := r.resolveTargetPeer(target, knownPeers)
	if err != nil {
		return "", 0, "", err
	}
	if err := r.connectAddrInfo(ctx, *info); err != nil {
		L_debug("a2a libp2p: ping connect best effort failed", "peerID", info.ID, "error", err)
	}
	results := r.ping.Ping(ctx, info.ID)
	select {
	case result := <-results:
		if result.Error != nil {
			return "", 0, "", result.Error
		}
		return info.ID.String(), result.RTT, "pong", nil
	case <-ctx.Done():
		return "", 0, "", ctx.Err()
	}
}

func (r *Runtime) SubmitRemoteTask(ctx context.Context, target, taskID, input string, knownPeers []PeerCandidate) (<-chan TaskUpdate, error) {
	info, err := r.resolveTargetPeer(target, knownPeers)
	if err != nil {
		return nil, err
	}
	if err := r.connectAddrInfo(ctx, *info); err != nil {
		return nil, err
	}
	stream, err := r.host.NewStream(ctx, info.ID, protocol.ID(r.cfg.RPCProtocolID))
	if err != nil {
		return nil, err
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
	info, err := r.resolveTargetPeer(target, knownPeers)
	if err != nil {
		return nil, err
	}
	if err := r.connectAddrInfo(ctx, *info); err != nil {
		return nil, err
	}
	stream, err := r.host.NewStream(ctx, info.ID, protocol.ID(r.cfg.RPCProtocolID))
	if err != nil {
		return nil, err
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
	info, err := r.resolveTargetPeer(target, knownPeers)
	if err != nil {
		return TaskUpdate{}, err
	}
	if err := r.connectAddrInfo(ctx, *info); err != nil {
		return TaskUpdate{}, err
	}
	stream, err := r.host.NewStream(ctx, info.ID, protocol.ID(r.cfg.RPCProtocolID))
	if err != nil {
		return TaskUpdate{}, err
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
			if r.mode == RuntimeModeNode && r.cfg.RendezvousEnabled {
				r.registerRendezvous(ctx)
			}
		case <-queryTicker.C:
			if r.mode == RuntimeModeNode && r.cfg.RendezvousEnabled && r.shouldRunRendezvousQuery(time.Now()) {
				r.queryRendezvous(ctx)
			}
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
		return cloneStrings(r.cfg.BootstrapPeers), "config", nil
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
	return valid, "dns_txt", nil
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

func (r *Runtime) connectAddrInfo(ctx context.Context, info peer.AddrInfo) error {
	if r.host == nil {
		return fmt.Errorf("host not started")
	}
	L_trace("a2a libp2p: dialing peer", "peerID", info.ID, "addrs", len(info.Addrs))
	r.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
	if err := r.host.Connect(ctx, info); err != nil {
		L_debug("a2a libp2p: dial failed", "peerID", info.ID, "error", err)
		return fmt.Errorf("connect %s: %w", info.ID, err)
	}
	L_trace("a2a libp2p: dial established", "peerID", info.ID, "addrs", len(info.Addrs))
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
	L_info("a2a libp2p: peer connected", "peerID", conn.RemotePeer(), "addr", conn.RemoteMultiaddr(), "relayed", conn.Stat().Limited)
	r.observePeer(PeerObservation{
		PeerID:          conn.RemotePeer().String(),
		Addrs:           multiaddrsToStrings([]ma.Multiaddr{conn.RemoteMultiaddr()}),
		Connected:       true,
		Relayed:         conn.Stat().Limited,
		LastSeen:        time.Now(),
		LastConnectedAt: time.Now(),
	})
}

func (r *Runtime) handlePeerDisconnected(conn network.Conn) {
	L_info("a2a libp2p: peer disconnected", "peerID", conn.RemotePeer(), "addr", conn.RemoteMultiaddr(), "relayed", conn.Stat().Limited)
	r.observePeer(PeerObservation{
		PeerID:           conn.RemotePeer().String(),
		Addrs:            multiaddrsToStrings([]ma.Multiaddr{conn.RemoteMultiaddr()}),
		Connected:        false,
		Relayed:          conn.Stat().Limited,
		LastSeen:         time.Now(),
		LastDisconnectAt: time.Now(),
	})
}

func (r *Runtime) registerRendezvous(ctx context.Context) {
	advertised := r.AdvertisedAddrs()
	if len(advertised) == 0 {
		L_info("a2a libp2p: rendezvous register proceeding with empty addresses", "namespace", r.cfg.RendezvousNamespace)
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
		r.rendezvousMu.Lock()
		namespace := strings.TrimSpace(req.Namespace)
		if namespace == "" {
			namespace = r.cfg.RendezvousNamespace
		}
		bucket := r.rendezvousData[namespace]
		if bucket == nil {
			bucket = make(map[string]rendezvousEntry)
			r.rendezvousData[namespace] = bucket
		}
		sanitized, dropped, reasons := r.sanitizeRemoteRegistrationAddrs(req.PeerID, req.Addrs)
		bucket[req.PeerID] = rendezvousEntry{
			PeerID:    req.PeerID,
			Addrs:     sanitized,
			ExpiresAt: time.Now().Add(2 * time.Minute),
		}
		r.rendezvousMu.Unlock()
		if dropped > 0 {
			L_info("a2a libp2p: rendezvous registration sanitized",
				"peerID", req.PeerID,
				"namespace", namespace,
				"kept", len(sanitized),
				"dropped", dropped,
				"reasons", formatCounts(reasons),
				"mode", r.cfg.RendezvousAdmissionMode,
			)
		}
		L_trace("a2a libp2p: rendezvous peer registered", "peerID", req.PeerID, "namespace", namespace, "addrs", len(sanitized))
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
	if strings.TrimSpace(namespace) == "" {
		namespace = r.cfg.RendezvousNamespace
	}
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

func (r *Runtime) resolveTargetPeer(target string, knownPeers []PeerCandidate) (*peer.AddrInfo, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("peer target is required")
	}
	if id, err := peer.Decode(target); err == nil {
		return &peer.AddrInfo{ID: id, Addrs: r.host.Peerstore().Addrs(id)}, nil
	}
	for _, candidate := range knownPeers {
		if candidate.PeerID == target || candidate.Alias == target {
			info, err := addrInfoFromStrings(candidate.PeerID, candidate.Addrs)
			if err != nil {
				id, decodeErr := peer.Decode(candidate.PeerID)
				if decodeErr != nil {
					return nil, decodeErr
				}
				return &peer.AddrInfo{ID: id, Addrs: r.host.Peerstore().Addrs(id)}, nil
			}
			return info, nil
		}
	}
	return nil, fmt.Errorf("peer %s not found", target)
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
