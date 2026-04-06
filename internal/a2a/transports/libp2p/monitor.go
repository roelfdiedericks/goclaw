package libp2p

import (
	"sort"
	"strings"
	"time"
)

type RendezvousEntrySnapshot struct {
	PeerID    string    `json:"peerId"`
	Addrs     []string  `json:"addrs,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

type RendezvousNamespaceSnapshot struct {
	Namespace string                   `json:"namespace"`
	Entries   []RendezvousEntrySnapshot `json:"entries,omitempty"`
}

func (r *Runtime) RendezvousSnapshot() []RendezvousNamespaceSnapshot {
	r.rendezvousMu.Lock()
	defer r.rendezvousMu.Unlock()

	now := time.Now()
	namespaces := make([]string, 0, len(r.rendezvousData))
	for namespace := range r.rendezvousData {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)

	out := make([]RendezvousNamespaceSnapshot, 0, len(namespaces))
	for _, namespace := range namespaces {
		bucket := r.rendezvousData[namespace]
		if bucket == nil {
			continue
		}
		peerIDs := make([]string, 0, len(bucket))
		for peerID, entry := range bucket {
			if now.After(entry.ExpiresAt) {
				delete(bucket, peerID)
				continue
			}
			peerIDs = append(peerIDs, peerID)
		}
		sort.Strings(peerIDs)
		namespaceSnapshot := RendezvousNamespaceSnapshot{
			Namespace: namespace,
			Entries:   make([]RendezvousEntrySnapshot, 0, len(peerIDs)),
		}
		for _, peerID := range peerIDs {
			entry := bucket[peerID]
			namespaceSnapshot.Entries = append(namespaceSnapshot.Entries, RendezvousEntrySnapshot{
				PeerID:    entry.PeerID,
				Addrs:     cloneMonitorStrings(entry.Addrs),
				ExpiresAt: entry.ExpiresAt,
			})
		}
		out = append(out, namespaceSnapshot)
	}
	return out
}

func cloneMonitorStrings(values []string) []string {
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
