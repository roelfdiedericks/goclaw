package memorygraph

import (
	"encoding/json"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func marshalOutput(v any) (*types.ToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return types.TextResult(string(b)), nil
}
