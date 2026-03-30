package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	setuppairing "github.com/roelfdiedericks/goclaw/internal/setup/pairing"
)

type PairingAPI struct{}

func NewPairingAPI() *PairingAPI {
	return &PairingAPI{}
}

type pairingRequest struct {
	SessionID string `json:"sessionId"`
	Surface   string `json:"surface,omitempty"`
	BotToken  string `json:"botToken,omitempty"`
}

func (p *PairingAPI) HandleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	channel, action, ok := parseSetupPairingPath(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid pairing path"})
		return
	}

	var req pairingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Pairing session ID is required"})
		return
	}

	var res bus.CommandResult
	switch channel {
	case "telegram":
		switch action {
		case "start":
			res = bus.SendCommandWithSource("telegram.pairing", "start", setuppairing.TelegramStartRequest{
				StartRequest: setuppairing.StartRequest{BaseRequest: setuppairing.BaseRequest{SessionID: req.SessionID, Surface: req.Surface}},
				BotToken:     req.BotToken,
			}, "http", "")
		case "status":
			res = bus.SendCommandWithSource("telegram.pairing", "status", setuppairing.StatusRequest{
				BaseRequest: setuppairing.BaseRequest{SessionID: req.SessionID, Surface: req.Surface},
			}, "http", "")
		case "cancel":
			res = bus.SendCommandWithSource("telegram.pairing", "cancel", setuppairing.CancelRequest{
				BaseRequest: setuppairing.BaseRequest{SessionID: req.SessionID, Surface: req.Surface},
			}, "http", "")
		default:
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unsupported pairing action"})
			return
		}
	case "whatsapp":
		switch action {
		case "start":
			res = bus.SendCommandWithSource("whatsapp.pairing", "start", setuppairing.WhatsAppStartRequest{
				StartRequest: setuppairing.StartRequest{BaseRequest: setuppairing.BaseRequest{SessionID: req.SessionID, Surface: req.Surface}},
			}, "http", "")
		case "status":
			res = bus.SendCommandWithSource("whatsapp.pairing", "status", setuppairing.StatusRequest{
				BaseRequest: setuppairing.BaseRequest{SessionID: req.SessionID, Surface: req.Surface},
			}, "http", "")
		case "cancel":
			res = bus.SendCommandWithSource("whatsapp.pairing", "cancel", setuppairing.CancelRequest{
				BaseRequest: setuppairing.BaseRequest{SessionID: req.SessionID, Surface: req.Surface},
			}, "http", "")
		default:
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unsupported pairing action"})
			return
		}
	default:
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unsupported pairing channel"})
		return
	}

	if res.Error != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: res.Message})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: res.Message, Data: res.Data})
}

func parseSetupPairingPath(path string) (channel string, action string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/setup/api/pairing/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
