package telegram

import (
	"bytes"

	"github.com/leonid-shevtsov/telegold"
	"github.com/yuin/goldmark"
)

// Telegram HTML renderer using goldmark + telegold
var telegramMarkdown = goldmark.New(goldmark.WithRenderer(telegold.NewRenderer()))

// FormatMessage converts markdown to Telegram-compatible HTML.
// If conversion fails, returns the original markdown as fallback.
func FormatMessage(markdown string) string {
	if markdown == "" {
		return ""
	}

	var buf bytes.Buffer
	if err := telegramMarkdown.Convert([]byte(markdown), &buf); err != nil {
		// Fallback to raw text on conversion error
		return markdown
	}

	result := buf.String()
	if result == "" {
		return markdown
	}

	return result
}

// FormatMessageSafe converts markdown to Telegram HTML, returning both
// the formatted result and whether conversion succeeded.
// Use this when you need to know if fallback was used.
func FormatMessageSafe(markdown string) (formatted string, ok bool) {
	if markdown == "" {
		return "", true
	}

	var buf bytes.Buffer
	if err := telegramMarkdown.Convert([]byte(markdown), &buf); err != nil {
		return markdown, false
	}

	result := buf.String()
	if result == "" {
		return markdown, false
	}

	return result, true
}
