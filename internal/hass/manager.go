package hass

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Manager handles persistent WebSocket connections for event subscriptions.
// It manages the connection lifecycle, event filtering, debouncing, and
// event injection into the agent's session.
type Manager struct {
	cfg           config.HomeAssistantConfig
	injector      types.EventInjector
	dataDir       string
	subscriptions map[string]*Subscription
	debounce      map[string]time.Time // "entity_id:state" -> last fired
	conn          *websocket.Conn
	msgID         int
	subscriptionID int // HA subscription ID for state_changed
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	connected     bool
	reconnecting  bool
}

// NewManager creates a new HASS event subscription manager.
func NewManager(cfg config.HomeAssistantConfig, injector types.EventInjector, dataDir string) *Manager {
	return &Manager{
		cfg:           cfg,
		injector:      injector,
		dataDir:       dataDir,
		subscriptions: make(map[string]*Subscription),
		debounce:      make(map[string]time.Time),
	}
}

// Start loads persisted subscriptions and connects if any exist.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Load persisted subscriptions
	path := m.getSubscriptionPath()
	subs, err := LoadSubscriptions(path)
	if err != nil {
		L_warn("hass: failed to load subscriptions", "error", err)
	}

	m.mu.Lock()
	for i := range subs {
		sub := subs[i]
		m.subscriptions[sub.ID] = &sub
		L_debug("hass: loaded subscription", "id", sub.ID, "pattern", sub.Pattern, "regex", sub.Regex, "debounce", sub.DebounceSeconds, "wake", sub.Wake)
	}
	m.mu.Unlock()

	L_info("hass: manager started", "subscriptions", len(subs), "path", path)

	// If we have subscriptions, connect
	if len(subs) > 0 {
		L_debug("hass: starting connection loop for persisted subscriptions")
		go m.connectLoop()
	}

	return nil
}

// Stop gracefully shuts down the manager.
func (m *Manager) Stop() {
	L_info("hass: manager stopping")
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}
	m.connected = false
	m.mu.Unlock()

	m.wg.Wait()
	L_info("hass: manager stopped")
}

// Subscribe adds a new subscription and persists it.
// If not connected, attempts to connect in the background.
func (m *Manager) Subscribe(sub Subscription) error {
	m.mu.Lock()
	m.subscriptions[sub.ID] = &sub
	needsConnect := !m.connected && !m.reconnecting
	m.mu.Unlock()

	// Persist
	if err := m.saveSubscriptions(); err != nil {
		L_error("hass: failed to save subscriptions", "error", err)
		return err
	}

	L_info("hass: subscription added", "id", sub.ID, "pattern", sub.Pattern, "regex", sub.Regex, "debounce", sub.DebounceSeconds, "wake", sub.Wake, "full", sub.Full)

	// Start connection if needed
	if needsConnect {
		L_debug("hass: starting connection loop for new subscription")
		go m.connectLoop()
	} else {
		L_debug("hass: subscription added to existing connection", "connected", m.connected, "reconnecting", m.reconnecting)
	}

	return nil
}

// Unsubscribe removes a subscription by ID.
func (m *Manager) Unsubscribe(id string) error {
	m.mu.Lock()
	_, exists := m.subscriptions[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("subscription not found: %s", id)
	}
	delete(m.subscriptions, id)
	remaining := len(m.subscriptions)
	m.mu.Unlock()

	// Persist
	if err := m.saveSubscriptions(); err != nil {
		L_error("hass: failed to save subscriptions", "error", err)
		return err
	}

	L_info("hass: subscription removed", "id", id, "remaining", remaining)

	// If no subscriptions left, disconnect
	if remaining == 0 {
		m.mu.Lock()
		if m.conn != nil {
			m.conn.Close()
			m.conn = nil
		}
		m.connected = false
		m.mu.Unlock()
	}

	return nil
}

// GetSubscriptions returns all active subscriptions.
func (m *Manager) GetSubscriptions() []Subscription {
	m.mu.RLock()
	defer m.mu.RUnlock()

	subs := make([]Subscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, *sub)
	}
	return subs
}

// IsConnected returns whether the WebSocket is currently connected.
func (m *Manager) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// connectLoop handles connection with exponential backoff.
func (m *Manager) connectLoop() {
	m.mu.Lock()
	if m.reconnecting {
		L_debug("hass: connect loop already running, skipping")
		m.mu.Unlock()
		return
	}
	m.reconnecting = true
	m.mu.Unlock()

	m.wg.Add(1)
	defer m.wg.Done()

	// Parse reconnect delay
	reconnectDelay := 5 * time.Second
	if m.cfg.ReconnectDelay != "" {
		if d, err := time.ParseDuration(m.cfg.ReconnectDelay); err == nil {
			reconnectDelay = d
		}
	}

	delay := reconnectDelay
	maxDelay := 5 * time.Minute

	L_debug("hass: connect loop started", "reconnectDelay", reconnectDelay, "maxDelay", maxDelay)

	for {
		select {
		case <-m.ctx.Done():
			L_debug("hass: connect loop cancelled")
			m.mu.Lock()
			m.reconnecting = false
			m.mu.Unlock()
			return
		default:
		}

		// Check if we still have subscriptions
		m.mu.RLock()
		subCount := len(m.subscriptions)
		m.mu.RUnlock()
		if subCount == 0 {
			L_debug("hass: no subscriptions, stopping connect loop")
			m.mu.Lock()
			m.reconnecting = false
			m.mu.Unlock()
			return
		}

		L_debug("hass: attempting connection", "subscriptions", subCount)

		// Attempt connection
		if err := m.connect(); err != nil {
			L_warn("hass: connection failed, retrying", "error", err, "delay", delay, "subscriptions", subCount)

			select {
			case <-time.After(delay):
				// Exponential backoff
				delay = delay * 2
				if delay > maxDelay {
					delay = maxDelay
				}
				L_debug("hass: backoff delay completed, next delay", "nextDelay", delay)
			case <-m.ctx.Done():
				L_debug("hass: connect loop cancelled during backoff")
				m.mu.Lock()
				m.reconnecting = false
				m.mu.Unlock()
				return
			}
			continue
		}

		// Reset delay on successful connection
		delay = reconnectDelay
		L_debug("hass: connection established, starting read loop")

		// Run the read loop
		m.readLoop()

		// If we get here, connection was lost
		m.mu.Lock()
		m.connected = false
		if m.conn != nil {
			m.conn.Close()
			m.conn = nil
		}
		m.mu.Unlock()

		L_debug("hass: read loop exited, connection lost")

		// Check if context is done before retrying
		select {
		case <-m.ctx.Done():
			L_debug("hass: connect loop cancelled after disconnect")
			m.mu.Lock()
			m.reconnecting = false
			m.mu.Unlock()
			return
		default:
			L_info("hass: connection lost, reconnecting", "delay", delay)
		}
	}
}

// connect establishes the WebSocket connection and authenticates.
func (m *Manager) connect() error {
	wsURL := m.buildWebSocketURL()
	L_debug("hass: connecting to websocket", "url", wsURL, "insecure", m.cfg.Insecure)

	// Configure dialer
	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}
	if m.cfg.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Connect
	conn, _, err := dialer.DialContext(m.ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	L_debug("hass: websocket connected, waiting for auth_required")

	// Read auth_required
	var authReq HAMessage
	if err := conn.ReadJSON(&authReq); err != nil {
		conn.Close()
		return fmt.Errorf("read auth_required: %w", err)
	}
	if authReq.Type != "auth_required" {
		conn.Close()
		return fmt.Errorf("unexpected message: %s", authReq.Type)
	}
	L_debug("hass: received auth_required, sending auth")

	// Send auth
	authMsg := HAAuthMessage{
		Type:        "auth",
		AccessToken: m.cfg.Token,
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		conn.Close()
		return fmt.Errorf("send auth: %w", err)
	}

	// Read auth result
	var authResult HAMessage
	if err := conn.ReadJSON(&authResult); err != nil {
		conn.Close()
		return fmt.Errorf("read auth result: %w", err)
	}
	if authResult.Type != "auth_ok" {
		conn.Close()
		return fmt.Errorf("auth failed: %s", authResult.Type)
	}
	L_debug("hass: authentication successful")

	// Subscribe to state_changed events
	m.mu.Lock()
	m.msgID++
	subMsgID := m.msgID
	m.mu.Unlock()

	subMsg := HASubscribeMessage{
		ID:        subMsgID,
		Type:      "subscribe_events",
		EventType: "state_changed",
	}
	L_debug("hass: subscribing to state_changed events", "msgID", subMsgID)
	if err := conn.WriteJSON(subMsg); err != nil {
		conn.Close()
		return fmt.Errorf("send subscribe: %w", err)
	}

	// Read subscribe result
	var subResult HAMessage
	if err := conn.ReadJSON(&subResult); err != nil {
		conn.Close()
		return fmt.Errorf("read subscribe result: %w", err)
	}
	if subResult.Success != nil && !*subResult.Success {
		conn.Close()
		errMsg := "unknown"
		if subResult.Error != nil {
			errMsg = subResult.Error.Message
		}
		return fmt.Errorf("subscribe failed: %s", errMsg)
	}

	m.mu.Lock()
	m.conn = conn
	m.connected = true
	m.subscriptionID = subMsgID
	m.mu.Unlock()

	L_info("hass: connected and subscribed to state_changed events")
	return nil
}

// readLoop reads messages from the WebSocket.
func (m *Manager) readLoop() {
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		m.mu.RLock()
		conn := m.conn
		m.mu.RUnlock()

		if conn == nil {
			return
		}

		var msg HAMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				L_warn("hass: websocket read error", "error", err)
			}
			return
		}

		// Handle event
		if msg.Type == "event" && msg.Event != nil {
			m.handleEvent(msg.Event)
		}
	}
}

// handleEvent processes a state_changed event.
func (m *Manager) handleEvent(event *HAEvent) {
	if event.EventType != "state_changed" {
		L_trace("hass: ignoring non-state_changed event", "type", event.EventType)
		return
	}

	entityID := event.Data.EntityID
	if entityID == "" {
		L_trace("hass: ignoring event with empty entity_id")
		return
	}

	// Get old and new state
	oldState := ""
	newState := ""
	if event.Data.OldState != nil {
		oldState = event.Data.OldState.State
	}
	if event.Data.NewState != nil {
		newState = event.Data.NewState.State
	}

	L_trace("hass: event received", "entity", entityID, "oldState", oldState, "newState", newState)

	// Find matching subscriptions
	m.mu.RLock()
	subCount := len(m.subscriptions)
	var matches []*Subscription
	for _, sub := range m.subscriptions {
		if MatchSubscription(sub, entityID) {
			matches = append(matches, sub)
		}
	}
	m.mu.RUnlock()

	if len(matches) == 0 {
		L_trace("hass: no matching subscriptions", "entity", entityID, "subscriptionCount", subCount)
		return
	}

	L_debug("hass: event matched", "entity", entityID, "oldState", oldState, "newState", newState, "matchCount", len(matches), "subscriptionCount", subCount)

	// Process each matching subscription
	for _, sub := range matches {
		m.processMatch(sub, event, entityID, newState)
	}
}

// processMatch handles a subscription match with debouncing.
func (m *Manager) processMatch(sub *Subscription, event *HAEvent, entityID, newState string) {
	// Debounce key is entity_id:state
	debounceKey := entityID + ":" + newState
	now := time.Now()

	m.mu.Lock()
	lastFired, exists := m.debounce[debounceKey]
	debounceSeconds := sub.DebounceSeconds
	if debounceSeconds <= 0 {
		debounceSeconds = 5 // Default
	}

	if exists && now.Sub(lastFired) < time.Duration(debounceSeconds)*time.Second {
		sinceLast := now.Sub(lastFired)
		m.mu.Unlock()
		L_debug("hass: event debounced", "entity", entityID, "state", newState, "subID", sub.ID, "sinceLast", sinceLast.String(), "debounceWindow", debounceSeconds)
		return
	}

	m.debounce[debounceKey] = now
	m.mu.Unlock()

	// Format the event message
	message := m.formatEvent(sub, event)

	L_debug("hass: processing matched event", "entity", entityID, "state", newState, "subID", sub.ID, "pattern", sub.Pattern, "regex", sub.Regex, "wake", sub.Wake, "full", sub.Full)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if sub.Wake {
		// wake=true: Invoke the agent immediately with the event as a prompt
		// Wrap with instructions for agent processing
		prompt := message + "\n\nProcess this event. Reply EVENT_OK if no action needed."

		L_debug("hass: invoking agent for wake event", "entity", entityID, "subID", sub.ID, "promptLen", len(prompt))

		if err := m.injector.InvokeAgent(ctx, prompt); err != nil {
			L_error("hass: failed to invoke agent", "error", err, "entity", entityID, "subID", sub.ID)
			return
		}

		L_debug("hass: agent invocation started", "entity", entityID, "state", newState, "subID", sub.ID)
	} else {
		// wake=false: Inject as system event, agent sees it on next user interaction
		L_debug("hass: injecting passive event", "entity", entityID, "subID", sub.ID, "messageLen", len(message))

		if err := m.injector.InjectSystemEvent(ctx, message); err != nil {
			L_error("hass: failed to inject event", "error", err, "entity", entityID, "subID", sub.ID)
			return
		}

		L_debug("hass: event injected successfully", "entity", entityID, "state", newState, "subID", sub.ID)
	}
}

// formatEvent creates the message text for injection.
func (m *Manager) formatEvent(sub *Subscription, event *HAEvent) string {
	// Determine prefix
	prefix := m.cfg.EventPrefix
	if prefix == "" {
		prefix = "[HomeAssistant Event]"
	}
	if sub.Prefix != "" {
		prefix = sub.Prefix
	}

	var payload interface{}
	if sub.Full {
		// Full format
		payload = FullEventPayload{
			EntityID:  event.Data.EntityID,
			NewState:  event.Data.NewState,
			OldState:  event.Data.OldState,
			TimeFired: event.TimeFired,
		}
	} else {
		// Brief format
		oldState := ""
		if event.Data.OldState != nil {
			oldState = event.Data.OldState.State
		}

		friendlyName := ""
		if event.Data.NewState != nil && event.Data.NewState.Attributes != nil {
			if fn, ok := event.Data.NewState.Attributes["friendly_name"].(string); ok {
				friendlyName = fn
			}
		}

		state := ""
		if event.Data.NewState != nil {
			state = event.Data.NewState.State
		}

		payload = BriefEventPayload{
			EntityID:     event.Data.EntityID,
			State:        state,
			OldState:     oldState,
			FriendlyName: friendlyName,
			TimeFired:    event.TimeFired,
		}
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		L_error("hass: failed to marshal event payload", "error", err)
		return prefix + " [error formatting event]"
	}

	return prefix + " " + string(jsonData)
}

// saveSubscriptions persists all subscriptions to disk.
func (m *Manager) saveSubscriptions() error {
	m.mu.RLock()
	subs := make([]Subscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, *sub)
	}
	m.mu.RUnlock()

	return SaveSubscriptions(m.getSubscriptionPath(), subs)
}

// getSubscriptionPath returns the path to the subscription file.
func (m *Manager) getSubscriptionPath() string {
	filename := m.cfg.SubscriptionFile
	if filename == "" {
		filename = "hass-subscriptions.json"
	}
	return filepath.Join(m.dataDir, filename)
}

// buildWebSocketURL converts the REST URL to a WebSocket URL.
func (m *Manager) buildWebSocketURL() string {
	url := m.cfg.URL
	url = strings.TrimSuffix(url, "/")

	if strings.HasPrefix(url, "https://") {
		url = "wss://" + strings.TrimPrefix(url, "https://")
	} else if strings.HasPrefix(url, "http://") {
		url = "ws://" + strings.TrimPrefix(url, "http://")
	}

	return url + "/api/websocket"
}
