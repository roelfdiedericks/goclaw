package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

type fakeDriver struct{}

func (d *fakeDriver) ID() string { return "fake" }
func (d *fakeDriver) SupportsTransport(transportID string) bool {
	return transportID == "fake-transport"
}
func (d *fakeDriver) LaunchSpec(ctx context.Context, req LaunchSpecRequest) (LaunchSpec, error) {
	return LaunchSpec{Command: "fake", Args: []string{"acp"}}, nil
}

type fakeTransport struct {
	newCalled  bool
	loadCalled bool
	lastMode   string
	cancelled  bool
	closed     bool
	promptFn   func(context.Context, *SessionHandle, PromptRequest) (*PromptResult, error)
}

func (t *fakeTransport) ID() string { return "fake-transport" }
func (t *fakeTransport) NewSession(ctx context.Context, req NewSessionRequest) (*SessionHandle, error) {
	t.newCalled = true
	return &SessionHandle{
		SessionID: "sess-1",
		CWD:       req.CWD,
		Mode:      req.Mode,
		Transport: t.ID(),
		Driver:    req.Driver.ID(),
		runtime:   struct{}{},
	}, nil
}
func (t *fakeTransport) LoadSession(ctx context.Context, req LoadSessionRequest) (*SessionHandle, error) {
	t.loadCalled = true
	return &SessionHandle{
		SessionID: req.SessionID,
		CWD:       req.CWD,
		Mode:      req.Mode,
		Transport: t.ID(),
		Driver:    req.Driver.ID(),
		runtime:   struct{}{},
	}, nil
}
func (t *fakeTransport) SetMode(ctx context.Context, handle *SessionHandle, mode string) error {
	t.lastMode = mode
	handle.Mode = mode
	return nil
}
func (t *fakeTransport) Prompt(ctx context.Context, handle *SessionHandle, req PromptRequest) (*PromptResult, error) {
	if t.promptFn != nil {
		return t.promptFn(ctx, handle, req)
	}
	if req.OnEvent != nil {
		req.OnEvent(ACPEvent{Type: EventTextDelta, Payload: TextDeltaPayload{Text: "hello"}, Timestamp: time.Now()})
		req.OnEvent(ACPEvent{Type: EventTodoUpdate, Payload: TodoUpdatePayload{Todos: []TodoItem{{ID: "1", Content: "todo", Status: "pending"}}}, Timestamp: time.Now()})
	}
	if req.OnPermission != nil {
		decision, err := req.OnPermission(PermissionRequest{
			SessionID: "sess-1",
			ToolTitle: "git status",
			Options: []PermissionOption{
				{ID: "allow-once", Label: "Allow once"},
				{ID: "reject-once", Label: "Reject"},
			},
		})
		if err != nil {
			return nil, err
		}
		if decision != PermissionAllowOnce {
			return nil, fmt.Errorf("expected allow-once, got %s", decision)
		}
	}
	return &PromptResult{StopReason: "end_turn", FinalText: "hello"}, nil
}
func (t *fakeTransport) Cancel(ctx context.Context, handle *SessionHandle) error {
	t.cancelled = true
	return nil
}
func (t *fakeTransport) Close(ctx context.Context, handle *SessionHandle) error {
	t.closed = true
	return nil
}

func TestManagerAttachAndPrompt(t *testing.T) {
	mgr := &Manager{
		defaultCWD:  "/tmp",
		transports:  map[string]Transport{"fake-transport": &fakeTransport{}},
		drivers:     map[string]Driver{"fake": &fakeDriver{}},
		attachments: map[string]*attachment{},
	}
	u := &user.User{ID: "owner", Role: user.RoleOwner, ACPAllowed: true}

	info, err := mgr.Attach(context.Background(), AttachRequest{
		SessionKey: "primary",
		User:       u,
		DriverID:   "fake",
		Transport:  "fake-transport",
		CWD:        "/tmp/work",
		Mode:       "plan",
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if info.SessionID != "sess-1" {
		t.Fatalf("unexpected session id: %s", info.SessionID)
	}
	if !mgr.IsAttached("primary") {
		t.Fatalf("expected attached state")
	}

	res, err := mgr.Prompt(context.Background(), "primary", "hello", PromptOptions{})
	if err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if res.FinalText != "hello" {
		t.Fatalf("unexpected final text: %q", res.FinalText)
	}
	inspect, err := mgr.Inspect("primary")
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if inspect.BufferedEvents == 0 {
		t.Fatalf("expected buffered events")
	}
	if len(inspect.Todos) != 1 {
		t.Fatalf("expected todo update to be recorded")
	}
}

func TestManagerAttachDeniedByACPAllowed(t *testing.T) {
	mgr := &Manager{
		defaultCWD:  "/tmp",
		transports:  map[string]Transport{"fake-transport": &fakeTransport{}},
		drivers:     map[string]Driver{"fake": &fakeDriver{}},
		attachments: map[string]*attachment{},
	}
	u := &user.User{ID: "guest", Role: user.RoleGuest, ACPAllowed: false}
	_, err := mgr.Attach(context.Background(), AttachRequest{
		SessionKey: "user:guest",
		User:       u,
		DriverID:   "fake",
		Transport:  "fake-transport",
		CWD:        "/tmp/work",
	})
	if err == nil {
		t.Fatalf("expected ACP attach to be denied")
	}
}

func TestManagerPromptWaitsForInteractiveResponse(t *testing.T) {
	transport := &fakeTransport{}
	mgr := &Manager{
		defaultCWD:  "/tmp",
		transports:  map[string]Transport{"fake-transport": transport},
		drivers:     map[string]Driver{"fake": &fakeDriver{}},
		attachments: map[string]*attachment{},
	}
	u := &user.User{ID: "owner", Role: user.RoleOwner, ACPAllowed: true}
	if _, err := mgr.Attach(context.Background(), AttachRequest{
		SessionKey: "primary",
		User:       u,
		DriverID:   "fake",
		Transport:  "fake-transport",
		CWD:        "/tmp/work",
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	expected := string(BuildCursorAskQuestionAnsweredResponse([]QuestionAnswer{{
		QuestionID:        "q1",
		SelectedOptionIDs: []string{"a"},
	}}))
	transport.promptFn = func(ctx context.Context, handle *SessionHandle, req PromptRequest) (*PromptResult, error) {
		if req.OnInteractive == nil {
			t.Fatalf("expected interactive callback")
		}
		result, err := req.OnInteractive(ctx, ACPDriverExtensionPayload{
			Driver:       "cursor",
			Method:       "cursor/ask_question",
			Interactive:  true,
			SemanticKind: "interactive_question",
			ToolCallID:   "tool-q",
			Summary:      "Cursor asked a question",
			Payload:      json.RawMessage(`{"toolCallId":"tool-q"}`),
		})
		if err != nil {
			return nil, err
		}
		if got := string(result); got != expected {
			t.Fatalf("unexpected interactive response payload: %s", got)
		}
		return &PromptResult{StopReason: "end_turn", FinalText: "done"}, nil
	}

	doneCh := make(chan error, 1)
	go func() {
		_, err := mgr.Prompt(context.Background(), "primary", "hello", PromptOptions{})
		doneCh <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		info, err := mgr.Inspect("primary")
		if err != nil {
			t.Fatalf("inspect failed: %v", err)
		}
		if len(info.PendingRequests) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pending request")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := mgr.Respond("primary", ACPDriverExtensionResponse{
		Driver:          "cursor",
		Method:          "cursor/ask_question",
		ToolCallID:      "tool-q",
		ResponsePayload: json.RawMessage(expected),
	}); err != nil {
		t.Fatalf("respond failed: %v", err)
	}

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("prompt failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for prompt to finish")
	}
}

func TestManagerCancelPendingHandoffUnblocksPrompt(t *testing.T) {
	transport := &fakeTransport{}
	mgr := &Manager{
		defaultCWD:  "/tmp",
		transports:  map[string]Transport{"fake-transport": transport},
		drivers:     map[string]Driver{"fake": &fakeDriver{}},
		attachments: map[string]*attachment{},
	}
	u := &user.User{ID: "owner", Role: user.RoleOwner, ACPAllowed: true}
	if _, err := mgr.Attach(context.Background(), AttachRequest{
		SessionKey: "primary",
		User:       u,
		DriverID:   "fake",
		Transport:  "fake-transport",
		CWD:        "/tmp/work",
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	transport.promptFn = func(ctx context.Context, handle *SessionHandle, req PromptRequest) (*PromptResult, error) {
		if req.OnInteractive == nil {
			t.Fatalf("expected interactive callback")
		}
		_, err := req.OnInteractive(ctx, ACPDriverExtensionPayload{
			Driver:       "cursor",
			Method:       "cursor/ask_question",
			Interactive:  true,
			SemanticKind: "interactive_question",
			ToolCallID:   "tool-q",
			Summary:      "Cursor asked a question",
			Payload:      json.RawMessage(`{"toolCallId":"tool-q"}`),
		})
		return nil, err
	}

	doneCh := make(chan error, 1)
	go func() {
		_, err := mgr.Prompt(context.Background(), "primary", "hello", PromptOptions{})
		doneCh <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		info, err := mgr.Inspect("primary")
		if err != nil {
			t.Fatalf("inspect failed: %v", err)
		}
		if len(info.PendingRequests) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pending request")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancelled, err := mgr.CancelPendingHandoff(context.Background(), "primary")
	if err != nil {
		t.Fatalf("cancel pending handoff failed: %v", err)
	}
	if len(cancelled) != 1 {
		t.Fatalf("expected one cancelled request, got %d", len(cancelled))
	}
	if cancelled[0].ToolCallID != "tool-q" {
		t.Fatalf("unexpected cancelled tool call id: %q", cancelled[0].ToolCallID)
	}
	if !transport.cancelled {
		t.Fatalf("expected transport cancel to be called")
	}

	select {
	case err := <-doneCh:
		if err == nil {
			t.Fatalf("expected prompt to be cancelled")
		}
		if err != ErrPendingInteractiveHandoff {
			t.Fatalf("expected ErrPendingInteractiveHandoff, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for prompt to finish")
	}

	info, err := mgr.Inspect("primary")
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if info.PromptRunning {
		t.Fatalf("expected prompt to be stopped after handoff cancel")
	}
	if len(info.PendingRequests) != 0 {
		t.Fatalf("expected pending requests to be cleared")
	}
}

func TestManagerCancelPendingHandoffWithoutAttachmentIsNoop(t *testing.T) {
	mgr := &Manager{
		defaultCWD:  "/tmp",
		transports:  map[string]Transport{"fake-transport": &fakeTransport{}},
		drivers:     map[string]Driver{"fake": &fakeDriver{}},
		attachments: map[string]*attachment{},
	}
	cancelled, err := mgr.CancelPendingHandoff(context.Background(), "primary")
	if err != nil {
		t.Fatalf("expected no error for unattached session, got %v", err)
	}
	if len(cancelled) != 0 {
		t.Fatalf("expected no cancelled requests, got %d", len(cancelled))
	}
}
