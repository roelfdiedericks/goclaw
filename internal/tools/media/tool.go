package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mediastore "github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool inspects the live media store state.
type Tool struct {
	store *mediastore.MediaStore
}

func NewTool(store *mediastore.MediaStore) *Tool {
	return &Tool{store: store}
}

func (t *Tool) Name() string { return "media" }

func (t *Tool) Description() string {
	return "Inspect GoClaw's media storage usage, quotas, retention, and category policies. Use this before saving or generating large files, or when deciding between keeper, uploads, and temporary media categories."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"info"},
				"description": "Media store action to perform. Use \"info\" to inspect storage configuration, usage, quotas, retention, and warnings.",
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        mediastore.KnownCategories(),
				"description": "Optional top-level media category to focus on. If omitted, returns a full store summary.",
			},
			"includeWarnings": map[string]any{
				"type":        "boolean",
				"description": "Whether to include warning details in the response. Defaults to true.",
			},
		},
		"required": []string{"action"},
	}
}

type input struct {
	Action          string `json:"action"`
	Category        string `json:"category"`
	IncludeWarnings *bool  `json:"includeWarnings"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type output struct {
	OK         bool                                    `json:"ok"`
	Action     string                                  `json:"action"`
	Category   string                                  `json:"category,omitempty"`
	Summary    string                                  `json:"summary"`
	Store      mediastore.MediaStoreInfo               `json:"store,omitempty"`
	Categories map[string]mediastore.MediaCategoryInfo `json:"categories,omitempty"`
	Warnings   []string                                `json:"warnings,omitempty"`
	Error      *errorPayload                           `json:"error,omitempty"`
}

func (t *Tool) Execute(ctx context.Context, rawInput json.RawMessage) (*types.ToolResult, error) {
	var in input
	if err := json.Unmarshal(rawInput, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	action := strings.TrimSpace(in.Action)
	if action == "" {
		return nil, fmt.Errorf("action is required")
	}
	if action != "info" {
		return nil, fmt.Errorf("unsupported media action %q", in.Action)
	}

	includeWarnings := true
	if in.IncludeWarnings != nil {
		includeWarnings = *in.IncludeWarnings
	}

	info, err := t.storeInfo(in.Category, includeWarnings)
	if err != nil {
		return marshalOutput(buildErrorOutput(action, in.Category, err))
	}

	result := output{
		OK:         true,
		Action:     action,
		Summary:    mediastore.FormatInfoSummary(info, strings.TrimSpace(in.Category)),
		Store:      info.Store,
		Categories: info.Categories,
		Warnings:   info.Warnings,
	}
	if strings.TrimSpace(in.Category) != "" {
		result.Category = strings.TrimSpace(in.Category)
	}
	return marshalOutput(result)
}

func (t *Tool) storeInfo(category string, includeWarnings bool) (mediastore.MediaInfoSnapshot, error) {
	if t.store != nil {
		return mediastore.BuildStoreInfo(t.store, category, includeWarnings)
	}
	return mediastore.CurrentStoreInfo(category, includeWarnings)
}

func buildErrorOutput(action, category string, err error) output {
	out := output{
		OK:      false,
		Action:  action,
		Summary: "Failed to inspect media storage.",
		Error: &errorPayload{
			Code:    "unknown",
			Message: err.Error(),
		},
	}
	if strings.TrimSpace(category) != "" {
		out.Category = strings.TrimSpace(category)
	}
	switch {
	case errors.Is(err, mediastore.ErrStoreNotInitialized):
		out.Summary = "Media store is not initialized."
		out.Error.Code = "not_initialized"
		out.Error.Message = "Media store is not initialized yet."
	case errors.Is(err, mediastore.ErrUnknownCategory):
		out.Summary = "Unknown media category."
		out.Error.Code = "invalid_category"
		out.Error.Message = err.Error()
	}
	return out
}

func marshalOutput(v any) (*types.ToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return types.TextResult(string(b)), nil
}
