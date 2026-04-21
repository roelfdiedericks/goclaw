package session

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const configPath = "session"

// ConfigFormDef returns the form definition for SessionConfig
func ConfigFormDef() forms.FormDef {
	presetOptions := make([]forms.Option, 0, len(LCMPresets)+1)
	for _, preset := range LCMPresets {
		presetOptions = append(presetOptions, forms.Option{
			Label: preset.Label,
			Value: preset.Name,
		})
	}
	presetOptions = append(presetOptions, forms.Option{
		Label: "Custom",
		Value: LCMPresetCustom,
	})

	injectionModeOptions := []forms.Option{
		{Label: "Budget-fit frontier (recommended)", Value: LCMSummaryInjectionModeFrontier},
		{Label: "Inject every stored summary (debug)", Value: LCMSummaryInjectionModeAll},
	}

	return forms.FormDef{
		Title:       "Session Management",
		Description: "Configure session persistence and context management",
		Sections: []forms.Section{
			{
				Title: "Storage",
				Fields: []forms.Field{
					{Name: "store", Title: "Storage Backend", Type: forms.Select, Default: "sqlite",
						Options: []forms.Option{{Label: "SQLite", Value: "sqlite"}},
						Desc:    "Storage backend for sessions"},
					{Name: "storePath", Title: "Store Path", Type: forms.Text, Desc: "Path to storage (DB file or sessions directory)"},
				},
			},
			{
				Title:     "OpenClaw Inheritance",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "inherit", Title: "Enable Inheritance", Type: forms.Toggle, Desc: "Inherit context from OpenClaw session"},
					{Name: "inheritPath", Title: "Sessions Directory", Type: forms.Text, Desc: "Path to OpenClaw sessions directory"},
					{Name: "inheritFrom", Title: "Inherit From", Type: forms.Text, Desc: "Session key to inherit from (e.g., agent:main:main)"},
				},
			},
			{
				Title:     "Memory Flush",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "memoryFlush.enabled", Title: "Enable Memory Flush", Type: forms.Toggle, Desc: "Prompt for memory writes at context thresholds"},
				},
			},
			{
				Title: "Summarization",
				Fields: []forms.Field{
					{Name: "summarization.checkpoint.enabled", Title: "Enable Checkpoints", Type: forms.Toggle, Desc: "Generate rolling checkpoints"},
					{Name: "summarization.compaction.lcm.enabled", Title: "Enable Lossless Context Management", Type: forms.Toggle, Desc: "Enable DAG-backed compaction recall, condensation, and XML summary context"},
					{Name: "summarization.compaction.lcm.preset", Title: "Summarization Style", Type: forms.Select, Default: defaultLCMPreset, Options: presetOptions, Desc: "Pick a ready-made LCM footprint preset. Switch to Custom to edit frontier budget and summary cap directly."},
				},
			},
			{
				Title:     "Advanced Injection Controls",
				Collapsed: true,
				ShowWhen:  "summarization.compaction.lcm.enabled=true,summarization.compaction.lcm.preset=custom",
				Fields: []forms.Field{
					{Name: "summarization.compaction.lcm.summaryInjectionMode", Title: "Summary Injection Mode", Type: forms.Select, Default: LCMSummaryInjectionModeFrontier, Options: injectionModeOptions, Desc: "Choose whether to inject every summary or only a non-overlapping frontier."},
					{Name: "summarization.compaction.lcm.maxInjectedSummaryTokens", Title: "Max Injected Summary Tokens", Type: forms.Number, Default: defaultLCMBudgetTokens, Min: 500, Max: 32000, Step: 250, Desc: "Approximate prompt budget reserved for injected LCM summary XML."},
					{Name: "summarization.compaction.lcm.summaryMaxOverageFactor", Title: "Summary Max Overage Factor", Type: forms.Number, Default: defaultLCMOverage, Min: 1, Max: 8, Step: 0.5, Desc: "Hard cap generated summaries when they grow too far past their target token budget."},
				},
			},
			{
				Title:     "Advanced Summary Generation",
				Collapsed: true,
				ShowWhen:  "summarization.compaction.lcm.enabled=true",
				Fields: []forms.Field{
					{Name: "summarization.retryIntervalSeconds", Title: "Retry Interval (seconds)", Type: forms.Number, Default: 60, Min: 5, Max: 3600, Step: 5, Desc: "How often background retry and condensation work should run."},
					{Name: "summarization.compaction.leafTargetTokens", Title: "Leaf Summary Target Tokens", Type: forms.Number, Default: 800, Min: 100, Max: 8000, Step: 50, Desc: "Target length for new leaf summaries generated from compacted raw history."},
					{Name: "summarization.compaction.condensedTargetTokens", Title: "Condensed Summary Target Tokens", Type: forms.Number, Default: 1200, Min: 100, Max: 12000, Step: 50, Desc: "Target length for recursive condensed summaries built from older summary blocks."},
				},
			},
			{
				Title:     "Advanced Condensation / Retention",
				Collapsed: true,
				ShowWhen:  "summarization.compaction.lcm.enabled=true",
				Fields: []forms.Field{
					{Name: "summarization.compaction.reserveTokens", Title: "Reserve Tokens", Type: forms.Number, Default: 4000, Min: 500, Max: 32000, Step: 250, Desc: "Reserved token headroom before compaction should trigger."},
					{Name: "summarization.compaction.maxMessages", Title: "Max Messages Before Compaction", Type: forms.Number, Default: 500, Min: 0, Max: 10000, Step: 10, Desc: "Trigger compaction when the session grows past this many messages. Set 0 to disable the message-count trigger."},
					{Name: "summarization.compaction.preferCheckpoint", Title: "Prefer Checkpoint Summaries", Type: forms.Toggle, Desc: "Reuse a recent checkpoint summary for compaction when it already covers enough context."},
					{Name: "summarization.compaction.keepPercent", Title: "Keep Percent", Type: forms.Number, Default: 50, Min: 1, Max: 100, Step: 1, Desc: "Percent of newest messages to keep after compaction when no explicit fresh-tail count is set."},
					{Name: "summarization.compaction.minMessages", Title: "Minimum Messages To Keep", Type: forms.Number, Default: 20, Min: 1, Max: 1000, Step: 1, Desc: "Never compact below this many recent messages."},
					{Name: "summarization.compaction.freshTailCount", Title: "Fresh Tail Count", Type: forms.Number, Default: 0, Min: 0, Max: 2000, Step: 1, Desc: "When set above zero, keep exactly this many newest messages instead of using Keep Percent."},
					{Name: "summarization.compaction.freshTailMaxTokens", Title: "Fresh Tail Max Tokens", Type: forms.Number, Default: 0, Min: 0, Max: 32000, Step: 250, Desc: "Optional extra token cap for the raw fresh tail that remains after compaction."},
					{Name: "summarization.compaction.leafMinFanout", Title: "Leaf Min Fanout", Type: forms.Number, Default: 4, Min: 2, Max: 64, Step: 1, Desc: "Minimum uncovered leaf summaries required before creating a depth-1 condensed summary."},
					{Name: "summarization.compaction.condensedMinFanout", Title: "Condensed Min Fanout", Type: forms.Number, Default: 4, Min: 2, Max: 64, Step: 1, Desc: "Minimum condensed children required before creating the next higher summary depth."},
					{Name: "summarization.compaction.incrementalMaxDepth", Title: "Incremental Max Depth", Type: forms.Number, Default: 2, Min: 1, Max: 16, Step: 1, Desc: "Highest summary depth GoClaw should build incrementally in the background."},
				},
			},
		},
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers config commands for session.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters config commands.
func UnregisterCommands() {
	bus.UnregisterCommand(configPath, "apply")
}

// handleApply publishes the config.applied event for listeners to react
func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*SessionConfig)
	if !ok {
		return bus.CommandResult{
			Error:   fmt.Errorf("expected *SessionConfig, got %T", cmd.Payload),
			Message: "invalid payload type",
		}
	}

	L_info("session: config applied", "store", cfg.Store, "storePath", cfg.StorePath)
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{
		Success: true,
		Message: "Config applied",
	}
}
