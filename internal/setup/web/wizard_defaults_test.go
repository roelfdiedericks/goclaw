package web

import (
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/configapply"
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
	if data.AgentName == "" {
		t.Fatalf("expected agent name default to be seeded")
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

func TestValidateStepAgentRequiresName(t *testing.T) {
	data := setup.NewWizardData()
	data.AgentName = ""
	errs := validateStep("agent", data)
	if errs["AgentName"] == "" {
		t.Fatalf("expected agent-name validation error")
	}
}

func TestValidateStepAgentTypingMaxLength(t *testing.T) {
	data := setup.NewWizardData()
	data.AgentName = "GoClaw"
	data.AgentTyping = strings.Repeat("x", setup.WizardAgentTypingMaxLen+1)
	errs := validateStep("agent", data)
	if errs["AgentTyping"] == "" {
		t.Fatalf("expected agent typing max-length validation error")
	}
}

func TestValidateStepLLMAPIKeyRequirements(t *testing.T) {
	data := setup.NewWizardData()
	data.LLMOnboardingChoice = setup.LLMChoiceCloudProvider
	data.LLMProviderID = "anthropic"
	data.LLMBaseURL = "https://api.anthropic.com"
	data.LLMAPIKey = ""
	if errs := validateStep("llm", data); errs["LLMAPIKey"] == "" {
		t.Fatalf("expected anthropic to require an API key during setup")
	}

	data = setup.NewWizardData()
	data.LLMOnboardingChoice = setup.LLMChoiceLocalGemma
	data.LLMManagedModelID = "gemma4-e2b"
	if errs := validateStep("llm", data); len(errs) != 0 {
		t.Fatalf("expected local gemma choice to validate, got %#v", errs)
	}

	data = setup.NewWizardData()
	data.LLMOnboardingChoice = setup.LLMChoiceExistingLlamaCpp
	data.LLMBaseURL = "http://127.0.0.1:8080"
	data.LLMModel = "ggml-org/gemma-4-E2B-it-GGUF:Q8_0"
	if errs := validateStep("llm", data); len(errs) != 0 {
		t.Fatalf("expected existing llama.cpp to validate, got %#v", errs)
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
		"SandboxPreset":            setup.SandboxPresetHardened,
		"SandboxAdvanced":          false,
		"SandboxConsentHardened":   true,
		"SandboxConsentAssistant":  false,
		"SandboxConsentPermissive": false,
	}
	if err := updateWizardData(data, payload); err != nil {
		t.Fatalf("updateWizardData failed: %v", err)
	}

	if data.SandboxMode != "home" {
		t.Fatalf("expected hardened preset to apply home mode, got %q", data.SandboxMode)
	}
	if !data.ExecSandboxEnabled || !data.BrowserSandboxEnabled {
		t.Fatalf("expected hardened preset to keep exec/browser sandboxing enabled")
	}
	if !data.FileToolsSandboxEnabled {
		t.Fatalf("expected hardened preset to keep file tools sandboxing enabled")
	}
}

func TestValidateStepSecurityCustomPresetNeedsNoConsent(t *testing.T) {
	data := setup.NewWizardData()
	data.SandboxPreset = setup.SandboxPresetCustom
	errs := validateStep("security", data)
	if len(errs) != 0 {
		t.Fatalf("expected no preset-consent errors for custom preset, got %#v", errs)
	}
}

func TestSecurityStepAdvancedSandboxHidesCategoriesWhenDisabled(t *testing.T) {
	def := getStepFormDef("security", setup.NewWizardData())
	if def == nil {
		t.Fatalf("expected security form definition")
	}

	var advanced *forms.Section
	for i := range def.Sections {
		if def.Sections[i].Title == "Advanced Sandbox Settings" {
			advanced = &def.Sections[i]
			break
		}
	}
	if advanced == nil || advanced.Nested == nil || len(advanced.Nested.Sections) == 0 {
		t.Fatalf("expected advanced sandbox settings nested sections")
	}

	top := advanced.Nested.Sections[0]
	if top.ShowWhen != "SandboxAdvanced=true" {
		t.Fatalf("expected top advanced showWhen SandboxAdvanced=true, got %q", top.ShowWhen)
	}
	if len(top.Fields) != 1 || top.Fields[0].Name != "SandboxEnabled" {
		t.Fatalf("expected only SandboxEnabled at top advanced level, got %#v", top.Fields)
	}
	if top.Nested == nil {
		t.Fatalf("expected nested sections under SandboxEnabled")
	}

	var foundEnabled bool
	var foundDisabled bool
	for _, sec := range top.Nested.Sections {
		switch sec.ShowWhen {
		case "SandboxEnabled=true":
			foundEnabled = true
			if len(sec.Fields) != 4 {
				t.Fatalf("expected 4 fields when SandboxEnabled=true, got %d", len(sec.Fields))
			}
		case "SandboxEnabled=false":
			foundDisabled = true
			if sec.Desc != "Sandbox categories and mode are not applicable while sandboxing is disabled." {
				t.Fatalf("unexpected disabled sandbox advanced message: %q", sec.Desc)
			}
		}
	}
	if !foundEnabled || !foundDisabled {
		t.Fatalf("expected both enabled and disabled SandboxEnabled conditional sections")
	}
}
