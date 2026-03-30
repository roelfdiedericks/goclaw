package telegram

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	setuppairing "github.com/roelfdiedericks/goclaw/internal/setup/pairing"

	tele "gopkg.in/telebot.v4"
)

const (
	pairingComponent      = "telegram.pairing"
	pairingCodeLength     = 6
	pairingExpiry         = 10 * time.Minute
	defaultPollAfterMs    = 1500
	telegramUpdatesTimout = 20
)

var telegramPairings = newTelegramPairingManager()

type telegramPairingManager struct {
	mu          sync.Mutex
	sessions    map[string]*telegramPairingSession
	workers     map[string]*telegramPairingWorker
	runtimeBots map[string]*Bot
}

type telegramPairingSession struct {
	ID        string
	Token     string
	Code      string
	Status    setuppairing.Status
	CreatedAt time.Time
}

type telegramPairingWorker struct {
	token       string
	botUsername string
	ctx         context.Context
	cancel      context.CancelFunc
}

type telegramAPIError struct {
	Method      string
	StatusCode  int
	ErrorCode   int
	Description string
}

func (e *telegramAPIError) Error() string {
	parts := []string{fmt.Sprintf("telegram %s failed", e.Method)}
	if e.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("http=%d", e.StatusCode))
	}
	if e.ErrorCode > 0 {
		parts = append(parts, fmt.Sprintf("code=%d", e.ErrorCode))
	}
	if e.Description != "" {
		parts = append(parts, e.Description)
	}
	return strings.Join(parts, " ")
}

func newTelegramPairingManager() *telegramPairingManager {
	return &telegramPairingManager{
		sessions:    make(map[string]*telegramPairingSession),
		workers:     make(map[string]*telegramPairingWorker),
		runtimeBots: make(map[string]*Bot),
	}
}

// RegisterPairingCommands registers setup pairing handlers for Telegram.
func RegisterPairingCommands() {
	bus.RegisterCommand(pairingComponent, "start", handlePairingStart)
	bus.RegisterCommand(pairingComponent, "status", handlePairingStatus)
	bus.RegisterCommand(pairingComponent, "cancel", handlePairingCancel)
}

// UnregisterPairingCommands unregisters setup pairing handlers for Telegram.
func UnregisterPairingCommands() {
	bus.UnregisterComponent(pairingComponent)
}

func registerRuntimePairingBot(b *Bot) {
	if b == nil || b.config == nil || b.config.BotToken == "" {
		return
	}
	telegramPairings.mu.Lock()
	defer telegramPairings.mu.Unlock()
	telegramPairings.runtimeBots[b.config.BotToken] = b
}

func unregisterRuntimePairingBot(b *Bot) {
	if b == nil || b.config == nil || b.config.BotToken == "" {
		return
	}
	telegramPairings.mu.Lock()
	defer telegramPairings.mu.Unlock()
	delete(telegramPairings.runtimeBots, b.config.BotToken)
}

func tryHandlePairingMessage(token string, sender *tele.User, text string) bool {
	handled, _, _ := telegramPairings.tryComplete(token, sender, text)
	return handled
}

func handlePairingStart(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(setuppairing.TelegramStartRequest)
	if !ok {
		ptr, ok := cmd.Payload.(*setuppairing.TelegramStartRequest)
		if !ok || ptr == nil {
			return bus.CommandResult{Error: fmt.Errorf("invalid telegram pairing payload: %T", cmd.Payload), Message: "Invalid pairing request"}
		}
		req = *ptr
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return bus.CommandResult{Error: fmt.Errorf("session ID is required"), Message: "Pairing session ID is required"}
	}
	if strings.TrimSpace(req.BotToken) == "" {
		return bus.CommandResult{Error: fmt.Errorf("bot token is required"), Message: "Telegram bot token is required before pairing"}
	}

	status, err := telegramPairings.start(req)
	if err != nil {
		return bus.CommandResult{Error: err, Message: err.Error()}
	}
	return bus.CommandResult{Success: true, Message: status.Message, Data: status}
}

func handlePairingStatus(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(setuppairing.StatusRequest)
	if !ok {
		ptr, ok := cmd.Payload.(*setuppairing.StatusRequest)
		if !ok || ptr == nil {
			return bus.CommandResult{Error: fmt.Errorf("invalid telegram pairing payload: %T", cmd.Payload), Message: "Invalid pairing status request"}
		}
		req = *ptr
	}
	status := telegramPairings.status(req.SessionID)
	return bus.CommandResult{Success: true, Message: status.Message, Data: status}
}

func handlePairingCancel(cmd bus.Command) bus.CommandResult {
	req, ok := cmd.Payload.(setuppairing.CancelRequest)
	if !ok {
		ptr, ok := cmd.Payload.(*setuppairing.CancelRequest)
		if !ok || ptr == nil {
			return bus.CommandResult{Error: fmt.Errorf("invalid telegram pairing payload: %T", cmd.Payload), Message: "Invalid pairing cancel request"}
		}
		req = *ptr
	}
	status := telegramPairings.cancel(req.SessionID)
	return bus.CommandResult{Success: true, Message: status.Message, Data: status}
}

func (m *telegramPairingManager) start(req setuppairing.TelegramStartRequest) (setuppairing.Status, error) {
	botUsername, err := telegramconfig.TestToken(req.BotToken)
	if err != nil {
		return setuppairing.Status{}, fmt.Errorf("validate telegram bot token: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if existing, ok := m.sessions[req.SessionID]; ok {
		existing.Token = req.BotToken
		m.refreshExpiryLocked(existing, now)
		if existing.Status.State == setuppairing.StateWaiting {
			return existing.Status, nil
		}
		delete(m.sessions, req.SessionID)
	}

	code, err := randomPairingCode(pairingCodeLength)
	if err != nil {
		return setuppairing.Status{}, fmt.Errorf("generate telegram pairing code: %w", err)
	}

	status := setuppairing.Status{
		Channel:     "telegram",
		SessionID:   req.SessionID,
		State:       setuppairing.StateWaiting,
		Phase:       "waiting_code",
		Message:     "Send the one-time code to your Telegram bot from the owner account.",
		StartedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(pairingExpiry),
		PollAfterMs: defaultPollAfterMs,
		Artifacts: &setuppairing.Artifacts{
			Code: code,
		},
	}

	session := &telegramPairingSession{
		ID:        req.SessionID,
		Token:     req.BotToken,
		Code:      code,
		Status:    status,
		CreatedAt: now,
	}
	m.sessions[req.SessionID] = session

	if _, ok := m.runtimeBots[req.BotToken]; !ok {
		m.ensureWorkerLocked(req.BotToken, botUsername)
	}

	logging.L_info("telegram: pairing started", "sessionID", req.SessionID, "surface", req.Surface, "bot", "@"+botUsername)
	return session.Status, nil
}

func (m *telegramPairingManager) status(sessionID string) setuppairing.Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return setuppairing.Status{
			Channel:     "telegram",
			SessionID:   sessionID,
			State:       setuppairing.StateNotStarted,
			Phase:       "idle",
			Message:     "Telegram pairing has not started yet.",
			PollAfterMs: defaultPollAfterMs,
		}
	}

	m.refreshExpiryLocked(session, time.Now())
	return session.Status
}

func (m *telegramPairingManager) cancel(sessionID string) setuppairing.Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return setuppairing.Status{
			Channel:     "telegram",
			SessionID:   sessionID,
			State:       setuppairing.StateCancelled,
			Phase:       "cancelled",
			Message:     "Telegram pairing was not running.",
			PollAfterMs: defaultPollAfterMs,
		}
	}

	session.Status.State = setuppairing.StateCancelled
	session.Status.Phase = "cancelled"
	session.Status.Message = "Telegram pairing was cancelled."
	session.Status.UpdatedAt = time.Now()
	session.Status.PollAfterMs = defaultPollAfterMs
	m.stopWorkerIfIdleLocked(session.Token)
	return session.Status
}

func (m *telegramPairingManager) refreshExpiryLocked(session *telegramPairingSession, now time.Time) {
	if session == nil || session.Status.IsTerminal() {
		return
	}
	if !session.Status.ExpiresAt.IsZero() && now.After(session.Status.ExpiresAt) {
		session.Status.State = setuppairing.StateExpired
		session.Status.Phase = "expired"
		session.Status.Message = "Telegram pairing expired. Restart pairing to get a new one-time code."
		session.Status.UpdatedAt = now
		m.stopWorkerIfIdleLocked(session.Token)
	}
}

func (m *telegramPairingManager) tryComplete(token string, sender *tele.User, text string) (bool, string, setuppairing.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalized := strings.TrimSpace(text)
	now := time.Now()
	for _, session := range m.sessions {
		if session.Token != token {
			continue
		}
		m.refreshExpiryLocked(session, now)
		if session.Status.State != setuppairing.StateWaiting {
			continue
		}
		if normalized != session.Code {
			continue
		}

		displayName := strings.TrimSpace(strings.TrimSpace(sender.FirstName) + " " + strings.TrimSpace(sender.LastName))
		session.Status.State = setuppairing.StatePaired
		session.Status.Phase = "paired"
		session.Status.Message = "Telegram owner pairing complete."
		session.Status.UpdatedAt = now
		session.Status.Identity = &setuppairing.Identity{
			Provider:    "telegram",
			ID:          fmt.Sprintf("%d", sender.ID),
			Username:    sender.Username,
			DisplayName: displayName,
			FirstName:   sender.FirstName,
			LastName:    sender.LastName,
		}
		m.stopWorkerIfIdleLocked(token)
		logging.L_info("telegram: pairing completed", "sessionID", session.ID, "userID", sender.ID)
		return true, "Pairing successful. Return to GoClaw setup to continue.", session.Status
	}

	return false, "", setuppairing.Status{}
}

func (m *telegramPairingManager) ensureWorkerLocked(token string, botUsername string) {
	if worker, ok := m.workers[token]; ok {
		select {
		case <-worker.ctx.Done():
		default:
			return
		}
		delete(m.workers, token)
	}

	ctx, cancel := context.WithCancel(context.Background())
	worker := &telegramPairingWorker{token: token, botUsername: botUsername, ctx: ctx, cancel: cancel}
	m.workers[token] = worker

	go m.runWorker(worker)
}

func (m *telegramPairingManager) stopWorkerIfIdleLocked(token string) {
	if m.hasWaitingSessionLocked(token) {
		return
	}
	worker, ok := m.workers[token]
	if !ok {
		return
	}
	delete(m.workers, token)
	worker.cancel()
}

func (m *telegramPairingManager) hasWaitingSessionLocked(token string) bool {
	now := time.Now()
	for _, session := range m.sessions {
		if session.Token != token {
			continue
		}
		m.refreshExpiryLocked(session, now)
		if session.Status.State == setuppairing.StateWaiting {
			return true
		}
	}
	return false
}

func (m *telegramPairingManager) runWorker(worker *telegramPairingWorker) {
	offset := int64(0)
	logging.L_info("telegram: pairing worker started", "bot", "@"+worker.botUsername)
	for {
		if worker.ctx.Err() != nil {
			return
		}
		updates, err := telegramGetUpdates(worker.ctx, worker.token, offset)
		if err != nil {
			if worker.ctx.Err() != nil {
				return
			}
			logging.L_warn("telegram: pairing getUpdates failed", "bot", "@"+worker.botUsername, "error", err)
			if apiErr, ok := err.(*telegramAPIError); ok {
				if fatal, message := classifyTelegramPollingError(apiErr); fatal {
					m.failSessionsForToken(worker.token, message)
					m.mu.Lock()
					delete(m.workers, worker.token)
					m.mu.Unlock()
					worker.cancel()
					return
				}
				m.noteSessionsForToken(worker.token, fmt.Sprintf("Telegram polling error: %s. Retrying...", apiErr.Description))
			}
			select {
			case <-time.After(2 * time.Second):
			case <-worker.ctx.Done():
				return
			}
			continue
		}
		if len(updates) > 0 {
			logging.L_info("telegram: pairing updates received", "bot", "@"+worker.botUsername, "count", len(updates))
		}
		for _, update := range updates {
			offset = maxInt64(offset, update.UpdateID+1)
			if update.Message == nil || update.Message.Chat == nil || update.Message.Chat.Type != "private" {
				continue
			}
			if strings.TrimSpace(update.Message.Text) == "" {
				continue
			}
			logging.L_info("telegram: pairing message received",
				"bot", "@"+worker.botUsername,
				"userID", update.Message.From.ID,
				"username", update.Message.From.Username,
				"text", truncatePairingText(update.Message.Text),
			)
			user := &tele.User{
				ID:        update.Message.From.ID,
				Username:  update.Message.From.Username,
				FirstName: update.Message.From.FirstName,
				LastName:  update.Message.From.LastName,
			}
			handled, ack, _ := m.tryComplete(worker.token, user, update.Message.Text)
			if !handled {
				logging.L_info("telegram: pairing message ignored", "bot", "@"+worker.botUsername, "reason", "code mismatch")
			}
			if handled && ack != "" {
				if err := telegramSendMessage(worker.ctx, worker.token, update.Message.Chat.ID, ack); err != nil {
					logging.L_debug("telegram: pairing ack failed", "error", err)
				}
			}
		}

		m.mu.Lock()
		if !m.hasWaitingSessionLocked(worker.token) {
			delete(m.workers, worker.token)
			m.mu.Unlock()
			worker.cancel()
			return
		}
		m.mu.Unlock()
	}
}

func (m *telegramPairingManager) failSessionsForToken(token string, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, session := range m.sessions {
		if session.Token != token || session.Status.IsTerminal() {
			continue
		}
		session.Status.State = setuppairing.StateFailed
		session.Status.Phase = "failed"
		session.Status.Message = message
		session.Status.UpdatedAt = now
	}
}

func (m *telegramPairingManager) noteSessionsForToken(token string, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, session := range m.sessions {
		if session.Token != token || session.Status.IsTerminal() {
			continue
		}
		session.Status.Message = message
		session.Status.UpdatedAt = now
	}
}

func randomPairingCode(length int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, length)
	randBytes := make([]byte, length)
	if _, err := rand.Read(randBytes); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(randBytes[i])%len(alphabet)]
	}
	return string(buf), nil
}

type telegramUpdateResponse struct {
	OK          bool             `json:"ok"`
	Result      []telegramUpdate `json:"result"`
	ErrorCode   int              `json:"error_code"`
	Description string           `json:"description"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64            `json:"message_id"`
	Text      string           `json:"text"`
	Chat      *telegramChat    `json:"chat"`
	From      telegramFromUser `json:"from"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type telegramFromUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func telegramGetUpdates(ctx context.Context, token string, offset int64) ([]telegramUpdate, error) {
	values := url.Values{}
	values.Set("timeout", fmt.Sprintf("%d", telegramUpdatesTimout))
	if offset > 0 {
		values.Set("offset", fmt.Sprintf("%d", offset))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", token, values.Encode()), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var decoded telegramUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if !decoded.OK {
		return nil, &telegramAPIError{
			Method:      "getUpdates",
			StatusCode:  resp.StatusCode,
			ErrorCode:   decoded.ErrorCode,
			Description: decoded.Description,
		}
	}
	return decoded.Result, nil
}

func telegramSendMessage(ctx context.Context, token string, chatID int64, text string) error {
	values := url.Values{}
	values.Set("chat_id", fmt.Sprintf("%d", chatID))
	values.Set("text", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage failed with status %d", resp.StatusCode)
	}
	return nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func classifyTelegramPollingError(err *telegramAPIError) (bool, string) {
	if err == nil {
		return false, ""
	}
	desc := strings.ToLower(strings.TrimSpace(err.Description))
	switch {
	case strings.Contains(desc, "terminated by other getupdates request"):
		return true, "Telegram pairing cannot listen for messages because another process is already polling this bot. Stop the running GoClaw gateway or any other Telegram bot process, then restart pairing."
	case strings.Contains(desc, "can't use getupdates method while webhook is active"):
		return true, "Telegram pairing cannot listen for messages while a webhook is active for this bot. Remove the webhook for this bot, then restart pairing."
	case strings.Contains(desc, "unauthorized"), strings.Contains(desc, "not found"):
		return true, fmt.Sprintf("Telegram pairing could not start: %s", err.Description)
	default:
		return false, ""
	}
}

func truncatePairingText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 40 {
		return text
	}
	return text[:40] + "..."
}
