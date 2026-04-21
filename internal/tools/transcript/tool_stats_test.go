package transcript

import (
	"strings"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/session"
	transcriptpkg "github.com/roelfdiedericks/goclaw/internal/transcript"
)

func TestAssembleLCMStatsPayloadPreservesTranscriptFields(t *testing.T) {
	ts := transcriptpkg.TranscriptStats{
		TotalChunks:             1842,
		ChunksWithEmbeddings:    1800,
		ChunksNeedingEmbeddings: 42,
		PendingMessages:         3,
		ChunksIndexedSession:    500,
		LastSync:                time.Date(2026, 4, 21, 9, 15, 2, 0, time.UTC),
		Provider:                "ollama:nomic-embed-text",
	}

	payload := assembleLCMStatsPayload(ts, nil, nil)

	if got := payload["totalChunks"]; got != 1842 {
		t.Errorf("totalChunks = %v, want 1842", got)
	}
	if got := payload["chunksWithEmbeddings"]; got != 1800 {
		t.Errorf("chunksWithEmbeddings = %v, want 1800", got)
	}
	if got := payload["chunksNeedingEmbeddings"]; got != 42 {
		t.Errorf("chunksNeedingEmbeddings = %v, want 42", got)
	}
	if got := payload["pendingMessages"]; got != 3 {
		t.Errorf("pendingMessages = %v, want 3", got)
	}
	if got := payload["provider"]; got != "ollama:nomic-embed-text" {
		t.Errorf("provider = %v, want ollama:nomic-embed-text", got)
	}
	// When compactor is nil the lcm block still exists with enabled=false.
	lcm, ok := payload["lcm"].(map[string]any)
	if !ok {
		t.Fatalf("lcm block missing or wrong type: %T", payload["lcm"])
	}
	if lcm["enabled"] != false {
		t.Errorf("lcm.enabled = %v, want false for nil compactor", lcm["enabled"])
	}
}

func TestAssembleLCMStatsPayloadShape(t *testing.T) {
	compactor := session.NewCompactionManager(&session.CompactionManagerConfig{
		LCMEnabled:               true,
		Preset:                   session.LCMPresetBalanced,
		SummaryInjectionMode:     session.LCMSummaryInjectionModeFrontier,
		MaxInjectedSummaryTokens: 4000,
		SummaryMaxOverageFactor:  3.0,
		FreshTailCount:           10,
		FreshTailMaxTokens:       4000,
		LeafMinFanout:            4,
		CondensedMinFanout:       4,
		IncrementalMaxDepth:      2,
		LeafTargetTokens:         800,
		CondensedTargetTokens:    1200,
		RetryIntervalSeconds:     60,
	})

	dag := session.CompactionDAGStats{
		Leaves:                     237,
		Condensed:                  12,
		CondensedByDepth:           map[int]int{1: 12},
		UnparentedLeaves:           189,
		UnparentedCondensedByDepth: map[int]int{1: 12},
		MaxDepth:                   1,
		Pending:                    0,
		FTSRows:                    249,
		NextBatchSize:              4,
		NextBatchNewDepth:          1,
	}

	payload := assembleLCMStatsPayload(transcriptpkg.TranscriptStats{}, compactor, &dag)

	lcm, ok := payload["lcm"].(map[string]any)
	if !ok {
		t.Fatalf("lcm block missing")
	}
	if lcm["enabled"] != true {
		t.Errorf("lcm.enabled = %v, want true", lcm["enabled"])
	}
	if lcm["activePreset"] != session.LCMPresetBalanced {
		t.Errorf("lcm.activePreset = %v, want balanced", lcm["activePreset"])
	}
	cfg, ok := lcm["config"].(session.LCMConfigSnapshot)
	if !ok {
		t.Fatalf("lcm.config missing or wrong type: %T", lcm["config"])
	}
	if cfg.MaxInjectedSummaryTokens != 4000 {
		t.Errorf("config.MaxInjectedSummaryTokens = %d, want 4000", cfg.MaxInjectedSummaryTokens)
	}
	if cfg.RetryIntervalSeconds != 60 {
		t.Errorf("config.RetryIntervalSeconds = %d, want 60", cfg.RetryIntervalSeconds)
	}

	dagOut, ok := lcm["dag"].(map[string]any)
	if !ok {
		t.Fatalf("lcm.dag missing")
	}
	if dagOut["unparentedLeaves"] != 189 {
		t.Errorf("dag.unparentedLeaves = %v, want 189", dagOut["unparentedLeaves"])
	}
	nextTick, ok := dagOut["nextTick"].(map[string]any)
	if !ok {
		t.Fatalf("dag.nextTick missing")
	}
	if nextTick["batchSize"] != 4 {
		t.Errorf("nextTick.batchSize = %v, want 4", nextTick["batchSize"])
	}
	desc, _ := nextTick["description"].(string)
	if desc == "" {
		t.Errorf("nextTick.description is empty")
	}
	if !strings.Contains(desc, "depth-1") {
		t.Errorf("nextTick.description = %q, want it to mention depth-1", desc)
	}

	presets, ok := payload["presets"].([]map[string]any)
	if !ok {
		t.Fatalf("presets missing or wrong type: %T", payload["presets"])
	}
	if len(presets) != 4 {
		t.Errorf("len(presets) = %d, want 4", len(presets))
	}
	expectedNames := map[string]bool{
		session.LCMPresetBalanced:    false,
		session.LCMPresetAggressive:  false,
		session.LCMPresetLongTerm:    false,
		session.LCMPresetRecallHeavy: false,
	}
	for _, p := range presets {
		name, _ := p["name"].(string)
		if _, ok := expectedNames[name]; ok {
			expectedNames[name] = true
		}
		if desc, _ := p["description"].(string); desc == "" {
			t.Errorf("preset %s has empty description", name)
		}
	}
	for name, seen := range expectedNames {
		if !seen {
			t.Errorf("preset %q missing from catalog", name)
		}
	}

	semantics, ok := payload["semantics"].(map[string]any)
	if !ok {
		t.Fatalf("semantics missing")
	}
	glossary, ok := semantics["fieldGlossary"].(map[string]string)
	if !ok {
		t.Fatalf("semantics.fieldGlossary missing or wrong type: %T", semantics["fieldGlossary"])
	}
	// Every field exposed in LCMConfigSnapshot JSON must have a glossary
	// entry so agent suggestions can be grounded.
	for _, field := range []string{
		"enabled", "preset", "summaryInjectionMode", "maxInjectedSummaryTokens",
		"summaryMaxOverageFactor", "freshTailCount", "freshTailMaxTokens",
		"leafMinFanout", "condensedMinFanout", "incrementalMaxDepth",
		"leafTargetTokens", "condensedTargetTokens", "retryIntervalSeconds",
	} {
		if _, ok := glossary[field]; !ok {
			t.Errorf("fieldGlossary missing entry for %q", field)
		}
	}

	modes, ok := semantics["injectionModes"].(map[string]string)
	if !ok {
		t.Fatalf("semantics.injectionModes missing or wrong type: %T", semantics["injectionModes"])
	}
	if modes[session.LCMSummaryInjectionModeFrontier] == "" {
		t.Errorf("injectionModes.frontier description is empty")
	}
	if modes[session.LCMSummaryInjectionModeAll] == "" {
		t.Errorf("injectionModes.all description is empty")
	}

	if _, ok := semantics["catchUpBehavior"].(string); !ok {
		t.Errorf("semantics.catchUpBehavior missing or not string")
	}
	if drift, ok := semantics["driftSignals"].([]string); !ok || len(drift) == 0 {
		t.Errorf("semantics.driftSignals missing or empty: %v", semantics["driftSignals"])
	}
}

func TestAssembleLCMStatsPayloadIdleNextTick(t *testing.T) {
	compactor := session.NewCompactionManager(&session.CompactionManagerConfig{
		LCMEnabled: true,
		Preset:     session.LCMPresetBalanced,
	})
	dag := session.CompactionDAGStats{
		CondensedByDepth:           map[int]int{},
		UnparentedCondensedByDepth: map[int]int{},
		// NextBatchSize/NewDepth left 0 = idle
	}

	payload := assembleLCMStatsPayload(transcriptpkg.TranscriptStats{}, compactor, &dag)
	lcm := payload["lcm"].(map[string]any)
	dagOut := lcm["dag"].(map[string]any)
	nextTick := dagOut["nextTick"].(map[string]any)
	desc, _ := nextTick["description"].(string)
	if !strings.Contains(strings.ToLower(desc), "idle") {
		t.Errorf("idle nextTick description = %q, want mention of 'idle'", desc)
	}
}

func TestDescriptionMentionsTuningAndPresetCatalog(t *testing.T) {
	tool := NewTool(nil, nil, nil, false)
	desc := tool.Description()
	if !strings.Contains(desc, "preset catalog") {
		t.Errorf("Description missing 'preset catalog' hint: %s", desc)
	}
	if !strings.Contains(desc, "tuning") {
		t.Errorf("Description missing 'tuning' hint: %s", desc)
	}
}
