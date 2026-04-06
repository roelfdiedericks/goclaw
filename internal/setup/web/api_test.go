package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/configapply"
)

func TestSetConfigPathRootMergeIsNonDestructive(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.Name = "GoClaw"
	cfg.Gateway.WorkingDir = "/old/workspace"

	err := setConfigPath(cfg, "/", map[string]interface{}{
		"gateway": map[string]interface{}{
			"workingDir": "/new/workspace",
		},
	})
	if err != nil {
		t.Fatalf("setConfigPath returned error: %v", err)
	}

	if cfg.Gateway.WorkingDir != "/new/workspace" {
		t.Fatalf("expected gateway workingDir to be updated, got %q", cfg.Gateway.WorkingDir)
	}
	if cfg.Agent.Name != "GoClaw" {
		t.Fatalf("expected unrelated root key to be preserved, got agent name %q", cfg.Agent.Name)
	}
}

func TestValidateRootPayloadRejectsUnknownKeys(t *testing.T) {
	section := FindSection(EditorSectionsForMode(false), "gateway")
	if section == nil {
		t.Fatalf("gateway section not found")
	}

	if err := validateRootPayload(section, map[string]interface{}{
		"gateway": map[string]interface{}{},
		"agent":   map[string]interface{}{},
	}); err != nil {
		t.Fatalf("expected valid gateway payload, got error: %v", err)
	}

	if err := validateRootPayload(section, map[string]interface{}{
		"gateway": map[string]interface{}{},
		"hacker":  true,
	}); err == nil {
		t.Fatalf("expected unknown root key to be rejected")
	}
}

func TestValidateSectionPayloadAgainstSchemaRejectsUnknownFields(t *testing.T) {
	section := FindSection(EditorSectionsForMode(false), "llm")
	if section == nil {
		t.Fatalf("llm section not found")
	}

	validPayload := map[string]interface{}{
		"agent": map[string]interface{}{
			"models": []string{"anthropic/claude-sonnet-4-20250514"},
		},
	}
	if err := validateSectionPayloadAgainstSchema(section, validPayload); err != nil {
		t.Fatalf("expected valid payload, got error: %v", err)
	}

	invalidPayload := map[string]interface{}{
		"agent": map[string]interface{}{
			"models": []string{"anthropic/claude-sonnet-4-20250514"},
			"hacker": true,
		},
	}
	if err := validateSectionPayloadAgainstSchema(section, invalidPayload); err == nil {
		t.Fatalf("expected unknown nested field to be rejected")
	}
}

func TestValidateSectionPayloadProviderListAndRolesAndModelChain(t *testing.T) {
	llmProviders := FindSection(EditorSectionsForMode(false), "llm-providers")
	if llmProviders == nil {
		t.Fatalf("llm-providers section not found")
	}
	providerPayload := map[string]interface{}{
		"providers": map[string]interface{}{
			"ollama_local": map[string]interface{}{
				"driver":  "ollama",
				"baseURL": "http://127.0.0.1:11434",
			},
			"anthropic": map[string]interface{}{
				"driver":        "anthropic",
				"apiKey":        "sk-ant-test",
				"promptCaching": true,
			},
		},
	}
	if err := validateSectionPayloadAgainstSchema(llmProviders, providerPayload); err != nil {
		t.Fatalf("expected llm-providers payload to be valid, got %v", err)
	}

	roles := FindSection(EditorSectionsForMode(false), "roles")
	if roles == nil {
		t.Fatalf("roles section not found")
	}
	rolesPayload := map[string]interface{}{
		"operator": map[string]interface{}{
			"tools":       "*",
			"skills":      []string{"git", "shell"},
			"memory":      "full",
			"transcripts": "own",
			"commands":    true,
		},
	}
	if err := validateSectionPayloadAgainstSchema(roles, rolesPayload); err != nil {
		t.Fatalf("expected roles payload to be valid, got %v", err)
	}

	llm := FindSection(EditorSectionsForMode(false), "llm")
	if llm == nil {
		t.Fatalf("llm section not found")
	}
	modelChainPayload := map[string]interface{}{
		"agent": map[string]interface{}{
			"models": []string{
				"anthropic/claude-sonnet-4-20250514",
				"openrouter/openai/gpt-4.1",
			},
		},
	}
	if err := validateSectionPayloadAgainstSchema(llm, modelChainPayload); err != nil {
		t.Fatalf("expected model-chain payload to be valid, got %v", err)
	}
}

func TestAPILoadConfigSeedsDefaultsWhenConfigMissing(t *testing.T) {
	api := NewAPI(filepath.Join(t.TempDir(), "missing-goclaw.json"), configapply.CallerWebStandalone, EditorSectionsForMode(false))

	result, err := api.loadConfig()
	if err != nil {
		t.Fatalf("expected defaults to be seeded, got error: %v", err)
	}
	if result == nil || result.Config == nil {
		t.Fatalf("expected seeded config result")
	}
	if result.SourcePath != "" {
		t.Fatalf("expected no source path for seeded defaults, got %q", result.SourcePath)
	}
	if result.Config.Gateway.WorkingDir == "" {
		t.Fatalf("expected default working dir to be set")
	}
	if len(result.Config.LLM.Agent.Models) == 0 {
		t.Fatalf("expected default llm agent model chain")
	}
}

func TestAPIResolveSavePathFallsBackToExplicitPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "goclaw.json")
	api := NewAPI(target, configapply.CallerWebStandalone, EditorSectionsForMode(false))

	path := api.resolveSavePath(&config.LoadResult{})
	if path != target {
		t.Fatalf("expected explicit config path %q, got %q", target, path)
	}
}

func TestAPIResolveSavePathFallsBackToLoadedDefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(cwd)

	api := NewAPI("", configapply.CallerWebStandalone, EditorSectionsForMode(false))
	path := api.resolveSavePath(&config.LoadResult{})
	if filepath.Base(path) != "goclaw.json" {
		t.Fatalf("expected fallback goclaw.json path, got %q", path)
	}
}

func TestHandleSectionActionInvokesCommand(t *testing.T) {
	api := NewAPI(filepath.Join(t.TempDir(), "missing-goclaw.json"), configapply.CallerWebStandalone, EditorSectionsForMode(false))
	bus.RegisterCommand("media", "stats", func(cmd bus.Command) bus.CommandResult {
		return bus.CommandResult{
			Success: true,
			Message: "Current media usage: 0.1 GB of 50 GB total.",
			Data:    map[string]any{"ok": true},
		}
	})
	defer bus.UnregisterCommand("media", "stats")

	req := httptest.NewRequest(http.MethodPost, "/setup/api/section-action/media/stats", nil)
	rec := httptest.NewRecorder()
	api.HandleSectionAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success response, got %+v", resp)
	}
	if resp.Message == "" {
		t.Fatalf("expected success message")
	}
}

func TestExpandVoiceLLMSectionPayloadAppliesEffectsPreset(t *testing.T) {
	payload := map[string]interface{}{
		"effects": map[string]interface{}{
			"preset": "battlestar",
		},
	}

	runtimePayload, err := expandVoiceLLMSectionPayload(payload)
	if err != nil {
		t.Fatalf("expandVoiceLLMSectionPayload: %v", err)
	}

	effects, ok := runtimePayload["effects"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected effects map in runtime payload, got %#v", runtimePayload["effects"])
	}
	if got := effects["mode"]; got != "both" {
		t.Fatalf("expected battlestar mode both, got %#v", got)
	}
	ring, ok := effects["ring"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected ring map in runtime payload, got %#v", effects["ring"])
	}
	if got := ring["carrierFreq"]; got != float64(200) {
		t.Fatalf("expected battlestar carrier freq 200, got %#v", got)
	}
	bitcrush, ok := effects["bitcrush"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected bitcrush map in runtime payload, got %#v", effects["bitcrush"])
	}
	if got := bitcrush["bitDepth"]; got != float64(8) {
		t.Fatalf("expected battlestar bit depth 8, got %#v", got)
	}
}
