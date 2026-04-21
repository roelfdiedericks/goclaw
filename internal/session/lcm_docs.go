package session

// LCM documentation surface. The prose here is agent-facing: short, concrete,
// and paired one-for-one with the fields and modes exposed through the
// transcript.stats tool. Keep it colocated with LCMPresets so preset
// definitions and the text that describes them update together.

// LCMPresetCatalog returns a defensive copy of the built-in LCM preset
// definitions. Callers (e.g. the transcript tool) serialise this to agents
// so they can quote preset names, descriptions, and field values back to the
// user without duplicating the catalog.
func LCMPresetCatalog() []LCMPresetDef {
	out := make([]LCMPresetDef, len(LCMPresets))
	copy(out, LCMPresets)
	return out
}

// LCMInjectionModeDescriptions maps summary-injection mode names to
// agent-facing descriptions of what each mode does at prompt-build time.
var LCMInjectionModeDescriptions = map[string]string{
	LCMSummaryInjectionModeFrontier: "Inject the budget-fit newest summary blocks (newest-first selection), rendered oldest-first in the prompt. Respects maxInjectedSummaryTokens.",
	LCMSummaryInjectionModeAll:      "Inject every stored summary block regardless of the token budget. Higher prompt cost, maximum recall. Useful for debugging and large-context models.",
}

// LCMFieldGlossary maps LCM configuration field names (as exposed in
// LCMConfigSnapshot and LCMPresetDef) to a single-line agent-facing
// description. Every field name the agent can see in the stats payload
// MUST have an entry here so suggestions can be grounded in real semantics.
var LCMFieldGlossary = map[string]string{
	"enabled":                  "Master switch for Lossless Context Management. When false, no summaries are produced or injected and prompts fall back to the raw fresh-tail window.",
	"preset":                   "Resolved preset name (balanced, aggressive, long_term_memory, recall_heavy, or custom). Drives all other LCM field defaults.",
	"summaryInjectionMode":     "Strategy for selecting summary blocks to inject: 'frontier' uses a token budget, 'all' injects every stored summary.",
	"maxInjectedSummaryTokens": "Prompt budget the injector fills with newest-first summary blocks (frontier mode). Higher = more historical context at higher prompt cost.",
	"summaryMaxOverageFactor":  "Hard cap multiplier on per-summary token size before truncation. Protects against pathologically large summaries.",
	"freshTailCount":           "Most recent raw-message turns kept uncompacted on every prompt. Separate from the summary budget.",
	"freshTailMaxTokens":       "Optional token-budget ceiling for the fresh tail when it would otherwise exceed the count limit in tokens.",
	"leafMinFanout":            "Oldest un-parented leaf summaries required before the background loop produces one depth-1 condensed node. Lower = faster condensation, more overhead.",
	"condensedMinFanout":       "Un-parented condensed nodes at depth d required before the loop promotes them into one depth-(d+1) node. Same meaning as leafMinFanout but one level up.",
	"incrementalMaxDepth":      "Highest depth the condensation loop will build. Depth-2 is the frontier cap for most presets. Nodes above this depth are never created.",
	"leafTargetTokens":         "Target token size the LLM aims for when writing a leaf summary. Soft target; overage is capped by summaryMaxOverageFactor.",
	"condensedTargetTokens":    "Target token size for condensed summaries (groups of leaves or lower condensed nodes). Typically larger than leafTargetTokens.",
	"retryIntervalSeconds":     "Cadence of the background condensation/retry loop. Each tick consumes one fanout-sized batch of the oldest un-parented candidates.",
}

// LCMCatchUpBehaviorDescription explains, in one paragraph, how LCM drains a
// backlog of un-parented leaves over time. Surfaced verbatim in the agent
// stats payload so the agent can quote it to the user when diagnosing why
// recall is thinner than expected right after LCM was enabled.
const LCMCatchUpBehaviorDescription = "Each condensation tick (every retryIntervalSeconds) consumes exactly one fanout-sized batch of the oldest un-parented candidates and produces one new parent node. When unparentedLeaves >= leafMinFanout, the loop is actively draining: expect roughly one new depth-1 node per tick until the backlog is below fanout. When unparentedLeaves, all unparentedCondensedByDepth entries, and pending are 0, the DAG is at steady state and no further work will happen until new messages arrive."

// LCMDriftSignals are diagnostic heuristics. The agent matches these
// descriptions against live DAG stats to give confident explanations to the
// user without re-deriving the semantics each call.
var LCMDriftSignals = []string{
	"unparentedLeaves >> leafMinFanout and condensed == 0: LCM was likely disabled for a while and the background loop is now catching up; expect ~retryIntervalSeconds per fanout-sized batch until drained.",
	"unparentedCondensedByDepth[d] > 0 and maxDepth < incrementalMaxDepth: depth-(d+1) promotion is still pending; the DAG is thinner than the configured ceiling but will fill in over time.",
	"unparentedCondensedByDepth is empty and maxDepth < incrementalMaxDepth: DAG is steady-state shallower than the configured ceiling, which is normal when total compactions are below the fanout-power needed to reach the max depth.",
	"pending > 0: one or more summaries failed to generate and are scheduled to retry; recall through grep_summaries on those IDs falls back to raw messages until the retry succeeds.",
	"nextTick.batchSize == 0 and unparentedLeaves == 0 and unparentedCondensedByDepth is empty: idle; LCM has nothing to do until new messages arrive.",
}
