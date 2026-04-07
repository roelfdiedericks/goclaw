package a2a

import (
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/roelfdiedericks/goclaw/internal/a2apeers"
)

func TestRefreshTrustedPeersLockedPreservesDiscoveredAddrsWhenTrustedAddrsEmpty(t *testing.T) {
	registry, err := a2apeers.LoadFromPath(filepath.Join(t.TempDir(), "a2apeers.json"))
	if err != nil {
		t.Fatalf("load peer registry: %v", err)
	}
	peerID := mustManagerTestPeerID(t)
	if err := registry.Upsert(a2apeers.Peer{
		Type:      a2apeers.TypeLibp2p,
		PeerID:    peerID,
		Alias:     "friend",
		LocalUser: "rodent",
		Enabled:   true,
	}); err != nil {
		t.Fatalf("upsert trusted peer: %v", err)
	}

	manager := &Manager{
		peerRegistry: registry,
		peers: map[string]*PeerRecord{
			peerID: {
				PeerID: peerID,
				Addrs:  []string{"/ip4/34.35.192.27/udp/4001/quic-v1/p2p/" + peerID},
			},
		},
	}

	manager.refreshTrustedPeersLocked()
	record := manager.peers[peerID]
	if record == nil {
		t.Fatal("expected trusted peer record to remain present")
	}
	if len(record.Addrs) != 1 {
		t.Fatalf("expected discovered addrs to be preserved, got %#v", record.Addrs)
	}
	if got := record.Addrs[0]; got != "/ip4/34.35.192.27/udp/4001/quic-v1/p2p/"+peerID {
		t.Fatalf("unexpected preserved addr: %s", got)
	}
	if record.Alias != "friend" || record.LocalUser != "rodent" {
		t.Fatalf("expected trusted metadata to be applied, got alias=%q user=%q", record.Alias, record.LocalUser)
	}
}

func mustManagerTestPeerID(t *testing.T) string {
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
