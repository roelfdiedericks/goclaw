package subagent_cancel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cronpkg "github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
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
				"description": "The runId to stop.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"cancel", "kill"},
				"description": "How to stop it. cancel is the normal stop. kill is a hard stop and only works on the run itself.",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"self", "subtree"},
				"description": "What to stop. self stops only this run. subtree also stops its child runs.",
			},
			"cascade": map[string]any{
				"type":        "boolean",
				"description": "If true, also stop child runs. Default true.",
			},
		},
		"required": []string{"runId"},
	}
}

type cancelInput struct {
	RunID   string `json:"runId"`
	Mode    string `json:"mode"`
	Scope   string `json:"scope"`
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
	if sessionCtx == nil || sessionCtx.User == nil {
		return nil, delegatedrun.EnsureSubagentOwner(false)
	}
	if err := delegatedrun.EnsureSubagentOwner(sessionCtx.User.IsOwner()); err != nil {
		return nil, err
	}

	service := cronpkg.GetService()
	if service == nil {
		return nil, fmt.Errorf("cron service is not running")
	}

	control, err := delegatedrun.NormalizeCancelControl(delegatedrun.CancelControlInput{
		Mode:    in.Mode,
		Scope:   in.Scope,
		Cascade: in.Cascade,
	})
	if err != nil {
		return nil, err
	}
	rec, ok := service.GetDelegatedRun(runID)
	if !ok {
		return nil, fmt.Errorf("run not found: %s", runID)
	}
	if err := delegatedrun.ValidateCancelControlForRun(rec, control); err != nil {
		return nil, err
	}

	if control.Mode == "kill" {
		err = service.KillDelegatedRun(runID)
	} else if control.Cascade {
		err = service.CancelDelegatedRunCascade(runID)
	} else {
		err = service.CancelDelegatedRun(runID)
	}
	if err != nil {
		L_warn("subagent_cancel: failed", "runID", runID, "mode", control.Mode, "scope", control.Scope, "error", err)
		return nil, err
	}

	L_info("subagent_cancel: requested", "runID", runID, "mode", control.Mode, "scope", control.Scope, "cascade", control.Cascade)
	out, _ := json.MarshalIndent(map[string]any{
		"ok":      true,
		"runId":   runID,
		"mode":    control.Mode,
		"scope":   control.Scope,
		"cascade": control.Cascade,
	}, "", "  ")
	return types.TextResult(string(out)), nil
}
