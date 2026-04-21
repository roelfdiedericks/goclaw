package context

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	itools "github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

type promptTestTool struct {
	name, description string
}

func (t promptTestTool) Name() string           { return t.name }
func (t promptTestTool) Description() string    { return t.description }
func (t promptTestTool) Schema() map[string]any { return map[string]any{} }
func (t promptTestTool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	return nil, nil
}

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

func TestBuildSystemPromptIgnoresTokenCountsForStability(t *testing.T) {
	base := PromptParams{
		WorkspaceDir:   "/tmp/workspace",
		Channel:        "telegram",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
		MaxTokens:      8000,
		TotalTokens:    100,
	}
	a := BuildSystemPrompt(base)
	base.TotalTokens = 7500
	b := BuildSystemPrompt(base)
	if a != b {
		t.Fatalf("system prompt should not change when only TotalTokens/usage changes (context status is ephemeral)")
	}
	if strings.Contains(a, "## Context Status") {
		t.Fatalf("context status must not appear in system prompt")
	}
}

func TestBuildContextStatusSectionFormatsUsage(t *testing.T) {
	s := BuildContextStatusSection(500, 1000)
	if !strings.Contains(s, "## Context Status") {
		t.Fatalf("expected context status heading: %q", s)
	}
	if !strings.Contains(s, "[Context: 0k/1k tokens (50%)]") {
		t.Fatalf("unexpected status body: %q", s)
	}
}

func TestBuildSystemPromptIncludesSubagentGuidanceWhenToolsAvailable(t *testing.T) {
	reg := itools.NewRegistry()
	reg.Register(promptTestTool{name: "subagent_spawn", description: "start one worker"})
	reg.Register(promptTestTool{name: "subagent_fanout", description: "start many workers"})
	reg.Register(promptTestTool{name: "subagent_status", description: "inspect workers"})

	prompt := BuildSystemPrompt(PromptParams{
		WorkspaceDir:   "/tmp/workspace",
		Channel:        "telegram",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
		Tools:          reg,
	})

	required := []string{
		"## Delegated Subagents",
		"`subagent_spawn`: start one worker. It returns a `runId` immediately and, by default, sends a completion callback later.",
		"`subagent_fanout`: start several workers in parallel and get their results in the current turn. It tries to return full child outputs inline. If everything does not fit in the current session headroom, it returns as many full results as fit plus explicit run IDs for the rest. By default it does not send a later completion callback.",
		"`subagent_fanout` returns `ok=false` when one or more worker runs failed, timed out, were canceled, or failed to start",
		"Optional `extraSummary` is secondary. GoClaw only returns it when the summary covered all worker outputs and the worker outcomes were healthy.",
		"Use `subagent_fanout` when you want several worker results back in the current turn so you can interpret them yourself.",
		"If `subagent_fanout` returns `ok=false`, treat it as a real failure or partial failure.",
		"Use `subagent_status action=info` only when fanout tells you some results did not fit inline, or when you want to inspect one worker more closely.",
	}
	for _, snippet := range required {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected prompt to contain %q", snippet)
		}
	}
}

func TestBuildAgentExtractionSectionUsesRecallFirstGuidance(t *testing.T) {
	prompt := buildAgentExtractionSection()
	required := []string{
		"memory_graph_recall",
		"decide whether to skip, enrich/update existing knowledge, or store something new",
		"Recognition is not completion.",
		"Do not continue to your response until the memory decision has been executed through the tool workflow.",
		"Recall First, Then Decide",
	}
	for _, snippet := range required {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("expected extraction section to contain %q", snippet)
		}
	}
}

func TestBuildSystemPromptIncludesLCMSectionsOnlyWhenEnabled(t *testing.T) {
	base := PromptParams{
		WorkspaceDir:   "/tmp/workspace",
		Channel:        "telegram",
		WorkspaceFiles: []WorkspaceFile{},
		IncludeMemory:  true,
	}

	prompt := BuildSystemPrompt(base)
	if strings.Contains(prompt, "## Lossless Recall Policy") {
		t.Fatalf("did not expect LCM policy when disabled")
	}

	prompt = BuildSystemPrompt(PromptParams{
		WorkspaceDir:        "/tmp/workspace",
		Channel:             "telegram",
		WorkspaceFiles:      []WorkspaceFile{},
		IncludeMemory:       true,
		LCMEnabled:          true,
		CompactionCount:     1,
		CompactionMaxDepth:  2,
		CompactionCondensed: 2,
	})
	if !strings.Contains(prompt, "## Lossless Recall Policy") {
		t.Fatalf("expected LCM policy when enabled")
	}
	if !strings.Contains(prompt, "## Compacted Conversation Context") {
		t.Fatalf("expected dynamic compacted-context guidance when summaries are present")
	}
	if !strings.Contains(prompt, "Deeply compacted context: expand before asserting specifics.") {
		t.Fatalf("expected heavy dynamic compacted-context guidance")
	}
}

func TestLCMStaticPolicyReferencesRealMemoryGraphToolNames(t *testing.T) {
	prompt := BuildSystemPrompt(PromptParams{
		WorkspaceDir:    "/tmp/workspace",
		Channel:         "telegram",
		WorkspaceFiles:  []WorkspaceFile{},
		IncludeMemory:   true,
		LCMEnabled:      true,
		CompactionCount: 1,
	})

	// Positive: real tool names must appear.
	for _, name := range []string{"memory_graph_store", "memory_graph_recall", "memory_graph_query"} {
		if !strings.Contains(prompt, name) {
			t.Fatalf("expected LCM policy to reference %q", name)
		}
	}

	// Negative: the invented short names must not appear as standalone tool names.
	// We check the backtick-bounded forms to avoid matching substrings of the real
	// names (e.g. "memory_recall" is a suffix of "memory_graph_recall").
	for _, bad := range []string{"`memory_store`", "`memory_recall`", "`memory_query`"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("expected LCM policy NOT to reference invented tool %s", bad)
		}
	}
}
