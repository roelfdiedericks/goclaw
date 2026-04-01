package telegram

import (
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/acp"
)

func TestPopInteractiveStatesForPending(t *testing.T) {
	b := &Bot{
		interactiveStates: map[string]*telegramInteractiveState{
			"keep": {
				ID:         "keep",
				SessionKey: "primary",
				Method:     "cursor/create_plan",
				ToolCallID: "tool-plan",
			},
			"cancel": {
				ID:         "cancel",
				SessionKey: "primary",
				Method:     "cursor/ask_question",
				ToolCallID: "tool-q",
			},
			"other-session": {
				ID:         "other-session",
				SessionKey: "user:abc",
				Method:     "cursor/ask_question",
				ToolCallID: "tool-q",
			},
		},
	}

	cancelled := b.popInteractiveStatesForPending("primary", []acp.AttachmentPendingRequestInfo{{
		Method:     "cursor/ask_question",
		ToolCallID: "tool-q",
	}})
	if len(cancelled) != 1 {
		t.Fatalf("expected one cancelled state, got %d", len(cancelled))
	}
	if cancelled[0].ID != "cancel" {
		t.Fatalf("expected cancelled state ID 'cancel', got %q", cancelled[0].ID)
	}
	if _, ok := b.interactiveStates["cancel"]; ok {
		t.Fatalf("expected cancelled state to be removed from map")
	}
	if _, ok := b.interactiveStates["keep"]; !ok {
		t.Fatalf("expected unrelated state to remain")
	}
	if _, ok := b.interactiveStates["other-session"]; !ok {
		t.Fatalf("expected other-session state to remain")
	}
}

func TestTelegramQuestionOptionsAddsSyntheticOther(t *testing.T) {
	b := &Bot{}
	options := b.telegramQuestionOptions(acp.QuestionItem{
		ID:     "q1",
		Prompt: "Pick one",
		Options: []acp.QuestionOption{
			{ID: "coffee", Label: "Coffee"},
			{ID: "tea", Label: "Tea"},
		},
	})
	if len(options) != 3 {
		t.Fatalf("expected synthetic other option to be appended, got %d options", len(options))
	}
	last := options[len(options)-1]
	if last.ID != "__other__" || last.Label != "Other..." {
		t.Fatalf("expected synthetic other option, got %#v", last)
	}
}

func TestTelegramQuestionOptionsDoesNotDuplicateOther(t *testing.T) {
	b := &Bot{}
	options := b.telegramQuestionOptions(acp.QuestionItem{
		ID:     "q1",
		Prompt: "Pick one",
		Options: []acp.QuestionOption{
			{ID: "coffee", Label: "Coffee"},
			{ID: "other", Label: "Other..."},
		},
	})
	count := 0
	for _, option := range options {
		if b.telegramQuestionOptionIsOther(option) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one other option, got %d", count)
	}
}

func TestTelegramQuestionTextShowsOtherHandoffHint(t *testing.T) {
	b := &Bot{}
	text := b.telegramQuestionText(&telegramInteractiveState{
		Question: acp.QuestionPayload{
			Title: "One pick",
			Questions: []acp.QuestionItem{{
				ID:     "q1",
				Prompt: "Pick one",
				Options: []acp.QuestionOption{
					{ID: "coffee", Label: "Coffee"},
				},
			}},
		},
		OtherRequested: true,
	}, "")
	if want := "Continue in chat with your custom answer."; !strings.Contains(text, want) {
		t.Fatalf("expected other handoff hint %q in question text %q", want, text)
	}
}

func TestShouldUseTelegramPollForQuestion(t *testing.T) {
	b := &Bot{}
	if !b.shouldUseTelegramPollForQuestion(acp.QuestionPayload{
		Questions: []acp.QuestionItem{{
			ID:            "q1",
			Prompt:        "Pick all",
			AllowMultiple: true,
			Options: []acp.QuestionOption{
				{ID: "a", Label: "A"},
				{ID: "b", Label: "B"},
			},
		}},
	}) {
		t.Fatalf("expected poll mode for single multi-select question")
	}
	if b.shouldUseTelegramPollForQuestion(acp.QuestionPayload{
		Questions: []acp.QuestionItem{{
			ID:            "q1",
			Prompt:        "Pick one",
			AllowMultiple: false,
			Options: []acp.QuestionOption{
				{ID: "a", Label: "A"},
				{ID: "b", Label: "B"},
			},
		}},
	}) {
		t.Fatalf("did not expect poll mode for single-choice question")
	}
	if b.shouldUseTelegramPollForQuestion(acp.QuestionPayload{
		Questions: []acp.QuestionItem{
			{
				ID:            "q1",
				Prompt:        "Pick all",
				AllowMultiple: true,
				Options: []acp.QuestionOption{
					{ID: "a", Label: "A"},
					{ID: "b", Label: "B"},
				},
			},
			{
				ID:            "q2",
				Prompt:        "Pick all",
				AllowMultiple: true,
				Options: []acp.QuestionOption{
					{ID: "c", Label: "C"},
					{ID: "d", Label: "D"},
				},
			},
		},
	}) {
		t.Fatalf("did not expect poll mode for multi-question payload")
	}
}
