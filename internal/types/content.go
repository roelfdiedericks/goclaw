// Package types provides shared types for content blocks and tool results.
package types

// ContentBlock represents a single block of content in a message or tool result.
// Supports text, images, and audio with ephemeral media resolution.
type ContentBlock struct {
	Type string `json:"type"` // "text", "image", or "audio"

	// Text content
	Text string `json:"text,omitempty"`

	// Media fields (shared pattern for image/audio)
	// FilePath is stored in session; Data is resolved at LLM request time
	FilePath string `json:"filePath,omitempty"` // Disk reference (stored in session)
	Data     string `json:"data,omitempty"`     // Base64 data (resolved at LLM time, NOT stored)
	MimeType string `json:"mimeType,omitempty"` // e.g., "image/jpeg", "audio/ogg"

	// Audio-specific
	Duration int `json:"duration,omitempty"` // Duration in seconds (for audio)

	// Source tracking
	Source string `json:"source,omitempty"` // "telegram", "camera", "browser", etc.
}

// ToolResult represents the structured result from a tool execution.
// Tools return this instead of a plain string.
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"is_error,omitempty"`
}

// TextResult creates a ToolResult with a single text block.
func TextResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: text},
		},
	}
}

// ErrorResult creates a ToolResult with an error message.
func ErrorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: msg},
		},
		IsError: true,
	}
}

// ImageRefResult creates a ToolResult with text and an image file reference.
// The gateway will resolve the FilePath to base64 Data at LLM request time.
func ImageRefResult(filePath, mimeType, caption string) *ToolResult {
	blocks := []ContentBlock{}
	if caption != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: caption})
	}
	blocks = append(blocks, ContentBlock{
		Type:     "image",
		FilePath: filePath,
		MimeType: mimeType,
	})
	return &ToolResult{Content: blocks}
}

// AudioRefResult creates a ToolResult with an audio file reference.
func AudioRefResult(filePath, mimeType string, duration int, source string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{
			{
				Type:     "audio",
				FilePath: filePath,
				MimeType: mimeType,
				Duration: duration,
				Source:   source,
			},
		},
	}
}

// TextBlock creates a text ContentBlock.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// ImageBlock creates an image ContentBlock with a file reference.
func ImageBlock(filePath, mimeType, source string) ContentBlock {
	return ContentBlock{
		Type:     "image",
		FilePath: filePath,
		MimeType: mimeType,
		Source:   source,
	}
}

// AudioBlock creates an audio ContentBlock with a file reference.
func AudioBlock(filePath, mimeType string, duration int, source string) ContentBlock {
	return ContentBlock{
		Type:     "audio",
		FilePath: filePath,
		MimeType: mimeType,
		Duration: duration,
		Source:   source,
	}
}

// GetText returns the concatenated text from all text blocks.
func (r *ToolResult) GetText() string {
	if r == nil {
		return ""
	}
	var result string
	for _, block := range r.Content {
		if block.Type == "text" && block.Text != "" {
			if result != "" {
				result += "\n"
			}
			result += block.Text
		}
	}
	return result
}

// HasMedia returns true if the result contains any image or audio blocks.
func (r *ToolResult) HasMedia() bool {
	if r == nil {
		return false
	}
	for _, block := range r.Content {
		if block.Type == "image" || block.Type == "audio" {
			return true
		}
	}
	return false
}
