package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	sesspkg "github.com/roelfdiedericks/goclaw/internal/session"
	toolsubagentspawn "github.com/roelfdiedericks/goclaw/internal/tools/subagent_spawn"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type delegatedAgentRunnerStub struct {
	sessions *sesspkg.Manager
}

func (s *delegatedAgentRunnerStub) RunAgent(ctx context.Context, req gateway.AgentRequest, events chan<- gateway.AgentEvent) error {
	defer close(events)

	events <- gateway.EventAgentStart{RunID: "run-http-test", Source: "http", SessionKey: sesspkg.PrimarySession}

	sessionKey := "user:" + req.User.ID
	if req.User != nil && req.User.IsOwner() {
		sessionKey = sesspkg.PrimarySession
	}
	toolCtx := types.WithSessionContext(ctx, &types.SessionContext{
		Channel:    req.Source,
		ChatID:     req.ChatID,
		SessionKey: sessionKey,
		User:       req.User,
	})

	tool := toolsubagentspawn.NewTool(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			callID := "delegated_return:" + strings.TrimSpace(runID)
			input, _ := json.Marshal(map[string]any{
				"action":  "return_to_requester",
				"runId":   runID,
				"source":  source,
				"session": sessionKey,
			})
			sess := s.sessions.Get(sessionKey)
			sess.AddToolUse(callID, "subagent_spawn", input, "", "")
			sess.AddToolResult(callID, strings.TrimSpace(message), nil, "")
			return nil
		},
		nil,
	)

	_, err := tool.Execute(toolCtx, json.RawMessage(`{
		"prompt":"http send integration",
		"resultMode":"return_to_requester",
		"dispatchOrder":"queue_first",
		"fallbackMode":"none"
	}`))
	if err != nil {
		return err
	}

	events <- gateway.EventAgentEnd{RunID: "run-http-test", FinalText: "spawned"}
	return nil
}

func (s *delegatedAgentRunnerStub) AgentIdentity() *gwtypes.AgentIdentityConfig { return nil }
func (s *delegatedAgentRunnerStub) SupervisionConfig() *gwtypes.SupervisionConfig { return nil }
func (s *delegatedAgentRunnerStub) StopAllUserSessions(userID string) (int, error) { return 0, nil }
func (s *delegatedAgentRunnerStub) RequestShutdown(userID string) error { return nil }
func (s *delegatedAgentRunnerStub) ListDelegatedRuns() []delegatedrun.RunRecord { return nil }
func (s *delegatedAgentRunnerStub) GetDelegatedRun(runID string) (delegatedrun.RunRecord, bool) {
	return delegatedrun.RunRecord{}, false
}
func (s *delegatedAgentRunnerStub) CancelDelegatedRun(runID string) error { return nil }
func (s *delegatedAgentRunnerStub) ListDelegatedRunEvents(sinceID int64, limit int) []delegatedrun.RunEvent {
	return nil
}

type delegatedCronGatewayStub struct{}

func (g *delegatedCronGatewayStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	defer close(events)
	events <- cron.AgentEndEvent{FinalText: "delegated complete"}
}
func (g *delegatedCronGatewayStub) GetOwnerUserID() string { return "owner" }
func (g *delegatedCronGatewayStub) InjectSystemEvent(ctx context.Context, text string) error { return nil }
func (g *delegatedCronGatewayStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return delivery.Report{}
}
func (g *delegatedCronGatewayStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	return delivery.Report{}
}
func (g *delegatedCronGatewayStub) HandoffCronResult(ctx context.Context, jobName, result string) error { return nil }

func TestHandleSendTriggersSubagentReturnToRequesterInSession(t *testing.T) {
	_ = cron.NewService(cron.NewStore("", ""), &delegatedCronGatewayStub{})
	cron.GetService().SetDelegatedRunsEnabled(true, "", delegatedrun.SpawnLimits{})

	server := &Server{
		channel: NewHTTPChannel(nil),
	}
	server.channel.server = server

	sessionID := "http-session-1"
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	server.channel.sessions = map[string]*SSESession{
		sessionID: {
			SessionID: sessionID,
			UserID:    u.ID,
			User:      u,
		},
	}

	sessionManager := sesspkg.NewManager()
	server.channel.SetGateway(&delegatedAgentRunnerStub{sessions: sessionManager})

	body := bytes.NewBufferString(`{"message":"please spawn delegated subagent"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/send", body)
	req = req.WithContext(setSessionInContext(setUserInContext(req.Context(), u), sessionID))
	w := httptest.NewRecorder()

	server.handleSend(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d body=%s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	var msgs []sesspkg.Message
	for time.Now().Before(deadline) {
		msgs = sessionManager.Get(sesspkg.PrimarySession).GetMessages()
		if len(msgs) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected synthetic tool_use/tool_result in requester session; got %d messages", len(msgs))
	}

	toolUse := msgs[len(msgs)-2]
	toolResult := msgs[len(msgs)-1]
	if toolUse.Role != "tool_use" || toolResult.Role != "tool_result" {
		t.Fatalf("expected trailing tool_use/tool_result, got %q/%q", toolUse.Role, toolResult.Role)
	}
	if !strings.HasPrefix(toolUse.ToolUseID, "delegated_return:") {
		t.Fatalf("expected delegated_return toolUseID, got %q", toolUse.ToolUseID)
	}
	if toolResult.ToolUseID != toolUse.ToolUseID {
		t.Fatalf("expected tool_result toolUseID %q, got %q", toolUse.ToolUseID, toolResult.ToolUseID)
	}
}

