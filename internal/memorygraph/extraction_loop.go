package memorygraph

import (
	"context"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Context keys for tool execution
type contextKey string

const ContextKeyUsername contextKey = "username"

// ExtractionLoop is a mini agentic loop for memory extraction.
// It uses the recall-first pattern: check existing memories before saving.
type ExtractionLoop struct {
	manager  *Manager
	provider llm.Provider
	toolReg  *tools.Registry
	maxTurns int
}

// LoopExtractionInput contains input for the extraction loop.
// Named differently from ExtractionContext in extraction.go to avoid conflict.
type LoopExtractionInput struct {
	Username     string   // From message.UserID
	Channel      string   // From message source
	SessionKey   string   // Session identifier
	Conversation string   // Formatted conversation text for LLM
	MessageIDs   []string // IDs of messages being extracted
	SourceType   string   // "live" or source type for bulk
	SourceFile   string   // For bulk ingestion only
}

// LoopExtractionResult contains the output of an extraction loop run.
// Named differently from ExtractionResult in extraction.go to avoid conflict.
type LoopExtractionResult struct {
	MemoriesSaved int
	Recalls       int
	Summary       string
	Error         error
}

// NewExtractionLoop creates a new extraction loop.
// Uses the summarization provider (cheap, fast model).
func NewExtractionLoop(mgr *Manager) (*ExtractionLoop, error) {
	registry := llm.GetRegistry()
	if registry == nil {
		return nil, fmt.Errorf("no LLM registry available")
	}

	provider, err := registry.GetProvider("summarization")
	if err != nil {
		return nil, fmt.Errorf("no summarization provider: %w", err)
	}

	// Create tool registry with just recall + store
	toolReg := tools.NewRegistry()
	toolReg.Register(NewRecallTool(mgr))
	toolReg.Register(NewStoreTool(mgr))

	return &ExtractionLoop{
		manager:  mgr,
		provider: provider,
		toolReg:  toolReg,
		maxTurns: 10,
	}, nil
}

// Run executes the extraction loop on the given context.
func (e *ExtractionLoop) Run(ctx context.Context, ec LoopExtractionInput) (*LoopExtractionResult, error) {
	// Inject username into context for tools
	ctx = context.WithValue(ctx, ContextKeyUsername, ec.Username)

	messages := []types.Message{
		{Role: "user", Content: buildLoopUserPrompt(ec)},
	}
	toolDefs := e.toolReg.Definitions()

	result := &LoopExtractionResult{}

	for turn := 0; turn < e.maxTurns; turn++ {
		// Collect response (non-streaming for extraction)
		var responseText string
		response, err := e.provider.StreamMessage(
			ctx,
			messages,
			toolDefs,
			loopSystemPrompt,
			func(delta string) { responseText += delta },
			nil,
		)
		if err != nil {
			result.Error = err
			return result, err
		}

		// Check if response has tool use
		if !response.HasToolUse() {
			// No tool use - extraction complete
			result.Summary = response.Text
			if result.Summary == "" {
				result.Summary = responseText
			}
			break
		}

		// Execute tool (log errors but continue - extraction is best-effort)
		toolResult, err := e.toolReg.Execute(ctx, response.ToolName, response.ToolInput)
		resultText := ""
		if err != nil {
			L_warn("extraction tool failed", "tool", response.ToolName, "error", err)
			resultText = fmt.Sprintf("Error: %v", err)
		} else {
			resultText = toolResult.GetText()
		}

		// Track stats
		if response.ToolName == "memory_graph_recall" {
			result.Recalls++
		} else if response.ToolName == "memory_graph_store" && err == nil {
			result.MemoriesSaved++
		}

		// Add to messages for next turn
		messages = append(messages,
			types.Message{
				Role:      "assistant",
				ToolUseID: response.ToolUseID,
				ToolName:  response.ToolName,
				ToolInput: response.ToolInput,
			},
			types.Message{
				Role:      "user",
				ToolUseID: response.ToolUseID,
				Content:   resultText,
			},
		)
	}

	L_debug("extraction loop completed",
		"recalls", result.Recalls,
		"saved", result.MemoriesSaved,
		"turns", len(messages)/2,
	)

	return result, nil
}

// buildLoopUserPrompt creates the user prompt for extraction loop.
func buildLoopUserPrompt(ec LoopExtractionInput) string {
	prompt := "Extract memories from this conversation:\n\n"

	if ec.Username != "" {
		prompt += fmt.Sprintf("User: %s\n", ec.Username)
	}
	if ec.Channel != "" {
		prompt += fmt.Sprintf("Channel: %s\n", ec.Channel)
	}
	prompt += "\n---\n\n"
	prompt += ec.Conversation

	return prompt
}
