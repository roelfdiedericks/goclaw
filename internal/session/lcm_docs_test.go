package session

import "testing"

// TestLCMFieldGlossaryCoversAllPresetFields asserts that every configurable
// field of LCMPresetDef has a corresponding entry in LCMFieldGlossary. This
// guards against drift between the preset definitions and the agent-facing
// prose that describes them.
func TestLCMFieldGlossaryCoversAllPresetFields(t *testing.T) {
	required := []string{
		"summaryInjectionMode",
		"maxInjectedSummaryTokens",
		"summaryMaxOverageFactor",
		"freshTailCount",
		"freshTailMaxTokens",
		"leafMinFanout",
		"condensedMinFanout",
		"incrementalMaxDepth",
		"leafTargetTokens",
		"condensedTargetTokens",
	}
	for _, field := range required {
		entry, ok := LCMFieldGlossary[field]
		if !ok {
			t.Errorf("LCMFieldGlossary missing entry for preset field %q", field)
			continue
		}
		if entry == "" {
			t.Errorf("LCMFieldGlossary entry for %q is empty", field)
		}
	}
}

// TestLCMFieldGlossaryCoversConfigSnapshot asserts that every field the
// transcript tool exposes via LCMConfigSnapshot has a glossary entry. This
// is the contract the agent reads against.
func TestLCMFieldGlossaryCoversConfigSnapshot(t *testing.T) {
	required := []string{
		"enabled",
		"preset",
		"summaryInjectionMode",
		"maxInjectedSummaryTokens",
		"summaryMaxOverageFactor",
		"freshTailCount",
		"freshTailMaxTokens",
		"leafMinFanout",
		"condensedMinFanout",
		"incrementalMaxDepth",
		"leafTargetTokens",
		"condensedTargetTokens",
		"retryIntervalSeconds",
	}
	for _, field := range required {
		if entry, ok := LCMFieldGlossary[field]; !ok || entry == "" {
			t.Errorf("LCMFieldGlossary missing/empty entry for %q (exposed in LCMConfigSnapshot)", field)
		}
	}
}

func TestLCMInjectionModeDescriptionsCoverAllModes(t *testing.T) {
	for _, mode := range []string{LCMSummaryInjectionModeFrontier, LCMSummaryInjectionModeAll} {
		desc, ok := LCMInjectionModeDescriptions[mode]
		if !ok {
			t.Errorf("LCMInjectionModeDescriptions missing entry for %q", mode)
			continue
		}
		if desc == "" {
			t.Errorf("LCMInjectionModeDescriptions entry for %q is empty", mode)
		}
	}
}

func TestLCMPresetCatalogReturnsCopy(t *testing.T) {
	catalog := LCMPresetCatalog()
	if len(catalog) != len(LCMPresets) {
		t.Fatalf("LCMPresetCatalog returned %d entries, want %d", len(catalog), len(LCMPresets))
	}
	// Mutating the returned slice must not affect the internal catalog.
	catalog[0].Description = "MUTATED"
	if LCMPresets[0].Description == "MUTATED" {
		t.Errorf("LCMPresetCatalog did not return a defensive copy: internal catalog was mutated")
	}
}

func TestLCMDriftSignalsNonEmpty(t *testing.T) {
	if len(LCMDriftSignals) == 0 {
		t.Fatal("LCMDriftSignals must have at least one entry")
	}
	for i, s := range LCMDriftSignals {
		if s == "" {
			t.Errorf("LCMDriftSignals[%d] is empty", i)
		}
	}
}

func TestLCMCatchUpBehaviorDescriptionNonEmpty(t *testing.T) {
	if LCMCatchUpBehaviorDescription == "" {
		t.Error("LCMCatchUpBehaviorDescription must not be empty")
	}
}
