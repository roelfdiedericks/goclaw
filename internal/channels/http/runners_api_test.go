package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type runnersGatewayStub struct {
	runs   []delegatedrun.RunRecord
	events []delegatedrun.RunEvent

	mu        sync.Mutex
	sinceArgs []int64
	callCh    chan struct{}
}

func (s *runnersGatewayStub) RunAgent(ctx context.Context, req gateway.AgentRequest, events chan<- gateway.AgentEvent) error {
	close(events)
	return nil
}

func (s *runnersGatewayStub) AgentIdentity() *gwtypes.AgentIdentityConfig      { return nil }
func (s *runnersGatewayStub) SupervisionConfig() *gwtypes.SupervisionConfig    { return nil }
func (s *runnersGatewayStub) StopAllUserSessions(userID string) (int, error)   { return 0, nil }
func (s *runnersGatewayStub) RequestShutdown(userID string) error               { return nil }
func (s *runnersGatewayStub) GetDelegatedRun(runID string) (delegatedrun.RunRecord, bool) {
	return delegatedrun.RunRecord{}, false
}
func (s *runnersGatewayStub) CancelDelegatedRun(runID string) error { return nil }

func (s *runnersGatewayStub) ListDelegatedRuns() []delegatedrun.RunRecord {
	return s.runs
}

func (s *runnersGatewayStub) ListDelegatedRunEvents(sinceID int64, limit int) []delegatedrun.RunEvent {
	s.mu.Lock()
	s.sinceArgs = append(s.sinceArgs, sinceID)
	s.mu.Unlock()

	if s.callCh != nil {
		select {
		case s.callCh <- struct{}{}:
		default:
		}
	}

	out := make([]delegatedrun.RunEvent, 0, len(s.events))
	for _, ev := range s.events {
		if ev.ID > sinceID {
			out = append(out, ev)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *runnersGatewayStub) firstSinceArg() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sinceArgs) == 0 {
		return -1
	}
	return s.sinceArgs[0]
}

func TestHandleRunnersReturnsSnapshot(t *testing.T) {
	started := time.Unix(1710000000, 0).UTC()
	finished := started.Add(3 * time.Second)
	gw := &runnersGatewayStub{
		runs: []delegatedrun.RunRecord{
			{
				RunID:      "run-1",
				ParentRunID: "",
				RequesterType: "subagent",
				RequesterID: "http:chat-1",
				SessionKey: "subagent:abc",
				Purpose:    "research",
				State:      delegatedrun.RunStateCompleted,
				StartedAt:  started,
				FinishedAt: &finished,
				Result: delegatedrun.RunResult{
					FinalText: "done",
				},
			},
		},
	}

	s := &Server{channel: NewHTTPChannel(nil)}
	s.channel.server = s
	s.channel.SetGateway(gw)

	u := &user.User{ID: "owner", Role: user.RoleOwner}
	req := httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	req = req.WithContext(setUserInContext(req.Context(), u))
	w := httptest.NewRecorder()

	s.handleRunners(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	items, ok := resp["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one runner snapshot item, got %#v", resp["items"])
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected item object, got %#v", items[0])
	}
	if first["runId"] != "run-1" {
		t.Fatalf("expected runId run-1, got %#v", first["runId"])
	}
	result, ok := first["result"].(map[string]any)
	if !ok || result["finalText"] != "done" {
		t.Fatalf("expected result.finalText=done, got %#v", first["result"])
	}
}

func TestHandleRunnerEventsResumesFromSinceAndStreamsEvents(t *testing.T) {
	gw := &runnersGatewayStub{
		callCh: make(chan struct{}, 1),
		events: []delegatedrun.RunEvent{
			{
				ID:        11,
				RunID:     "run-1",
				EventType: "completed",
				Payload: map[string]any{
					"state": "completed",
				},
				Timestamp: time.Unix(1710000000, 0).UTC(),
			},
		},
	}
	s := &Server{channel: NewHTTPChannel(nil)}
	s.channel.server = s
	s.channel.SetGateway(gw)

	u := &user.User{ID: "owner", Role: user.RoleOwner}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/runners/events?since=10", nil)
	req.Header.Set("Last-Event-ID", "4")
	req = req.WithContext(setUserInContext(ctx, u))
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleRunnerEvents(w, req)
	}()

	select {
	case <-gw.callCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for first events poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for SSE handler shutdown")
	}

	if got := gw.firstSinceArg(); got != 10 {
		t.Fatalf("expected since query parameter to drive resume cursor (10), got %d", got)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: delegated.run.completed") {
		t.Fatalf("expected delegated completed event in SSE stream, body=%q", body)
	}
	if !strings.Contains(body, "\nid: 11\n") {
		t.Fatalf("expected streamed event id 11, body=%q", body)
	}
	if !strings.Contains(body, "\"runId\":\"run-1\"") {
		t.Fatalf("expected runId payload in SSE body, body=%q", body)
	}
}

