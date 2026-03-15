package context

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptIncludesCronHandoffSectionOnlyForCronHandoffChannel(t *testing.T) {
	base := PromptParams{
		WorkspaceDir:   "/tmp/workspace",
		Channel:        "telegram",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
	}

	normalPrompt := BuildSystemPrompt(base)
	if strings.Contains(normalPrompt, "## Cron Job Handoffs") {
		t.Fatalf("did not expect cron handoff section for normal channel")
	}

	handoffPrompt := BuildSystemPrompt(PromptParams{
		WorkspaceDir:   "/tmp/workspace",
		Channel:        "cron_handoff",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
	})
	if !strings.Contains(handoffPrompt, "## Cron Job Handoffs") {
		t.Fatalf("expected cron handoff section for cron_handoff channel")
	}
	if !strings.Contains(handoffPrompt, "That content has NOT been delivered to the user yet.") {
		t.Fatalf("expected cron handoff instructions in prompt")
	}
}

func TestBuildSystemPromptUsesModelFacingCronInstructions(t *testing.T) {
	prompt := BuildSystemPrompt(PromptParams{
		WorkspaceDir:   "/tmp/workspace",
		Channel:        "telegram",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
	})

	required := []string{
		"Choose one output pattern:",
		"If the cron task wants you to produce text for the user, just reply with that text",
		"If the cron task wants you to use the message tool to send the output yourself, do that and then reply exactly SILENT_OK",
		"Never both send a message with the message tool AND also return the same text as your final reply",
	}

	for _, snippet := range required {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected cron prompt section to contain %q", snippet)
		}
	}

	rejected := []string{
		"If delivery is enabled, your response is sent to channels",
		"SILENT_OK suppresses delivery",
	}

	for _, snippet := range rejected {
		if strings.Contains(prompt, snippet) {
			t.Fatalf("did not expect outdated cron wording %q", snippet)
		}
	}
}

func TestBuildSystemPromptIncludesVisibleHomeWithoutBackingLeak(t *testing.T) {
	prompt := BuildSystemPrompt(PromptParams{
		WorkspaceDir:   "/Users/rodent/.goclaw/workspace",
		VisibleHomeDir: "/Users/rodent",
		SandboxMode:    "home",
		Channel:        "telegram",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
	})

	if !strings.Contains(prompt, "Your visible HOME directory is: /Users/rodent") {
		t.Fatalf("expected visible home guidance, prompt: %s", prompt)
	}
	if strings.Contains(prompt, ".goclaw/sandbox/home") {
		t.Fatalf("did not expect backing sandbox path leak, prompt: %s", prompt)
	}
}
