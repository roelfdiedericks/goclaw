package setup

import "testing"

func TestBuildPresetsIncludesLlamaCppManagedPreset(t *testing.T) {
	presets := BuildPresets()
	for _, preset := range presets {
		if preset.Key != "llamacpp-managed" {
			continue
		}
		if preset.Driver != "llamacpp" {
			t.Fatalf("expected llamacpp driver, got %q", preset.Driver)
		}
		if !preset.Synthetic || !preset.IsLocal {
			t.Fatalf("expected synthetic local preset, got %#v", preset)
		}
		if preset.LlamaCpp == nil || preset.LlamaCpp.Mode == "" || preset.LlamaCpp.ManagedModelID == "" {
			t.Fatalf("expected llama.cpp managed config, got %#v", preset.LlamaCpp)
		}
		if preset.DefaultModel == "" {
			t.Fatalf("expected default model for synthetic preset")
		}
		return
	}
	t.Fatalf("expected llamacpp managed preset in preset list")
}
