package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadNormalizesLCMConfig asserts the invariant that after loading a
// config from disk, SessionConfig.Summarization.Compaction (and its nested
// LCMConfig) are fully normalized, regardless of whether the on-disk JSON
// sets every field. Downstream reads may trust these values directly.
func TestLoadNormalizesLCMConfig(t *testing.T) {
	t.Parallel()

	// Minimal config: only a preset name; rely on normalization to fill the rest.
	body := `{
		"gateway": {},
		"session": {
			"summarization": {
				"compaction": {
					"lcm": {
						"preset": "balanced"
					}
				}
			}
		}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "goclaw.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	result, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	c := result.Config.Session.Summarization.Compaction
	if c.LCM.Preset != "balanced" {
		t.Fatalf("expected preset=balanced, got %q", c.LCM.Preset)
	}
	if !c.LCM.Enabled {
		t.Fatalf("expected LCM.Enabled=true from struct-tag default after load, got false")
	}
	if c.LCM.SummaryInjectionMode != "frontier" {
		t.Fatalf("expected frontier mode from balanced preset, got %q", c.LCM.SummaryInjectionMode)
	}
	if c.LCM.MaxInjectedSummaryTokens != 4000 {
		t.Fatalf("expected balanced budget 4000, got %d", c.LCM.MaxInjectedSummaryTokens)
	}
	if c.FreshTailCount != 10 {
		t.Fatalf("expected balanced FreshTailCount=10, got %d", c.FreshTailCount)
	}
	if c.LeafMinFanout != 4 {
		t.Fatalf("expected balanced LeafMinFanout=4, got %d", c.LeafMinFanout)
	}
	if c.CondensedTargetTokens != 1200 {
		t.Fatalf("expected balanced CondensedTargetTokens=1200, got %d", c.CondensedTargetTokens)
	}
}

// TestLoadEmptyLCMFallsBackToBalanced asserts that an LCM-absent config on
// disk still lands on balanced after load, via detection over struct-tag
// defaults. This keeps the raw Enabled reads elsewhere in the codebase safe.
func TestLoadEmptyLCMFallsBackToBalanced(t *testing.T) {
	t.Parallel()

	body := `{
		"gateway": {},
		"session": {}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "goclaw.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	result, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	c := result.Config.Session.Summarization.Compaction
	if c.LCM.Preset != "balanced" {
		t.Fatalf("expected preset detection to resolve empty LCM to balanced, got %q", c.LCM.Preset)
	}
	if !c.LCM.Enabled {
		t.Fatalf("expected LCM.Enabled=true by default, got false")
	}
}
