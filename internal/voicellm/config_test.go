package voicellm

import "testing"

func TestConfigToConfigFormDataDetectsPreset(t *testing.T) {
	cfg := Config{
		Effects: EffectsConfig{
			Mode: "both",
			Ring: RingModConfig{
				CarrierFreq: 200,
				Mix:         0.7,
			},
			Bitcrush: BitcrushConfig{
				BitDepth:   8,
				Downsample: 2,
			},
		},
	}

	formData := cfg.ToConfigFormData()
	if formData.Effects.Preset != "battlestar" {
		t.Fatalf("expected battlestar preset, got %q", formData.Effects.Preset)
	}
}

func TestConfigFormDataToConfigAppliesPresetValues(t *testing.T) {
	formData := ConfigFormData{
		Effects: EffectsFormData{
			Preset: "dalek",
		},
	}

	cfg := formData.ToConfig()
	if cfg.Effects.Mode != "ring" {
		t.Fatalf("expected ring mode, got %q", cfg.Effects.Mode)
	}
	if cfg.Effects.Ring.CarrierFreq != 30 {
		t.Fatalf("expected dalek carrier frequency, got %v", cfg.Effects.Ring.CarrierFreq)
	}
	if cfg.Effects.Ring.Mix != 0.8 {
		t.Fatalf("expected dalek mix, got %v", cfg.Effects.Ring.Mix)
	}
}

func TestConfigFormDefIncludesAudioEffectsPresetAndCustomSections(t *testing.T) {
	def := ConfigFormDef()

	var audioEffects *struct {
		fieldName string
		nested    *bool
	}
	for _, section := range def.Sections {
		if section.Title == "Audio Effects" {
			nested := section.Nested != nil
			audioEffects = &struct {
				fieldName string
				nested    *bool
			}{
				fieldName: section.FieldName,
				nested:    &nested,
			}
			break
		}
	}
	if audioEffects == nil {
		t.Fatalf("expected Audio Effects section")
	}
	if audioEffects.fieldName != "effects" {
		t.Fatalf("expected Audio Effects field name to be effects, got %q", audioEffects.fieldName)
	}
	if !*audioEffects.nested {
		t.Fatalf("expected Audio Effects section to use nested form")
	}

	effectsDef := EffectsConfigWebFormDef()
	if len(effectsDef.Sections) < 4 {
		t.Fatalf("expected nested effects sections, got %d", len(effectsDef.Sections))
	}
	if effectsDef.Sections[0].Fields[0].Name != "preset" {
		t.Fatalf("expected first effects field to be preset, got %q", effectsDef.Sections[0].Fields[0].Name)
	}
	if effectsDef.Sections[1].ShowWhen != "effects.preset=custom" {
		t.Fatalf("unexpected custom mode showWhen: %q", effectsDef.Sections[1].ShowWhen)
	}
	if effectsDef.Sections[2].FieldName != "ring" {
		t.Fatalf("expected ring section field name, got %q", effectsDef.Sections[2].FieldName)
	}
	if effectsDef.Sections[3].FieldName != "bitcrush" {
		t.Fatalf("expected bitcrush section field name, got %q", effectsDef.Sections[3].FieldName)
	}
}
