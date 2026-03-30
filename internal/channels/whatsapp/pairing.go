package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	setuppairing "github.com/roelfdiedericks/goclaw/internal/setup/pairing"
)

const whatsappPairingComponent = "whatsapp.pairing"
const whatsappPairingPollAfterMs = 1500

var whatsappPairings = newWhatsAppPairingManager()

type whatsAppPairingManager struct {
	mu       sync.Mutex
	sessions map[string]*whatsAppPairingSession
}

type whatsAppPairingSession struct {
	id     string
	status setuppairing.Status
	cancel context.CancelFunc
}

func newWhatsAppPairingManager() *whatsAppPairingManager {
	return &whatsAppPairingManager{
		sessions: make(map[string]*whatsAppPairingSession),
	}
}

// RegisterPairingCommands registers setup pairing handlers for WhatsApp.
func RegisterPairingCommands() {
	bus.RegisterCommand(whatsappPairingComponent, "start", handleWhatsAppPairingStart)
	bus.RegisterCommand(whatsappPairingComponent, "status", handleWhatsAppPairingStatus)
	bus.RegisterCommand(whatsappPairingComponent, "cancel", handleWhatsAppPairingCancel)
}

// UnregisterPairingCommands unregisters setup pairing handlers for WhatsApp.
func UnregisterPairingCommands() {
	bus.UnregisterComponent(whatsappPairingComponent)
}

func handleWhatsAppPairingStart(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(setuppairing.WhatsAppStartRequest)
	if !ok {
		ptr, ok := cmd.Payload.(*setuppairing.WhatsAppStartRequest)
		if !ok || ptr == nil {
			return bus.CommandResult{Error: fmt.Errorf("invalid whatsapp pairing payload: %T", cmd.Payload), Message: "Invalid pairing request"}
		}
		req = *ptr
	}
	status, err := whatsappPairings.start(req)
	if err != nil {
		return bus.CommandResult{Error: err, Message: err.Error()}
	}
	return bus.CommandResult{Success: true, Message: status.Message, Data: status}
}

func handleWhatsAppPairingStatus(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(setuppairing.StatusRequest)
	if !ok {
		ptr, ok := cmd.Payload.(*setuppairing.StatusRequest)
		if !ok || ptr == nil {
			return bus.CommandResult{Error: fmt.Errorf("invalid whatsapp pairing payload: %T", cmd.Payload), Message: "Invalid pairing status request"}
		}
		req = *ptr
	}
	status := whatsappPairings.status(req.SessionID)
	return bus.CommandResult{Success: true, Message: status.Message, Data: status}
}

func handleWhatsAppPairingCancel(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(setuppairing.CancelRequest)
	if !ok {
		ptr, ok := cmd.Payload.(*setuppairing.CancelRequest)
		if !ok || ptr == nil {
			return bus.CommandResult{Error: fmt.Errorf("invalid whatsapp pairing payload: %T", cmd.Payload), Message: "Invalid pairing cancel request"}
		}
		req = *ptr
	}
	status := whatsappPairings.cancel(req.SessionID)
	return bus.CommandResult{Success: true, Message: status.Message, Data: status}
}

func (m *whatsAppPairingManager) start(req setuppairing.WhatsAppStartRequest) (setuppairing.Status, error) {
	if req.SessionID == "" {
		return setuppairing.Status{}, fmt.Errorf("pairing session ID is required")
	}

	m.mu.Lock()
	if existing, ok := m.sessions[req.SessionID]; ok {
		if !existing.status.IsTerminal() {
			status := existing.status
			m.mu.Unlock()
			return status, nil
		}
		delete(m.sessions, req.SessionID)
	}
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	session := &whatsAppPairingSession{
		id: req.SessionID,
		status: setuppairing.Status{
			Channel:     "whatsapp",
			SessionID:   req.SessionID,
			State:       setuppairing.StateWaiting,
			Phase:       "starting",
			Message:     "Preparing WhatsApp pairing...",
			StartedAt:   now,
			UpdatedAt:   now,
			PollAfterMs: whatsappPairingPollAfterMs,
		},
		cancel: cancel,
	}
	m.sessions[req.SessionID] = session
	m.mu.Unlock()

	logging.L_info("whatsapp: pairing started", "sessionID", req.SessionID, "surface", req.Surface)
	go m.run(ctx, session)
	return session.status, nil
}

func (m *whatsAppPairingManager) status(sessionID string) setuppairing.Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return setuppairing.Status{
			Channel:     "whatsapp",
			SessionID:   sessionID,
			State:       setuppairing.StateNotStarted,
			Phase:       "idle",
			Message:     "WhatsApp pairing has not started yet.",
			PollAfterMs: whatsappPairingPollAfterMs,
		}
	}
	return session.status
}

func (m *whatsAppPairingManager) cancel(sessionID string) setuppairing.Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return setuppairing.Status{
			Channel:     "whatsapp",
			SessionID:   sessionID,
			State:       setuppairing.StateCancelled,
			Phase:       "cancelled",
			Message:     "WhatsApp pairing was not running.",
			PollAfterMs: whatsappPairingPollAfterMs,
		}
	}

	session.cancel()
	session.status.State = setuppairing.StateCancelled
	session.status.Phase = "cancelled"
	session.status.Message = "WhatsApp pairing was cancelled."
	session.status.UpdatedAt = time.Now()
	return session.status
}

func (m *whatsAppPairingManager) run(ctx context.Context, session *whatsAppPairingSession) {
	logging.L_info("whatsapp: pairing worker started", "sessionID", session.id)
	dbPath, err := paths.DataPath("whatsapp.db")
	if err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to resolve WhatsApp database: %v", err))
		return
	}
	if err := paths.EnsureParentDir(dbPath); err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to create WhatsApp database directory: %v", err))
		return
	}
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to open WhatsApp database: %v", err))
		return
	}
	defer db.Close()

	storeLog := waLog.Logger(&goclawLogger{module: "pairing-store"})
	container := sqlstore.NewWithDB(db, "sqlite3", storeLog)
	if err := container.Upgrade(ctx); err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to upgrade WhatsApp store: %v", err))
		return
	}
	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to inspect existing WhatsApp devices: %v", err))
		return
	}
	for _, d := range devices {
		_ = d.Delete(ctx)
	}

	device := container.NewDevice()
	client := whatsmeow.NewClient(device, &goclawLogger{module: "pairing-client"})
	connected := make(chan struct{}, 1)
	client.AddEventHandler(func(evt interface{}) {
		switch evt.(type) {
		case *events.Connected:
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to get WhatsApp QR channel: %v", err))
		return
	}
	if err := client.Connect(); err != nil {
		m.fail(session.id, fmt.Sprintf("Failed to connect WhatsApp pairing client: %v", err))
		return
	}
	defer client.Disconnect()

	for {
		select {
		case <-ctx.Done():
			return
		case <-connected:
			jid := ""
			phone := ""
			if client.Store.ID != nil {
				jid = client.Store.ID.String()
				phone = client.Store.ID.User
			}
			logging.L_info("whatsapp: pairing completed", "sessionID", session.id, "jid", jid)
			m.setStatus(session.id, func(status *setuppairing.Status) {
				status.State = setuppairing.StatePaired
				status.Phase = "paired"
				status.Message = "WhatsApp owner pairing complete."
				status.UpdatedAt = time.Now()
				status.Identity = &setuppairing.Identity{
					Provider: "whatsapp",
					ID:       jid,
					JID:      jid,
					Phone:    phone,
				}
			})
			return
		case item, ok := <-qrChan:
			if !ok {
				m.fail(session.id, "WhatsApp pairing channel closed unexpectedly.")
				return
			}
			switch item.Event {
			case "code":
				logging.L_info("whatsapp: pairing qr issued", "sessionID", session.id)
				m.setStatus(session.id, func(status *setuppairing.Status) {
					status.State = setuppairing.StateWaiting
					status.Phase = "waiting_qr"
					status.Message = "Scan the QR code with WhatsApp on your phone."
					status.UpdatedAt = time.Now()
					status.Artifacts = &setuppairing.Artifacts{
						QRCode:  item.Code,
						QRLabel: "WhatsApp > Settings > Linked Devices > Link a Device",
					}
				})
			case "success":
				logging.L_info("whatsapp: pairing qr accepted", "sessionID", session.id)
				m.setStatus(session.id, func(status *setuppairing.Status) {
					status.State = setuppairing.StateWaiting
					status.Phase = "waiting_sync"
					status.Message = "QR accepted. Waiting for WhatsApp to finish syncing."
					status.UpdatedAt = time.Now()
				})
			case "timeout":
				logging.L_warn("whatsapp: pairing qr expired", "sessionID", session.id)
				m.setStatus(session.id, func(status *setuppairing.Status) {
					status.State = setuppairing.StateExpired
					status.Phase = "expired"
					status.Message = "WhatsApp QR code expired. Restart pairing to generate a new QR code."
					status.UpdatedAt = time.Now()
				})
				return
			default:
				m.fail(session.id, fmt.Sprintf("WhatsApp pairing failed: %s", item.Event))
				return
			}
		}
	}
}

func (m *whatsAppPairingManager) setStatus(sessionID string, update func(*setuppairing.Status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	if !ok {
		return
	}
	update(&session.status)
}

func (m *whatsAppPairingManager) fail(sessionID, message string) {
	m.setStatus(sessionID, func(status *setuppairing.Status) {
		status.State = setuppairing.StateFailed
		status.Phase = "failed"
		status.Message = message
		status.UpdatedAt = time.Now()
	})
	logging.L_warn("whatsapp: pairing failed", "sessionID", sessionID, "message", message)
}
