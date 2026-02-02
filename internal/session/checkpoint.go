package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// CheckpointGenerator generates rolling checkpoints asynchronously
type CheckpointGenerator struct {
	config       *CheckpointGeneratorConfig
	store        Store
	sessionKey   string
	llmClient    CheckpointLLMClient
	mainClient   CheckpointLLMClient // Fallback client
	mu           sync.Mutex
	lastGenTime  time.Time
	lastGenTurns int
}

// CheckpointGeneratorConfig holds checkpoint generation settings
type CheckpointGeneratorConfig struct {
	Enabled                bool
	Model                  string
	FallbackToMain         bool
	TokenThresholdPercents []int // e.g., [25, 50, 75]
	TurnThreshold          int   // Generate every N user messages
	MinTokensForGen        int   // Don't checkpoint if < N tokens
}

// CheckpointLLMClient interface for LLM calls
type CheckpointLLMClient interface {
	GenerateCheckpoint(ctx context.Context, messages []Message, currentTokens int) (*CheckpointData, error)
	IsAvailable() bool
	Model() string
}

// NewCheckpointGenerator creates a new checkpoint generator
func NewCheckpointGenerator(cfg *CheckpointGeneratorConfig) *CheckpointGenerator {
	return &CheckpointGenerator{
		config: cfg,
	}
}

// SetStore sets the store for checkpoint persistence
func (g *CheckpointGenerator) SetStore(store Store, sessionKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.store = store
	g.sessionKey = sessionKey
}

// SetLLMClients sets the LLM clients for checkpoint generation
func (g *CheckpointGenerator) SetLLMClients(checkpointClient, mainClient CheckpointLLMClient) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.llmClient = checkpointClient
	g.mainClient = mainClient
}

// ShouldCheckpoint determines if a checkpoint should be generated
func (g *CheckpointGenerator) ShouldCheckpoint(sess *Session) bool {
	if g == nil || g.config == nil || !g.config.Enabled {
		return false
	}

	// Don't checkpoint if not enough tokens
	if sess.GetTotalTokens() < g.config.MinTokensForGen {
		return false
	}

	// Check token thresholds
	usage := sess.GetContextUsage()
	for _, threshold := range g.config.TokenThresholdPercents {
		thresholdPercent := float64(threshold) / 100.0
		if usage >= thresholdPercent {
			// Check if we already have a checkpoint at this threshold
			if g.hasCheckpointAtThreshold(sess, threshold) {
				continue
			}
			L_debug("checkpoint threshold reached", "percent", threshold, "usage", usage)
			return true
		}
	}

	// Check turn threshold
	userCount := sess.UserMessageCount()
	if g.config.TurnThreshold > 0 && userCount > 0 {
		// Check if enough turns since last checkpoint
		turnsSinceLast := userCount - g.lastGenTurns
		if turnsSinceLast >= g.config.TurnThreshold {
			L_debug("checkpoint turn threshold reached",
				"userCount", userCount,
				"lastGen", g.lastGenTurns,
				"threshold", g.config.TurnThreshold)
			return true
		}
	}

	return false
}

// hasCheckpointAtThreshold checks if there's already a checkpoint covering this threshold
func (g *CheckpointGenerator) hasCheckpointAtThreshold(sess *Session, threshold int) bool {
	if sess.LastCheckpoint == nil {
		return false
	}

	// If last checkpoint covered more tokens than current threshold, we're good
	thresholdTokens := (sess.GetMaxTokens() * threshold) / 100
	return sess.LastCheckpoint.Checkpoint.TokensAtCheckpoint >= thresholdTokens
}

// GenerateAsync generates a checkpoint asynchronously
func (g *CheckpointGenerator) GenerateAsync(sess *Session, sessionFile string) {
	if g == nil {
		return
	}

	go func() {
		// Recover from any panics to prevent crashing the whole process
		defer func() {
			if r := recover(); r != nil {
				L_error("checkpoint goroutine panic recovered", "recover", r)
			}
		}()

		// Use longer timeout for checkpoint generation
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		err := g.Generate(ctx, sess, sessionFile)
		if err != nil {
			L_warn("checkpoint generation failed (non-fatal)", "error", err)
		}
	}()
}

// Generate creates a checkpoint synchronously
func (g *CheckpointGenerator) Generate(ctx context.Context, sess *Session, sessionFile string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Get the appropriate LLM client
	client := g.getClient()
	if client == nil {
		return fmt.Errorf("no LLM client available for checkpoint generation")
	}

	L_info("generating checkpoint",
		"model", client.Model(),
		"tokens", sess.GetTotalTokens(),
		"messages", len(sess.Messages))

	// Generate the checkpoint
	messages := sess.GetMessages()
	checkpoint, err := client.GenerateCheckpoint(ctx, messages, sess.GetTotalTokens())
	if err != nil {
		return fmt.Errorf("failed to generate checkpoint: %w", err)
	}

	// Set metadata
	checkpoint.TokensAtCheckpoint = sess.GetTotalTokens()
	checkpoint.MessageCountAtCheckpoint = len(messages)

	// Write to store (preferred) or JSONL (legacy)
	if g.store != nil && g.sessionKey != "" {
		storedCP := &StoredCheckpoint{
			ID:                       GenerateRecordID(),
			Timestamp:                time.Now(),
			Summary:                  checkpoint.Summary,
			Topics:                   checkpoint.Topics,
			KeyDecisions:             checkpoint.KeyDecisions,
			OpenQuestions:            checkpoint.OpenQuestions,
			TokensAtCheckpoint:       checkpoint.TokensAtCheckpoint,
			MessageCountAtCheckpoint: checkpoint.MessageCountAtCheckpoint,
			GeneratedBy:              client.Model(),
		}
		if parentID := sess.GetLastRecordID(); parentID != nil {
			storedCP.ParentID = *parentID
		}

		if err := g.store.AppendCheckpoint(ctx, g.sessionKey, storedCP); err != nil {
			return fmt.Errorf("failed to write checkpoint to store: %w", err)
		}

		sess.SetLastRecordID(storedCP.ID)
		L_info("checkpoint saved to store", "id", storedCP.ID)
	}

	// Update generation tracking
	g.lastGenTime = time.Now()
	g.lastGenTurns = sess.UserMessageCount()

	L_info("checkpoint generated",
		"topics", len(checkpoint.Topics),
		"decisions", len(checkpoint.KeyDecisions),
		"questions", len(checkpoint.OpenQuestions))

	return nil
}

// getClient returns the best available LLM client
func (g *CheckpointGenerator) getClient() CheckpointLLMClient {
	// Try checkpoint-specific client first
	if g.llmClient != nil && g.llmClient.IsAvailable() {
		return g.llmClient
	}

	// Fall back to main client if configured
	if g.config.FallbackToMain && g.mainClient != nil && g.mainClient.IsAvailable() {
		L_debug("falling back to main model for checkpoint")
		return g.mainClient
	}

	return nil
}

// CheckpointPrompt is the prompt template for generating structured summaries
const CheckpointPrompt = `Analyze this conversation and provide a structured summary for context preservation.

Return a JSON object with this exact structure:
{
  "summary": "A comprehensive 2-4 paragraph summary of the conversation so far",
  "topics": ["topic1", "topic2", ...],
  "keyDecisions": ["decision1", "decision2", ...],
  "openQuestions": ["question1", "question2", ...]
}

Guidelines:
- summary: Cover the main discussion points, what was accomplished, and current context
- topics: List 3-7 main topics discussed
- keyDecisions: List important decisions or conclusions reached (if any)
- openQuestions: List unresolved questions or pending items (if any)

Respond ONLY with the JSON object, no other text.`

// ParseCheckpointResponse parses the LLM response into CheckpointData
func ParseCheckpointResponse(response string) (*CheckpointData, error) {
	var data CheckpointData
	if err := json.Unmarshal([]byte(response), &data); err != nil {
		// Try to extract JSON from response if it has extra text
		start := findJSONStart(response)
		end := findJSONEnd(response, start)
		if start >= 0 && end > start {
			jsonStr := response[start : end+1]
			if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
				// JSON extraction failed - fall back to using raw response as summary
				L_info("checkpoint: JSON extraction failed, using raw response as summary",
					"jsonError", err,
					"responseLen", len(response))
				return &CheckpointData{
					Summary: truncateForLog(response, 4000),
				}, nil
			}
			L_debug("checkpoint: extracted JSON from mixed response", "jsonStart", start)
		} else {
			// No JSON found - model probably returned plain text summary
			// Use it as-is rather than failing
			L_info("checkpoint: no JSON found, using raw response as summary",
				"responseLen", len(response))
			return &CheckpointData{
				Summary: truncateForLog(response, 4000),
			}, nil
		}
	}
	return &data, nil
}

// truncateForLog truncates a string for logging, adding ellipsis if needed
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// findJSONStart finds the start of a JSON object in a string
func findJSONStart(s string) int {
	for i, c := range s {
		if c == '{' {
			return i
		}
	}
	return -1
}

// findJSONEnd finds the end of a JSON object starting from the given position
func findJSONEnd(s string, start int) int {
	if start < 0 || start >= len(s) {
		return -1
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if c == '\\' && inString {
			escaped = true
			continue
		}

		if c == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// BuildMessagesForCheckpoint builds a condensed message list for checkpoint generation
func BuildMessagesForCheckpoint(messages []Message) string {
	var result string
	for i, msg := range messages {
		role := msg.Role
		content := msg.Content

		// Truncate very long messages
		if len(content) > 2000 {
			content = content[:2000] + "... [truncated]"
		}

		result += fmt.Sprintf("[%d] %s: %s\n\n", i+1, role, content)
	}
	return result
}

// CompactionSummaryPrompt is the prompt for generating compaction summaries
const CompactionSummaryPrompt = `Summarize this conversation for context preservation. Include:
1. Main topics discussed
2. Key decisions or conclusions
3. Current state/context
4. Any pending items or open questions

Keep the summary concise but comprehensive (max 2000 tokens).
Format as plain text, not JSON.`
