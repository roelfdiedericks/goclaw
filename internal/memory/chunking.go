package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// DefaultChunkTokens is the target size for each chunk in tokens
	// (~4 chars per token is a rough estimate)
	DefaultChunkTokens = 400

	// DefaultChunkOverlap is the overlap between chunks in tokens
	DefaultChunkOverlap = 80

	// charsPerToken is an estimate for token counting
	charsPerToken = 4
)

// Chunk represents a text chunk from a file
type Chunk struct {
	Text      string // The actual text content
	StartLine int    // 1-indexed start line
	EndLine   int    // 1-indexed end line (inclusive)
	Hash      string // SHA256 hash of the text
}

// ChunkOptions configures the chunking behavior
type ChunkOptions struct {
	TargetTokens  int // Target tokens per chunk (default: 400)
	OverlapTokens int // Overlap tokens between chunks (default: 80)
}

// DefaultChunkOptions returns default chunking options
func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{
		TargetTokens:  DefaultChunkTokens,
		OverlapTokens: DefaultChunkOverlap,
	}
}

// ChunkMarkdown splits markdown content into overlapping chunks
// The chunking is line-aware and tries to respect markdown structure
func ChunkMarkdown(content string, opts ChunkOptions) []Chunk {
	if opts.TargetTokens <= 0 {
		opts.TargetTokens = DefaultChunkTokens
	}
	if opts.OverlapTokens < 0 {
		opts.OverlapTokens = DefaultChunkOverlap
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	targetChars := opts.TargetTokens * charsPerToken
	overlapChars := opts.OverlapTokens * charsPerToken

	var chunks []Chunk
	currentChunk := strings.Builder{}
	currentStartLine := 1
	currentCharCount := 0

	for i, line := range lines {
		lineNum := i + 1         // 1-indexed
		lineLen := len(line) + 1 // +1 for newline

		// Check if adding this line would exceed target
		if currentCharCount > 0 && currentCharCount+lineLen > targetChars {
			// Save current chunk
			text := strings.TrimSpace(currentChunk.String())
			if text != "" {
				chunks = append(chunks, Chunk{
					Text:      text,
					StartLine: currentStartLine,
					EndLine:   lineNum - 1,
					Hash:      hashText(text),
				})
			}

			// Start new chunk with overlap
			// Find overlap start position
			overlapStart := findOverlapStart(lines, lineNum-1, overlapChars)
			currentChunk.Reset()
			currentStartLine = overlapStart + 1
			currentCharCount = 0

			// Add overlap lines
			for j := overlapStart; j < i; j++ {
				currentChunk.WriteString(lines[j])
				currentChunk.WriteString("\n")
				currentCharCount += len(lines[j]) + 1
			}
		}

		// Add current line
		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
		currentCharCount += lineLen
	}

	// Don't forget the last chunk
	text := strings.TrimSpace(currentChunk.String())
	if text != "" {
		chunks = append(chunks, Chunk{
			Text:      text,
			StartLine: currentStartLine,
			EndLine:   len(lines),
			Hash:      hashText(text),
		})
	}

	return chunks
}

// findOverlapStart finds the line index to start overlap from
// Returns 0-indexed line number
func findOverlapStart(lines []string, endLine int, overlapChars int) int {
	charCount := 0
	startLine := endLine

	for i := endLine - 1; i >= 0; i-- {
		lineLen := len(lines[i]) + 1
		if charCount+lineLen > overlapChars {
			break
		}
		charCount += lineLen
		startLine = i
	}

	return startLine
}

// hashText returns SHA256 hash of text
func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// estimateTokens estimates the number of tokens in text
func estimateTokens(text string) int {
	return (len(text) + charsPerToken - 1) / charsPerToken
}
