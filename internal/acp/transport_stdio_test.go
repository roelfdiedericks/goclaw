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

func TestBuildDriverExtensionPayloadPreservesPlanPhases(t *testing.T) {
	raw := json.RawMessage(`{
		"toolCallId":"tool-plan",
		"name":"Plan title",
		"overview":"Overview",
		"plan":"# Plan",
		"phases":[{"name":"phase-1","todos":[{"id":"todo-1","content":"Inspect","status":"pending"}]}]
	}`)
	payload := buildDriverExtensionPayload("cursor/create_plan", raw)
	if payload.Driver != "cursor" {
		t.Fatalf("expected cursor driver, got %q", payload.Driver)
	}
	if payload.SemanticKind != "interactive_approval" {
		t.Fatalf("expected interactive approval semantic kind, got %q", payload.SemanticKind)
	}
	var decoded PlanRequestPayload
	if err := json.Unmarshal(payload.Payload, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(decoded.Phases) != 1 || len(decoded.Phases[0].Todos) != 1 {
		t.Fatalf("expected phase todos to be preserved, got %#v", decoded.Phases)
	}
	if decoded.Phases[0].Todos[0].Content != "Inspect" {
		t.Fatalf("expected nested todo content to survive, got %#v", decoded.Phases[0].Todos[0])
	}
}

func TestBuildCanonicalInteractiveResponses(t *testing.T) {
	ask := BuildCursorAskQuestionAnsweredResponse([]QuestionAnswer{{
		QuestionID:        "q1",
		SelectedOptionIDs: []string{"a", "b"},
	}})
	if got := string(ask); got != `{"outcome":{"answers":[{"questionId":"q1","selectedOptionIds":["a","b"]}],"outcome":"answered"}}` {
		t.Fatalf("unexpected ask response: %s", got)
	}

	approve := BuildCursorCreatePlanAcceptedResponse("")
	if got := string(approve); got != `{"outcome":{"outcome":"accepted","planUri":""}}` {
		t.Fatalf("unexpected approve response: %s", got)
	}

	reject := BuildCursorCreatePlanRejectedResponse("Needs revision.")
	if got := string(reject); got != `{"outcome":{"outcome":"rejected","reason":"Needs revision."}}` {
		t.Fatalf("unexpected reject response: %s", got)
	}
}
