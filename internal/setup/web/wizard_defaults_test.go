package web

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/configapply"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/setup"
)

func TestNewWizardAPISeedsDefaultsWhenConfigMissing(t *testing.T) {
	api := NewWizardAPI("/tmp/definitely-missing-goclaw.json", configapply.CallerWebStandalone)
	if api == nil || api.state == nil || api.state.Data == nil {
		t.Fatalf("expected wizard api state to be initialized")
	}

	data := api.state.Data
	if data.ConfigExists {
		t.Fatalf("expected missing config to not be marked as existing")
	}
	if data.ExistingConfig == nil {
		t.Fatalf("expected default config seed to be attached")
	}
	if data.WorkspacePath == "" {
		t.Fatalf("expected workspace default to be seeded")
	}
	if data.HTTPListen == "" {
		t.Fatalf("expected HTTP listen default to be seeded")
	}
	if data.LLMProviderID == "" {
		t.Fatalf("expected LLM provider default to be seeded")
	}
	if data.LLMModel == "" {
		t.Fatalf("expected LLM model default to be seeded")
	}
	if !data.SandboxEnabled || !data.ExecSandboxEnabled || !data.BrowserSandboxEnabled || !data.FileToolsSandboxEnabled {
		t.Fatalf("expected sandbox defaults to be seeded on missing config")
	}
	if data.SandboxPreset != setup.SandboxPresetAssistant {
		t.Fatalf("expected assistant sandbox preset by default, got %q", data.SandboxPreset)
	}
	if data.SandboxMode == "" {
		t.Fatalf("expected sandbox mode default to be seeded")
	}
}

func TestApplyWizardFormDefaultsSeedsMissingValuesOnly(t *testing.T) {
	data := setup.NewWizardData()
	data.HTTPEnabled = false

	def := &forms.FormDef{
		Sections: []forms.Section{
			{
				Fields: []forms.Field{
					{Name: "WorkspacePath", Type: forms.Text, Default: "~/.goclaw/workspace"},
					{Name: "HTTPEnabled", Type: forms.Toggle, Default: true},
					{Name: "VoiceLLMVoice", Type: forms.Text, Default: "Eve"},
				},
			},
		},
	}

	applyWizardFormDefaults(data, def)
	if data.WorkspacePath != "~/.goclaw/workspace" {
		t.Fatalf("expected WorkspacePath default to be applied, got %q", data.WorkspacePath)
	}
	if data.HTTPEnabled != true {
		t.Fatalf("expected HTTPEnabled default to be applied when value is zero")
	}
	if data.VoiceLLMVoice != "Eve" {
		t.Fatalf("expected VoiceLLMVoice default to be applied, got %q", data.VoiceLLMVoice)
	}

	data.MarkDirty("HTTPEnabled")
	data.HTTPEnabled = false
	applyWizardFormDefaults(data, def)
	if data.HTTPEnabled != false {
		t.Fatalf("expected dirty field to retain user value")
	}
}

func TestValidateStepSecurityRequiresPresetConsent(t *testing.T) {
	data := setup.NewWizardData()

	data.SandboxPreset = setup.SandboxPresetPermissive
	data.SandboxConsentPermissive = false
	if errs := validateStep("security", data); errs["SandboxConsentPermissive"] == "" {
		t.Fatalf("expected permissive consent error")
	}
	data.SandboxConsentPermissive = true

	data.SandboxPreset = setup.SandboxPresetAssistant
	data.SandboxConsentAssistant = false
	if errs := validateStep("security", data); errs["SandboxConsentAssistant"] == "" {
		t.Fatalf("expected assistant consent error")
	}
	data.SandboxConsentAssistant = true

	data.SandboxPreset = setup.SandboxPresetHardened
	data.SandboxConsentHardened = false
	if errs := validateStep("security", data); errs["SandboxConsentHardened"] == "" {
		t.Fatalf("expected hardened consent error")
	}
}

func TestUpdateWizardDataSecurityPresetAppliesWhenAdvancedDisabled(t *testing.T) {
	data := setup.NewWizardData()
	payload := map[string]interface{}{
		"SandboxPreset":           setup.SandboxPresetHardened,
		"SandboxAdvanced":         false,
		"SandboxConsentHardened":  true,
		"SandboxConsentAssistant": false,
		"SandboxConsentPermissive": false,
	}
	if err := updateWizardData(data, payload); err != nil {
		t.Fatalf("updateWizardData failed: %v", err)
	}

	if data.SandboxMode != "home" {
		t.Fatalf("expected hardened preset to apply home mode, got %q", data.SandboxMode)
	}
	if data.ExecSandboxEnabled || data.BrowserSandboxEnabled {
		t.Fatalf("expected hardened preset to disable exec/browser sandboxing")
	}
	if !data.FileToolsSandboxEnabled {
		t.Fatalf("expected hardened preset to keep file tools sandboxing enabled")
	}
}
