package setup

import (
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

func TestBuildConfigFromWizardDataSeedsBuiltInHugotEmbeddings(t *testing.T) {
	cfg := buildConfigFromWizardData(&WizardData{})

	if got := getStringSlice(cfg, "llm.embeddings.models"); len(got) != 1 || got[0] != llm.BuiltInHugotProviderAlias+"/"+llm.DefaultHugotEmbeddingModel {
		t.Fatalf("expected default Hugot embeddings chain, got %#v", got)
	}

	providers, ok := cfg["llm"].(map[string]interface{})["providers"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected llm.providers map to be present")
	}
	hugotCfgRaw, ok := providers[llm.BuiltInHugotProviderAlias]
	if !ok {
		t.Fatalf("expected built-in provider %q to be present", llm.BuiltInHugotProviderAlias)
	}
	hugotCfg, ok := hugotCfgRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected built-in provider config to be a map")
	}
	if got := hugotCfg["driver"]; got != "hugot" {
		t.Fatalf("expected built-in provider driver hugot, got %#v", got)
	}
}

func TestBuildConfigFromWizardDataPreservesExistingEmbeddingsChain(t *testing.T) {
	data := &WizardData{
		ExistingConfig: &config.Config{
			LLM: llm.LLMConfig{
				Embeddings: llm.LLMPurposeConfig{
					Models: []string{"existing/provider-model"},
				},
			},
		},
	}

	cfg := buildConfigFromWizardData(data)
	got := getStringSlice(cfg, "llm.embeddings.models")
	if len(got) != 1 || got[0] != "existing/provider-model" {
		t.Fatalf("expected existing embeddings chain to be preserved, got %#v", got)
	}
}

func TestNewWizardDataDefaultsAssistantPreset(t *testing.T) {
	data := NewWizardData()
	if data.SandboxPreset != SandboxPresetAssistant {
		t.Fatalf("expected assistant preset default, got %q", data.SandboxPreset)
	}
	if data.SandboxMode != sandbox.ModeAutoDocsWrite {
		t.Fatalf("expected assistant mode default %q, got %q", sandbox.ModeAutoDocsWrite, data.SandboxMode)
	}
}

func TestBuildConfigFromWizardDataAppliesPresetMapping(t *testing.T) {
	data := NewWizardData()
	data.SandboxPreset = SandboxPresetHardened
	data.SandboxAdvanced = false
	data.MarkDirty("SandboxPreset")

	cfg := buildConfigFromWizardData(data)
	if got := getBool(cfg, "sandbox.general.enabled"); !got {
		t.Fatalf("expected hardened preset to keep sandbox enabled")
	}
	if got := getString(cfg, "sandbox.general.mode"); got != sandbox.ModeHome {
		t.Fatalf("expected hardened preset home mode, got %q", got)
	}
	if got := getBool(cfg, "sandbox.general.execEnabled"); !got {
		t.Fatalf("expected hardened preset to keep exec sandboxing enabled")
	}
	if got := getBool(cfg, "sandbox.general.browserEnabled"); !got {
		t.Fatalf("expected hardened preset to keep browser sandboxing enabled")
	}
	if got := getBool(cfg, "sandbox.general.fileToolsEnabled"); !got {
		t.Fatalf("expected hardened preset to keep file tools sandboxing enabled")
	}
}

func TestBuildConfigFromWizardDataAdvancedOverridesPreset(t *testing.T) {
	data := NewWizardData()
	data.SandboxPreset = SandboxPresetPermissive
	data.SandboxAdvanced = true
	data.SandboxEnabled = true
	data.SandboxMode = sandbox.ModeHome
	data.ExecSandboxEnabled = true
	data.BrowserSandboxEnabled = false
	data.FileToolsSandboxEnabled = true
	data.MarkDirty("SandboxPreset", "SandboxAdvanced", "SandboxEnabled", "SandboxMode", "ExecSandboxEnabled", "BrowserSandboxEnabled", "FileToolsSandboxEnabled")

	cfg := buildConfigFromWizardData(data)
	if got := getBool(cfg, "sandbox.general.enabled"); !got {
		t.Fatalf("expected advanced override to keep sandbox enabled")
	}
	if got := getString(cfg, "sandbox.general.mode"); got != sandbox.ModeHome {
		t.Fatalf("expected advanced override mode home, got %q", got)
	}
	if got := getBool(cfg, "sandbox.general.execEnabled"); !got {
		t.Fatalf("expected advanced override exec sandbox enabled")
	}
	if got := getBool(cfg, "sandbox.general.browserEnabled"); got {
		t.Fatalf("expected advanced override browser sandbox disabled")
	}
}

func TestBuildConfigFromWizardDataWritesAgentIdentityWhenDirty(t *testing.T) {
	data := NewWizardData()
	data.AgentName = "Clawbert"
	data.AgentEmoji = ":crab:"
	data.AgentTyping = "Clawbert is thinking..."
	data.MarkDirty("AgentName", "AgentEmoji", "AgentTyping")

	cfg := buildConfigFromWizardData(data)
	if got := getString(cfg, "agent.name"); got != "Clawbert" {
		t.Fatalf("expected agent.name to be written, got %q", got)
	}
	if got := getString(cfg, "agent.emoji"); got != ":crab:" {
		t.Fatalf("expected agent.emoji to be written, got %q", got)
	}
	if got := getString(cfg, "agent.typing"); got != "Clawbert is thinking..." {
		t.Fatalf("expected agent.typing to be written, got %q", got)
	}
}

func TestDetectSandboxPresetReturnsCustomForManualBlend(t *testing.T) {
	preset, advanced := DetectSandboxPreset(true, sandbox.ModeAutoDocsRead, true, true, true)
	if preset != SandboxPresetCustom {
		t.Fatalf("expected custom preset for manual blend, got %q", preset)
	}
	if !advanced {
		t.Fatalf("expected custom preset to mark advanced=true")
	}
}

func TestApplyOpenClawImportRestoresBaseStateWhenDisabled(t *testing.T) {
	data := NewWizardData()
	data.WorkspacePath = "/home/openclaw/.goclaw/workspace"
	data.TelegramEnabled = false
	data.TelegramToken = "goclaw-token"
	data.UserTelegramID = "base-id"
	data.captureBaseImportState()

	data.openClawImportState = wizardImportState{
		WorkspacePath:   "/home/openclaw/.openclaw/workspace",
		TelegramEnabled: true,
		TelegramToken:   "openclaw-token",
		UserTelegramID:  "openclaw-id",
	}

	data.ApplyOpenClawImport(true)
	if data.WorkspacePath != "/home/openclaw/.openclaw/workspace" || data.TelegramToken != "openclaw-token" || !data.TelegramEnabled || data.UserTelegramID != "openclaw-id" {
		t.Fatalf("expected OpenClaw values to be applied, got %+v", data)
	}

	data.ApplyOpenClawImport(false)
	if data.WorkspacePath != "/home/openclaw/.goclaw/workspace" {
		t.Fatalf("expected base workspace restored, got %q", data.WorkspacePath)
	}
	if data.TelegramEnabled {
		t.Fatalf("expected base telegram enabled to be restored")
	}
	if data.TelegramToken != "goclaw-token" {
		t.Fatalf("expected base telegram token restored, got %q", data.TelegramToken)
	}
	if data.UserTelegramID != "base-id" {
		t.Fatalf("expected base telegram user restored, got %q", data.UserTelegramID)
	}
}

func TestResetPairingStageRestoresInitialOwnerIDs(t *testing.T) {
	data := NewWizardData()
	data.InitialUserTelegramID = "initial-telegram"
	data.InitialUserWhatsAppID = "initial-whatsapp"
	data.UserTelegramID = "new-telegram"
	data.UserWhatsAppID = "new-whatsapp"

	data.ResetPairingStage()

	if data.UserTelegramID != "initial-telegram" {
		t.Fatalf("expected telegram owner restored, got %q", data.UserTelegramID)
	}
	if data.UserWhatsAppID != "initial-whatsapp" {
		t.Fatalf("expected whatsapp owner restored, got %q", data.UserWhatsAppID)
	}
}

func TestBuildWizardStepsMatchesBrowserLikeOrderWithoutPairing(t *testing.T) {
	data := NewWizardData()
	data.TelegramEnabled = false
	data.WhatsAppEnabled = false
	data.OpenClawExists = true

	steps := buildWizardSteps(data)
	got := wizardStepTitles(steps)
	want := []string{
		"Welcome",
		"Agent Identity",
		"Workspace",
		"Owner Account",
		"Communication Channels",
		"LLM Provider",
		"Voice Settings",
		"Security & Skills",
		"Review & Finish",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected step order:\n got: %v\nwant: %v", got, want)
	}
}

func TestBuildWizardStepsIncludesConditionalPairingOnlyWhenChannelsNeedIt(t *testing.T) {
	data := NewWizardData()

	if containsWizardStep(buildWizardSteps(data), "Channel Pairing") {
		t.Fatalf("expected pairing step to be omitted when Telegram and WhatsApp are disabled")
	}

	data.TelegramEnabled = true
	if !containsWizardStep(buildWizardSteps(data), "Channel Pairing") {
		t.Fatalf("expected pairing step when Telegram is enabled")
	}

	data.TelegramEnabled = false
	data.WhatsAppEnabled = true
	if !containsWizardStep(buildWizardSteps(data), "Channel Pairing") {
		t.Fatalf("expected pairing step when WhatsApp is enabled")
	}
}

func TestStepLLMProviderRequiresProviderAndAPIKey(t *testing.T) {
	data := NewWizardData()
	step := stepLLMProvider(data)

	if step.OnExit == nil {
		t.Fatalf("expected llm step to validate on exit")
	}
	if err := step.OnExit(nil); err == nil {
		t.Fatalf("expected missing provider to fail validation")
	}

	data.LLMProviderID = "anthropic"
	if err := step.OnExit(nil); err == nil {
		t.Fatalf("expected missing API key to fail validation for remote provider")
	}

	data.LLMAPIKey = "sk-ant-test"
	if err := step.OnExit(nil); err != nil {
		t.Fatalf("expected remote provider with API key to pass, got %v", err)
	}

	data.LLMProviderID = "ollama"
	data.LLMDriver = "ollama"
	data.LLMBaseURL = "http://10.0.0.25:11434"
	data.LLMAPIKey = ""
	if err := step.OnExit(nil); err != nil {
		t.Fatalf("expected ollama without API key to pass, got %v", err)
	}

	data.LLMProviderID = "custom"
	data.LLMAPIKey = ""
	if err := step.OnExit(nil); err != nil {
		t.Fatalf("expected custom provider without API key to pass, got %v", err)
	}
}

func wizardStepTitles(steps []forms.WizardStep) []string {
	titles := make([]string, 0, len(steps))
	for _, step := range steps {
		titles = append(titles, step.Title)
	}
	return titles
}

func containsWizardStep(steps []forms.WizardStep, title string) bool {
	for _, step := range steps {
		if step.Title == title {
			return true
		}
	}
	return false
}

func getBool(cfg map[string]interface{}, path string) bool {
	val, _ := getPath(cfg, path)
	b, _ := val.(bool)
	return b
}

func getString(cfg map[string]interface{}, path string) string {
	val, _ := getPath(cfg, path)
	s, _ := val.(string)
	return s
}

func getPath(cfg map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	current := interface{}(cfg)
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		next, ok := m[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}
