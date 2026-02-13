package transcript

import (
	"regexp"
	"strings"
)

// Message represents a message from the sessions database
type Message struct {
	ID         string
	SessionKey string
	Timestamp  int64
	Role       string
	Content    string
	UserID     string
}

// minContentLength is the minimum content length to index
const minContentLength = 50

// codeBlockPattern matches markdown code blocks
var codeBlockPattern = regexp.MustCompile("(?s)```[^`]*```")

// ShouldIndex determines if a message should be indexed
func ShouldIndex(msg *Message) bool {
	// Skip tool-related messages
	if msg.Role == "tool_use" || msg.Role == "tool_result" {
		return false
	}

	// Skip system messages
	if msg.Role == "system" {
		return false
	}

	// Only index user and assistant messages
	if msg.Role != "user" && msg.Role != "assistant" {
		return false
	}

	// Skip heartbeat messages
	content := strings.ToLower(msg.Content)
	if strings.Contains(content, "heartbeat") {
		return false
	}

	// Skip memory checkpoint messages
	if strings.Contains(content, "memory checkpoint") {
		return false
	}

	// Skip very short messages
	if len(msg.Content) < minContentLength {
		return false
	}

	// Skip messages that are predominantly code blocks (>80% code)
	if isCodeHeavy(msg.Content) {
		return false
	}

	return true
}

// isCodeHeavy returns true if the content is predominantly code blocks
func isCodeHeavy(content string) bool {
	if len(content) == 0 {
		return false
	}

	// Find all code blocks
	codeBlocks := codeBlockPattern.FindAllString(content, -1)
	if len(codeBlocks) == 0 {
		return false
	}

	// Calculate total code length
	codeLength := 0
	for _, block := range codeBlocks {
		codeLength += len(block)
	}

	// Check if code is more than 80% of content
	return float64(codeLength) > float64(len(content))*0.8
}

// CleanContent removes code blocks and normalizes whitespace for better embedding
func CleanContent(content string) string {
	// Remove code blocks (they don't help with semantic search)
	cleaned := codeBlockPattern.ReplaceAllString(content, " [code block] ")

	// Normalize whitespace
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	return cleaned
}
