package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type mockDeliveryChannel struct {
	name             string
	hasUser          bool
	reachable        bool
	deliverErr       error
	deliveries       []string
	systemDeliveries []delivery.SystemMessage
}

func (m *mockDeliveryChannel) Name() string { return m.name }
func (m *mockDeliveryChannel) Send(ctx context.Context, msg string) error { return nil }
func (m *mockDeliveryChannel) SendMirror(ctx context.Context, source, userMsg string) error {
	return nil
}
func (m *mockDeliveryChannel) HasUser(u *user.User) bool { return m.hasUser }
func (m *mockDeliveryChannel) StreamEvent(u *user.User, event AgentEvent) bool { return false }
func (m *mockDeliveryChannel) DeliverAssistantMessage(ctx context.Context, u *user.User, message string) error {
	if m.deliverErr != nil {
		return m.deliverErr
	}
	m.deliveries = append(m.deliveries, message)
	return nil
}
func (m *mockDeliveryChannel) DeliverSystemMessage(ctx context.Context, u *user.User, msg delivery.SystemMessage) error {
	if m.deliverErr != nil {
		return m.deliverErr
	}
	m.systemDeliveries = append(m.systemDeliveries, msg)
	return nil
}
func (m *mockDeliveryChannel) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	return nil
}
func (m *mockDeliveryChannel) DeliveryReachable(u *user.User) (bool, string) {
	if !m.reachable {
		return false, delivery.ReasonUnreachable
	}
	return true, ""
}

func testUserRegistry() *user.Registry {
	thinking := false
	sandbox := true
	users := user.UsersConfig{
		"owner": {
			Name:          "Owner",
			Role:          "owner",
			TelegramID:    "123",
			Thinking:      &thinking,
			Sandbox:       &sandbox,
			ThinkingLevel: nil,
		},
	}
	roles := user.RolesConfig{
		"owner": {Tools: "*", Skills: "*", Memory: "full", Transcripts: "all", Commands: true},
	}
	return user.NewRegistryFromUsers(users, roles)
}

func TestDeliverAssistantOutputUsesUserAwareChannels(t *testing.T) {
	reachable := &mockDeliveryChannel{name: "telegram", hasUser: true, reachable: true}
	unreachable := &mockDeliveryChannel{name: "http", hasUser: true, reachable: false}
	noUser := &mockDeliveryChannel{name: "whatsapp", hasUser: false, reachable: true}
	failing := &mockDeliveryChannel{name: "tui", hasUser: true, reachable: true, deliverErr: errors.New("boom")}

	g := &Gateway{
		users:    testUserRegistry(),
		channels: map[string]Channel{"telegram": reachable, "http": unreachable, "whatsapp": noUser, "tui": failing},
	}

	report := g.DeliverAssistantOutput(context.Background(), "owner", delivery.AssistantMessage{
		Source:         "cron",
		Content:        "hello",
		Persist:        false,
		PersistKind:    "delivered",
		PersistContent: "hello",
	})

	if !report.Generated {
		t.Fatalf("expected generated report")
	}
	if report.DeliveredTo != 1 {
		t.Fatalf("expected exactly one successful delivery, got %d", report.DeliveredTo)
	}
	if len(reachable.deliveries) != 1 || reachable.deliveries[0] != "hello" {
		t.Fatalf("expected reachable channel to receive message once, got %#v", reachable.deliveries)
	}

	results := map[string]delivery.Result{}
	for _, result := range report.Results {
		results[result.Channel] = result
	}
	if results["http"].Reason != delivery.ReasonUnreachable {
		t.Fatalf("expected http to be marked unreachable, got %#v", results["http"])
	}
	if results["whatsapp"].Reason != delivery.ReasonHasNoUser {
		t.Fatalf("expected whatsapp to be marked no-user, got %#v", results["whatsapp"])
	}
	if results["tui"].Reason != delivery.ReasonError {
		t.Fatalf("expected tui to be marked error, got %#v", results["tui"])
	}
}

func TestDeliverSystemMessageUsesSystemSurface(t *testing.T) {
	reachable := &mockDeliveryChannel{name: "telegram", hasUser: true, reachable: true}
	g := &Gateway{
		users:    testUserRegistry(),
		channels: map[string]Channel{"telegram": reachable},
	}

	report := g.DeliverSystemMessage(context.Background(), "owner", delivery.SystemMessage{
		Kind:    delivery.SystemKindStatus,
		Source:  "status",
		Title:   "Status",
		Content: "Running cron...",
	})

	if !report.Delivered() {
		t.Fatalf("expected system delivery to succeed")
	}
	if len(reachable.deliveries) != 0 {
		t.Fatalf("expected assistant surface to remain unused, got %#v", reachable.deliveries)
	}
	if len(reachable.systemDeliveries) != 1 {
		t.Fatalf("expected one system delivery, got %#v", reachable.systemDeliveries)
	}
	if reachable.systemDeliveries[0].Kind != delivery.SystemKindStatus {
		t.Fatalf("expected status kind, got %#v", reachable.systemDeliveries[0])
	}
}
