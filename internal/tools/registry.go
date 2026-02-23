package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Registry holds all registered tools
type Registry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewRegistry creates a new tool registry
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

// Get returns a tool by name
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Has returns true if a tool with the given name is registered
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// Execute runs a tool by name with the given input
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (*types.ToolResult, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}

	return tool.Execute(ctx, input)
}

// List returns all registered tool names
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Definitions returns all tools in Anthropic API format
func (r *Registry) Definitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, ToDefinition(tool))
	}
	return defs
}

// Count returns the number of registered tools
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// BuildToolSummary generates a system prompt section listing available tools.
// This is useful for LLM providers that benefit from seeing tool lists in the
// system prompt (e.g., non-Anthropic models). Anthropic models handle tools
// well from schema alone, so this is optional for Claude.
//
// Returns a formatted string like:
//
//	## Available Tools
//	- read: Read file contents from the workspace
//	- write: Create or overwrite files
//	- browser: Headless browser for JavaScript-rendered pages...
func (r *Registry) BuildToolSummary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.tools) == 0 {
		return ""
	}

	// Get sorted tool names for consistent output
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("## Available Tools\n")
	sb.WriteString("Tool names are case-sensitive. Call tools exactly as listed.\n")

	for _, name := range names {
		tool := r.tools[name]
		desc := tool.Description()

		// Truncate long descriptions to first sentence or 80 chars
		summary := truncateDescription(desc, 100)
		sb.WriteString(fmt.Sprintf("- %s: %s\n", name, summary))
	}

	return sb.String()
}

// truncateDescription shortens a description for the summary view
func truncateDescription(desc string, maxLen int) string {
	// First try to get just the first sentence
	if idx := strings.Index(desc, ". "); idx > 0 && idx < maxLen {
		return desc[:idx+1]
	}

	// Otherwise truncate at maxLen
	if len(desc) <= maxLen {
		return desc
	}

	// Find last space before maxLen to avoid cutting words
	truncated := desc[:maxLen]
	if idx := strings.LastIndex(truncated, " "); idx > maxLen/2 {
		truncated = truncated[:idx]
	}
	return truncated + "..."
}
