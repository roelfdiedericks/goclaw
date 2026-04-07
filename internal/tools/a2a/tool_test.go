package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	a2adomain "github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type backendStub struct {
	status        a2adomain.Status
	peers         []a2adomain.PeerRecord
	tasks         []a2adomain.TaskSummary
	pairing       a2adomain.PairingPayload
	pingResult    a2adomain.PingResult
	pingErr       error
	submitTaskID  string
	submitUpdates <-chan a2adomain.TaskSnapshot
	submitErr     error
	resumeUpdates <-chan a2adomain.TaskSnapshot
	resumeErr     error
	cancelResult  a2adomain.TaskSnapshot
	cancelErr     error
	lastPeer      string
	lastFilter    string
	lastTaskPeer  string
	lastMessage   string
	lastTaskID    string
}

func (b *backendStub) GetA2AStatus() a2adomain.Status {
	return b.status
}

func (b *backendStub) ListA2APeers(filter string) []a2adomain.PeerRecord {
	b.lastFilter = filter
	return b.peers
}

func (b *backendStub) ListA2ATasks(filter string, peer string) []a2adomain.TaskSummary {
	b.lastFilter = filter
	b.lastTaskPeer = peer
	return b.tasks
}

func (b *backendStub) GetA2APairingPayload() a2adomain.PairingPayload {
	return b.pairing
}

func (b *backendStub) PingA2APeer(ctx context.Context, target string) (a2adomain.PingResult, error) {
	b.lastPeer = target
	return b.pingResult, b.pingErr
}

func (b *backendStub) SubmitA2ATask(ctx context.Context, target string, input string) (string, <-chan a2adomain.TaskSnapshot, error) {
	b.lastPeer = target
	b.lastMessage = input
	return b.submitTaskID, b.submitUpdates, b.submitErr
}

func (b *backendStub) ResumeA2ATask(ctx context.Context, target string, taskID string) (<-chan a2adomain.TaskSnapshot, error) {
	b.lastPeer = target
	b.lastTaskID = taskID
	return b.resumeUpdates, b.resumeErr
}

func (b *backendStub) CancelA2ATask(ctx context.Context, target string, taskID string) (a2adomain.TaskSnapshot, error) {
	b.lastPeer = target
	b.lastTaskID = taskID
	return b.cancelResult, b.cancelErr
}

func ownerContext() context.Context {
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		User: &user.User{ID: "owner", Role: user.RoleOwner},
	})
}

func nonOwnerContext() context.Context {
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		User: &user.User{ID: "user", Role: user.RoleUser},
	})
}

func channelWithSnapshots(snapshots ...a2adomain.TaskSnapshot) <-chan a2adomain.TaskSnapshot {
	ch := make(chan a2adomain.TaskSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		ch <- snapshot
	}
	close(ch)
	return ch
}

func decodeToolJSON(t *testing.T, result *types.ToolResult) map[string]any {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("missing tool result content")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &decoded); err != nil {
		t.Fatalf("unmarshal tool result: %v\n%s", err, result.Content[0].Text)
	}
	return decoded
}

func TestA2AToolStatusPassthrough(t *testing.T) {
	tool := NewTool(&backendStub{
		status: a2adomain.Status{
			Enabled:         true,
			LifecycleState:  a2adomain.LifecycleStateRunning,
			LocalPeerID:     "peer-1",
			ConnectedPeers:  2,
			ActiveTransport: "libp2p",
		},
	})

	result, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"status"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	decoded := decodeToolJSON(t, result)
	if decoded["action"] != "status" {
		t.Fatalf("unexpected action: %#v", decoded["action"])
	}
	status := decoded["status"].(map[string]any)
	if status["localPeerId"] != "peer-1" {
		t.Fatalf("unexpected localPeerId: %#v", status["localPeerId"])
	}
}

func TestA2AToolPeersAndTasksPassthrough(t *testing.T) {
	backend := &backendStub{
		peers: []a2adomain.PeerRecord{{PeerID: "peer-1", Alias: "wsl"}},
		tasks: []a2adomain.TaskSummary{{TaskID: "task-1", PeerID: "peer-1"}},
	}
	tool := NewTool(backend)

	peersResult, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"peers","filter":"trusted"}`))
	if err != nil {
		t.Fatalf("peers Execute returned error: %v", err)
	}
	if backend.lastFilter != "trusted" {
		t.Fatalf("expected trusted filter, got %q", backend.lastFilter)
	}
	peersDecoded := decodeToolJSON(t, peersResult)
	if peersDecoded["filter"] != "trusted" {
		t.Fatalf("unexpected peers filter: %#v", peersDecoded["filter"])
	}

	tasksResult, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"tasks","peer":"wsl"}`))
	if err != nil {
		t.Fatalf("tasks Execute returned error: %v", err)
	}
	if backend.lastFilter != "all" {
		t.Fatalf("expected default all filter, got %q", backend.lastFilter)
	}
	if backend.lastTaskPeer != "wsl" {
		t.Fatalf("expected peer scope wsl, got %q", backend.lastTaskPeer)
	}
	tasksDecoded := decodeToolJSON(t, tasksResult)
	if tasksDecoded["peer"] != "wsl" {
		t.Fatalf("unexpected tasks peer: %#v", tasksDecoded["peer"])
	}
}

func TestA2AToolPingSuccessAndFailure(t *testing.T) {
	successTool := NewTool(&backendStub{
		pingResult: a2adomain.PingResult{
			PeerID:  "peer-1",
			Success: true,
			Latency: 25 * time.Millisecond,
			Message: "pong",
		},
	})

	result, err := successTool.Execute(ownerContext(), json.RawMessage(`{"action":"ping","peer":"wsl"}`))
	if err != nil {
		t.Fatalf("ping Execute returned error: %v", err)
	}
	decoded := decodeToolJSON(t, result)
	ping := decoded["result"].(map[string]any)
	if ping["latency"] != "25ms" {
		t.Fatalf("unexpected latency: %#v", ping["latency"])
	}

	failureTool := NewTool(&backendStub{pingErr: errors.New("a2a disabled")})
	if _, err := failureTool.Execute(ownerContext(), json.RawMessage(`{"action":"ping","peer":"wsl"}`)); err == nil || err.Error() != "a2a disabled" {
		t.Fatalf("expected ping error propagation, got %v", err)
	}
}

func TestA2AToolSubmitReturnsFinalSnapshot(t *testing.T) {
	backend := &backendStub{
		submitTaskID: "task-123",
		submitUpdates: channelWithSnapshots(
			a2adomain.TaskSnapshot{TaskID: "task-123", PeerID: "peer-1", State: a2adomain.TaskStateRunning},
			a2adomain.TaskSnapshot{TaskID: "task-123", PeerID: "peer-1", State: a2adomain.TaskStateCompleted, Content: "done", SessionKey: "a2a:1", ContextID: "ctx-1"},
		),
	}
	tool := NewTool(backend)

	result, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"submit","peer":"wsl","message":"hello"}`))
	if err != nil {
		t.Fatalf("submit Execute returned error: %v", err)
	}
	if backend.lastPeer != "wsl" || backend.lastMessage != "hello" {
		t.Fatalf("submit backend inputs not preserved: peer=%q message=%q", backend.lastPeer, backend.lastMessage)
	}
	decoded := decodeToolJSON(t, result)
	snapshot := decoded["snapshot"].(map[string]any)
	if decoded["taskId"] != "task-123" {
		t.Fatalf("unexpected taskId: %#v", decoded["taskId"])
	}
	if snapshot["state"] != string(a2adomain.TaskStateCompleted) {
		t.Fatalf("unexpected final state: %#v", snapshot["state"])
	}
	if snapshot["content"] != "done" {
		t.Fatalf("unexpected snapshot content: %#v", snapshot["content"])
	}
}

func TestA2AToolResumeReturnsFinalSnapshot(t *testing.T) {
	backend := &backendStub{
		resumeUpdates: channelWithSnapshots(
			a2adomain.TaskSnapshot{TaskID: "task-1", PeerID: "peer-1", State: a2adomain.TaskStateRunning},
			a2adomain.TaskSnapshot{TaskID: "task-1", PeerID: "peer-1", State: a2adomain.TaskStateCompleted, Content: "resumed"},
		),
	}
	tool := NewTool(backend)

	result, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"resume","peer":"wsl","taskId":"task-1"}`))
	if err != nil {
		t.Fatalf("resume Execute returned error: %v", err)
	}
	if backend.lastTaskID != "task-1" {
		t.Fatalf("unexpected resume task ID: %q", backend.lastTaskID)
	}
	decoded := decodeToolJSON(t, result)
	snapshot := decoded["snapshot"].(map[string]any)
	if snapshot["content"] != "resumed" {
		t.Fatalf("unexpected resumed content: %#v", snapshot["content"])
	}
}

func TestA2AToolCancelReturnsSnapshot(t *testing.T) {
	tool := NewTool(&backendStub{
		cancelResult: a2adomain.TaskSnapshot{TaskID: "task-1", PeerID: "peer-1", State: a2adomain.TaskStateCancelled},
	})

	result, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"cancel","peer":"wsl","taskId":"task-1"}`))
	if err != nil {
		t.Fatalf("cancel Execute returned error: %v", err)
	}
	decoded := decodeToolJSON(t, result)
	snapshot := decoded["snapshot"].(map[string]any)
	if snapshot["state"] != string(a2adomain.TaskStateCancelled) {
		t.Fatalf("unexpected cancel state: %#v", snapshot["state"])
	}
}

func TestA2AToolOwnerOnly(t *testing.T) {
	tool := NewTool(&backendStub{})
	if _, err := tool.Execute(nonOwnerContext(), json.RawMessage(`{"action":"status"}`)); err == nil || err.Error() != "A2A tool is owner-only" {
		t.Fatalf("expected owner-only error, got %v", err)
	}
}

func TestA2AToolMissingBackend(t *testing.T) {
	tool := NewTool(nil)
	if _, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"status"}`)); err == nil || err.Error() != "A2A tool backend not configured" {
		t.Fatalf("expected missing backend error, got %v", err)
	}
}

func TestA2AToolSubmitReturnsPartialSnapshotOnTimeout(t *testing.T) {
	ch := make(chan a2adomain.TaskSnapshot, 1)
	ch <- a2adomain.TaskSnapshot{TaskID: "task-1", PeerID: "peer-1", State: a2adomain.TaskStateRunning, SessionKey: "a2a:1"}
	backend := &backendStub{
		submitTaskID:  "task-1",
		submitUpdates: ch,
	}
	tool := NewTool(backend)

	result, err := tool.Execute(ownerContext(), json.RawMessage(`{"action":"submit","peer":"wsl","message":"hello","timeoutSeconds":1}`))
	if err != nil {
		t.Fatalf("submit Execute returned error: %v", err)
	}

	decoded := decodeToolJSON(t, result)
	snapshot := decoded["snapshot"].(map[string]any)
	if snapshot["taskId"] != "task-1" {
		t.Fatalf("unexpected taskId in partial snapshot: %#v", snapshot["taskId"])
	}
	if snapshot["error"] != context.DeadlineExceeded.Error() {
		t.Fatalf("unexpected partial error: %#v", snapshot["error"])
	}
}
