package web

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/a2apeers"
)

type A2ARuntimeProvider interface {
	GetA2AStatus() a2a.Status
	ListA2APeers(filter string) []a2a.PeerRecord
	GetA2APairingPayload() a2a.PairingPayload
	PingA2APeer(ctx context.Context, target string) (a2a.PingResult, error)
	A2APeerRegistry() *a2apeers.Registry
	RefreshA2ATrustedPeers()
}
