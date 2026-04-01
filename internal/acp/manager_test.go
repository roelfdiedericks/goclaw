package acp

import (
	"context"
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
