package llm

import (
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

func TestConvertMessageAssistantIncludesPhase(t *testing.T) {
	p := &OaiNextProvider{}
	items := p.convertMessage(types.Message{
		Role:    "assistant",
		Content: "final response",
		Phase:   "final_answer",
	})

	if len(items) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(items))
	}
	if items[0].Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", items[0].Role)
	}
	if items[0].Phase != "final_answer" {
		t.Fatalf("expected phase final_answer, got %q", items[0].Phase)
	}
}

func TestBuildRequestIncrementalPreservesAssistantPhase(t *testing.T) {
	p := &OaiNextProvider{
		model:            "gpt-5.4",
		maxTokens:        1000,
		responseID:       "resp_prev",
		lastMessageCount: 1,
	}
	store := true

	messages := []types.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "done", Phase: "final_answer"},
	}
	req := p.buildRequest(messages, nil, "", nil, true, &store)

	if req.PreviousResponseID != "resp_prev" {
		t.Fatalf("expected previous_response_id resp_prev, got %q", req.PreviousResponseID)
	}
	if len(req.Input) != 1 {
		t.Fatalf("expected 1 incremental input item, got %d", len(req.Input))
	}
	if req.Input[0].Phase != "final_answer" {
		t.Fatalf("expected incremental item phase final_answer, got %q", req.Input[0].Phase)
	}
}

func TestHandleOutputItemDoneCapturesAssistantPhase(t *testing.T) {
	p := &OaiNextProvider{}
	clientToolNames := map[string]bool{}
	var clientToolCalls []*oaiOutputItem
	var assistantPhase string
	var textBuilder strings.Builder

	p.handleOutputItemDone(
		&oaiOutputItem{
			Type:  oaiItemTypeMessage,
			Role:  "assistant",
			Phase: "commentary",
			Content: []oaiContentPart{
				{Type: "output_text", Text: "working on it"},
			},
		},
		nil,
		clientToolNames,
		&clientToolCalls,
		&assistantPhase,
		&textBuilder,
	)

	if assistantPhase != "commentary" {
		t.Fatalf("expected assistant phase commentary, got %q", assistantPhase)
	}
	if textBuilder.String() != "working on it" {
		t.Fatalf("expected fallback text from output item, got %q", textBuilder.String())
	}
}
