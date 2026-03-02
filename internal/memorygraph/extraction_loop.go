package memorygraph

import (
	"context"
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Context keys for tool execution
type contextKey string

const (
	ContextKeyUsername         contextKey = "username"
	ContextKeyDefaultTimestamp contextKey = "defaultTimestamp"
)

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
	Username         string    // From message.UserID
	Channel          string    // From message source
	SessionKey       string    // Session identifier
	Conversation     string    // Formatted conversation text for LLM
	MessageIDs       []string  // IDs of messages being extracted
	SourceType       string    // "live" or source type for bulk
	SourceFile       string    // For bulk ingestion only
	ConversationTime time.Time // When this conversation happened (for occurred_at default)
}

// LoopExtractionResult contains the output of an extraction loop run.
// Named differently from ExtractionResult in extraction.go to avoid conflict.
type LoopExtractionResult struct {
	MemoriesSaved int
	Recalls       int
	Skips         int
	Summary       string
	Error         error
}

// getExtractionProvider returns an LLM provider for memory extraction.
// Tries memory_extraction -> summarization -> agent in order.
func getExtractionProvider() (llm.Provider, error) {
	registry := llm.GetRegistry()
	if registry == nil {
		return nil, fmt.Errorf("no LLM registry available")
	}

	for _, purpose := range []string{"memory_extraction", "summarization", "agent"} {
		provider, err := registry.GetProvider(purpose)
		if err == nil {
			L_debug("extraction: using provider", "purpose", purpose, "model", provider.Model())
			return provider, nil
		}
		L_debug("extraction: purpose unavailable", "purpose", purpose, "error", err)
	}

	return nil, fmt.Errorf("no provider available for extraction")
}

// NewExtractionLoop creates a new extraction loop.
// Uses the memory_extraction provider (falls back to summarization, then agent).
func NewExtractionLoop(mgr *Manager) (*ExtractionLoop, error) {
	provider, err := getExtractionProvider()
	if err != nil {
		return nil, err
	}

	// Create tool registry with recall, store, and skip
	toolReg := tools.NewRegistry()
	toolReg.Register(NewRecallTool(mgr))
	toolReg.Register(NewStoreTool(mgr))
	toolReg.Register(NewSkipTool())

	return &ExtractionLoop{
		manager:  mgr,
		provider: provider,
		toolReg:  toolReg,
		maxTurns: 10,
	}, nil
}

// Run executes the extraction loop on the given context.
func (e *ExtractionLoop) Run(ctx context.Context, ec LoopExtractionInput) (*LoopExtractionResult, error) {
	// Inject username and default timestamp into context for tools
	ctx = context.WithValue(ctx, ContextKeyUsername, ec.Username)
	if !ec.ConversationTime.IsZero() {
		ctx = context.WithValue(ctx, ContextKeyDefaultTimestamp, ec.ConversationTime)
	}

	messages := []types.Message{
		{Role: "user", Content: buildLoopUserPrompt(ec)},
	}
	toolDefs := e.toolReg.Definitions()

	result := &LoopExtractionResult{}

	// Disable server-side tools - extraction uses only our recall/store tools
	opts := &llm.StreamOptions{DisableServerTools: true}

	// Track consecutive empty recalls to detect loops
	consecutiveEmptyRecalls := 0
	const maxEmptyRecalls = 10 // After 10 empty recalls, nudge the model

	for turn := 0; turn < e.maxTurns; turn++ {
		// Collect response (non-streaming for extraction)
		var responseText string
		response, err := e.provider.StreamMessage(
			ctx,
			messages,
			toolDefs,
			loopSystemPrompt,
			func(delta string) { responseText += delta },
			opts,
		)
		if err != nil {
			result.Error = err
			return result, err
		}

		// Log any text output (may contain [DECISION] lines for debugging)
		if responseText != "" {
			L_debug("extraction: llm text output", "turn", turn, "text", responseText)
		}

		// Check if response has tool use
		if !response.HasToolUse() {
			// No tool use - extraction complete
			result.Summary = response.Text
			if result.Summary == "" {
				result.Summary = responseText
			}
			// Debug: show what model returned instead of tools
			if turn == 0 {
				summaryPreview := result.Summary
				if len(summaryPreview) > 200 {
					summaryPreview = summaryPreview[:200] + "..."
				}
				L_debug("extraction: model did not use tools", "response", summaryPreview)
			}
			break
		}

		// Get all tool calls from response
		toolCalls := response.ToolCalls
		L_debug("extraction: processing tool calls", "count", len(toolCalls))

		// Execute ALL tool calls and collect results
		type toolExecution struct {
			call   llm.ToolCallInfo
			result string
			err    error
		}
		executions := make([]toolExecution, 0, len(toolCalls))

		for _, tc := range toolCalls {
			L_debug("extraction: tool call", "tool", tc.Name, "input", string(tc.Input))
			toolResult, err := e.toolReg.Execute(ctx, tc.Name, tc.Input)
			resultText := ""
			if err != nil {
				L_warn("extraction tool failed", "tool", tc.Name, "error", err)
				resultText = fmt.Sprintf("Error: %v", err)
			} else {
				resultText = toolResult.GetText()
				L_debug("extraction: tool result", "tool", tc.Name, "resultLen", len(resultText))
			}

			// Track stats and detect recall loops
			if tc.Name == "memory_graph_recall" {
				result.Recalls++
				// Check if recall returned no results
				if resultText == "No memories found." {
					consecutiveEmptyRecalls++
					if consecutiveEmptyRecalls >= maxEmptyRecalls {
						// Nudge the model to stop recalling and start storing
						resultText = "No memories found. STOP RECALLING. Call memory_graph_store() or memory_graph_skip() NOW. Do not output text - call the tool."
						L_warn("extraction: recall loop detected, nudging model", "consecutiveEmpty", consecutiveEmptyRecalls)
					}
				} else {
					consecutiveEmptyRecalls = 0 // Reset on successful recall
				}
			} else if tc.Name == "memory_graph_store" && err == nil {
				result.MemoriesSaved++
				consecutiveEmptyRecalls = 0 // Reset when storing
			} else if tc.Name == "memory_graph_skip" {
				result.Skips++
				consecutiveEmptyRecalls = 0 // Reset when skipping
			}

			executions = append(executions, toolExecution{
				call:   tc,
				result: resultText,
				err:    err,
			})
		}

		// Add all tool call messages followed by all tool result messages
		for _, exec := range executions {
			messages = append(messages, types.Message{
				Role:      "assistant",
				ToolUseID: exec.call.ID,
				ToolName:  exec.call.Name,
				ToolInput: exec.call.Input,
			})
		}
		for _, exec := range executions {
			messages = append(messages, types.Message{
				Role:      "user",
				ToolUseID: exec.call.ID,
				Content:   exec.result,
			})
		}
	}

	L_debug("extraction loop completed",
		"recalls", result.Recalls,
		"saved", result.MemoriesSaved,
		"skips", result.Skips,
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
	if !ec.ConversationTime.IsZero() {
		prompt += fmt.Sprintf("Conversation date: %s\n", ec.ConversationTime.Format("2006-01-02 15:04"))
	}
	prompt += "\n---\n\n"
	prompt += ec.Conversation

	return prompt
}
