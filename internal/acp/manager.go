package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type attachment struct {
	info             AttachmentInfo
	handle           *SessionHandle
	eventBuffer      []ACPEvent
	bufferLimit      int
	activeCancel     context.CancelFunc
	promptDone       chan struct{}
	pendingRequests  map[string]*pendingInteractiveRequest
	recentExtensions []AttachmentExtensionInfo
	mu               sync.Mutex
}

type pendingInteractiveRequest struct {
	info       AttachmentPendingRequestInfo
	responseCh chan pendingInteractiveResult
}

type pendingInteractiveResult struct {
	payload json.RawMessage
	err     error
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
	ErrPendingInteractiveHandoff = errors.New("acp interactive request cancelled for handoff")
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
		handle:          handle,
		bufferLimit:     200,
		pendingRequests: map[string]*pendingInteractiveRequest{},
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
	var cancel context.CancelFunc
	var pending []pendingInteractiveRequest
	att.mu.Lock()
	att.info.Attached = false
	att.info.CurrentState = "detached"
	att.info.LastActivity = time.Now()
	att.info.PromptRunning = false
	cancel = att.activeCancel
	att.activeCancel = nil
	pending = att.collectAndClearPendingLocked()
	info := att.info
	info.PendingRequests = nil
	info.RecentExtensions = cloneAttachmentExtensionInfo(att.recentExtensions)
	att.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	failPendingRequests(pending, fmt.Errorf("ACP session detached"))
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
	var cancel context.CancelFunc
	var pending []pendingInteractiveRequest
	att.mu.Lock()
	cancel = att.activeCancel
	att.activeCancel = nil
	att.info.PromptRunning = false
	pending = att.collectAndClearPendingLocked()
	att.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	failPendingRequests(pending, fmt.Errorf("ACP session closed"))
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
	info.PendingRequests = att.pendingInfosLocked()
	info.RecentExtensions = cloneAttachmentExtensionInfo(att.recentExtensions)
	return &info, nil
}

func (m *Manager) Prompt(ctx context.Context, sessionKey string, text string, opts PromptOptions) (*PromptResult, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, fmt.Errorf("no ACP session attached")
	}

	promptCtx, cancel := context.WithCancel(ctx)
	promptDone := make(chan struct{})
	att.mu.Lock()
	if att.info.PromptRunning {
		att.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("ACP prompt already running for %s", sessionKey)
	}
	att.activeCancel = cancel
	att.promptDone = promptDone
	att.info.PromptRunning = true
	att.info.CurrentState = "running"
	att.info.LastActivity = time.Now()
	transportID := att.info.Transport
	handle := att.handle
	att.mu.Unlock()

	defer func() {
		cancel()
		att.mu.Lock()
		if att.promptDone == promptDone {
			att.promptDone = nil
		}
		att.activeCancel = nil
		att.info.PromptRunning = false
		if att.info.Attached {
			att.info.CurrentState = "idle"
		} else {
			att.info.CurrentState = "detached"
		}
		att.info.LastActivity = time.Now()
		att.mu.Unlock()
		close(promptDone)
	}()

	result, err := m.resolveTransport(transportID)
	if err != nil {
		return nil, err
	}

	res, err := result.Prompt(promptCtx, handle, PromptRequest{
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
		OnInteractive: func(waitCtx context.Context, payload ACPDriverExtensionPayload) (json.RawMessage, error) {
			return m.waitForInteractiveResponse(waitCtx, att, payload)
		},
	})
	if err != nil {
		att.mu.Lock()
		att.info.CurrentState = "error"
		att.info.LastActivity = time.Now()
		att.mu.Unlock()
		return nil, err
	}
	att.mu.Lock()
	att.info.LastAssistant = res.FinalText
	att.info.LastActivity = time.Now()
	att.mu.Unlock()
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
	att.mu.Lock()
	defer att.mu.Unlock()
	att.info.LastActivity = time.Now()
	switch ev.Type {
	case EventDriverExt:
		if payload, ok := ev.Payload.(ACPDriverExtensionPayload); ok {
			att.recordExtensionLocked(payload)
			switch payload.Method {
			case "cursor/update_todos":
				var todoPayload TodoUpdatePayload
				if err := json.Unmarshal(payload.Payload, &todoPayload); err == nil {
					m.applyTodoUpdateLocked(att, todoPayload)
				}
			case "cursor/create_plan":
				var planPayload PlanRequestPayload
				if err := json.Unmarshal(payload.Payload, &planPayload); err == nil {
					att.info.LastPlanName = planPayload.Name
					att.info.LastPlanOverview = planPayload.Overview
				}
			case "cursor/ask_question":
				var questionPayload QuestionPayload
				if err := json.Unmarshal(payload.Payload, &questionPayload); err == nil {
					att.info.LastQuestion = questionPayload.Title
					if len(questionPayload.Questions) > 0 && questionPayload.Questions[0].Prompt != "" {
						att.info.LastQuestion = questionPayload.Questions[0].Prompt
					}
				}
			}
		}
	case EventTodoUpdate:
		if payload, ok := ev.Payload.(TodoUpdatePayload); ok {
			m.applyTodoUpdateLocked(att, payload)
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
	transportID := att.info.Transport
	handle := att.handle
	att.mu.Unlock()
	transport, err := m.resolveTransport(transportID)
	if err != nil {
		return nil, err
	}
	if err := transport.SetMode(ctx, handle, mode); err != nil {
		return nil, err
	}
	att.mu.Lock()
	att.info.Mode = mode
	att.info.LastActivity = time.Now()
	info := att.info
	info.PendingRequests = att.pendingInfosLocked()
	info.RecentExtensions = cloneAttachmentExtensionInfo(att.recentExtensions)
	att.mu.Unlock()
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
	transportID := att.info.Transport
	handle := att.handle
	cancel := att.activeCancel
	promptDone := att.promptDone
	att.info.LastActivity = time.Now()
	pending := att.collectAndClearPendingLocked()
	att.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	failPendingRequests(pending, context.Canceled)
	transport, err := m.resolveTransport(transportID)
	if err != nil {
		return err
	}
	if err := transport.Cancel(ctx, handle); err != nil {
		return err
	}
	if promptDone != nil {
		select {
		case <-promptDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *Manager) PendingInteractiveRequests(sessionKey string) ([]AttachmentPendingRequestInfo, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, fmt.Errorf("no ACP session attached")
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	return att.pendingInfosLocked(), nil
}

func (m *Manager) CancelPendingHandoff(ctx context.Context, sessionKey string) ([]AttachmentPendingRequestInfo, error) {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return nil, nil
	}

	att.mu.Lock()
	if len(att.pendingRequests) == 0 {
		att.mu.Unlock()
		return nil, nil
	}
	transportID := att.info.Transport
	handle := att.handle
	promptDone := att.promptDone
	pendingReqs := att.collectAndClearPendingLocked()
	cancelled := make([]AttachmentPendingRequestInfo, 0, len(pendingReqs))
	for _, req := range pendingReqs {
		cancelled = append(cancelled, req.info)
	}
	att.info.LastActivity = time.Now()
	att.mu.Unlock()

	failPendingRequests(pendingReqs, ErrPendingInteractiveHandoff)

	transport, err := m.resolveTransport(transportID)
	if err != nil {
		return cancelled, err
	}
	if err := transport.Cancel(ctx, handle); err != nil {
		return cancelled, err
	}
	if promptDone != nil {
		select {
		case <-promptDone:
		case <-ctx.Done():
			return cancelled, ctx.Err()
		}
	}
	return cancelled, nil
}

func (m *Manager) Steer(ctx context.Context, sessionKey string, text string, opts PromptOptions) (*PromptResult, error) {
	return m.Prompt(ctx, sessionKey, text, opts)
}

func (m *Manager) Respond(sessionKey string, resp ACPDriverExtensionResponse) error {
	m.mu.RLock()
	att := m.attachments[sessionKey]
	m.mu.RUnlock()
	if att == nil {
		return fmt.Errorf("no ACP session attached")
	}
	key := interactiveRequestKey(resp.Driver, resp.Method, resp.ToolCallID)
	att.mu.Lock()
	pending, ok := att.pendingRequests[key]
	if !ok {
		att.mu.Unlock()
		return fmt.Errorf("no pending ACP interactive request for %s", key)
	}
	delete(att.pendingRequests, key)
	att.info.LastActivity = time.Now()
	att.mu.Unlock()
	select {
	case pending.responseCh <- pendingInteractiveResult{payload: cloneRawMessage(resp.ResponsePayload)}:
	default:
	}
	return nil
}

func (m *Manager) waitForInteractiveResponse(ctx context.Context, att *attachment, payload ACPDriverExtensionPayload) (json.RawMessage, error) {
	info := AttachmentPendingRequestInfo{
		Driver:       payload.Driver,
		Method:       payload.Method,
		Interactive:  payload.Interactive,
		SemanticKind: payload.SemanticKind,
		ToolCallID:   payload.ToolCallID,
		Summary:      payload.Summary,
		CreatedAt:    time.Now(),
	}
	key := interactiveRequestKey(payload.Driver, payload.Method, payload.ToolCallID)
	req := &pendingInteractiveRequest{
		info:       info,
		responseCh: make(chan pendingInteractiveResult, 1),
	}
	att.mu.Lock()
	if att.pendingRequests == nil {
		att.pendingRequests = map[string]*pendingInteractiveRequest{}
	}
	if _, exists := att.pendingRequests[key]; exists {
		att.mu.Unlock()
		return nil, fmt.Errorf("ACP interactive request already pending for %s", key)
	}
	att.pendingRequests[key] = req
	att.info.LastActivity = time.Now()
	att.mu.Unlock()

	defer func() {
		att.mu.Lock()
		if current, ok := att.pendingRequests[key]; ok && current == req {
			delete(att.pendingRequests, key)
		}
		att.info.LastActivity = time.Now()
		att.mu.Unlock()
	}()

	select {
	case result := <-req.responseCh:
		return cloneRawMessage(result.payload), result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) applyTodoUpdateLocked(att *attachment, payload TodoUpdatePayload) {
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
		return
	}
	att.info.Todos = append([]TodoItem(nil), payload.Todos...)
}

func (att *attachment) recordExtensionLocked(payload ACPDriverExtensionPayload) {
	att.recentExtensions = append(att.recentExtensions, AttachmentExtensionInfo{
		Driver:       payload.Driver,
		Method:       payload.Method,
		Interactive:  payload.Interactive,
		SemanticKind: payload.SemanticKind,
		ToolCallID:   payload.ToolCallID,
		Summary:      payload.Summary,
		ObservedAt:   time.Now(),
	})
	if len(att.recentExtensions) > 20 {
		att.recentExtensions = att.recentExtensions[len(att.recentExtensions)-20:]
	}
}

func (att *attachment) collectAndClearPendingLocked() []pendingInteractiveRequest {
	if len(att.pendingRequests) == 0 {
		return nil
	}
	out := make([]pendingInteractiveRequest, 0, len(att.pendingRequests))
	for key, req := range att.pendingRequests {
		out = append(out, *req)
		delete(att.pendingRequests, key)
	}
	return out
}

func (att *attachment) pendingInfosLocked() []AttachmentPendingRequestInfo {
	if len(att.pendingRequests) == 0 {
		return nil
	}
	out := make([]AttachmentPendingRequestInfo, 0, len(att.pendingRequests))
	for _, req := range att.pendingRequests {
		out = append(out, req.info)
	}
	return out
}

func cloneAttachmentExtensionInfo(items []AttachmentExtensionInfo) []AttachmentExtensionInfo {
	if len(items) == 0 {
		return nil
	}
	out := make([]AttachmentExtensionInfo, len(items))
	copy(out, items)
	return out
}

func interactiveRequestKey(driver, method, toolCallID string) string {
	driver = strings.TrimSpace(driver)
	method = strings.TrimSpace(method)
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return driver + "::" + method
	}
	return driver + "::" + method + "::" + toolCallID
}

func failPendingRequests(pending []pendingInteractiveRequest, err error) {
	for _, req := range pending {
		select {
		case req.responseCh <- pendingInteractiveResult{err: err}:
		default:
		}
	}
}
