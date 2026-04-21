package session

import "strings"

const (
	LCMSummaryInjectionModeFrontier = "frontier"
	LCMSummaryInjectionModeAll      = "all"

	LCMPresetBalanced    = "balanced"
	LCMPresetAggressive  = "aggressive"
	LCMPresetLongTerm    = "long_term_memory"
	LCMPresetRecallHeavy = "recall_heavy"
	LCMPresetCustom      = "custom"

	defaultLCMPreset       = LCMPresetBalanced
	defaultLCMBudgetTokens = 4000
	defaultLCMOverage      = 3.0
)

// LCMPresetDef is a recipe that owns both the injection-time LCM fields and the
// outer CompactionSubConfig retention/condensation fields. A named preset fully
// describes both sides; detection compares the entire field set.
type LCMPresetDef struct {
	Name        string
	Label       string
	Description string

	// Injection-side (LCMConfig)
	SummaryInjectionMode     string
	MaxInjectedSummaryTokens int
	SummaryMaxOverageFactor  float64

	// Retention-side (CompactionSubConfig fresh-tail)
	FreshTailCount     int
	FreshTailMaxTokens int

	// Condensation-side (CompactionSubConfig DAG)
	LeafMinFanout         int
	CondensedMinFanout    int
	IncrementalMaxDepth   int
	LeafTargetTokens      int
	CondensedTargetTokens int
}

var LCMPresets = []LCMPresetDef{
	{
		Name:                     LCMPresetBalanced,
		Label:                    "Balanced",
		Description:              "Recommended default. Inject a budget-fit frontier with moderate prompt footprint.",
		SummaryInjectionMode:     LCMSummaryInjectionModeFrontier,
		MaxInjectedSummaryTokens: 4000,
		SummaryMaxOverageFactor:  3,
		FreshTailCount:           10,
		FreshTailMaxTokens:       4000,
		LeafMinFanout:            4,
		CondensedMinFanout:       4,
		IncrementalMaxDepth:      2,
		LeafTargetTokens:         800,
		CondensedTargetTokens:    1200,
	},
	{
		Name:                     LCMPresetAggressive,
		Label:                    "Aggressive",
		Description:              "Keep prompt footprint small by using a tighter frontier budget and stricter summary cap.",
		SummaryInjectionMode:     LCMSummaryInjectionModeFrontier,
		MaxInjectedSummaryTokens: 2500,
		SummaryMaxOverageFactor:  2,
		FreshTailCount:           5,
		FreshTailMaxTokens:       2000,
		LeafMinFanout:            3,
		CondensedMinFanout:       3,
		IncrementalMaxDepth:      3,
		LeafTargetTokens:         500,
		CondensedTargetTokens:    800,
	},
	{
		Name:                     LCMPresetLongTerm,
		Label:                    "Long-term Memory",
		Description:              "Favor more historical detail while still using frontier injection instead of every summary block.",
		SummaryInjectionMode:     LCMSummaryInjectionModeFrontier,
		MaxInjectedSummaryTokens: 6500,
		SummaryMaxOverageFactor:  4,
		FreshTailCount:           20,
		FreshTailMaxTokens:       8000,
		LeafMinFanout:            5,
		CondensedMinFanout:       5,
		IncrementalMaxDepth:      3,
		LeafTargetTokens:         1200,
		CondensedTargetTokens:    2000,
	},
	{
		Name:                     LCMPresetRecallHeavy,
		Label:                    "Recall-heavy",
		Description:              "Maximize recall by injecting every stored summary block. Useful for debugging and large-context models.",
		SummaryInjectionMode:     LCMSummaryInjectionModeAll,
		MaxInjectedSummaryTokens: 12000,
		SummaryMaxOverageFactor:  4,
		FreshTailCount:           30,
		FreshTailMaxTokens:       12000,
		LeafMinFanout:            6,
		CondensedMinFanout:       6,
		IncrementalMaxDepth:      2,
		LeafTargetTokens:         1500,
		CondensedTargetTokens:    2500,
	},
}

// NormalizeSessionConfig is the single normalization point for SessionConfig.
// Callers on every load/save path (applyRuntimeDefaults, web save, TUI save)
// invoke this to apply preset-driven defaults across the entire Summarization
// subtree. Downstream reads of SessionConfig may then trust the values directly.
func NormalizeSessionConfig(cfg SessionConfig) SessionConfig {
	cfg.Summarization.Compaction = NormalizeCompactionConfig(cfg.Summarization.Compaction)
	return cfg
}

// NormalizeCompactionConfig is the full-scope normalizer for the compaction
// subtree. Semantics:
//
//   - Named preset (balanced/aggressive/long_term_memory/recall_heavy): preset
//     is authoritative; both injection and outer fields are overwritten with
//     the preset's values.
//   - "custom" preset: user values are preserved; no preset application.
//   - Empty preset: detect from the full field set. If every field matches a
//     known preset, return that preset name. Otherwise mark as "custom" and
//     preserve the user's values. This keeps in-flight customization (edited
//     JSON without a "preset" key) from being silently overwritten.
//
// The fresh-install path (goclaw.json missing or empty LCM subtree) lands on
// "balanced" because struct-tag defaults fill every preset field with
// balanced's values, so detection returns "balanced".
func NormalizeCompactionConfig(cfg CompactionSubConfig) CompactionSubConfig {
	cfg.LCM = NormalizeLCMConfig(cfg.LCM)

	presetName := normalizeLCMPresetName(cfg.LCM.Preset)

	if presetName == "" {
		presetName = detectLCMPresetName(cfg)
	}

	if presetName != LCMPresetCustom {
		if preset, ok := lookupLCMPreset(presetName); ok {
			cfg.FreshTailCount = preset.FreshTailCount
			cfg.FreshTailMaxTokens = preset.FreshTailMaxTokens
			cfg.LeafMinFanout = preset.LeafMinFanout
			cfg.CondensedMinFanout = preset.CondensedMinFanout
			cfg.IncrementalMaxDepth = preset.IncrementalMaxDepth
			cfg.LeafTargetTokens = preset.LeafTargetTokens
			cfg.CondensedTargetTokens = preset.CondensedTargetTokens
			cfg.LCM.SummaryInjectionMode = preset.SummaryInjectionMode
			cfg.LCM.MaxInjectedSummaryTokens = preset.MaxInjectedSummaryTokens
			cfg.LCM.SummaryMaxOverageFactor = preset.SummaryMaxOverageFactor
		}
	}

	cfg.LCM.Preset = presetName
	return cfg
}

// NormalizeLCMConfig validates the nested LCMConfig in isolation. When a preset
// is named, its injection-side values override the struct values. Missing
// values fall back to defaults. Preset detection is NOT performed here because
// detection needs the outer CompactionSubConfig; go through
// NormalizeCompactionConfig for full-scope detection.
//
// Empty Preset is left as "" so the caller (usually NormalizeCompactionConfig)
// can decide whether to detect, default, or mark custom based on full scope.
func NormalizeLCMConfig(cfg LCMConfig) LCMConfig {
	presetName := normalizeLCMPresetName(cfg.Preset)
	if presetName != "" && presetName != LCMPresetCustom {
		if preset, ok := lookupLCMPreset(presetName); ok {
			cfg.SummaryInjectionMode = preset.SummaryInjectionMode
			cfg.MaxInjectedSummaryTokens = preset.MaxInjectedSummaryTokens
			cfg.SummaryMaxOverageFactor = preset.SummaryMaxOverageFactor
			cfg.Preset = presetName
		}
	}

	cfg.SummaryInjectionMode = normalizeLCMSummaryInjectionMode(cfg.SummaryInjectionMode)
	if cfg.MaxInjectedSummaryTokens <= 0 {
		cfg.MaxInjectedSummaryTokens = defaultLCMBudgetTokens
	}
	if cfg.SummaryMaxOverageFactor <= 0 {
		cfg.SummaryMaxOverageFactor = defaultLCMOverage
	}

	return cfg
}

func normalizeLCMSummaryInjectionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case LCMSummaryInjectionModeAll:
		return LCMSummaryInjectionModeAll
	case LCMSummaryInjectionModeFrontier:
		return LCMSummaryInjectionModeFrontier
	default:
		return LCMSummaryInjectionModeFrontier
	}
}

func normalizeLCMPresetName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return ""
	case LCMPresetBalanced:
		return LCMPresetBalanced
	case LCMPresetAggressive:
		return LCMPresetAggressive
	case LCMPresetLongTerm:
		return LCMPresetLongTerm
	case LCMPresetRecallHeavy:
		return LCMPresetRecallHeavy
	case LCMPresetCustom:
		return LCMPresetCustom
	default:
		return LCMPresetCustom
	}
}

// detectLCMPresetName returns a known preset name only when every field on the
// CompactionSubConfig (injection + retention + condensation) matches the preset
// verbatim. Any mismatch yields "custom". This keeps the UI honest: a user who
// tweaked even one knob sees "custom", not a misleading preset badge.
func detectLCMPresetName(cfg CompactionSubConfig) string {
	for _, preset := range LCMPresets {
		if cfg.LCM.SummaryInjectionMode != preset.SummaryInjectionMode {
			continue
		}
		if cfg.LCM.MaxInjectedSummaryTokens != preset.MaxInjectedSummaryTokens {
			continue
		}
		if cfg.LCM.SummaryMaxOverageFactor != preset.SummaryMaxOverageFactor {
			continue
		}
		if cfg.FreshTailCount != preset.FreshTailCount {
			continue
		}
		if cfg.FreshTailMaxTokens != preset.FreshTailMaxTokens {
			continue
		}
		if cfg.LeafMinFanout != preset.LeafMinFanout {
			continue
		}
		if cfg.CondensedMinFanout != preset.CondensedMinFanout {
			continue
		}
		if cfg.IncrementalMaxDepth != preset.IncrementalMaxDepth {
			continue
		}
		if cfg.LeafTargetTokens != preset.LeafTargetTokens {
			continue
		}
		if cfg.CondensedTargetTokens != preset.CondensedTargetTokens {
			continue
		}
		return preset.Name
	}
	return LCMPresetCustom
}

func lookupLCMPreset(name string) (LCMPresetDef, bool) {
	for _, preset := range LCMPresets {
		if preset.Name == name {
			return preset, true
		}
	}
	return LCMPresetDef{}, false
}
