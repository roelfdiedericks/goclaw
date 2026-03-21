package contentguard

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMaxToolResultBytes caps text payload size kept in session context.
	DefaultMaxToolResultBytes = 64 * 1024
)

// Result describes a sanitization operation.
type Result struct {
	Text          string
	Changed       bool
	Reason        string
	MIME          string
	OriginalBytes int
}

// ToolResultText sanitizes tool result text for safe session/context usage.
func ToolResultText(text string) Result {
	return ToolResultBytes([]byte(text), "")
}

// ToolResultBytes sanitizes raw bytes for safe session/context usage.
func ToolResultBytes(data []byte, mimeHint string) Result {
	origSize := len(data)
	mime := mimeHint
	if mime == "" {
		mime = detectMIME(data)
	}
	if binary, reason := isBinary(data, mime); binary {
		return Result{
			Text:          fmt.Sprintf("[tool result omitted: %s | mime=%s | bytes=%d]", reason, mime, origSize),
			Changed:       true,
			Reason:        reason,
			MIME:          mime,
			OriginalBytes: origSize,
		}
	}
	text := string(data)
	if origSize > DefaultMaxToolResultBytes {
		return Result{
			Text:          truncateWithNotice(text, DefaultMaxToolResultBytes),
			Changed:       true,
			Reason:        "oversize",
			MIME:          mime,
			OriginalBytes: origSize,
		}
	}
	return Result{
		Text:          text,
		Changed:       false,
		Reason:        "",
		MIME:          mime,
		OriginalBytes: origSize,
	}
}

// IsBinaryContent reports whether payload appears binary.
func IsBinaryContent(data []byte, mimeHint string) bool {
	mime := mimeHint
	if mime == "" {
		mime = detectMIME(data)
	}
	binary, _ := isBinary(data, mime)
	return binary
}

func detectMIME(data []byte) string {
	if len(data) == 0 {
		return "application/octet-stream"
	}
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	return strings.ToLower(http.DetectContentType(sniff))
}

func isBinary(data []byte, mime string) (bool, string) {
	if len(data) == 0 {
		return false, ""
	}
	if strings.HasPrefix(mime, "application/pdf") ||
		strings.HasPrefix(mime, "application/octet-stream") ||
		strings.HasPrefix(mime, "application/zip") ||
		strings.HasPrefix(mime, "application/x-") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "video/") ||
		strings.HasPrefix(mime, "image/") {
		return true, "binary mime"
	}
	for _, b := range data {
		if b == 0x00 {
			return true, "contains nul byte"
		}
	}
	if !utf8.Valid(data) {
		return true, "invalid utf8"
	}
	nonPrintable := 0
	for _, b := range data {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			nonPrintable++
		}
	}
	if float64(nonPrintable)/float64(len(data)) > 0.10 {
		return true, "high control-byte ratio"
	}
	return false, ""
}

func truncateWithNotice(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	notice := fmt.Sprintf("\n[tool result truncated to %d bytes for context safety]", maxBytes)
	keep := maxBytes - len(notice)
	if keep < 0 {
		keep = 0
	}
	return text[:keep] + notice
}
