package delegatedrun

import (
	"fmt"
	"strings"
)

const defaultCompletionResultLimit = 3000
const CompletionPayloadSchema = "delegated_completion.v1"
const CompletionPayloadKind = "task_completion"
const DefaultReplyInstruction = "Interpret this delegated completion event, then reply in your normal assistant voice with the most relevant result details."

// CompletionEnvelope is the shared payload source for requester completion delivery.
type CompletionEnvelope struct {
	RunID              string
	State              RunState
	NormalizedStatus   string
	StatusLabel        string
	WaitError          string
	ToolError          string
	ResultText         string
	ResultFallback     string
	UsageLine          string
	ReplyInstruction   string
}

// BuildCompletionEnvelope normalizes delegated completion state into a reusable envelope.
func BuildCompletionEnvelope(runID string, state RunState, result RunResult, waitErr error) CompletionEnvelope {
	normalized, label := normalizeCompletionStatus(state)
	usageLine := formatUsageLine(result.Usage)
	toolErr := CompletionToolError(state, result, waitErr)
	fallback := completionResultFallback(state, toolErr, waitErr)
	env := CompletionEnvelope{
		RunID:            runID,
		State:            state,
		NormalizedStatus: normalized,
		StatusLabel:      label,
		ToolError:        toolErr,
		ResultFallback:   fallback,
		UsageLine:        usageLine,
		ReplyInstruction: DefaultReplyInstruction,
	}
	if waitErr != nil {
		env.WaitError = waitErr.Error()
	}
	env.ResultText = strings.TrimSpace(result.FinalText)
	return env
}

// RenderMessage renders a deterministic human-readable completion summary.
// Internal orchestration hints such as ReplyInstruction are intentionally excluded
// from this text so direct channel delivery stays user-facing.
func (e CompletionEnvelope) RenderMessage(maxResultChars int) string {
	if maxResultChars <= 0 {
		maxResultChars = defaultCompletionResultLimit
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Subagent run completed.\nrunId: %s\nstate: %s", e.RunID, e.State))
	if e.NormalizedStatus != "" {
		b.WriteString(fmt.Sprintf("\nstatus: %s", e.NormalizedStatus))
	}
	if e.WaitError != "" {
		b.WriteString(fmt.Sprintf("\nwaitError: %s", e.WaitError))
	}
	if e.ToolError != "" {
		b.WriteString(fmt.Sprintf("\nerror: %s", e.ToolError))
	}
	if e.UsageLine != "" {
		b.WriteString(fmt.Sprintf("\nusage: %s", e.UsageLine))
	}
	text := strings.TrimSpace(e.ResultText)
	if text != "" {
		if len(text) > maxResultChars {
			text = text[:maxResultChars] + "...(truncated)"
		}
		b.WriteString("\n\nresult:\n")
		b.WriteString(text)
	} else if e.ResultFallback != "" {
		b.WriteString("\n\nresult:\n")
		b.WriteString(e.ResultFallback)
	}
	return b.String()
}

// CompletionToolError extracts a normalized tool-level error from run outcome.
func CompletionToolError(state RunState, result RunResult, waitErr error) string {
	if waitErr != nil {
		return waitErr.Error()
	}
	if result.Error != "" {
		return result.Error
	}
	switch state {
	case RunStateFailed, RunStateTimeout, RunStateCanceled:
		return string(state)
	default:
		return ""
	}
}

func normalizeCompletionStatus(state RunState) (string, string) {
	switch state {
	case RunStateCompleted:
		return "completed", "success"
	case RunStateFailed:
		return "failed", "failed"
	case RunStateTimeout:
		return "timeout", "timeout"
	case RunStateCanceled:
		return "canceled", "canceled"
	case RunStateRunning:
		return "running", "in_progress"
	case RunStateQueued:
		return "queued", "queued"
	default:
		return "unknown", "unknown"
	}
}

func completionResultFallback(state RunState, toolError string, waitErr error) string {
	if waitErr != nil {
		return "No final text was produced because waiting for delegated completion failed."
	}
	if strings.TrimSpace(toolError) != "" {
		return fmt.Sprintf("No final text was produced. Delegated run ended with %s.", strings.TrimSpace(toolError))
	}
	switch state {
	case RunStateCompleted:
		return "Delegated run completed without final text output."
	case RunStateCanceled:
		return "Delegated run was canceled before producing final text."
	case RunStateTimeout:
		return "Delegated run timed out before producing final text."
	default:
		return "Delegated run finished without final text output."
	}
}

func formatUsageLine(usage RunUsage) string {
	parts := make([]string, 0, 5)
	if usage.InputTokens > 0 {
		parts = append(parts, fmt.Sprintf("in=%d", usage.InputTokens))
	}
	if usage.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", usage.OutputTokens))
	}
	if usage.CacheReadTokens > 0 {
		parts = append(parts, fmt.Sprintf("cacheRead=%d", usage.CacheReadTokens))
	}
	if usage.CacheWriteTokens > 0 {
		parts = append(parts, fmt.Sprintf("cacheWrite=%d", usage.CacheWriteTokens))
	}
	if usage.EstimatedCostMicroUSD > 0 {
		parts = append(parts, fmt.Sprintf("costMicroUsd=%d", usage.EstimatedCostMicroUSD))
	}
	return strings.Join(parts, " ")
}
