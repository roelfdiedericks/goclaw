package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/configapply"
	"github.com/roelfdiedericks/goclaw/internal/jobs"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
)

func TestHandleGetPresetsIncludesSyntheticLlamaCppPreset(t *testing.T) {
	api := NewAPI(filepath.Join(t.TempDir(), "goclaw.json"), configapply.CallerWebStandalone, EditorSectionsForMode(false))
	req := httptest.NewRequest(http.MethodGet, "/setup/api/presets", nil)
	rec := httptest.NewRecorder()

	api.HandleGetPresets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Presets []struct {
				ID        string `json:"id"`
				Driver    string `json:"driver"`
				Synthetic bool   `json:"synthetic"`
				LlamaCpp  struct {
					Mode           string `json:"mode"`
					ManagedModelID string `json:"managedModelID"`
				} `json:"llamacpp"`
			} `json:"presets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	for _, preset := range resp.Data.Presets {
		if preset.ID == "llamacpp-managed" {
			if preset.Driver != "llamacpp" || !preset.Synthetic {
				t.Fatalf("unexpected synthetic preset payload %#v", preset)
			}
			if preset.LlamaCpp.Mode != "managed" || preset.LlamaCpp.ManagedModelID == "" {
				t.Fatalf("expected managed llamacpp config in preset payload, got %#v", preset.LlamaCpp)
			}
			return
		}
	}
	t.Fatalf("expected synthetic llamacpp preset in response")
}

func TestHandleGetLocalLLMIncludesManagedProvidersAndModels(t *testing.T) {
	origLatestFunc := localLLMLatestRuntimeVersionFunc
	localLLMLatestRuntimeVersionFunc = func(ctx context.Context) (string, error) { return "b9999", nil }
	localLLMLatestRuntimeCache = struct {
		Value     string
		Err       string
		FetchedAt time.Time
	}{}
	t.Cleanup(func() {
		localLLMLatestRuntimeVersionFunc = origLatestFunc
		localLLMLatestRuntimeCache = struct {
			Value     string
			Err       string
			FetchedAt time.Time
		}{}
	})

	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	writeTestConfig(t, configPath, &config.Config{
		LLM: llm.LLMConfig{
			Providers: map[string]llm.LLMProviderConfig{
				"local": {
					Driver: "llamacpp",
					LlamaCpp: &llm.LlamaCppProviderConfig{
						Mode:           "managed",
						ManagedModelID: "gemma4-e2b",
						Host:           "127.0.0.1",
						Port:           8080,
					},
				},
			},
			Agent: llm.LLMPurposeConfig{
				Models: []string{"local/managed"},
			},
		},
	})
	api := NewAPI(configPath, configapply.CallerWebStandalone, EditorSectionsForMode(false))
	req := httptest.NewRequest(http.MethodGet, "/setup/api/local-llm", nil)
	rec := httptest.NewRecorder()

	api.HandleLocalLLM(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			ManagedProviders []struct {
				Alias          string `json:"alias"`
				IsAgentDefault bool   `json:"isAgentDefault"`
			} `json:"managedProviders"`
			Models []struct {
				ID          string `json:"id"`
				Recommended bool   `json:"recommended"`
			} `json:"models"`
			Status struct {
				SystemProfile struct {
					Recommended string `json:"recommended"`
				} `json:"systemProfile"`
			} `json:"status"`
			RuntimeVersion struct {
				Latest               string `json:"latest"`
				Configured           string `json:"configured"`
				UsingLatestByDefault bool   `json:"usingLatestByDefault"`
				Effective            string `json:"effective"`
			} `json:"runtimeVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success response")
	}
	if len(resp.Data.ManagedProviders) != 1 || resp.Data.ManagedProviders[0].Alias != "local" || !resp.Data.ManagedProviders[0].IsAgentDefault {
		t.Fatalf("unexpected managed providers payload %#v", resp.Data.ManagedProviders)
	}
	if len(resp.Data.Models) == 0 {
		t.Fatalf("expected managed model list in response")
	}
	if resp.Data.Status.SystemProfile.Recommended == "" {
		t.Fatalf("expected normalized system profile in response")
	}
	if resp.Data.RuntimeVersion.Latest != "b9999" {
		t.Fatalf("expected latest runtime version b9999, got %#v", resp.Data.RuntimeVersion)
	}
	if resp.Data.RuntimeVersion.Configured != "" || !resp.Data.RuntimeVersion.UsingLatestByDefault || resp.Data.RuntimeVersion.Effective != "b9999" {
		t.Fatalf("unexpected runtime version payload %#v", resp.Data.RuntimeVersion)
	}
}

func TestHandleLocalLLMActionCancelsRunningJob(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	writeTestConfig(t, configPath, &config.Config{})
	api := NewAPI(configPath, configapply.CallerWebStandalone, EditorSectionsForMode(false))

	job := jobs.GetManager().Start(jobs.StartSpec{
		OwnerComponent: "local_llm",
		OwnerAction:    "start",
		Cancelable:     true,
	}, func(ctx context.Context, reporter *jobs.Reporter) (interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	body, err := json.Marshal(map[string]any{
		"action": "cancel_job",
		"jobID":  job.JobID,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/setup/api/local-llm", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	api.HandleLocalLLM(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, ok := jobs.GetManager().Status(job.JobID)
		if !ok {
			t.Fatalf("expected job %s to exist", job.JobID)
		}
		if status.State == jobs.StateCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected canceled job state")
}

func TestHandleLocalLLMActionEnsureLatestRuntimeUsesResolvedLatestVersion(t *testing.T) {
	origLatestFunc := localLLMLatestRuntimeVersionFunc
	localLLMLatestRuntimeVersionFunc = func(ctx context.Context) (string, error) { return "b4242", nil }
	localLLMLatestRuntimeCache = struct {
		Value     string
		Err       string
		FetchedAt time.Time
	}{}
	t.Cleanup(func() {
		localLLMLatestRuntimeVersionFunc = origLatestFunc
		localLLMLatestRuntimeCache = struct {
			Value     string
			Err       string
			FetchedAt time.Time
		}{}
		localllm.RegisterCommands()
	})

	var captured localllm.ManagedSpec
	bus.RegisterCommand("local_llm", "ensure_runtime", func(cmd bus.Command) bus.CommandResult {
		spec, ok := cmd.Payload.(localllm.ManagedSpec)
		if !ok {
			t.Fatalf("expected ManagedSpec payload, got %T", cmd.Payload)
		}
		captured = spec
		return bus.CommandResult{Success: true, Message: "ok"}
	})

	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	writeTestConfig(t, configPath, &config.Config{})
	api := NewAPI(configPath, configapply.CallerWebStandalone, EditorSectionsForMode(false))

	body, err := json.Marshal(map[string]any{
		"action": "ensure_latest_runtime",
		"modelID": "gemma4-e2b",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/setup/api/local-llm", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	api.HandleLocalLLM(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if captured.RuntimeVersion != "b4242" || captured.ModelID != "gemma4-e2b" {
		t.Fatalf("unexpected ensure_latest_runtime payload %#v", captured)
	}
}

func TestHandleLocalLLMActionConfigureManagedProviderPersistsConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	writeTestConfig(t, configPath, &config.Config{})
	api := NewAPI(configPath, configapply.CallerWebStandalone, EditorSectionsForMode(false))

	body, err := json.Marshal(map[string]any{
		"action":  "configure_managed_provider",
		"modelID": "gemma4-e2b",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/setup/api/local-llm", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	api.HandleLocalLLM(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			ConfigUpdated bool   `json:"configUpdated"`
			ProviderAlias string `json:"providerAlias"`
			ProviderConfig struct {
				Driver   string `json:"driver"`
				Subtype  string `json:"subtype"`
				LlamaCpp struct {
					Mode           string `json:"mode"`
					ManagedModelID string `json:"managedModelID"`
					Host           string `json:"host"`
					Port           int    `json:"port"`
				} `json:"llamacpp"`
			} `json:"providerConfig"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || !resp.Data.ConfigUpdated || resp.Data.ProviderAlias == "" {
		t.Fatalf("unexpected response payload %#v", resp)
	}
	if resp.Data.ProviderConfig.Driver != "llamacpp" || resp.Data.ProviderConfig.LlamaCpp.ManagedModelID != "gemma4-e2b" {
		t.Fatalf("unexpected provider payload %#v", resp.Data.ProviderConfig)
	}

	result, err := config.LoadFromPath(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	provider, ok := result.Config.LLM.Providers[resp.Data.ProviderAlias]
	if !ok {
		t.Fatalf("expected provider %q in saved config", resp.Data.ProviderAlias)
	}
	if provider.Driver != "llamacpp" || provider.Subtype != "llamacpp-managed" || provider.LlamaCpp == nil {
		t.Fatalf("unexpected saved provider %#v", provider)
	}
	if provider.LlamaCpp.Mode != "managed" || provider.LlamaCpp.ManagedModelID != "gemma4-e2b" {
		t.Fatalf("unexpected saved llamacpp config %#v", provider.LlamaCpp)
	}
	if provider.LlamaCpp.Host != "127.0.0.1" || provider.LlamaCpp.Port != 8080 {
		t.Fatalf("expected default managed host/port, got %#v", provider.LlamaCpp)
	}
}

func TestHandleLocalLLMActionUseForAgentUpdatesProviderAndChain(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	writeTestConfig(t, configPath, &config.Config{
		LLM: llm.LLMConfig{
			Providers: map[string]llm.LLMProviderConfig{
				"local": {
					Driver:  "llamacpp",
					Subtype: "llamacpp-managed",
					LlamaCpp: &llm.LlamaCppProviderConfig{
						Mode:           "managed",
						ManagedModelID: "gemma4-e2b",
						Host:           "127.0.0.1",
						Port:           8080,
					},
				},
			},
			Agent: llm.LLMPurposeConfig{
				Models: []string{
					"anthropic/claude-sonnet-4-20250514",
					"local/custom-model",
				},
			},
		},
	})
	api := NewAPI(configPath, configapply.CallerWebStandalone, EditorSectionsForMode(false))

	body, err := json.Marshal(map[string]any{
		"action":  "use_for_agent",
		"modelID": "gemma4-e4b",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/setup/api/local-llm", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	api.HandleLocalLLM(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	result, err := config.LoadFromPath(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	provider := result.Config.LLM.Providers["local"]
	if provider.LlamaCpp == nil || provider.LlamaCpp.ManagedModelID != "gemma4-e4b" {
		t.Fatalf("expected provider model to be updated, got %#v", provider.LlamaCpp)
	}
	if len(result.Config.LLM.Agent.Models) != 2 {
		t.Fatalf("unexpected agent model chain %#v", result.Config.LLM.Agent.Models)
	}
	if result.Config.LLM.Agent.Models[0] != "anthropic/claude-sonnet-4-20250514" {
		t.Fatalf("expected existing primary model to be preserved, got %#v", result.Config.LLM.Agent.Models)
	}
	if result.Config.LLM.Agent.Models[1] != "local/managed" {
		t.Fatalf("expected managed local model ref to replace existing alias entry, got %#v", result.Config.LLM.Agent.Models)
	}
}

func writeTestConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

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
