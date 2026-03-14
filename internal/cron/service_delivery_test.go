package cron

import (
	"context"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/delivery"
)

type deliveryGatewayStub struct {
	ownerUserID string
	lastAssistant *delivery.AssistantMessage
	lastSystem    *delivery.SystemMessage
	report      delivery.Report
}

func (d *deliveryGatewayStub) RunAgentForCron(ctx context.Context, req AgentRequest, events chan<- AgentEvent) {
	close(events)
}

func (d *deliveryGatewayStub) GetOwnerUserID() string {
	return d.ownerUserID
}

func (d *deliveryGatewayStub) InjectSystemEvent(ctx context.Context, text string) error {
	return nil
}

func (d *deliveryGatewayStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	copyMsg := msg
	d.lastAssistant = &copyMsg
	return d.report
}

func (d *deliveryGatewayStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	copyMsg := msg
	d.lastSystem = &copyMsg
	return d.report
}

func TestDeliverAssistantOutputUsesGatewayDeliverySeam(t *testing.T) {
	gw := &deliveryGatewayStub{
		ownerUserID: "owner",
		report:      delivery.Report{Generated: true, DeliveredTo: 1},
	}
	svc := &Service{gateway: gw}

	svc.deliverAssistantOutput(context.Background(), "cron", "Hello world")

	if gw.lastAssistant == nil {
		t.Fatalf("expected assistant delivery to be sent to gateway")
	}
	if gw.lastAssistant.Source != "cron" {
		t.Fatalf("expected source cron, got %q", gw.lastAssistant.Source)
	}
	if !gw.lastAssistant.Persist {
		t.Fatalf("expected cron assistant delivery to request persistence")
	}
	if gw.lastAssistant.PersistKind != "delivered" {
		t.Fatalf("expected persist kind delivered, got %q", gw.lastAssistant.PersistKind)
	}
	if gw.lastAssistant.Content != "Hello world" {
		t.Fatalf("expected raw assistant content, got %q", gw.lastAssistant.Content)
	}
	if gw.lastAssistant.PersistContent != "Hello world" {
		t.Fatalf("expected raw persist content, got %q", gw.lastAssistant.PersistContent)
	}
}
