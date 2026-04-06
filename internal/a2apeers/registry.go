package a2apeers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

const (
	FileVersion = 1
	TypeLibp2p  = "libp2p"
)

type File struct {
	Version int    `json:"version"`
	Peers   []Peer `json:"peers"`
}

type Peer struct {
	Type      string   `json:"type"`
	Alias     string   `json:"alias,omitempty"`
	PeerID    string   `json:"peerId,omitempty"`
	Addrs     []string `json:"addrs,omitempty"`
	LocalUser string   `json:"localUser,omitempty"`
	Enabled   bool     `json:"enabled"`
	Notes     string   `json:"notes,omitempty"`
}

type Registry struct {
	path string

	mu   sync.RWMutex
	file File
}

func PathForConfig(configPath string) string {
	path, err := paths.A2APeersPath(configPath)
	if err != nil {
		return ""
	}
	return path
}

func LoadForConfig(configPath string) (*Registry, error) {
	return LoadFromPath(PathForConfig(configPath))
}

func LoadFromPath(path string) (*Registry, error) {
	r := &Registry{
		path: path,
		file: File{
			Version: FileVersion,
			Peers:   []Peer{},
		},
	}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Path() string {
	return r.path
}

func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *Registry) loadLocked() error {
	if strings.TrimSpace(r.path) == "" {
		r.file = File{Version: FileVersion, Peers: []Peer{}}
		return nil
	}

	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			r.file = File{Version: FileVersion, Peers: []Peer{}}
			L_info("a2apeers: file not found, starting empty", "path", r.path)
			return nil
		}
		return fmt.Errorf("read a2apeers.json at %s: %w", r.path, err)
	}

	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse a2apeers.json at %s: %w", r.path, err)
	}
	if file.Version == 0 {
		file.Version = FileVersion
	}
	if file.Peers == nil {
		file.Peers = []Peer{}
	}
	for i := range file.Peers {
		if err := normalizePeer(&file.Peers[i]); err != nil {
			return err
		}
	}
	r.file = file
	L_info("a2apeers: loaded", "path", r.path, "count", len(file.Peers))
	return nil
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.file.Peers)
}

func (r *Registry) List() []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Peer, len(r.file.Peers))
	copy(out, r.file.Peers)
	return out
}

func (r *Registry) GetLibp2p(peerID string) (*Peer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	peerID = strings.TrimSpace(peerID)
	for _, peer := range r.file.Peers {
		if peer.Type == TypeLibp2p && peer.PeerID == peerID {
			copyPeer := peer
			return &copyPeer, true
		}
	}
	return nil, false
}

func (r *Registry) Upsert(peer Peer) error {
	if err := normalizePeer(&peer); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	replaced := false
	for i := range r.file.Peers {
		if sameIdentity(r.file.Peers[i], peer) {
			r.file.Peers[i] = peer
			replaced = true
			break
		}
	}
	if !replaced {
		r.file.Peers = append(r.file.Peers, peer)
	}
	slices.SortFunc(r.file.Peers, func(a, b Peer) int {
		if a.Type == b.Type {
			return strings.Compare(a.PeerID, b.PeerID)
		}
		return strings.Compare(a.Type, b.Type)
	})
	return r.saveLocked()
}

func (r *Registry) DeleteLibp2p(peerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	peerID = strings.TrimSpace(peerID)
	out := make([]Peer, 0, len(r.file.Peers))
	removed := false
	for _, peer := range r.file.Peers {
		if peer.Type == TypeLibp2p && peer.PeerID == peerID {
			removed = true
			continue
		}
		out = append(out, peer)
	}
	if !removed {
		return fmt.Errorf("peer %s not found", peerID)
	}
	r.file.Peers = out
	return r.saveLocked()
}

func (r *Registry) saveLocked() error {
	file := r.file
	file.Version = FileVersion
	if file.Peers == nil {
		file.Peers = []Peer{}
	}

	if strings.TrimSpace(r.path) == "" {
		return fmt.Errorf("a2apeers path is required")
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", r.path, err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal a2apeers.json: %w", err)
	}
	if err := os.WriteFile(r.path, data, 0o600); err != nil {
		return fmt.Errorf("write a2apeers.json at %s: %w", r.path, err)
	}
	L_info("a2apeers: saved", "path", r.path, "count", len(file.Peers))
	return nil
}

func normalizePeer(peer *Peer) error {
	peer.Type = strings.TrimSpace(strings.ToLower(peer.Type))
	peer.Alias = strings.TrimSpace(peer.Alias)
	peer.PeerID = strings.TrimSpace(peer.PeerID)
	peer.LocalUser = strings.TrimSpace(peer.LocalUser)
	peer.Notes = strings.TrimSpace(peer.Notes)
	peer.Addrs = normalizeStringList(peer.Addrs)

	switch peer.Type {
	case TypeLibp2p:
		if peer.PeerID == "" {
			return fmt.Errorf("libp2p peerId is required")
		}
	default:
		return fmt.Errorf("unsupported peer type %q", peer.Type)
	}
	return nil
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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
	return out
}

func sameIdentity(left, right Peer) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case TypeLibp2p:
		return left.PeerID == right.PeerID
	default:
		return false
	}
}
