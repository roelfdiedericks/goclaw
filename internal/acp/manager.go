package acp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type attachment struct {
	info         AttachmentInfo
	handle       *SessionHandle
	eventBuffer  []ACPEvent
	bufferLimit  int
	activeCancel context.CancelFunc
	mu           sync.Mutex
}

type Manager struct {
	defaultCWD  string
	transports  map[string]Transport
	drivers     map[string]Driver
	attachments map[string]*attachment
	mu          sync.RWMutex
}

var (
	globalManager *Manager
	managerMu     sync.Mutex
)

func InitManager(defaultCWD string) *Manager {
	managerMu.Lock()
	defer managerMu.Unlock()
	if globalManager != nil {
		if strings.TrimSpace(defaultCWD) != "" {
			globalManager.defaultCWD = defaultCWD
		}
		return globalManager
	}
	globalManager = &Manager{
		defaultCWD:  defaultCWD,
		transports:  map[string]Transport{TransportLocalStdio: NewLocalStdioTransport()},
		drivers:     map[string]Driver{DriverCursor: NewCursorDriver()},
		attachments: make(map[string]*attachment),
	}
	return globalManager
}

func GetManager() *Manager {
	return globalManager
}

func (m *Manager) resolveCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		cwd = m.defaultCWD
	}
	if strings.TrimSpace(cwd) == "" {
		return "", fmt.Errorf("cwd is required")
	}
	return filepath.Abs(cwd)
}

func (m *Manager) resolveDriver(id string) (Driver, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = DriverCursor
	}
	driver, ok := m.drivers[id]
	if !ok {
		return nil, fmt.Errorf("unknown ACP driver: %s", id)
	}
	return driver, nil
}

func (m *Manager) resolveTransport(id string) (Transport, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = TransportLocalStdio
	}
	transport, ok := m.transports[id]
	if !ok {
		return nil, fmt.Errorf("unknown ACP transport: %s", id)
	}
	return transport, nil
}

func (m *Manager) Attach(ctx context.Context, req AttachRequest) (*AttachmentInfo, error) {
	if req.User == nil || !req.User.CanUseACP() {
		return nil, fmt.Errorf("ACP is not allowed for this user")
	}
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		return nil, fmt.Errorf("session key is required")
	}
	cwd, err := m.resolveCWD(req.CWD)
	if err != nil {
		return nil, err
	}
	driver, err := m.resolveDriver(req.DriverID)
	if err != nil {
		return nil, err
	}
	transport, err := m.resolveTransport(req.Transport)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	existing := m.attachments[sessionKey]
	if existing != nil {
		if strings.TrimSpace(req.SessionID) == "" {
			if cwd != existing.info.CWD || (strings.TrimSpace(req.Mode) != "" && req.Mode != existing.info.Mode) || driver.ID() != existing.info.Driver {
				m.mu.Unlock()
				return nil, fmt.Errorf("ACP session already attached for %s; detach or close it before changing cwd/driver/mode", sessionKey)
			}
			existing.info.Attached = true
			existing.info.LastActivity = time.Now()
			info := existing.info
			m.mu.Unlock()
			return &info, nil
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("ACP session already attached for %s; close it before loading a different session", sessionKey)
	}
	m.mu.Unlock()

	var handle *SessionHandle
	if strings.TrimSpace(req.SessionID) != "" {
		handle, err = transport.LoadSession(ctx, LoadSessionRequest{
			Driver:    driver,
			SessionID: strings.TrimSpace(req.SessionID),
			CWD:       cwd,
			Mode:      req.Mode,
		})
	} else {
		handle, err = transport.NewSession(ctx, NewSessionRequest{
			Driver: driver,
			CWD:    cwd,
			Mode:   req.Mode,
		})
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	att := &attachment{
		info: AttachmentInfo{
			SessionKey:   sessionKey,
			UserID:       req.User.ID,
			Attached:     true,
			SessionID:    handle.SessionID,
			CWD:          handle.CWD,
			Mode:         handle.Mode,
			Transport:    transport.ID(),
			Driver:       driver.ID(),
			LastActivity: now,
			CurrentState: "idle",
		},
		handle:      handle,
		bufferLimit: 200,
	}

	m.mu.Lock()
	m.attachments[sessionKey] = att
	m.mu.Unlock()
	L_info("acp: attached session", "sessionKey", sessionKey, "driver", att.info.Driver, "transport", att.info.Transport, "cwd", att.info.CWD, "sessionID", att.info.SessionID)
	info := att.info
	return &info, nil
}

func (m *Manager) IsAttached(sessionKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	att, ok := m.attachments[sessionKey]
	return ok && att.info.Attached
}

func (m *Manager) Detach(sessionKey string) (*AttachmentInfo, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, fmt.Errorf("no ACP session attached")
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	att.info.Attached = false
	att.info.CurrentState = "detached"
	att.info.LastActivity = time.Now()
	info := att.info
	return &info, nil
}

func (m *Manager) Close(ctx context.Context, sessionKey string) error {
	m.mu.Lock()
	att := m.attachments[sessionKey]
	if att != nil {
		delete(m.attachments, sessionKey)
	}
	m.mu.Unlock()
	if att == nil {
		return nil
	}
	if att.activeCancel != nil {
		att.activeCancel()
	}
	if transport, err := m.resolveTransport(att.info.Transport); err == nil {
		if err := transport.Close(ctx, att.handle); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Inspect(sessionKey string) (*AttachmentInfo, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, fmt.Errorf("no ACP session attached")
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	info := att.info
	info.BufferedEvents = len(att.eventBuffer)
	return &info, nil
}

func (m *Manager) Prompt(ctx context.Context, sessionKey string, text string, opts PromptOptions) (*PromptResult, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, fmt.Errorf("no ACP session attached")
	}

	att.mu.Lock()
	defer att.mu.Unlock()
	promptCtx, cancel := context.WithCancel(ctx)
	att.activeCancel = cancel
	att.info.CurrentState = "running"
	att.info.LastActivity = time.Now()
	defer func() {
		att.activeCancel = nil
		if att.info.Attached {
			att.info.CurrentState = "idle"
		} else {
			att.info.CurrentState = "detached"
		}
		att.info.LastActivity = time.Now()
		cancel()
	}()

	result, err := m.resolveTransport(att.info.Transport)
	if err != nil {
		return nil, err
	}

	res, err := result.Prompt(promptCtx, att.handle, PromptRequest{
		Text: text,
		OnEvent: func(ev ACPEvent) {
			m.recordEvent(att, ev)
			if opts.OnEvent != nil {
				opts.OnEvent(ev)
			}
		},
		OnPermission: func(req PermissionRequest) (PermissionDecision, error) {
			return m.defaultPermissionDecision(req), nil
		},
	})
	if err != nil {
		att.info.CurrentState = "error"
		return nil, err
	}
	att.info.LastAssistant = res.FinalText
	return res, nil
}

func (m *Manager) defaultPermissionDecision(req PermissionRequest) PermissionDecision {
	for _, opt := range req.Options {
		if opt.ID == string(PermissionAllowOnce) {
			return PermissionAllowOnce
		}
	}
	for _, opt := range req.Options {
		if strings.Contains(opt.ID, "skip") {
			return PermissionDecision(opt.ID)
		}
	}
	if len(req.Options) > 0 {
		return PermissionDecision(req.Options[0].ID)
	}
	return PermissionRejectOnce
}

func (m *Manager) recordEvent(att *attachment, ev ACPEvent) {
	att.info.LastActivity = time.Now()
	switch ev.Type {
	case EventTodoUpdate:
		if payload, ok := ev.Payload.(TodoUpdatePayload); ok {
			if payload.Merge {
				existing := map[string]int{}
				for i, item := range att.info.Todos {
					existing[item.ID] = i
				}
				for _, item := range payload.Todos {
					if idx, ok := existing[item.ID]; ok {
						att.info.Todos[idx] = item
					} else {
						att.info.Todos = append(att.info.Todos, item)
					}
				}
			} else {
				att.info.Todos = append([]TodoItem(nil), payload.Todos...)
			}
		}
	case EventPlanRequest:
		if payload, ok := ev.Payload.(PlanRequestPayload); ok {
			att.info.LastPlanName = payload.Name
			att.info.LastPlanOverview = payload.Overview
		}
	case EventQuestion:
		if payload, ok := ev.Payload.(QuestionPayload); ok {
			att.info.LastQuestion = payload.Title
			if len(payload.Questions) > 0 && payload.Questions[0].Prompt != "" {
				att.info.LastQuestion = payload.Questions[0].Prompt
			}
		}
	case EventTextDelta:
		if payload, ok := ev.Payload.(TextDeltaPayload); ok {
			att.info.LastAssistant += payload.Text
		}
	case EventStatus:
		if payload, ok := ev.Payload.(StatusPayload); ok {
			att.info.CurrentState = payload.Message
		}
	}
	att.eventBuffer = append(att.eventBuffer, ev)
	if len(att.eventBuffer) > att.bufferLimit {
		att.eventBuffer = att.eventBuffer[len(att.eventBuffer)-att.bufferLimit:]
	}
}

func (m *Manager) SetMode(ctx context.Context, sessionKey, mode string) (*AttachmentInfo, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, fmt.Errorf("no ACP session attached")
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	transport, err := m.resolveTransport(att.info.Transport)
	if err != nil {
		return nil, err
	}
	if err := transport.SetMode(ctx, att.handle, mode); err != nil {
		return nil, err
	}
	att.info.Mode = mode
	att.info.LastActivity = time.Now()
	info := att.info
	return &info, nil
}

func (m *Manager) Cancel(ctx context.Context, sessionKey string) error {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return fmt.Errorf("no ACP session attached")
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	transport, err := m.resolveTransport(att.info.Transport)
	if err != nil {
		return err
	}
	if err := transport.Cancel(ctx, att.handle); err != nil {
		return err
	}
	att.info.LastActivity = time.Now()
	return nil
}

func (m *Manager) Steer(ctx context.Context, sessionKey string, text string, opts PromptOptions) (*PromptResult, error) {
	return m.Prompt(ctx, sessionKey, text, opts)
}
