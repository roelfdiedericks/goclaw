package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/multiformats/go-multiaddr"
	"github.com/roelfdiedericks/goclaw/internal/a2apeers"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type A2APeersAPI struct {
	configPath string
	runtime    A2ARuntimeProvider
}

type A2APeerRequest struct {
	Type      string   `json:"type"`
	Alias     string   `json:"alias"`
	PeerID    string   `json:"peerId"`
	Addrs     []string `json:"addrs"`
	LocalUser string   `json:"localUser"`
	Enabled   bool     `json:"enabled"`
	Notes     string   `json:"notes"`
}

type A2APingRequest struct {
	PeerID string `json:"peerId"`
}

func NewA2APeersAPI(configPath string, runtime A2ARuntimeProvider) *A2APeersAPI {
	return &A2APeersAPI{
		configPath: configPath,
		runtime:    runtime,
	}
}

func (a *A2APeersAPI) HandleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	registry := a.registry()
	if registry == nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "A2A peer registry unavailable"})
		return
	}

	users, err := user.LoadUsersFromPath(user.GetUsersFilePathForConfig(a.configPath))
	if err != nil {
		L_error("a2a-peers-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]any{
			"peers": registry.List(),
			"users": usernamesFromConfig(users),
			"path":  registry.Path(),
		},
	})
}

func (a *A2APeersAPI) HandleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]any{
			"status": a.runtime.GetA2AStatus(),
			"peers":  a.runtime.ListA2APeers("all"),
		},
	})
}

func (a *A2APeersAPI) HandlePairing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    a.runtime.GetA2APairingPayload(),
	})
}

func (a *A2APeersAPI) HandlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req A2APingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}
	req.PeerID = strings.TrimSpace(req.PeerID)
	if req.PeerID == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  map[string]string{"peerId": "Peer ID is required"},
		})
		return
	}

	result, err := a.runtime.PingA2APeer(r.Context(), req.PeerID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    result,
		Message: result.Message,
	})
}

func (a *A2APeersAPI) HandlePeers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.handleCreatePeer(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func (a *A2APeersAPI) HandlePeer(w http.ResponseWriter, r *http.Request) {
	peerID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/setup/api/a2a/peers/"))
	if peerID == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Peer ID required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.handleGetPeer(w, r, peerID)
	case http.MethodPut:
		a.handleUpdatePeer(w, r, peerID)
	case http.MethodDelete:
		a.handleDeletePeer(w, r, peerID)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func (a *A2APeersAPI) handleGetPeer(w http.ResponseWriter, _ *http.Request, peerID string) {
	registry := a.registry()
	if registry == nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "A2A peer registry unavailable"})
		return
	}

	peer, ok := registry.GetLibp2p(peerID)
	if !ok {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Peer not found"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: peer})
}

func (a *A2APeersAPI) handleCreatePeer(w http.ResponseWriter, r *http.Request) {
	req, errs := a.decodeAndValidateRequest(r)
	if len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Errors: errs})
		return
	}

	registry := a.registry()
	if registry == nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "A2A peer registry unavailable"})
		return
	}
	if _, exists := registry.GetLibp2p(req.PeerID); exists {
		writeJSON(w, http.StatusConflict, APIResponse{
			Success: false,
			Errors:  map[string]string{"peerId": "Peer already exists"},
		})
		return
	}

	peer := requestToPeer(req)
	if err := registry.Upsert(peer); err != nil {
		L_error("a2a-peers-api: failed to create peer", "peerID", peer.PeerID, "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save peer"})
		return
	}
	a.runtime.RefreshA2ATrustedPeers()
	writeJSON(w, http.StatusCreated, APIResponse{
		Success: true,
		Data:    peer,
		Message: "Peer created",
	})
}

func (a *A2APeersAPI) handleUpdatePeer(w http.ResponseWriter, r *http.Request, peerID string) {
	registry := a.registry()
	if registry == nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "A2A peer registry unavailable"})
		return
	}
	if _, exists := registry.GetLibp2p(peerID); !exists {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Peer not found"})
		return
	}

	req, errs := a.decodeAndValidateRequest(r)
	if len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Errors: errs})
		return
	}
	if req.PeerID != peerID {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  map[string]string{"peerId": "Peer identity cannot be changed"},
		})
		return
	}

	peer := requestToPeer(req)
	if err := registry.Upsert(peer); err != nil {
		L_error("a2a-peers-api: failed to update peer", "peerID", peer.PeerID, "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save peer"})
		return
	}
	a.runtime.RefreshA2ATrustedPeers()
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    peer,
		Message: "Peer updated",
	})
}

func (a *A2APeersAPI) handleDeletePeer(w http.ResponseWriter, _ *http.Request, peerID string) {
	registry := a.registry()
	if registry == nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "A2A peer registry unavailable"})
		return
	}
	if err := registry.DeleteLibp2p(peerID); err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: err.Error()})
		return
	}
	a.runtime.RefreshA2ATrustedPeers()
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Peer deleted",
	})
}

func (a *A2APeersAPI) decodeAndValidateRequest(r *http.Request) (A2APeerRequest, map[string]string) {
	var req A2APeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, map[string]string{"_": "Invalid JSON"}
	}

	req.Type = strings.TrimSpace(strings.ToLower(req.Type))
	req.Alias = strings.TrimSpace(req.Alias)
	req.PeerID = strings.TrimSpace(req.PeerID)
	req.LocalUser = strings.TrimSpace(req.LocalUser)
	req.Notes = strings.TrimSpace(req.Notes)
	req.Addrs = normalizeAddrs(req.Addrs)

	errors := make(map[string]string)
	if req.Type == "" {
		req.Type = a2apeers.TypeLibp2p
	}
	if req.Type != a2apeers.TypeLibp2p {
		errors["type"] = "Unsupported peer type"
	}
	if req.PeerID == "" {
		errors["peerId"] = "Peer ID is required"
	}
	for _, addr := range req.Addrs {
		if _, err := multiaddr.NewMultiaddr(addr); err != nil {
			errors["addrs"] = "Addresses must be valid multiaddrs"
			break
		}
	}
	if req.LocalUser != "" {
		users, err := user.LoadUsersFromPath(user.GetUsersFilePathForConfig(a.configPath))
		if err != nil {
			errors["localUser"] = "Failed to validate local user"
		} else if _, ok := users[req.LocalUser]; !ok {
			errors["localUser"] = "Local user not found"
		}
	}
	return req, errors
}

func (a *A2APeersAPI) registry() *a2apeers.Registry {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.A2APeerRegistry()
}

func requestToPeer(req A2APeerRequest) a2apeers.Peer {
	return a2apeers.Peer{
		Type:      req.Type,
		Alias:     req.Alias,
		PeerID:    req.PeerID,
		Addrs:     req.Addrs,
		LocalUser: req.LocalUser,
		Enabled:   req.Enabled,
		Notes:     req.Notes,
	}
}

func normalizeAddrs(addrs []string) []string {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	seen := map[string]struct{}{}
	for _, addr := range addrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

func usernamesFromConfig(users user.UsersConfig) []string {
	out := make([]string, 0, len(users))
	for username := range users {
		out = append(out, username)
	}
	sort.Strings(out)
	return out
}
