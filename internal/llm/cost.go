package llm

import (
	"context"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	. "github.com/roelfdiedericks/goclaw/internal/metrics"
)

type purposeContextKey struct{}

// ContextWithPurpose returns a context with the LLM purpose (e.g. "agent", "summarization") attached.
func ContextWithPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, purposeContextKey{}, purpose)
}

// PurposeFromContext extracts the LLM purpose from the context, or "" if not set.
func PurposeFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(purposeContextKey{}).(string); ok {
		return v
	}
	return ""
}

// RequestCost holds the calculated cost breakdown for a single LLM request in USD.
type RequestCost struct {
	InputCost      float64
	OutputCost     float64
	CacheReadCost  float64
	CacheWriteCost float64
	TotalCost      float64
}

// resolvePricing merges config cost overrides with models.json pricing.
// Per-field priority: config override > models.json > zero.
func resolvePricing(cfg LLMProviderConfig, metadataProvider, model string) metadata.ModelCost {
	var base metadata.ModelCost
	if m, ok := metadata.Get().GetModel(metadataProvider, model); ok && m != nil {
		base = m.Cost
	}

	if cfg.CostInput > 0 {
		base.Input = cfg.CostInput
	}
	if cfg.CostOutput > 0 {
		base.Output = cfg.CostOutput
	}
	if cfg.CostCacheRead > 0 {
		base.CacheRead = cfg.CostCacheRead
	}
	if cfg.CostCacheWrite > 0 {
		base.CacheWrite = cfg.CostCacheWrite
	}

	return base
}

// CalculateRequestCost computes the cost of a request based on pricing and token counts.
// Pricing is in USD per 1M tokens. Returns cost in USD.
func CalculateRequestCost(pricing metadata.ModelCost, resp *Response) RequestCost {
	rc := RequestCost{
		InputCost:      float64(resp.InputTokens) * pricing.Input / 1_000_000,
		OutputCost:     float64(resp.OutputTokens) * pricing.Output / 1_000_000,
		CacheReadCost:  float64(resp.CacheReadTokens) * pricing.CacheRead / 1_000_000,
		CacheWriteCost: float64(resp.CacheCreationTokens) * pricing.CacheWrite / 1_000_000,
	}
	rc.TotalCost = rc.InputCost + rc.OutputCost + rc.CacheReadCost + rc.CacheWriteCost
	return rc
}

// emitCostMetrics resolves pricing and emits request_cost (gauge) and total_cost (counter)
// metrics in microdollars for a completed LLM request.
// If purpose is non-empty, also emits aggregated metrics under "purpose/<name>".
func emitCostMetrics(metricPrefix, purpose string, cfg LLMProviderConfig, metadataProvider, model string, resp *Response) {
	pricing := resolvePricing(cfg, metadataProvider, model)
	cost := CalculateRequestCost(pricing, resp)

	microCost := int64(cost.TotalCost * 1_000_000)
	MetricCost(metricPrefix, "cost", microCost)

	if purpose != "" {
		pp := "purpose/" + purpose
		MetricCost(pp, "cost", microCost)
		MetricAdd(pp, "input_tokens", int64(resp.InputTokens))
		MetricAdd(pp, "output_tokens", int64(resp.OutputTokens))
		MetricAdd(pp, "requests", 1)
	}

	L_debug("llm: request cost",
		"provider", metadataProvider,
		"model", model,
		"purpose", purpose,
		"inputCost", cost.InputCost,
		"outputCost", cost.OutputCost,
		"cacheReadCost", cost.CacheReadCost,
		"cacheWriteCost", cost.CacheWriteCost,
		"totalCost", cost.TotalCost,
		"microUSD", microCost,
	)
}

// EstimateInputCost returns the estimated input cost in USD for a given token count.
// Uses metadata pricing only (no config overrides — this is a rough pre-call estimate).
func EstimateInputCost(metadataProvider, model string, estimatedTokens int) float64 {
	m, ok := metadata.Get().GetModel(metadataProvider, model)
	if !ok || m == nil {
		return 0
	}
	return float64(estimatedTokens) * m.Cost.Input / 1_000_000
}
