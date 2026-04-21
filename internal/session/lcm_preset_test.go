package session

import (
	"testing"

	"github.com/creasty/defaults"
)

// normalizeFreshCompactionConfig mimics the production flow: struct-tag
// defaults first, then NormalizeCompactionConfig. Without defaults.Set the
// outer fields would be zero and detection would mark the config "custom".
func normalizeFreshCompactionConfig(t *testing.T, lcm LCMConfig) CompactionSubConfig {
	t.Helper()
	cfg := CompactionSubConfig{LCM: lcm}
	if err := defaults.Set(&cfg); err != nil {
		t.Fatalf("defaults.Set: %v", err)
	}
	return NormalizeCompactionConfig(cfg)
}

func TestNormalizeLCMConfigAppliesPresetValues(t *testing.T) {
	t.Parallel()

	cfg := NormalizeLCMConfig(LCMConfig{
		Enabled: true,
		Preset:  LCMPresetAggressive,
	})

	if cfg.Preset != LCMPresetAggressive {
		t.Fatalf("expected aggressive preset, got %q", cfg.Preset)
	}
	if cfg.SummaryInjectionMode != LCMSummaryInjectionModeFrontier {
		t.Fatalf("expected frontier mode, got %q", cfg.SummaryInjectionMode)
	}
	if cfg.MaxInjectedSummaryTokens != 2500 {
		t.Fatalf("expected aggressive max injected summary tokens 2500, got %d", cfg.MaxInjectedSummaryTokens)
	}
	if cfg.SummaryMaxOverageFactor != 2 {
		t.Fatalf("expected aggressive overage factor 2, got %v", cfg.SummaryMaxOverageFactor)
	}
}

func TestNormalizeLCMConfigDefaultsInjectionFieldsWhenEmpty(t *testing.T) {
	t.Parallel()

	cfg := NormalizeLCMConfig(LCMConfig{})

	if cfg.Preset != "" {
		t.Fatalf("expected empty preset (detection happens at full scope), got %q", cfg.Preset)
	}
	if cfg.SummaryInjectionMode != LCMSummaryInjectionModeFrontier {
		t.Fatalf("expected frontier injection mode fallback, got %q", cfg.SummaryInjectionMode)
	}
	if cfg.MaxInjectedSummaryTokens != defaultLCMBudgetTokens {
		t.Fatalf("expected default injected summary tokens %d, got %d", defaultLCMBudgetTokens, cfg.MaxInjectedSummaryTokens)
	}
	if cfg.SummaryMaxOverageFactor != defaultLCMOverage {
		t.Fatalf("expected default overage factor %v, got %v", defaultLCMOverage, cfg.SummaryMaxOverageFactor)
	}
}

func TestNormalizeSessionConfigFreshDefaultsToBalanced(t *testing.T) {
	t.Parallel()

	cfg := &SessionConfig{}
	if err := defaults.Set(cfg); err != nil {
		t.Fatalf("defaults.Set: %v", err)
	}
	normalized := NormalizeSessionConfig(*cfg)
	c := normalized.Summarization.Compaction

	if c.LCM.Preset != LCMPresetBalanced {
		t.Fatalf("expected balanced preset on fresh config, got %q", c.LCM.Preset)
	}
	if c.FreshTailCount != 10 {
		t.Fatalf("expected balanced FreshTailCount=10, got %d", c.FreshTailCount)
	}
	if c.FreshTailMaxTokens != 4000 {
		t.Fatalf("expected balanced FreshTailMaxTokens=4000, got %d", c.FreshTailMaxTokens)
	}
	if c.LeafMinFanout != 4 {
		t.Fatalf("expected balanced LeafMinFanout=4, got %d", c.LeafMinFanout)
	}
	if c.CondensedMinFanout != 4 {
		t.Fatalf("expected balanced CondensedMinFanout=4, got %d", c.CondensedMinFanout)
	}
	if c.IncrementalMaxDepth != 2 {
		t.Fatalf("expected balanced IncrementalMaxDepth=2, got %d", c.IncrementalMaxDepth)
	}
	if c.LeafTargetTokens != 800 {
		t.Fatalf("expected balanced LeafTargetTokens=800, got %d", c.LeafTargetTokens)
	}
	if c.CondensedTargetTokens != 1200 {
		t.Fatalf("expected balanced CondensedTargetTokens=1200, got %d", c.CondensedTargetTokens)
	}
}

func TestNormalizeCompactionConfigAppliesPresetOuterFields(t *testing.T) {
	t.Parallel()

	presets := map[string]struct {
		preset                string
		freshTail             int
		freshTailMax          int
		leafFanout            int
		condensedFanout       int
		maxDepth              int
		leafTarget            int
		condensedTarget       int
		summaryInjection      string
		injectedSummaryTokens int
		overage               float64
	}{
		"balanced":         {LCMPresetBalanced, 10, 4000, 4, 4, 2, 800, 1200, LCMSummaryInjectionModeFrontier, 4000, 3},
		"aggressive":       {LCMPresetAggressive, 5, 2000, 3, 3, 3, 500, 800, LCMSummaryInjectionModeFrontier, 2500, 2},
		"long_term_memory": {LCMPresetLongTerm, 20, 8000, 5, 5, 3, 1200, 2000, LCMSummaryInjectionModeFrontier, 6500, 4},
		"recall_heavy":     {LCMPresetRecallHeavy, 30, 12000, 6, 6, 2, 1500, 2500, LCMSummaryInjectionModeAll, 12000, 4},
	}

	for name, want := range presets {
		name := name
		want := want
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := NormalizeCompactionConfig(CompactionSubConfig{
				LCM: LCMConfig{Preset: want.preset},
			})
			if cfg.LCM.Preset != want.preset {
				t.Fatalf("preset: want %q got %q", want.preset, cfg.LCM.Preset)
			}
			if cfg.FreshTailCount != want.freshTail {
				t.Fatalf("FreshTailCount: want %d got %d", want.freshTail, cfg.FreshTailCount)
			}
			if cfg.FreshTailMaxTokens != want.freshTailMax {
				t.Fatalf("FreshTailMaxTokens: want %d got %d", want.freshTailMax, cfg.FreshTailMaxTokens)
			}
			if cfg.LeafMinFanout != want.leafFanout {
				t.Fatalf("LeafMinFanout: want %d got %d", want.leafFanout, cfg.LeafMinFanout)
			}
			if cfg.CondensedMinFanout != want.condensedFanout {
				t.Fatalf("CondensedMinFanout: want %d got %d", want.condensedFanout, cfg.CondensedMinFanout)
			}
			if cfg.IncrementalMaxDepth != want.maxDepth {
				t.Fatalf("IncrementalMaxDepth: want %d got %d", want.maxDepth, cfg.IncrementalMaxDepth)
			}
			if cfg.LeafTargetTokens != want.leafTarget {
				t.Fatalf("LeafTargetTokens: want %d got %d", want.leafTarget, cfg.LeafTargetTokens)
			}
			if cfg.CondensedTargetTokens != want.condensedTarget {
				t.Fatalf("CondensedTargetTokens: want %d got %d", want.condensedTarget, cfg.CondensedTargetTokens)
			}
			if cfg.LCM.SummaryInjectionMode != want.summaryInjection {
				t.Fatalf("SummaryInjectionMode: want %q got %q", want.summaryInjection, cfg.LCM.SummaryInjectionMode)
			}
			if cfg.LCM.MaxInjectedSummaryTokens != want.injectedSummaryTokens {
				t.Fatalf("MaxInjectedSummaryTokens: want %d got %d", want.injectedSummaryTokens, cfg.LCM.MaxInjectedSummaryTokens)
			}
			if cfg.LCM.SummaryMaxOverageFactor != want.overage {
				t.Fatalf("SummaryMaxOverageFactor: want %v got %v", want.overage, cfg.LCM.SummaryMaxOverageFactor)
			}
		})
	}
}

func TestDetectLCMPresetNameReturnsCustomWhenOuterFieldsDiverge(t *testing.T) {
	t.Parallel()

	// Injection-side matches balanced but FreshTailCount differs.
	cfg := CompactionSubConfig{
		FreshTailCount:        1,
		FreshTailMaxTokens:    4000,
		LeafMinFanout:         4,
		CondensedMinFanout:    4,
		IncrementalMaxDepth:   2,
		LeafTargetTokens:      800,
		CondensedTargetTokens: 1200,
		LCM: LCMConfig{
			SummaryInjectionMode:     LCMSummaryInjectionModeFrontier,
			MaxInjectedSummaryTokens: 4000,
			SummaryMaxOverageFactor:  3,
		},
	}

	if got := detectLCMPresetName(cfg); got != LCMPresetCustom {
		t.Fatalf("expected custom preset when outer fields diverge, got %q", got)
	}
}

func TestNormalizeCompactionConfigCustomPreservesValues(t *testing.T) {
	t.Parallel()

	cfg := NormalizeCompactionConfig(CompactionSubConfig{
		FreshTailCount:        7,
		FreshTailMaxTokens:    3333,
		LeafMinFanout:         9,
		CondensedMinFanout:    11,
		IncrementalMaxDepth:   5,
		LeafTargetTokens:      600,
		CondensedTargetTokens: 900,
		LCM: LCMConfig{
			Preset:                   LCMPresetCustom,
			SummaryInjectionMode:     LCMSummaryInjectionModeAll,
			MaxInjectedSummaryTokens: 9999,
			SummaryMaxOverageFactor:  5,
		},
	})

	if cfg.LCM.Preset != LCMPresetCustom {
		t.Fatalf("expected custom preset, got %q", cfg.LCM.Preset)
	}
	if cfg.FreshTailCount != 7 {
		t.Fatalf("expected custom FreshTailCount preserved, got %d", cfg.FreshTailCount)
	}
	if cfg.LeafMinFanout != 9 {
		t.Fatalf("expected custom LeafMinFanout preserved, got %d", cfg.LeafMinFanout)
	}
	if cfg.LCM.MaxInjectedSummaryTokens != 9999 {
		t.Fatalf("expected custom MaxInjectedSummaryTokens preserved, got %d", cfg.LCM.MaxInjectedSummaryTokens)
	}
}

func TestNormalizeCompactionConfigEmptyPresetDetectsBalancedAfterDefaults(t *testing.T) {
	t.Parallel()

	cfg := normalizeFreshCompactionConfig(t, LCMConfig{})

	if cfg.LCM.Preset != LCMPresetBalanced {
		t.Fatalf("expected detection to resolve empty preset to balanced after struct-tag defaults, got %q", cfg.LCM.Preset)
	}
}

func TestNormalizeSessionConfigAppliesPresetToNestedLCM(t *testing.T) {
	t.Parallel()

	cfg := NormalizeSessionConfig(SessionConfig{
		Summarization: SummarizationConfig{
			Compaction: CompactionSubConfig{
				LCM: LCMConfig{
					Enabled:                  true,
					Preset:                   LCMPresetBalanced,
					SummaryInjectionMode:     LCMSummaryInjectionModeFrontier,
					MaxInjectedSummaryTokens: 2500,
					SummaryMaxOverageFactor:  2,
				},
			},
		},
	})

	if cfg.Summarization.Compaction.LCM.Preset != LCMPresetBalanced {
		t.Fatalf("expected balanced preset, got %q", cfg.Summarization.Compaction.LCM.Preset)
	}
	if cfg.Summarization.Compaction.LCM.MaxInjectedSummaryTokens != 4000 {
		t.Fatalf("expected balanced budget 4000, got %d", cfg.Summarization.Compaction.LCM.MaxInjectedSummaryTokens)
	}
	if cfg.Summarization.Compaction.LCM.SummaryMaxOverageFactor != 3 {
		t.Fatalf("expected balanced overage factor 3, got %v", cfg.Summarization.Compaction.LCM.SummaryMaxOverageFactor)
	}
}
