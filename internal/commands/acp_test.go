package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/session"
)

type acpProviderStub struct {
	attachCalled bool
	attachDriver string
	attachCWD    string
	attachMode   string
	models       []acp.ACPModelOption
	setModel     string
	steerText    string
	steerStay    bool
}

func (s *acpProviderStub) GetSessionInfoForCommands(ctx context.Context, sessionKey string) (*SessionInfo, error) {
	return &SessionInfo{}, nil
}
func (s *acpProviderStub) ForceCompact(ctx context.Context, sessionKey string) (*session.CompactionResult, error) {
	return nil, nil
}
func (s *acpProviderStub) ResetSession(sessionKey string) error { return nil }
func (s *acpProviderStub) CleanOrphanedToolMessages(ctx context.Context, sessionKey string) (int, error) {
	return 0, nil
}
func (s *acpProviderStub) GetCompactionStatus(ctx context.Context) session.CompactionStatus {
	return session.CompactionStatus{}
}
func (s *acpProviderStub) GetSkillsStatusSection() string                          { return "" }
func (s *acpProviderStub) GetSkillsListForCommand() *SkillsListResult              { return nil }
func (s *acpProviderStub) TriggerHeartbeat(ctx context.Context) error              { return nil }
func (s *acpProviderStub) StopAllUserSessions(userID string) (int, error)          { return 0, nil }
func (s *acpProviderStub) ResumeAllUserSessions(userID string) (int, error)        { return 0, nil }
func (s *acpProviderStub) RequestShutdown(userID string) error                     { return nil }
func (s *acpProviderStub) GetHassInfo() *HassInfo                                  { return nil }
func (s *acpProviderStub) SetHassDebug(enabled bool)                               {}
func (s *acpProviderStub) ListHassSubscriptions() []HassSubscriptionInfo           { return nil }
func (s *acpProviderStub) GetLLMProviderStatus() *LLMProviderStatusResult          { return nil }
func (s *acpProviderStub) ResetLLMCooldowns() int                                  { return 0 }
func (s *acpProviderStub) GetEmbeddingsStatus() *EmbeddingsStatusResult            { return nil }
func (s *acpProviderStub) TriggerEmbeddingsRebuild() error                         { return nil }
func (s *acpProviderStub) ACPDetach(sessionKey string) (*acp.AttachmentInfo, error) { return nil, nil }
func (s *acpProviderStub) ACPInspect(sessionKey string) (*acp.AttachmentInfo, error) {
	return &acp.AttachmentInfo{SessionKey: sessionKey, Attached: true, SessionID: "sess-1", Driver: "cursor", Transport: "local-stdio", CWD: "/tmp/repo"}, nil
}
func (s *acpProviderStub) ACPClose(ctx context.Context, sessionKey string) error { return nil }
func (s *acpProviderStub) ACPSetMode(ctx context.Context, sessionKey string, mode string) (*acp.AttachmentInfo, error) {
	return &acp.AttachmentInfo{SessionKey: sessionKey, Attached: true, Mode: mode}, nil
}
func (s *acpProviderStub) ACPListModels(ctx context.Context, sessionKey string) ([]acp.ACPModelOption, error) {
	if len(s.models) == 0 {
		s.models = []acp.ACPModelOption{
			{FriendlyID: "claude-4.6-opus-high-thinking", Name: "Claude Opus 4.6", Current: true},
		}
	}
	return s.models, nil
}
func (s *acpProviderStub) ACPSetModel(ctx context.Context, sessionKey string, model string) (*acp.AttachmentInfo, error) {
	s.setModel = model
	return &acp.AttachmentInfo{SessionKey: sessionKey, Attached: true, CurrentModel: model}, nil
}
func (s *acpProviderStub) ACPSteer(ctx context.Context, sessionKey string, text string, stayAttached bool) (*acp.PromptResult, error) {
	s.steerText = text
	s.steerStay = stayAttached
	return &acp.PromptResult{FinalText: "ok"}, nil
}
func (s *acpProviderStub) ACPCancel(ctx context.Context, sessionKey string) error { return nil }
func (s *acpProviderStub) GetA2AStatus() a2a.Status                                 { return a2a.Status{} }
func (s *acpProviderStub) ListA2APeers(filter string) []a2a.PeerRecord              { return nil }
func (s *acpProviderStub) ListA2ATasks(filter string, peer string) []a2a.TaskSummary { return nil }
func (s *acpProviderStub) GetA2APairingPayload() a2a.PairingPayload                 { return a2a.PairingPayload{} }
func (s *acpProviderStub) PingA2APeer(ctx context.Context, target string) (a2a.PingResult, error) {
	return a2a.PingResult{}, nil
}
func (s *acpProviderStub) SubmitA2ATask(ctx context.Context, target string, input string) (string, <-chan a2a.TaskSnapshot, error) {
	return "", nil, nil
}
func (s *acpProviderStub) ResumeA2ATask(ctx context.Context, target string, taskID string) (<-chan a2a.TaskSnapshot, error) {
	return nil, nil
}
func (s *acpProviderStub) CancelA2ATask(ctx context.Context, target string, taskID string) (a2a.TaskSnapshot, error) {
	return a2a.TaskSnapshot{}, nil
}
func (s *acpProviderStub) ACPAttach(ctx context.Context, sessionKey string, userID string, driver string, cwd string, mode string, sessionID string) (*acp.AttachmentInfo, error) {
	s.attachCalled = true
	s.attachDriver = driver
	s.attachCWD = cwd
	s.attachMode = mode
	return &acp.AttachmentInfo{
		SessionKey: sessionKey,
		Attached:   true,
		SessionID:  "sess-1",
		Driver:     driver,
		Transport:  "local-stdio",
		CWD:        cwd,
		Mode:       mode,
	}, nil
}

func TestHandleACPAttachParsesDriverCWDAndMode(t *testing.T) {
	provider := &acpProviderStub{}
	res := handleACP(context.Background(), &CommandArgs{
		SessionKey: "primary",
		UserID:     "owner",
		Provider:   provider,
		RawArgs:    "attach cursor --cwd /tmp/repo --mode plan",
		Usage:      "attach [driver] [--cwd /path] [--mode mode]",
	})
	if res == nil {
		t.Fatalf("expected result")
	}
	if !provider.attachCalled {
		t.Fatalf("expected attach to be called")
	}
	if provider.attachDriver != "cursor" {
		t.Fatalf("expected driver cursor, got %q", provider.attachDriver)
	}
	if provider.attachCWD != "/tmp/repo" {
		t.Fatalf("expected cwd /tmp/repo, got %q", provider.attachCWD)
	}
	if provider.attachMode != "plan" {
		t.Fatalf("expected mode plan, got %q", provider.attachMode)
	}
}

func TestHandleACPSteerDefaultsToDetachAfterPrompt(t *testing.T) {
	provider := &acpProviderStub{}
	res := handleACP(context.Background(), &CommandArgs{
		SessionKey: "primary",
		UserID:     "owner",
		Provider:   provider,
		RawArgs:    "steer ask cursor for cwd",
		Usage:      "steer [--stay-attached] <message>",
	})
	if res == nil {
		t.Fatalf("expected result")
	}
	if provider.steerText != "ask cursor for cwd" {
		t.Fatalf("expected steer text to be passed through, got %q", provider.steerText)
	}
	if provider.steerStay {
		t.Fatalf("expected steer to detach by default")
	}
}

func TestHandleACPSteerParsesStayAttachedFlag(t *testing.T) {
	provider := &acpProviderStub{}
	res := handleACP(context.Background(), &CommandArgs{
		SessionKey: "primary",
		UserID:     "owner",
		Provider:   provider,
		RawArgs:    "steer --stay-attached ask cursor for cwd",
		Usage:      "steer [--stay-attached] <message>",
	})
	if res == nil {
		t.Fatalf("expected result")
	}
	if provider.steerText != "ask cursor for cwd" {
		t.Fatalf("expected steer text to be passed through, got %q", provider.steerText)
	}
	if !provider.steerStay {
		t.Fatalf("expected stay-attached flag to be passed through")
	}
}

func TestHandleACPModelList(t *testing.T) {
	provider := &acpProviderStub{
		models: []acp.ACPModelOption{
			{FriendlyID: "claude-4.6-opus-high-thinking", Name: "Claude Opus 4.6", Current: true},
			{FriendlyID: "auto", Name: "Automatic"},
		},
	}
	res := handleACP(context.Background(), &CommandArgs{
		SessionKey: "primary",
		UserID:     "owner",
		Provider:   provider,
		RawArgs:    "model list",
		Usage:      "model <list|friendly-id>",
	})
	if res == nil {
		t.Fatalf("expected result")
	}
	if got := res.Text; got == "" || !containsAll(got, "ACP models:", "claude-4.6-opus-high-thinking", "auto") {
		t.Fatalf("unexpected model list output: %q", got)
	}
}

func TestHandleACPModelSet(t *testing.T) {
	provider := &acpProviderStub{}
	res := handleACP(context.Background(), &CommandArgs{
		SessionKey: "primary",
		UserID:     "owner",
		Provider:   provider,
		RawArgs:    "model claude-4.6-opus-high-thinking",
		Usage:      "model <list|friendly-id>",
	})
	if res == nil {
		t.Fatalf("expected result")
	}
	if provider.setModel != "claude-4.6-opus-high-thinking" {
		t.Fatalf("expected model to be passed through, got %q", provider.setModel)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
