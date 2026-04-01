package acp

import (
	"encoding/json"
	"testing"

	goacp "github.com/ironpark/go-acp"
)

func TestToolUpdatePayloadPreservesToolState(t *testing.T) {
	adapter := &clientAdapter{}
	inProgress := goacp.ToolCallStatusInProgress
	completed := goacp.ToolCallStatusCompleted

	start := adapter.toolStartPayload(goacp.SessionUpdateToolCall{
		ToolCall: goacp.ToolCall{
			ToolCallID: goacp.ToolCallID("tool-1"),
			Title:      "git status",
			Status:     &inProgress,
			RawInput:   json.RawMessage(`{"command":"git status"}`),
		},
	})

	if got := string(start.Input); got != `{"command":"git status"}` {
		t.Fatalf("expected start input to be preserved, got %q", got)
	}

	update := adapter.toolUpdatePayload(goacp.SessionUpdateToolCallUpdate{
		ToolCallUpdate: goacp.ToolCallUpdate{
			ToolCallID: goacp.ToolCallID("tool-1"),
			Status:     &completed,
			RawOutput:  json.RawMessage(`"working tree clean"`),
		},
	})

	if update.Title != "git status" {
		t.Fatalf("expected update title to be merged from start, got %q", update.Title)
	}
	if got := string(update.Input); got != `{"command":"git status"}` {
		t.Fatalf("expected update input to be preserved, got %q", got)
	}
	if got := string(update.RawOutput); got != `"working tree clean"` {
		t.Fatalf("expected raw output to be preserved, got %q", got)
	}
	if !update.IsTerminal {
		t.Fatalf("expected completed update to be terminal")
	}
	if !update.IsSuccessful {
		t.Fatalf("expected completed update to be successful")
	}
}
