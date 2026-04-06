package libp2p

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
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
