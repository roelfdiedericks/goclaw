package acp_inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

type Tool struct{}

func NewTool() *Tool { return &Tool{} }

func (t *Tool) Name() string { return "acp_inspect" }

func (t *Tool) Description() string {
	return "Inspect the currently attached or detached ACP session for this GoClaw session identity. Read-only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"detail": map[string]any{
				"type":        "string",
				"description": "Optional detail level hint, currently informational only.",
			},
		},
	}
}

type inspectInput struct {
	Detail string `json:"detail"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in inspectInput
	_ = json.Unmarshal(input, &in)

	sc := types.GetSessionContext(ctx)
	if sc == nil || sc.User == nil {
		return nil, fmt.Errorf("missing session context")
	}
	sessionKey := strings.TrimSpace(sc.SessionKey)
	if sessionKey == "" {
		return nil, fmt.Errorf("missing session key")
	}
	mgr := acp.GetManager()
	if mgr == nil {
		return nil, fmt.Errorf("ACP manager not initialized")
	}
	info, err := mgr.Inspect(sessionKey)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("ACP session status\n")
	b.WriteString(fmt.Sprintf("  Session key: %s\n", info.SessionKey))
	b.WriteString(fmt.Sprintf("  Attached: %t\n", info.Attached))
	b.WriteString(fmt.Sprintf("  ACP session: %s\n", info.SessionID))
	b.WriteString(fmt.Sprintf("  Driver: %s\n", info.Driver))
	b.WriteString(fmt.Sprintf("  Transport: %s\n", info.Transport))
	b.WriteString(fmt.Sprintf("  Mode: %s\n", info.Mode))
	b.WriteString(fmt.Sprintf("  CWD: %s\n", info.CWD))
	if info.CurrentModel != "" {
		b.WriteString(fmt.Sprintf("  Model: %s\n", info.CurrentModel))
	}
	b.WriteString(fmt.Sprintf("  State: %s\n", info.CurrentState))
	b.WriteString(fmt.Sprintf("  Buffered events: %d\n", info.BufferedEvents))
	if info.LastAssistant != "" {
		b.WriteString(fmt.Sprintf("  Last assistant text: %s\n", info.LastAssistant))
	}
	if info.LastPlanName != "" {
		b.WriteString(fmt.Sprintf("  Last plan: %s\n", info.LastPlanName))
	}
	if info.LastPlanOverview != "" {
		b.WriteString(fmt.Sprintf("  Last plan overview: %s\n", info.LastPlanOverview))
	}
	if info.LastQuestion != "" {
		b.WriteString(fmt.Sprintf("  Last question: %s\n", info.LastQuestion))
	}
	if len(info.Todos) > 0 {
		b.WriteString("  Todos:\n")
		for _, todo := range info.Todos {
			b.WriteString(fmt.Sprintf("    - [%s] %s\n", todo.Status, todo.Content))
		}
	}
	if len(info.PendingRequests) > 0 {
		b.WriteString("  Pending interactive requests:\n")
		for _, pending := range info.PendingRequests {
			b.WriteString(fmt.Sprintf("    - [%s] %s (%s", pending.Driver, pending.Method, pending.SemanticKind))
			if pending.ToolCallID != "" {
				b.WriteString(", tool=" + pending.ToolCallID)
			}
			b.WriteString(")\n")
		}
	}
	if len(info.RecentExtensions) > 0 {
		b.WriteString("  Recent driver extensions:\n")
		for _, ext := range info.RecentExtensions {
			b.WriteString(fmt.Sprintf("    - [%s] %s (%s", ext.Driver, ext.Method, ext.SemanticKind))
			if ext.ToolCallID != "" {
				b.WriteString(", tool=" + ext.ToolCallID)
			}
			if ext.Summary != "" {
				b.WriteString(": " + ext.Summary)
			}
			b.WriteString(")\n")
		}
	}
	return types.TextResult(b.String()), nil
}
