package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Extractor uses LLM to extract memories from conversations
type Extractor struct {
	manager  *Manager
	provider llm.Provider
}

// NewExtractor creates a new memory extractor
func NewExtractor(manager *Manager) *Extractor {
	return &Extractor{
		manager: manager,
	}
}

// SetProvider updates the LLM provider used for extraction
func (e *Extractor) SetProvider(provider llm.Provider) {
	e.provider = provider
}

// ExtractionContext provides context for memory extraction
type ExtractionContext struct {
	Username      string
	Channel       string
	ChatID        string
	SessionKey    string
	MessageIDs    []string
	Conversation  string // The conversation text to extract from
	SourceFile    string // Source filename (e.g., "USER.md", "AGENTS.md") for context
	SourceType    string // "file" or "transcript"
}

// ExtractionResult contains the results of memory extraction
type ExtractionResult struct {
	Memories  []*Memory
	Skipped   int    // Number of duplicates/low-value memories skipped
	Error     error
}

// Extract analyzes a conversation and extracts memories
func (e *Extractor) Extract(ctx context.Context, ec ExtractionContext) (*ExtractionResult, error) {
	if e.provider == nil {
		return nil, fmt.Errorf("no LLM provider configured for extraction")
	}

	// Build the extraction prompt
	prompt := buildExtractionPrompt(ec.Conversation, ec.SourceFile, ec.SourceType)

	// Call LLM for extraction
	response, err := e.callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	// Parse the response
	extracted, err := parseExtractionResponse(response)
	if err != nil {
		L_warn("memorygraph: failed to parse extraction response", "error", err)
		return &ExtractionResult{Error: err}, nil
	}

	result := &ExtractionResult{}

	for _, em := range extracted {
		// Check for duplicates
		isDuplicate, existingUUID := e.checkDuplicate(ctx, em.Content, ec.Username)
		if isDuplicate {
			L_debug("memorygraph: skipping duplicate memory", "existing", existingUUID)
			result.Skipped++
			continue
		}

		// Create the memory
		mem := &Memory{
			Content:       em.Content,
			Type:          em.Type,
			Importance:    em.Importance,
			Confidence:    ConfidenceNotApplicable,
			Source:        "extraction",
			SourceSession: ec.SessionKey,
			Username:      ec.Username,
			Channel:       ec.Channel,
			ChatID:        ec.ChatID,
		}

		// Set confidence for pattern types
		if em.Confidence != nil {
			mem.Confidence = *em.Confidence
		}

		// Create the memory
		if err := e.manager.CreateMemory(ctx, mem); err != nil {
			L_warn("memorygraph: failed to create extracted memory", "error", err)
			continue
		}

		// Create associations if hints provided
		for _, hint := range em.Associations {
			assoc := &Association{
				SourceID:     mem.UUID,
				TargetID:     hint.TargetID,
				RelationType: hint.RelationType,
				Weight:       0.8, // Default weight for extracted associations
			}
			if err := e.manager.CreateAssociation(assoc); err != nil {
				L_debug("memorygraph: failed to create association", "error", err)
			}
		}

		result.Memories = append(result.Memories, mem)
	}

	L_info("memorygraph: extraction completed",
		"extracted", len(result.Memories),
		"skipped", result.Skipped,
	)

	return result, nil
}

// checkDuplicate checks if a similar memory already exists
func (e *Extractor) checkDuplicate(ctx context.Context, content string, username string) (bool, string) {
	// Search for similar memories
	results, err := e.manager.Search(ctx, SearchOptions{
		Query:      content,
		Username:   username,
		MaxResults: 5,
	})
	if err != nil {
		return false, ""
	}

	// Check for high similarity
	for _, r := range results {
		// If similarity score is very high, consider it a duplicate
		if r.Score > 0.95 {
			return true, r.Memory.UUID
		}
		// Also check for exact content match
		if normalizeContent(r.Memory.Content) == normalizeContent(content) {
			return true, r.Memory.UUID
		}
	}

	return false, ""
}

// normalizeContent normalizes content for comparison
func normalizeContent(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// callLLM calls the LLM provider for extraction
func (e *Extractor) callLLM(ctx context.Context, prompt string) (string, error) {
	// Use SimpleMessage for extraction (no tools, no streaming)
	response, err := e.provider.SimpleMessage(ctx, prompt, extractionSystemPrompt)
	if err != nil {
		return "", err
	}

	return response, nil
}

const extractionSystemPrompt = `You are a memory extraction assistant. Your task is to identify important information about the HUMAN USER that should be remembered long-term.

IMPORTANT DISTINCTION:
- "User" or "human" = the human person who owns this AI assistant
- "Agent" or "assistant" = the AI itself

Extract memories ABOUT THE HUMAN such as:
- Facts about the human (their name, job, family, location, interests, etc.)
- The human's preferences, likes, and dislikes
- Decisions the human has made
- Goals or intentions the human has expressed
- Significant events in the human's life
- The human's routines and habits
- Projects the human is working on
- People the human knows (relationships, colleagues, family)

Do NOT extract:
- Instructions or guidelines for the AI assistant (these are not memories)
- How the AI should behave or respond
- AI system configuration or rules
- Generic advice or best practices
- Trivial or temporary information
- Sensitive information (passwords, API keys) unless explicitly asked to remember
- Information that describes what the AI is or does

If the text is primarily instructions FOR an AI assistant rather than information ABOUT a human, return an empty array [].

For each extracted memory, determine:
1. content: A clear, standalone statement about the human (use "The user..." or their name)
2. type: One of: identity, fact, preference, decision, event, observation, goal, todo, routine, feedback, anomaly, correlation, prediction
3. importance: 0.0-1.0 based on how important this is to remember about the human

Respond with a JSON array of extracted memories. If nothing worth extracting about the human, return an empty array [].`

// buildExtractionPrompt creates the user prompt for extraction
func buildExtractionPrompt(conversation string, sourceFile string, sourceType string) string {
	var sourceContext string

	if sourceType == "transcript" {
		sourceContext = `
Source type: CONVERSATION TRANSCRIPT

This is a conversation between a human user and an AI assistant.
- Lines starting with "User:" or "Human:" are from the human
- Lines starting with "Assistant:" or "Agent:" are from the AI

Extract facts that the HUMAN revealed about themselves, such as:
- Personal information they shared (name, job, family, location)
- Preferences they expressed
- Decisions they made
- Goals or plans they mentioned
- Events they described
- Problems they're trying to solve

Do NOT extract:
- What the AI said or suggested (unless the human agreed/confirmed it)
- Generic questions the human asked
- Technical troubleshooting details (unless it reveals something about them)

`
	} else if sourceType == "markdown" || sourceFile != "" {
		sourceContext = fmt.Sprintf(`
Source type: FILE
Source file: %s

File type hints:
- USER.md = Profile/information about the human user - EXTRACT from this
- MEMORY.md, memory/*.md = Notes and memories about the human - EXTRACT from this
- TODO.md, PROJECTS.md = Human's tasks and projects - EXTRACT from this
- HOUSEHOLD.md = Information about the human's home/family - EXTRACT from this
- AGENTS.md, SOUL.md, IDENTITY.md = AI assistant instructions - SKIP these (not about the human)
- TOOLS.md, HEARTBEAT.md = AI configuration - SKIP these

`, sourceFile)
	}

	return fmt.Sprintf(`Extract important memories about the HUMAN USER from this content:
%s
<content>
%s
</content>

Remember: Extract facts ABOUT the human, not instructions FOR the AI.
If this is primarily AI configuration or instructions, return an empty array [].

Respond with a JSON array of objects, each with: content, type, importance (0.0-1.0), and optionally confidence (0.0-1.0 for pattern types).
Example: [{"content": "The user prefers dark mode", "type": "preference", "importance": 0.7}]`, sourceContext, conversation)
}

// parseExtractionResponse parses the LLM's JSON response
func parseExtractionResponse(response string) ([]ExtractedMemory, error) {
	// Clean up the response - find JSON array
	response = strings.TrimSpace(response)
	
	// Try to find JSON array in response
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	
	if start == -1 || end == -1 || end < start {
		// Try parsing as-is
		var memories []ExtractedMemory
		if err := json.Unmarshal([]byte(response), &memories); err != nil {
			return nil, fmt.Errorf("no valid JSON array found in response")
		}
		return memories, nil
	}

	jsonStr := response[start : end+1]
	
	var memories []ExtractedMemory
	if err := json.Unmarshal([]byte(jsonStr), &memories); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Validate and fix up extracted memories
	for i := range memories {
		// Validate type
		if !isValidType(memories[i].Type) {
			memories[i].Type = TypeObservation // Default to observation
		}

		// Clamp importance
		if memories[i].Importance < 0 {
			memories[i].Importance = 0.5
		}
		if memories[i].Importance > 1 {
			memories[i].Importance = 1.0
		}

		// Set default importance if zero
		if memories[i].Importance == 0 {
			if def, ok := DefaultImportance[memories[i].Type]; ok {
				memories[i].Importance = def
			} else {
				memories[i].Importance = 0.5
			}
		}
	}

	return memories, nil
}

// isValidType checks if a memory type is valid
func isValidType(t Type) bool {
	validTypes := map[Type]bool{
		TypeIdentity:    true,
		TypeFact:        true,
		TypePreference:  true,
		TypeDecision:    true,
		TypeEvent:       true,
		TypeObservation: true,
		TypeGoal:        true,
		TypeTodo:        true,
		TypeRoutine:     true,
		TypeFeedback:    true,
		TypeAnomaly:     true,
		TypeCorrelation: true,
		TypePrediction:  true,
	}
	return validTypes[t]
}

// ExtractFromMessages extracts memories from a slice of messages
func (e *Extractor) ExtractFromMessages(ctx context.Context, messages []types.Message, ec ExtractionContext) (*ExtractionResult, error) {
	// Build conversation text from messages
	var sb strings.Builder
	for _, msg := range messages {
		role := msg.Role
		if role == "assistant" {
			role = "Agent"
		} else if role == "user" {
			role = "User"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n\n", role, msg.Content))
	}

	ec.Conversation = sb.String()
	return e.Extract(ctx, ec)
}
