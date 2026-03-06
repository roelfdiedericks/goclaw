// Package media_display provides a tool for displaying media to users.
// This tool is primarily used in voice sessions where inline {{media:}} syntax
// would be spoken aloud. Instead, the tool creates a synthetic assistant message
// that flows through the normal enrichment and mirroring pipeline.
package media_display

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// RecordFunc is called to record a synthetic assistant message.
// This creates a message with {{media:path}} that flows through enrichMediaRefs and mirroring.
type RecordFunc func(ctx context.Context, u *user.User, source, message string) error

// Tool displays media to users without requiring inline {{media:}} syntax.
type Tool struct {
	recordFn RecordFunc
}

// NewTool creates a new media_display tool with the given record function.
// The recordFn is called to create synthetic assistant messages containing media references.
func NewTool(recordFn RecordFunc) *Tool {
	return &Tool{recordFn: recordFn}
}

func (t *Tool) Name() string {
	return "media_display"
}

func (t *Tool) Description() string {
	return "Display media (images, videos, screenshots) to the user. Use this in voice sessions instead of inline {{media:}} syntax. The media appears on the user's screen while you continue speaking."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Media path relative to media root (e.g., 'camera/snapshot.jpg', 'screenshots/desktop.png')",
			},
			"caption": map[string]any{
				"type":        "string",
				"description": "Optional caption to display with the media",
			},
		},
		"required": []string{"path"},
	}
}

type input struct {
	Path    string `json:"path"`
	Caption string `json:"caption"`
}

func (t *Tool) Execute(ctx context.Context, rawInput json.RawMessage) (*types.ToolResult, error) {
	var params input
	if err := json.Unmarshal(rawInput, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if params.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	if t.recordFn == nil {
		L_error("media_display: recordFn not set")
		return nil, fmt.Errorf("media_display tool not properly configured")
	}

	// Get session context to determine user and channel
	sessCtx := types.GetSessionContext(ctx)
	if sessCtx == nil {
		L_error("media_display: no session context")
		return nil, fmt.Errorf("no session context available")
	}
	if sessCtx.User == nil {
		L_error("media_display: no user in session context")
		return nil, fmt.Errorf("no user context available")
	}

	// Build synthetic message with media reference
	// Format: "caption\n\n{{media:path}}" or just "{{media:path}}"
	var message string
	if params.Caption != "" {
		message = fmt.Sprintf("%s\n\n{{media:%s}}", params.Caption, params.Path)
	} else {
		message = fmt.Sprintf("{{media:%s}}", params.Path)
	}

	// Record the synthetic assistant message
	// This flows through enrichMediaRefs and mirrorToOthers
	if err := t.recordFn(ctx, sessCtx.User, sessCtx.Channel, message); err != nil {
		L_error("media_display: failed to record message", "error", err, "path", params.Path)
		return nil, fmt.Errorf("failed to display media: %w", err)
	}

	L_info("media_display: media sent", "path", params.Path, "user", sessCtx.User.Name, "channel", sessCtx.Channel)

	// Return success confirmation to the model
	return types.TextResult(fmt.Sprintf("Media displayed: %s", params.Path)), nil
}
