// Package types provides shared type definitions to avoid import cycles.
package types

// ToolDefinition is the format required by LLM APIs for tool/function calling.
// This lives in types to break the llm → tools import cycle.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}
