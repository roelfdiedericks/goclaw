package libp2p

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	golibp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/holepunch"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	ma "github.com/multiformats/go-multiaddr"
)

func TestSanitizeRemoteRegistrationAddrsPublicSafe(t *testing.T) {
	peerID := mustTestPeerID(t)
	rt := New(Config{
		RendezvousNamespace:     "test-ns",
		RendezvousAdmissionMode: "public-safe",
	}, RuntimeModeBootstrap, Callbacks{})

	addrs, dropped, reasons := rt.sanitizeRemoteRegistrationAddrs(peerID, []string{
		"/ip4/192.168.0.42/tcp/4001/p2p/" + peerID,
		"/ip4/155.93.137.191/tcp/4001/p2p/" + peerID,
		"/ip4/127.0.0.1/tcp/4001/p2p/" + peerID,
	})

	if len(addrs) != 1 {
		t.Fatalf("expected 1 surviving address, got %d: %#v", len(addrs), addrs)
	}
	if got := addrs[0]; got != "/ip4/155.93.137.191/tcp/4001/p2p/"+peerID {
		t.Fatalf("unexpected surviving address: %s", got)
	}
	if dropped != 2 {
		t.Fatalf("expected 2 dropped addresses, got %d", dropped)
	}
	if reasons["private"] != 1 || reasons["loopback"] != 1 {
		t.Fatalf("unexpected reasons: %#v", reasons)
	}
}

func TestSanitizeRemoteRegistrationAddrsPrivateNetwork(t *testing.T) {
	peerID := mustTestPeerID(t)
	rt := New(Config{
		RendezvousNamespace:     "test-ns",
		RendezvousAdmissionMode: "private-network",
	}, RuntimeModeBootstrap, Callbacks{})

	addrs, dropped, reasons := rt.sanitizeRemoteRegistrationAddrs(peerID, []string{
		"/ip4/192.168.0.42/tcp/4001/p2p/" + peerID,
		"/ip4/100.64.1.10/tcp/4001/p2p/" + peerID,
		"/ip6/fd12:3456:789a::42/tcp/4001/p2p/" + peerID,
		"/ip4/169.254.10.20/tcp/4001/p2p/" + peerID,
		"/ip4/127.0.0.1/tcp/4001/p2p/" + peerID,
	})

	if len(addrs) != 3 {
		t.Fatalf("expected 3 surviving addresses, got %d: %#v", len(addrs), addrs)
	}
	if dropped != 2 {
		t.Fatalf("expected 2 dropped addresses, got %d", dropped)
	}
	if reasons["link-local"] != 1 || reasons["loopback"] != 1 {
		t.Fatalf("unexpected reasons: %#v", reasons)
	}
}

func TestListRendezvousSanitizesLegacyEntries(t *testing.T) {
	peerA := mustTestPeerID(t)
	peerB := mustTestPeerID(t)
	rt := New(Config{
		RendezvousNamespace:     "test-ns",
		RendezvousAdmissionMode: "public-safe",
	}, RuntimeModeBootstrap, Callbacks{})
	rt.rendezvousData["test-ns"] = map[string]rendezvousEntry{
		peerA: {
			PeerID: peerA,
			Addrs: []string{
				"/ip4/192.168.0.42/tcp/4001/p2p/" + peerA,
				"/ip4/155.93.137.191/tcp/4001/p2p/" + peerA,
			},
			ExpiresAt: time.Now().Add(time.Minute),
		},
		peerB: {
			PeerID: peerB,
			Addrs: []string{
				"/ip4/127.0.0.1/tcp/4001/p2p/" + peerB,
			},
			ExpiresAt: time.Now().Add(time.Minute),
		},
	}

	entries := rt.listRendezvous("test-ns", "")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, entry := range entries {
		switch entry.PeerID {
		case peerA:
			if len(entry.Addrs) != 1 || entry.Addrs[0] != "/ip4/155.93.137.191/tcp/4001/p2p/"+peerA {
				t.Fatalf("peer-a entry not sanitized correctly: %#v", entry.Addrs)
			}
		case peerB:
			if len(entry.Addrs) != 0 {
				t.Fatalf("peer-b empty entry should be preserved, got %#v", entry.Addrs)
			}
		default:
			t.Fatalf("unexpected peer entry: %s", entry.PeerID)
		}
	}
}

func TestSeedInfraRelayRendezvousEntryCreatesInfraOwnedRow(t *testing.T) {
	host, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	rt := New(Config{
		RendezvousNamespace: "test-ns",
	}, RuntimeModeBoth, Callbacks{})
	rt.host = host

	targetID := mustTestPeerID(t)
	decodedTarget, err := peer.Decode(targetID)
	if err != nil {
		t.Fatalf("decode target peer id: %v", err)
	}

	rt.seedInfraRelayRendezvousEntry(decodedTarget)

	entry, ok := rt.rendezvousData["test-ns"][targetID]
	if !ok {
		t.Fatal("expected infra-seeded rendezvous entry")
	}
	if entry.Source != rendezvousEntrySourceInfra {
		t.Fatalf("expected infra source, got %q", entry.Source)
	}
	if len(entry.Addrs) == 0 {
		t.Fatal("expected relay addresses in infra-seeded entry")
	}
	if !strings.Contains(entry.Addrs[0], "/p2p/"+host.ID().String()+"/p2p-circuit/p2p/"+targetID) {
		t.Fatalf("unexpected relay address: %s", entry.Addrs[0])
	}
}

func TestHandlePeerConnectedSeedsRelayEntryForDirectInfraConnection(t *testing.T) {
	ctx := context.Background()
	host, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create infra host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	peerHost, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create peer host: %v", err)
	}
	t.Cleanup(func() { _ = peerHost.Close() })

	if err := host.Connect(ctx, peer.AddrInfo{ID: peerHost.ID(), Addrs: peerHost.Addrs()}); err != nil {
		t.Fatalf("connect peer host: %v", err)
	}

	rt := New(Config{
		RendezvousNamespace: "test-ns",
	}, RuntimeModeBoth, Callbacks{})
	rt.host = host

	conns := host.Network().ConnsToPeer(peerHost.ID())
	if len(conns) == 0 {
		t.Fatal("expected active direct connection to peer host")
	}

	rt.handlePeerConnected(conns[0])

	entry, ok := rt.rendezvousData["test-ns"][peerHost.ID().String()]
	if !ok {
		t.Fatal("expected relay rendezvous entry to be seeded from direct infra connection")
	}
	if entry.Source != rendezvousEntrySourceInfra {
		t.Fatalf("expected infra source, got %q", entry.Source)
	}
	if len(entry.Addrs) == 0 {
		t.Fatal("expected relay circuit addrs in seeded entry")
	}
	if !strings.Contains(entry.Addrs[0], "/p2p/"+host.ID().String()+"/p2p-circuit/p2p/"+peerHost.ID().String()) {
		t.Fatalf("unexpected seeded relay address: %s", entry.Addrs[0])
	}
}

func TestRegisterSelfRendezvousEntryOverwritesInfraSeededRow(t *testing.T) {
	rt := New(Config{
		RendezvousNamespace:     "test-ns",
		RendezvousAdmissionMode: "public-safe",
	}, RuntimeModeBootstrap, Callbacks{})

	peerID := mustTestPeerID(t)
	namespace, _, _, _, previousSource, preservedInfra := rt.registerSelfRendezvousEntry("test-ns", peerID, []string{
		"/ip4/155.93.137.191/tcp/4001/p2p/" + peerID,
	})
	if namespace != "test-ns" || previousSource != "" || preservedInfra {
		t.Fatalf("unexpected initial self register result namespace=%q previousSource=%q preserved=%t", namespace, previousSource, preservedInfra)
	}

	rt.rendezvousData["test-ns"][peerID] = rendezvousEntry{
		PeerID:    peerID,
		Addrs:     []string{"/ip4/34.35.192.27/udp/4001/quic-v1/p2p/" + mustTestPeerID(t)},
		ExpiresAt: time.Now().Add(time.Minute),
		Source:    rendezvousEntrySourceInfra,
	}

	var sanitized []string
	namespace, sanitized, _, _, previousSource, preservedInfra = rt.registerSelfRendezvousEntry("test-ns", peerID, []string{
		"/ip4/155.93.137.191/tcp/4001/p2p/" + peerID,
	})
	if namespace != "test-ns" {
		t.Fatalf("unexpected namespace: %q", namespace)
	}
	if preservedInfra {
		t.Fatal("did not expect non-empty self registration to preserve infra row")
	}
	if previousSource != rendezvousEntrySourceInfra {
		t.Fatalf("expected previous infra source, got %q", previousSource)
	}
	entry := rt.rendezvousData["test-ns"][peerID]
	if entry.Source != rendezvousEntrySourceSelf {
		t.Fatalf("expected self source after overwrite, got %q", entry.Source)
	}
	if len(entry.Addrs) != len(sanitized) || len(entry.Addrs) != 1 {
		t.Fatalf("unexpected self-register addrs: %#v", entry.Addrs)
	}
}

func TestSeedInfraRelayRendezvousEntryDoesNotOverwriteSelfOwnedRow(t *testing.T) {
	host, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	rt := New(Config{
		RendezvousNamespace:     "test-ns",
		RendezvousAdmissionMode: "public-safe",
	}, RuntimeModeBootstrap, Callbacks{})
	rt.host = host

	targetID := mustTestPeerID(t)
	_, _, _, _, _, _ = rt.registerSelfRendezvousEntry("test-ns", targetID, []string{
		"/ip4/155.93.137.191/tcp/4001/p2p/" + targetID,
	})

	decodedTarget, err := peer.Decode(targetID)
	if err != nil {
		t.Fatalf("decode target peer id: %v", err)
	}
	rt.seedInfraRelayRendezvousEntry(decodedTarget)

	entry := rt.rendezvousData["test-ns"][targetID]
	if entry.Source != rendezvousEntrySourceSelf {
		t.Fatalf("expected self source to be preserved, got %q", entry.Source)
	}
	if len(entry.Addrs) != 1 || entry.Addrs[0] != "/ip4/155.93.137.191/tcp/4001/p2p/"+targetID {
		t.Fatalf("unexpected self-owned entry after infra seed attempt: %#v", entry.Addrs)
	}
}

func TestRegisterSelfRendezvousEntryEmptyPreservesInfraSeededRow(t *testing.T) {
	rt := New(Config{
		RendezvousNamespace:     "test-ns",
		RendezvousAdmissionMode: "public-safe",
	}, RuntimeModeBootstrap, Callbacks{})

	peerID := mustTestPeerID(t)
	relayAddr := "/ip4/34.35.192.27/udp/4001/quic-v1/p2p/" + mustTestPeerID(t)
	rt.rendezvousData["test-ns"] = map[string]rendezvousEntry{
		peerID: {
			PeerID:    peerID,
			Addrs:     []string{relayAddr},
			ExpiresAt: time.Now().Add(time.Minute),
			Source:    rendezvousEntrySourceInfra,
		},
	}

	namespace, effectiveAddrs, _, _, previousSource, preservedInfra := rt.registerSelfRendezvousEntry("test-ns", peerID, nil)
	if namespace != "test-ns" {
		t.Fatalf("unexpected namespace: %q", namespace)
	}
	if previousSource != rendezvousEntrySourceInfra {
		t.Fatalf("expected previous infra source, got %q", previousSource)
	}
	if !preservedInfra {
		t.Fatal("expected empty self registration to preserve infra row")
	}
	entry := rt.rendezvousData["test-ns"][peerID]
	if entry.Source != rendezvousEntrySourceInfra {
		t.Fatalf("expected infra source to remain, got %q", entry.Source)
	}
	if len(entry.Addrs) != 1 || entry.Addrs[0] != relayAddr {
		t.Fatalf("expected preserved relay addrs, got %#v", entry.Addrs)
	}
	if len(effectiveAddrs) != 1 || effectiveAddrs[0] != relayAddr {
		t.Fatalf("expected effective addrs to reflect preserved relay row, got %#v", effectiveAddrs)
	}
}

func TestRemoveInfraOwnedRendezvousEntry(t *testing.T) {
	rt := New(Config{
		RendezvousNamespace: "test-ns",
	}, RuntimeModeBootstrap, Callbacks{})

	peerID := mustTestPeerID(t)
	rt.rendezvousData["test-ns"] = map[string]rendezvousEntry{
		peerID: {
			PeerID:    peerID,
			Addrs:     []string{"/ip4/34.35.192.27/udp/4001/quic-v1/p2p/" + peerID},
			ExpiresAt: time.Now().Add(time.Minute),
			Source:    rendezvousEntrySourceInfra,
		},
	}

	if removed := rt.removeInfraOwnedRendezvousEntry("test-ns", peerID); !removed {
		t.Fatal("expected infra-owned entry to be removed")
	}
	if _, ok := rt.rendezvousData["test-ns"]; ok {
		t.Fatalf("expected empty namespace bucket to be deleted, got %#v", rt.rendezvousData["test-ns"])
	}
}

func TestRemoveInfraOwnedRendezvousEntryDoesNotRemoveSelfOwnedRow(t *testing.T) {
	rt := New(Config{
		RendezvousNamespace: "test-ns",
	}, RuntimeModeBootstrap, Callbacks{})

	peerID := mustTestPeerID(t)
	rt.rendezvousData["test-ns"] = map[string]rendezvousEntry{
		peerID: {
			PeerID:    peerID,
			Addrs:     []string{"/ip4/155.93.137.191/tcp/4001/p2p/" + peerID},
			ExpiresAt: time.Now().Add(time.Minute),
			Source:    rendezvousEntrySourceSelf,
		},
	}

	if removed := rt.removeInfraOwnedRendezvousEntry("test-ns", peerID); removed {
		t.Fatal("expected self-owned entry to be preserved")
	}
	if _, ok := rt.rendezvousData["test-ns"][peerID]; !ok {
		t.Fatal("expected self-owned entry to remain present")
	}
}

func TestAdvertisedAddressEvaluationLoggingIsDeduplicated(t *testing.T) {
	prevLevel := logging.GetLevel()
	logging.SetLevel(logging.LevelDebug)
	t.Cleanup(func() {
		logging.SetHook(nil)
		logging.SetLevel(prevLevel)
	})

	var lines []string
	logging.SetHook(func(level, msg string) {
		lines = append(lines, level+" "+msg)
	})

	rt := New(Config{}, RuntimeModeNode, Callbacks{})
	factory := rt.addrFactory([]ma.Multiaddr{
		ma.StringCast("/ip4/34.35.192.27/tcp/4001"),
		ma.StringCast("/ip4/34.35.192.27/udp/4001/quic-v1"),
	})

	first := factory(nil)
	second := factory(nil)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected explicit addresses to be preserved")
	}

	count := 0
	for _, line := range lines {
		if strings.Contains(line, "a2a libp2p: advertised address set evaluated") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one advertised address evaluation log, got %d: %#v", count, lines)
	}
}

func TestBootstrapRelayPeerSourceUsesBootstrapPeers(t *testing.T) {
	peerA := mustTestPeerID(t)
	peerB := mustTestPeerID(t)
	rt := New(Config{
		BootstrapPeers: []string{
			"/ip4/34.35.192.27/tcp/4001/p2p/" + peerA,
			"/ip4/34.35.192.27/udp/4001/quic-v1/p2p/" + peerB,
		},
	}, RuntimeModeNode, Callbacks{})

	source := rt.bootstrapRelayPeerSource()
	ch := source(context.Background(), 2)
	var ids []string
	for info := range ch {
		ids = append(ids, info.ID.String())
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 bootstrap relay candidates, got %d (%#v)", len(ids), ids)
	}
	if ids[0] != peerA || ids[1] != peerB {
		t.Fatalf("unexpected bootstrap relay candidates: %#v", ids)
	}
}

func TestNormalizeBootstrapEntriesPrefersQUICOverTCPForSamePeer(t *testing.T) {
	peerID := mustTestPeerID(t)
	entries := normalizeBootstrapEntries([]string{
		"/ip4/34.35.192.27/tcp/4001/p2p/" + peerID,
		"/ip4/34.35.192.27/udp/4001/quic-v1/p2p/" + peerID,
	})
	if len(entries) != 1 {
		t.Fatalf("expected one normalized entry, got %d: %#v", len(entries), entries)
	}
	if got := entries[0]; got != "/ip4/34.35.192.27/udp/4001/quic-v1/p2p/"+peerID {
		t.Fatalf("expected QUIC entry to win, got %s", got)
	}
}

func TestNatServiceEnabledByMode(t *testing.T) {
	tests := []struct {
		name string
		mode RuntimeMode
		cfg  Config
		want bool
	}{
		{name: "node", mode: RuntimeModeNode, want: false},
		{name: "bootstrap", mode: RuntimeModeBootstrap, want: true},
		{name: "relay", mode: RuntimeModeRelay, want: true},
		{name: "both", mode: RuntimeModeBoth, want: true},
		{name: "node relay server config", mode: RuntimeModeNode, cfg: Config{RelayServerEnabled: true}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := New(tt.cfg, tt.mode, Callbacks{})
			if got := rt.natServiceEnabled(); got != tt.want {
				t.Fatalf("natServiceEnabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestPeerPathMode(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		direct  int
		relayed int
		want    string
	}{
		{name: "disconnected", want: "disconnected"},
		{name: "relay only", total: 1, relayed: 1, want: "relay-only"},
		{name: "direct only", total: 1, direct: 1, want: "direct-only"},
		{name: "mixed", total: 2, direct: 1, relayed: 1, want: "mixed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := peerPathMode(tt.total, tt.direct, tt.relayed); got != tt.want {
				t.Fatalf("peerPathMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHolePunchTracerLogsEvents(t *testing.T) {
	prevLevel := logging.GetLevel()
	logging.SetLevel(logging.LevelTrace)
	t.Cleanup(func() {
		logging.SetHook(nil)
		logging.SetLevel(prevLevel)
	})

	var lines []string
	logging.SetHook(func(level, msg string) {
		lines = append(lines, level+" "+msg)
	})

	rt := New(Config{}, RuntimeModeNode, Callbacks{})
	peerID := peer.ID(mustTestPeerID(t))
	rt.Trace(&holepunch.Event{
		Remote: peerID,
		Type:   holepunch.StartHolePunchEvtT,
		Evt: &holepunch.StartHolePunchEvt{
			RemoteAddrs: []string{"/ip4/34.35.192.27/udp/4001/quic-v1"},
			RTT:         25 * time.Millisecond,
		},
	})
	rt.Trace(&holepunch.Event{
		Remote: peerID,
		Type:   holepunch.DirectDialEvtT,
		Evt: &holepunch.DirectDialEvt{
			Success:      false,
			EllapsedTime: 300 * time.Millisecond,
			Error:        "dial timeout",
		},
	})
	rt.Trace(&holepunch.Event{
		Remote: peerID,
		Type:   holepunch.EndHolePunchEvtT,
		Evt: &holepunch.EndHolePunchEvt{
			Success:      true,
			EllapsedTime: 450 * time.Millisecond,
		},
	})

	mustContain := []string{
		"a2a libp2p: hole punch started",
		"a2a libp2p: hole punch direct dial failed",
		"a2a libp2p: hole punch succeeded",
	}
	for _, want := range mustContain {
		found := false
		for _, line := range lines {
			if strings.Contains(line, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected log containing %q, got %#v", want, lines)
		}
	}
}

func TestReserveRendezvousRegisterSlotRespectsMinInterval(t *testing.T) {
	rt := New(Config{RendezvousEnabled: true}, RuntimeModeNode, Callbacks{})
	now := time.Now()
	if ok := rt.reserveRendezvousRegisterSlot(now, 0, "warmup"); !ok {
		t.Fatal("expected first register slot reservation to succeed")
	}
	if ok := rt.reserveRendezvousRegisterSlot(now.Add(500*time.Millisecond), 2*time.Second, "relay-addrs-updated"); ok {
		t.Fatal("expected second register slot reservation inside min interval to be skipped")
	}
	if ok := rt.reserveRendezvousRegisterSlot(now.Add(3*time.Second), 2*time.Second, "relay-addrs-updated"); !ok {
		t.Fatal("expected register slot reservation after min interval to succeed")
	}
}

func TestReserveRendezvousQuerySlotRespectsMinIntervalAndBypass(t *testing.T) {
	rt := New(Config{RendezvousEnabled: true}, RuntimeModeNode, Callbacks{})
	now := time.Now()
	rt.lastRendezvousQuery = now

	if ok := rt.reserveRendezvousQuerySlot(now.Add(10*time.Second), 30*time.Second, "ticker", false); ok {
		t.Fatal("expected query reservation inside min interval to be skipped")
	}
	if ok := rt.reserveRendezvousQuerySlot(now.Add(10*time.Second), 0, "warmup", true); !ok {
		t.Fatal("expected bypass query reservation to succeed")
	}
}

func TestResolveTargetPeerMergesKnownPeersAndPeerstore(t *testing.T) {
	host, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	peerID := mustTestPeerID(t)
	id, err := peer.Decode(peerID)
	if err != nil {
		t.Fatalf("decode peer id: %v", err)
	}
	host.Peerstore().AddAddrs(id, []ma.Multiaddr{
		ma.StringCast("/ip4/34.35.192.27/udp/4001/quic-v1"),
	}, time.Hour)

	rt := New(Config{}, RuntimeModeNode, Callbacks{})
	rt.host = host
	info, sources, err := rt.resolveTargetPeer("friend", []PeerCandidate{{
		PeerID: peerID,
		Alias:  "friend",
		Addrs:  []string{"/ip4/34.35.192.27/tcp/4001"},
	}})
	if err != nil {
		t.Fatalf("resolve target peer: %v", err)
	}
	if len(info.Addrs) != 2 {
		t.Fatalf("expected merged addrs from known peers and peerstore, got %d: %#v", len(info.Addrs), multiaddrsToStrings(info.Addrs))
	}
	if !strings.Contains(strings.Join(sources, ","), "peerstore") || !strings.Contains(strings.Join(sources, ","), "known-peers") {
		t.Fatalf("expected sources to include peerstore and known-peers, got %#v", sources)
	}
}

func TestPrepareTargetPeerSynthesizesRelayFallback(t *testing.T) {
	ctx := context.Background()
	host, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create local host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	relayHost, err := golibp2p.New(golibp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create relay host: %v", err)
	}
	t.Cleanup(func() { _ = relayHost.Close() })

	if err := host.Connect(ctx, peer.AddrInfo{ID: relayHost.ID(), Addrs: relayHost.Addrs()}); err != nil {
		t.Fatalf("connect bootstrap relay host: %v", err)
	}

	rt := New(Config{}, RuntimeModeNode, Callbacks{})
	rt.host = host
	rt.bootstrapEntries = []string{
		relayHost.Addrs()[0].Encapsulate(ma.StringCast("/p2p/" + relayHost.ID().String())).String(),
	}
	rt.bootstrapPeerIDs[relayHost.ID()] = struct{}{}

	targetID := mustTestPeerID(t)
	info, sources, err := rt.prepareTargetPeer(ctx, targetID, nil, false)
	if err != nil {
		t.Fatalf("prepare target peer: %v", err)
	}
	if len(info.Addrs) == 0 {
		t.Fatal("expected synthesized relay fallback addresses")
	}
	if !strings.Contains(strings.Join(sources, ","), "synthesized-relay") {
		t.Fatalf("expected synthesized-relay source, got %#v", sources)
	}
	got := info.Addrs[0].String()
	if !strings.Contains(got, "/p2p/"+relayHost.ID().String()+"/p2p-circuit/p2p/"+targetID) {
		t.Fatalf("unexpected synthesized relay address: %s", got)
	}
}

func TestNonRelayDialMultiaddrs(t *testing.T) {
	relay := ma.StringCast("/ip4/34.35.192.27/udp/4001/quic-v1/p2p/12D3KooWL4onTt8HXGUWWQsKWjkNUjFrYGWd5vws8AKucszLMkXY/p2p-circuit/p2p/12D3KooWQvSQkqdGcpEqxNjptAQNy4Mp87e1JirUvnJbPm5KEa3L")
	direct := ma.StringCast("/ip4/155.93.137.191/udp/45712/quic-v1")
	out := nonRelayDialMultiaddrs([]ma.Multiaddr{relay, direct, nil})
	if len(out) != 1 || out[0].String() != direct.String() {
		t.Fatalf("expected single direct addr, got %#v", multiaddrsToStrings(out))
	}
}

func TestCanAttemptBackgroundDirectUpgrade(t *testing.T) {
	now := time.Unix(1000, 0)
	cooldown := 30 * time.Second
	if canAttemptBackgroundDirectUpgrade(true, time.Time{}, now, cooldown) {
		t.Fatal("expected inflight to block")
	}
	if !canAttemptBackgroundDirectUpgrade(false, time.Time{}, now, cooldown) {
		t.Fatal("expected first attempt allowed")
	}
	if !canAttemptBackgroundDirectUpgrade(false, now.Add(-31*time.Second), now, cooldown) {
		t.Fatal("expected attempt after cooldown elapsed")
	}
	if canAttemptBackgroundDirectUpgrade(false, now.Add(-10*time.Second), now, cooldown) {
		t.Fatal("expected cooldown to block")
	}
}

func mustTestPeerID(t *testing.T) string {
	t.Helper()
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("derive peer id: %v", err)
	}
	return id.String()
}
