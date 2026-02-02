// Package tools provides the tool execution framework.
package tools

import (
	"context"
	"encoding/json"
)

// Tool is the interface that all tools must implement
type Tool interface {
	// Name returns the unique name of the tool
	Name() string

	// Description returns a human-readable description for the LLM
	Description() string

	// Schema returns the JSON Schema for the tool's input parameters
	Schema() map[string]any

	// Execute runs the tool with the given input
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolDefinition is the format required by Anthropic's API
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToDefinition converts a Tool to the API format
func ToDefinition(t Tool) ToolDefinition {
	return ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: t.Schema(),
	}
}
