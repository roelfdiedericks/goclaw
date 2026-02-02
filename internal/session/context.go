package session

import (
	"fmt"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BuildSessionFromRecords creates a Session from JSONL records
// It respects compaction boundaries and builds the message history
func BuildSessionFromRecords(sessionID string, records []Record) (*Session, error) {
	sess := &Session{
		ID:        sessionID,
		Messages:  make([]Message, 0),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	var lastCompaction *CompactionRecord
	var lastCheckpoint *CheckpointRecord

	// First pass: find the last compaction and checkpoint
	for _, r := range records {
		switch rec := r.(type) {
		case *CompactionRecord:
			lastCompaction = rec
		case *CheckpointRecord:
			lastCheckpoint = rec
		case *SessionRecord:
			sess.CreatedAt = rec.Timestamp
		}
	}

	// Second pass: build messages, skipping those before compaction
	for _, r := range records {
		msgRec, ok := r.(*MessageRecord)
		if !ok {
			continue
		}

		// Skip records before last compaction
		if lastCompaction != nil && shouldSkipBeforeCompaction(msgRec.ID, lastCompaction.FirstKeptEntryID) {
			continue
		}

		msg := convertMessageRecord(msgRec)
		if msg != nil {
			sess.Messages = append(sess.Messages, *msg)
		}

		sess.UpdatedAt = msgRec.Timestamp
	}

	// If we have a compaction summary, prepend it as context
	if lastCompaction != nil && lastCompaction.Summary != "" {
		summaryMsg := Message{
			ID:        "compaction-summary",
			Role:      "user",
			Content:   fmt.Sprintf("[Previous context summary]\n%s", lastCompaction.Summary),
			Source:    "system",
			Timestamp: lastCompaction.Timestamp,
		}
		sess.Messages = append([]Message{summaryMsg}, sess.Messages...)
	}

	// Store checkpoint reference for later use
	if lastCheckpoint != nil {
		sess.LastCheckpoint = lastCheckpoint
	}

	L_debug("built session from records",
		"sessionId", sessionID,
		"messages", len(sess.Messages),
		"hasCompaction", lastCompaction != nil,
		"hasCheckpoint", lastCheckpoint != nil)

	return sess, nil
}

// shouldSkipBeforeCompaction determines if a record ID is before the compaction point
// Record IDs are either 8-char hex or timestamp_hex format
func shouldSkipBeforeCompaction(recordID, firstKeptID string) bool {
	// Simple string comparison works for both formats
	// Hex IDs: lexicographic comparison approximates chronological order
	// Timestamp IDs: lexicographic comparison is chronological
	return recordID < firstKeptID
}

// convertMessageRecord converts a JSONL MessageRecord to a session Message
func convertMessageRecord(rec *MessageRecord) *Message {
	msg := &Message{
		ID:        rec.ID,
		Timestamp: rec.Timestamp,
	}

	switch rec.Message.Role {
	case "user":
		msg.Role = "user"
		msg.Content = ExtractTextContent(rec.Message.Content)
		// Try to extract source from message metadata if available
		msg.Source = "unknown"
		
		// Skip empty user messages
		if msg.Content == "" {
			return nil
		}

	case "assistant":
		msg.Role = "assistant"
		msg.Content = ExtractTextContent(rec.Message.Content)

		// Extract tool calls if present
		toolCalls := ExtractToolCalls(rec.Message.Content)
		if len(toolCalls) > 0 {
			// Store first tool call info (for simple cases)
			msg.ToolName = toolCalls[0].Name
			msg.ToolUseID = toolCalls[0].ID
			msg.ToolInput = toolCalls[0].Arguments
			
			// If this is a tool-only message (no text), mark as tool_use role
			if msg.Content == "" {
				msg.Role = "tool_use"
			}
		} else if msg.Content == "" {
			// Skip assistant messages with no text and no tool calls (errors, etc.)
			return nil
		}

	case "toolResult":
		msg.Role = "tool_result"
		msg.Content = ExtractTextContent(rec.Message.Content)
		msg.ToolUseID = rec.Message.ToolCallID
		msg.ToolName = rec.Message.ToolName

	default:
		// Unknown role, skip
		return nil
	}

	return msg
}

// BuildLLMMessages converts session messages to the format expected by the LLM
// This creates the message array to send to the Anthropic API
func BuildLLMMessages(sess *Session) []map[string]interface{} {
	var messages []map[string]interface{}

	for _, msg := range sess.Messages {
		switch msg.Role {
		case "user":
			messages = append(messages, map[string]interface{}{
				"role":    "user",
				"content": msg.Content,
			})

		case "assistant":
			content := msg.Content
			messages = append(messages, map[string]interface{}{
				"role":    "assistant",
				"content": content,
			})

		case "tool_use":
			// Tool use is part of assistant message in Anthropic format
			// This is handled differently - typically merged with assistant response

		case "tool_result":
			messages = append(messages, map[string]interface{}{
				"role":         "user",
				"content":      fmt.Sprintf("[Tool result for %s]\n%s", msg.ToolName, msg.Content),
				"tool_use_id":  msg.ToolUseID,
			})
		}
	}

	return messages
}

// GetMostRecentCheckpoint returns the most recent checkpoint from records
func GetMostRecentCheckpoint(records []Record) *CheckpointRecord {
	var latest *CheckpointRecord
	for _, r := range records {
		if cp, ok := r.(*CheckpointRecord); ok {
			latest = cp
		}
	}
	return latest
}

// GetCompactionCount counts the number of compaction records
func GetCompactionCount(records []Record) int {
	count := 0
	for _, r := range records {
		if _, ok := r.(*CompactionRecord); ok {
			count++
		}
	}
	return count
}

// GetLastRecordID returns the ID of the last record in the session
func GetLastRecordID(records []Record) *string {
	if len(records) == 0 {
		return nil
	}
	id := records[len(records)-1].GetID()
	return &id
}

// CalculateTotalTokens estimates total tokens from message records
func CalculateTotalTokens(records []Record) int {
	total := 0
	for _, r := range records {
		if msg, ok := r.(*MessageRecord); ok {
			if msg.Message.Usage != nil {
				total = msg.Message.Usage.TotalTokens
			}
		}
	}
	return total
}
