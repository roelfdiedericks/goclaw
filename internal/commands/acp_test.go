package commands

import (
	"context"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/session"
)

type acpProviderStub struct {
	attachCalled bool
	attachDriver string
	attachCWD    string
	attachMode   string
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
func (s *acpProviderStub) ACPSteer(ctx context.Context, sessionKey string, text string) (*acp.PromptResult, error) {
	return &acp.PromptResult{FinalText: "ok"}, nil
}
func (s *acpProviderStub) ACPCancel(ctx context.Context, sessionKey string) error { return nil }
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
