package subagent_cancel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cronpkg "github.com/roelfdiedericks/goclaw/internal/cron"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool cancels an active delegated subagent run.
type Tool struct{}

func NewTool() *Tool { return &Tool{} }

func (t *Tool) Name() string { return "subagent_cancel" }

func (t *Tool) Description() string {
	return "Cancel an active delegated run by runId. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"runId": map[string]any{
				"type":        "string",
				"description": "Delegated run ID to cancel",
			},
			"cascade": map[string]any{
				"type":        "boolean",
				"description": "If true (default), cancel child delegated runs as well",
			},
		},
		"required": []string{"runId"},
	}
}

type cancelInput struct {
	RunID   string `json:"runId"`
	Cascade *bool  `json:"cascade"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in cancelInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return nil, fmt.Errorf("runId is required")
	}

	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil || sessionCtx.User == nil || !sessionCtx.User.IsOwner() {
		return nil, fmt.Errorf("subagent tools are owner-only")
	}

	service := cronpkg.GetService()
	if service == nil {
		return nil, fmt.Errorf("cron service is not running")
	}

	cascade := true
	if in.Cascade != nil {
		cascade = *in.Cascade
	}

	var err error
	if cascade {
		err = service.CancelDelegatedRunCascade(runID)
	} else {
		err = service.CancelDelegatedRun(runID)
	}
	if err != nil {
		L_warn("subagent_cancel: failed", "runID", runID, "error", err)
		return nil, err
	}

	L_info("subagent_cancel: requested", "runID", runID, "cascade", cascade)
	out, _ := json.MarshalIndent(map[string]any{
		"ok":      true,
		"runId":   runID,
		"cascade": cascade,
	}, "", "  ")
	return types.TextResult(string(out)), nil
}

