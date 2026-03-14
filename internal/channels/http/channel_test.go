package http

import (
	"context"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

func TestDeliverSystemMessageEmitsSystemEvent(t *testing.T) {
	u := &user.User{ID: "owner", HTTPPasswordHash: "hash"}
	conn := &SSEConnection{
		Events: make(chan SSEEvent, 1),
		Done:   make(chan struct{}),
	}
	sess := &SSESession{
		SessionID:  "sess1",
		UserID:     u.ID,
		User:       u,
		activeConn: conn,
	}
	ch := &HTTPChannel{
		sessions: map[string]*SSESession{
			"sess1": sess,
		},
	}

	err := ch.DeliverSystemMessage(context.Background(), u, delivery.SystemMessage{
		Kind:    delivery.SystemKindStatus,
		Title:   "Status",
		Content: "Running cron...",
	})
	if err != nil {
		t.Fatalf("DeliverSystemMessage returned error: %v", err)
	}

	select {
	case event := <-conn.Events:
		if event.Event != "system" {
			t.Fatalf("expected system event, got %q", event.Event)
		}
		payload, ok := event.Data.(map[string]string)
		if !ok {
			t.Fatalf("expected map[string]string payload, got %#v", event.Data)
		}
		if payload["message"] != "**[Status]**\n\nRunning cron..." {
			t.Fatalf("unexpected system payload: %#v", payload)
		}
	default:
		t.Fatalf("expected a queued SSE system event")
	}
}
