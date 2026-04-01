package gateway

import (
	"encoding/json"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/acp"
)

func TestMapACPEventToGatewayEventsProgress(t *testing.T) {
	events := mapACPEventToGatewayEvents("run-1", acp.ACPEvent{
		Type: acp.EventToolUpdate,
		Payload: acp.ToolUpdatePayload{
			ToolCallID:  "tool-1",
			Title:       "git status",
			Status:      "in_progress",
			ContentText: "checking repo",
		},
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 mapped event, got %d", len(events))
	}
	progress, ok := events[0].(EventToolProgress)
	if !ok {
		t.Fatalf("expected EventToolProgress, got %T", events[0])
	}
	if progress.Status != "in_progress" {
		t.Fatalf("expected status in_progress, got %q", progress.Status)
	}
	if progress.DisplayResult != "checking repo" {
		t.Fatalf("expected display result to come from content text, got %q", progress.DisplayResult)
	}
}

func TestMapACPEventToGatewayEventsCompleted(t *testing.T) {
	events := mapACPEventToGatewayEvents("run-1", acp.ACPEvent{
		Type: acp.EventToolUpdate,
		Payload: acp.ToolUpdatePayload{
			ToolCallID:   "tool-1",
			Title:        "git status",
			Status:       "completed",
			RawOutput:    json.RawMessage(`"working tree clean"`),
			IsTerminal:   true,
			IsSuccessful: true,
		},
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 mapped event, got %d", len(events))
	}
	end, ok := events[0].(EventToolEnd)
	if !ok {
		t.Fatalf("expected EventToolEnd, got %T", events[0])
	}
	if end.Error != "" {
		t.Fatalf("expected no error for completed event, got %q", end.Error)
	}
	if end.DisplayResult != "working tree clean" {
		t.Fatalf("expected display result from raw output, got %q", end.DisplayResult)
	}
}

func TestMapACPEventToGatewayEventsFailed(t *testing.T) {
	events := mapACPEventToGatewayEvents("run-1", acp.ACPEvent{
		Type: acp.EventToolUpdate,
		Payload: acp.ToolUpdatePayload{
			ToolCallID:   "tool-1",
			Title:        "git status",
			Status:       "failed",
			RawOutput:    json.RawMessage(`"permission denied"`),
			IsTerminal:   true,
			IsSuccessful: false,
		},
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 mapped event, got %d", len(events))
	}
	end, ok := events[0].(EventToolEnd)
	if !ok {
		t.Fatalf("expected EventToolEnd, got %T", events[0])
	}
	if end.Error != "permission denied" {
		t.Fatalf("expected terminal failure to populate error, got %q", end.Error)
	}
	if end.DisplayResult != "" {
		t.Fatalf("expected failed event to keep display result empty, got %q", end.DisplayResult)
	}
}
