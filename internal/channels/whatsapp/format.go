package whatsapp

import (
	"regexp"
	"strings"
)

var (
	// Markdown bold **text** -> WhatsApp bold *text*
	boldPattern = regexp.MustCompile(`\*\*(.+?)\*\*`)

	// Markdown strikethrough ~~text~~ -> WhatsApp ~text~
	strikePattern = regexp.MustCompile(`~~(.+?)~~`)

	// Markdown headers ## text -> WhatsApp bold *TEXT*
	headerPattern = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)

	// Markdown links [text](url) -> text (url)
	linkPattern = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

	// HTML tags (strip them)
	htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

	// Markdown image syntax ![alt](url) -> just the URL
	imagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
)

// FormatMessage converts markdown to WhatsApp-compatible formatting.
// WhatsApp supports: *bold*, _italic_, ~strikethrough~, ```code blocks```, `inline code`
func FormatMessage(markdown string) string {
	if markdown == "" {
		return ""
	}

	text := markdown

	// Strip images first (before link processing)
	text = imagePattern.ReplaceAllString(text, "$2")

	// Convert markdown links [text](url) -> text (url)
	text = linkPattern.ReplaceAllString(text, "$1 ($2)")

	// Convert headers to bold (WhatsApp has no header concept)
	text = headerPattern.ReplaceAllString(text, "*$1*")

	// Convert **bold** to *bold* (WhatsApp bold syntax)
	text = boldPattern.ReplaceAllString(text, "*$1*")

	// Convert ~~strikethrough~~ to ~strikethrough~
	text = strikePattern.ReplaceAllString(text, "~$1~")

	// Markdown _italic_ and *italic* stay as-is (WhatsApp uses _italic_)
	// Single * without closing is already handled by WhatsApp as bold

	// Code blocks: ```code``` works natively in WhatsApp
	// Inline code: `code` works natively in WhatsApp

	// Strip any HTML tags
	text = htmlTagPattern.ReplaceAllString(text, "")

	// Clean up excessive blank lines
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(text)
}
