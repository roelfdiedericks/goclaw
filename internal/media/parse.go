// Package media provides image processing and storage utilities for GoClaw.
// parse.go implements MEDIA: token parsing from tool/agent output text.
package media

import (
	"regexp"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// MediaTokenRE matches MEDIA: tokens in output text.
// Format: MEDIA:<path_or_url>
// Allows optional wrapping backticks and captures the path/URL.
var MediaTokenRE = regexp.MustCompile(`\bMEDIA:\s*` + "`?" + `([^\n` + "`" + `]+)` + "`?")

// ParseResult contains the parsed output with media URLs extracted.
type ParseResult struct {
	Text      string   // Cleaned text with MEDIA: lines removed
	MediaURLs []string // Extracted media URLs/paths
}

// SplitMediaFromOutput parses MEDIA: tokens from output text and validates paths.
// Security: Only accepts:
//   - Relative paths starting with ./ (no directory traversal)
//   - HTTPS URLs
//
// Rejects:
//   - Absolute paths (/path/to/file)
//   - Tilde paths (~/path)
//   - Directory traversal (../path)
//   - HTTP (non-secure) URLs
//
// This matches OpenClaw's security model for MEDIA: tokens.
func SplitMediaFromOutput(raw string) ParseResult {
	if raw == "" {
		return ParseResult{Text: ""}
	}

	var mediaURLs []string
	var keptLines []string

	lines := strings.Split(raw, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if line starts with MEDIA:
		if !strings.HasPrefix(trimmed, "MEDIA:") {
			keptLines = append(keptLines, line)
			continue
		}

		// Try to extract media tokens from this line
		matches := MediaTokenRE.FindAllStringSubmatch(line, -1)
		if len(matches) == 0 {
			// No valid matches, keep the line as-is
			keptLines = append(keptLines, line)
			continue
		}

		hasValidMedia := false
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}

			candidate := cleanCandidate(match[1])
			if isValidMediaPath(candidate) {
				mediaURLs = append(mediaURLs, candidate)
				hasValidMedia = true
				logging.L_trace("media: extracted valid path", "path", candidate)
			} else {
				logging.L_trace("media: rejected invalid path", "path", candidate)
			}
		}

		// If we found valid media, remove the line; otherwise keep it
		if !hasValidMedia {
			keptLines = append(keptLines, line)
		}
	}

	// Clean up the text
	cleanedText := strings.Join(keptLines, "\n")
	cleanedText = strings.TrimSpace(cleanedText)

	// Collapse multiple newlines
	for strings.Contains(cleanedText, "\n\n\n") {
		cleanedText = strings.ReplaceAll(cleanedText, "\n\n\n", "\n\n")
	}

	result := ParseResult{
		Text:      cleanedText,
		MediaURLs: mediaURLs,
	}

	if len(mediaURLs) > 0 {
		logging.L_debug("media: parsed output", "mediaCount", len(mediaURLs), "textLength", len(cleanedText))
	}

	return result
}

// cleanCandidate removes common wrapping characters from a media path.
func cleanCandidate(raw string) string {
	s := strings.TrimSpace(raw)
	// Remove leading/trailing quotes, backticks, brackets
	s = strings.Trim(s, `"'`+"`"+`[]{}()`)
	return strings.TrimSpace(s)
}

// isValidMediaPath checks if a path is safe to use for media.
// Security model matches OpenClaw:
//   - Allow: ./relative/path (no traversal)
//   - Allow: https://url
//   - Reject: /absolute/path
//   - Reject: ~/path
//   - Reject: ../traversal
//   - Reject: http:// (non-secure)
func isValidMediaPath(path string) bool {
	if path == "" {
		return false
	}

	// Length sanity check
	if len(path) > 4096 {
		return false
	}

	// HTTPS URLs are allowed
	if strings.HasPrefix(path, "https://") {
		return true
	}

	// HTTP (non-secure) URLs are rejected
	if strings.HasPrefix(path, "http://") {
		return false
	}

	// Reject absolute paths
	if strings.HasPrefix(path, "/") {
		return false
	}

	// Reject tilde paths
	if strings.HasPrefix(path, "~") {
		return false
	}

	// Reject file:// URLs
	if strings.HasPrefix(path, "file://") {
		return false
	}

	// Only allow paths starting with ./
	if !strings.HasPrefix(path, "./") {
		return false
	}

	// Reject directory traversal
	if strings.Contains(path, "..") {
		return false
	}

	return true
}

// IsValidMediaPath is exported for use by other packages.
func IsValidMediaPath(path string) bool {
	return isValidMediaPath(path)
}
