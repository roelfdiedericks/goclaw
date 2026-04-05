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
	"strings"
	"sync"
	"time"

	golibp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
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

type Config struct {
	IdentityKeyFile      string
	ListenAddrs          []string
	BootstrapPeers       []string
	BootstrapSeedTXT     string
	MDNSEnabled          bool
	MDNSServiceName      string
	RendezvousEnabled    bool
	RendezvousNamespace  string
	RegisterIntervalSecs int
	QueryIntervalSecs    int
	RelayClientEnabled   bool
	RelayServerEnabled   bool
	StaticRelays         []string
	RPCProtocolID        string
	RendezvousProtocolID string
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
		cfg:            cfg,
		mode:           mode,
		callbacks:      callbacks,
		rendezvousData: make(map[string]map[string]rendezvousEntry),
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

	h, err := golibp2p.New(
		golibp2p.Identity(priv),
		golibp2p.ListenAddrs(listenAddrs...),
		golibp2p.EnableRelay(),
	)
	if err != nil {
		return fmt.Errorf("create libp2p host: %w", err)
	}
	r.host = h
	r.startedAt = time.Now()
	r.ping = pingproto.NewPingService(h)
	h.Network().Notify(&notifiee{runtime: r})

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
	if r.bootstrapDialEnabled() {
		if err := r.connectBootstrapPeers(ctx); err != nil {
			L_warn("a2a libp2p: bootstrap connect pass failed", "error", err)
		}
	} else {
		L_info("a2a libp2p: bootstrap connect pass skipped", "mode", r.mode)
	}
	if r.cfg.RelayClientEnabled {
		r.reserveStaticRelays(ctx)
	}
	if r.mode == RuntimeModeNode && r.cfg.MDNSEnabled {
		r.startMDNS()
	}
	go r.runBackground(ctx)
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
	return multiaddrsToStrings(r.host.Addrs())
}

func (r *Runtime) AdvertisedAddrs() []string {
	if r.host == nil {
		return nil
	}
	out := make([]string, 0, len(r.host.Addrs()))
	for _, addr := range r.host.Addrs() {
		out = append(out, addr.Encapsulate(ma.StringCast("/p2p/"+r.host.ID().String())).String())
	}
	return out
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
			_ = r.connectBootstrapPeers(ctx)
		case <-registerTicker.C:
			if r.mode == RuntimeModeNode && r.cfg.RendezvousEnabled {
				r.registerRendezvous(ctx)
			}
		case <-queryTicker.C:
			if r.mode == RuntimeModeNode && r.cfg.RendezvousEnabled {
				r.queryRendezvous(ctx)
			}
		}
	}
}

func (r *Runtime) connectBootstrapPeers(ctx context.Context) error {
	entries, source, resolutionErr := r.bootstrapPeerEntries()
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

func (r *Runtime) bootstrapPeerEntries() ([]string, string, error) {
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

func (r *Runtime) connectAddrInfo(ctx context.Context, info peer.AddrInfo) error {
	if r.host == nil {
		return fmt.Errorf("host not started")
	}
	r.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
	if err := r.host.Connect(ctx, info); err != nil {
		return fmt.Errorf("connect %s: %w", info.ID, err)
	}
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

func (r *Runtime) reserveStaticRelays(ctx context.Context) {
	for _, relayAddr := range r.cfg.StaticRelays {
		info, err := parseAddrInfo(relayAddr)
		if err != nil {
			L_warn("a2a libp2p: invalid static relay", "addr", relayAddr, "error", err)
			continue
		}
		if err := r.connectAddrInfo(ctx, *info); err != nil {
			L_warn("a2a libp2p: connect static relay failed", "peerID", info.ID, "error", err)
			continue
		}
		if _, err := relayclient.Reserve(ctx, r.host, *info); err != nil {
			L_warn("a2a libp2p: relay reservation failed", "peerID", info.ID, "error", err)
			continue
		}
	}
}

func (r *Runtime) startMDNS() {
	service := mdns.NewMdnsService(r.host, r.cfg.MDNSServiceName, &mdnsNotifee{runtime: r})
	if err := service.Start(); err != nil {
		L_warn("a2a libp2p: mdns start failed", "error", err)
	}
}

func (r *Runtime) observeDiscoveredPeer(info peer.AddrInfo) {
	if info.ID == r.host.ID() {
		return
	}
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
	}
}

func (r *Runtime) observePeer(observation PeerObservation) {
	if r.callbacks.OnPeerObserved != nil {
		r.callbacks.OnPeerObserved(observation)
	}
}

func (r *Runtime) handlePeerConnected(conn network.Conn) {
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
	payload := rendezvousRequest{
		Action:    "register",
		Namespace: r.cfg.RendezvousNamespace,
		PeerID:    r.host.ID().String(),
		Addrs:     r.AdvertisedAddrs(),
	}
	entries, _, err := r.bootstrapPeerEntries()
	if err != nil {
		L_warn("a2a libp2p: rendezvous bootstrap resolution failed", "error", err)
		return
	}
	for _, addr := range entries {
		info, err := parseAddrInfo(addr)
		if err != nil {
			continue
		}
		if err := r.sendRendezvousRequest(ctx, *info, payload, nil); err != nil {
			L_warn("a2a libp2p: rendezvous register failed", "peerID", info.ID, "error", err)
		}
	}
}

func (r *Runtime) queryRendezvous(ctx context.Context) {
	payload := rendezvousRequest{
		Action:    "list",
		Namespace: r.cfg.RendezvousNamespace,
		PeerID:    r.host.ID().String(),
	}
	entries, _, err := r.bootstrapPeerEntries()
	if err != nil {
		L_warn("a2a libp2p: rendezvous bootstrap resolution failed", "error", err)
		return
	}
	for _, addr := range entries {
		info, err := parseAddrInfo(addr)
		if err != nil {
			continue
		}
		var resp rendezvousResponse
		if err := r.sendRendezvousRequest(ctx, *info, payload, &resp); err != nil {
			L_warn("a2a libp2p: rendezvous query failed", "peerID", info.ID, "error", err)
			continue
		}
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
}

func (r *Runtime) sendRendezvousRequest(ctx context.Context, info peer.AddrInfo, payload rendezvousRequest, resp *rendezvousResponse) error {
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
		bucket[req.PeerID] = rendezvousEntry{
			PeerID:    req.PeerID,
			Addrs:     cloneStrings(req.Addrs),
			ExpiresAt: time.Now().Add(2 * time.Minute),
		}
		r.rendezvousMu.Unlock()
	case "list":
		resp.Entries = r.listRendezvous(req.Namespace, req.PeerID)
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
		out = append(out, entry)
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
	return out
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
