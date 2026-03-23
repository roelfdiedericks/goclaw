package subagent_status

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

// Tool queries delegated subagent run status.
type Tool struct{}

func NewTool() *Tool { return &Tool{} }

func (t *Tool) Name() string { return "subagent_status" }

func (t *Tool) Description() string {
	return "Get delegated run status by runId, or list recent delegated runs. Owner sessions only."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"runId": map[string]any{
				"type":        "string",
				"description": "Delegated run ID to inspect",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "List mode: max items to return (default 20)",
			},
		},
	}
}

type statusInput struct {
	RunID string `json:"runId"`
	Limit int    `json:"limit"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var in statusInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil || sessionCtx.User == nil || !sessionCtx.User.IsOwner() {
		return nil, fmt.Errorf("subagent tools are owner-only")
	}

	service := cronpkg.GetService()
	if service == nil {
		return nil, fmt.Errorf("cron service is not running")
	}

	if strings.TrimSpace(in.RunID) != "" {
		runID := strings.TrimSpace(in.RunID)
		L_debug("subagent_status: single run query", "runID", runID)
		rec, ok := service.GetDelegatedRun(runID)
		if !ok {
			L_warn("subagent_status: run not found", "runID", runID)
			return nil, fmt.Errorf("run not found: %s", in.RunID)
		}
		out, _ := json.MarshalIndent(map[string]any{
			"run": normalizeRecord(rec),
		}, "", "  ")
		L_debug("subagent_status: single run returned", "runID", runID, "state", rec.State)
		return types.TextResult(string(out)), nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	recs := service.ListDelegatedRuns()
	if len(recs) > limit {
		recs = recs[:limit]
	}
	L_debug("subagent_status: list query", "limit", limit, "returned", len(recs))
	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		items = append(items, normalizeRecord(rec))
	}
	out, _ := json.MarshalIndent(map[string]any{
		"items": items,
		"count": len(items),
	}, "", "  ")
	return types.TextResult(string(out)), nil
}

func normalizeRecord(rec delegatedrun.RunRecord) map[string]any {
	return map[string]any{
		"runId":         rec.RunID,
		"parentRunId":   rec.ParentRunID,
		"requesterType": rec.RequesterType,
		"requesterId":   rec.RequesterID,
		"sessionKey":    rec.SessionKey,
		"purpose":       rec.Purpose,
		"state":         rec.State,
		"startedAt":     rec.StartedAt,
		"finishedAt":    rec.FinishedAt,
		"result": map[string]any{
			"finalText": rec.Result.FinalText,
			"error":     rec.Result.Error,
			"usage":     rec.Result.Usage,
		},
	}
}

