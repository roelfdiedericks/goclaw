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
	"github.com/roelfdiedericks/goclaw/internal/session"
	setupcore "github.com/roelfdiedericks/goclaw/internal/setup"
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
	localLLMRecommendationsCache = struct {
		Value      setupcore.LocalModelRecommendations
		FetchedAt  time.Time
		ProfileKey string
	}{}
	t.Cleanup(func() {
		localLLMLatestRuntimeVersionFunc = origLatestFunc
		localLLMLatestRuntimeCache = struct {
			Value     string
			Err       string
			FetchedAt time.Time
		}{}
		localLLMRecommendationsCache = struct {
			Value      setupcore.LocalModelRecommendations
			FetchedAt  time.Time
			ProfileKey string
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
	localLLMRecommendationsCache = struct {
		Value      setupcore.LocalModelRecommendations
		FetchedAt  time.Time
		ProfileKey string
	}{}
	t.Cleanup(func() {
		localLLMLatestRuntimeVersionFunc = origLatestFunc
		localLLMLatestRuntimeCache = struct {
			Value     string
			Err       string
			FetchedAt time.Time
		}{}
		localLLMRecommendationsCache = struct {
			Value      setupcore.LocalModelRecommendations
			FetchedAt  time.Time
			ProfileKey string
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
		"action":  "ensure_latest_runtime",
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
			ConfigUpdated  bool   `json:"configUpdated"`
			ProviderAlias  string `json:"providerAlias"`
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

func TestNormalizeSessionSectionPayloadAppliesLCMPreset(t *testing.T) {
	payload := map[string]interface{}{
		"store":       "sqlite",
		"storePath":   "/tmp/sessions.db",
		"inherit":     false,
		"inheritPath": "/tmp/openclaw",
		"inheritFrom": "agent:main:main",
		"summarization": map[string]interface{}{
			"fallbackModel":        "claude-3-haiku-20240307",
			"failureThreshold":     3,
			"resetMinutes":         30,
			"retryIntervalSeconds": 60,
			"checkpoint": map[string]interface{}{
				"enabled":         true,
				"thresholds":      []int{25, 50, 75},
				"turnThreshold":   15,
				"minTokensForGen": 10000,
			},
			"compaction": map[string]interface{}{
				"reserveTokens":         4000,
				"maxMessages":           100,
				"preferCheckpoint":      true,
				"keepPercent":           50,
				"minMessages":           20,
				"freshTailCount":        0,
				"freshTailMaxTokens":    0,
				"leafMinFanout":         4,
				"condensedMinFanout":    4,
				"incrementalMaxDepth":   2,
				"leafTargetTokens":      800,
				"condensedTargetTokens": 1200,
				"lcm": map[string]interface{}{
					"enabled":                  true,
					"preset":                   session.LCMPresetAggressive,
					"summaryInjectionMode":     session.LCMSummaryInjectionModeFrontier,
					"maxInjectedSummaryTokens": 2500,
					"summaryMaxOverageFactor":  2,
				},
			},
		},
		"memoryFlush": map[string]interface{}{
			"enabled": true,
		},
	}

	runtimePayload, err := normalizeSessionSectionPayload(payload)
	if err != nil {
		t.Fatalf("normalizeSessionSectionPayload: %v", err)
	}

	summarization, ok := runtimePayload["summarization"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summarization map in runtime payload, got %#v", runtimePayload["summarization"])
	}
	compaction, ok := summarization["compaction"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected compaction map in runtime payload, got %#v", summarization["compaction"])
	}
	// Aggressive preset is authoritative: all outer fields reflect aggressive values,
	// not whatever the payload carried.
	if got := compaction["freshTailCount"]; got != float64(5) {
		t.Fatalf("expected aggressive freshTailCount 5, got %#v", got)
	}
	if got := compaction["freshTailMaxTokens"]; got != float64(2000) {
		t.Fatalf("expected aggressive freshTailMaxTokens 2000, got %#v", got)
	}
	if got := compaction["leafMinFanout"]; got != float64(3) {
		t.Fatalf("expected aggressive leafMinFanout 3, got %#v", got)
	}
	if got := compaction["condensedMinFanout"]; got != float64(3) {
		t.Fatalf("expected aggressive condensedMinFanout 3, got %#v", got)
	}
	if got := compaction["incrementalMaxDepth"]; got != float64(3) {
		t.Fatalf("expected aggressive incrementalMaxDepth 3, got %#v", got)
	}
	if got := compaction["leafTargetTokens"]; got != float64(500) {
		t.Fatalf("expected aggressive leafTargetTokens 500, got %#v", got)
	}
	if got := compaction["condensedTargetTokens"]; got != float64(800) {
		t.Fatalf("expected aggressive condensedTargetTokens 800, got %#v", got)
	}
	lcm, ok := compaction["lcm"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected lcm map in runtime payload, got %#v", compaction["lcm"])
	}
	if got := lcm["summaryInjectionMode"]; got != session.LCMSummaryInjectionModeFrontier {
		t.Fatalf("expected aggressive preset to use frontier mode, got %#v", got)
	}
	if got := lcm["maxInjectedSummaryTokens"]; got != float64(2500) {
		t.Fatalf("expected aggressive preset budget 2500, got %#v", got)
	}
	if got := lcm["summaryMaxOverageFactor"]; got != float64(2) {
		t.Fatalf("expected aggressive overage factor 2, got %#v", got)
	}
	if got := lcm["preset"]; got != session.LCMPresetAggressive {
		t.Fatalf("expected aggressive preset to persist in runtime payload, got %#v", got)
	}
}

func TestSetConfigPathSessionPayloadPreservesCompactionFields(t *testing.T) {
	cfg := &config.Config{}
	payload := map[string]interface{}{
		"store":       "sqlite",
		"storePath":   "/tmp/sessions.db",
		"inherit":     false,
		"inheritPath": "/tmp/openclaw",
		"inheritFrom": "agent:main:main",
		"summarization": map[string]interface{}{
			"fallbackModel":        "claude-3-haiku-20240307",
			"failureThreshold":     3,
			"resetMinutes":         30,
			"retryIntervalSeconds": 60,
			"checkpoint": map[string]interface{}{
				"enabled":         true,
				"thresholds":      []int{25, 50, 75},
				"turnThreshold":   15,
				"minTokensForGen": 10000,
			},
			"compaction": map[string]interface{}{
				"reserveTokens":         4000,
				"maxMessages":           100,
				"preferCheckpoint":      true,
				"keepPercent":           50,
				"minMessages":           20,
				"freshTailCount":        0,
				"freshTailMaxTokens":    0,
				"leafMinFanout":         4,
				"condensedMinFanout":    4,
				"incrementalMaxDepth":   2,
				"leafTargetTokens":      800,
				"condensedTargetTokens": 1200,
				"lcm": map[string]interface{}{
					"enabled":                  true,
					"preset":                   session.LCMPresetBalanced,
					"summaryInjectionMode":     session.LCMSummaryInjectionModeFrontier,
					"maxInjectedSummaryTokens": 4000,
					"summaryMaxOverageFactor":  3,
				},
			},
		},
		"memoryFlush": map[string]interface{}{
			"enabled": true,
		},
	}

	if err := setConfigPath(cfg, "/session", payload); err != nil {
		t.Fatalf("setConfigPath returned error: %v", err)
	}

	compaction := cfg.Session.Summarization.Compaction
	if compaction.LeafMinFanout != 4 {
		t.Fatalf("expected leafMinFanout 4 after setConfigPath, got %d", compaction.LeafMinFanout)
	}
	if compaction.CondensedMinFanout != 4 {
		t.Fatalf("expected condensedMinFanout 4 after setConfigPath, got %d", compaction.CondensedMinFanout)
	}
	if compaction.IncrementalMaxDepth != 2 {
		t.Fatalf("expected incrementalMaxDepth 2 after setConfigPath, got %d", compaction.IncrementalMaxDepth)
	}
	if compaction.LeafTargetTokens != 800 {
		t.Fatalf("expected leafTargetTokens 800 after setConfigPath, got %d", compaction.LeafTargetTokens)
	}
	if compaction.CondensedTargetTokens != 1200 {
		t.Fatalf("expected condensedTargetTokens 1200 after setConfigPath, got %d", compaction.CondensedTargetTokens)
	}
	if !compaction.LCM.Enabled {
		t.Fatalf("expected lcm enabled after setConfigPath")
	}
	if compaction.LCM.Preset != session.LCMPresetBalanced {
		t.Fatalf("expected balanced preset after setConfigPath, got %q", compaction.LCM.Preset)
	}
}

func TestSaveSessionSectionPersistsNormalizedLCMConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "goclaw.json")
	writeTestConfig(t, configPath, &config.Config{
		Session: session.SessionConfig{
			Store:       "sqlite",
			StorePath:   "/tmp/sessions.db",
			InheritPath: "/tmp/openclaw",
			InheritFrom: "agent:main:main",
			Summarization: session.SummarizationConfig{
				Checkpoint: session.CheckpointSubConfig{
					Enabled:         true,
					Thresholds:      []int{25, 50, 75},
					TurnThreshold:   15,
					MinTokensForGen: 10000,
				},
				Compaction: session.CompactionSubConfig{
					ReserveTokens:         4000,
					MaxMessages:           100,
					PreferCheckpoint:      true,
					KeepPercent:           50,
					MinMessages:           20,
					FreshTailCount:        0,
					FreshTailMaxTokens:    0,
					LeafMinFanout:         4,
					CondensedMinFanout:    4,
					IncrementalMaxDepth:   2,
					LeafTargetTokens:      800,
					CondensedTargetTokens: 1200,
				},
			},
		},
	})

	api := NewAPI(configPath, configapply.CallerWebStandalone, EditorSectionsForMode(false))
	payload := map[string]interface{}{
		"store":       "sqlite",
		"storePath":   "/tmp/sessions.db",
		"inherit":     false,
		"inheritPath": "/tmp/openclaw",
		"inheritFrom": "agent:main:main",
		"summarization": map[string]interface{}{
			"fallbackModel":        "claude-3-haiku-20240307",
			"failureThreshold":     3,
			"resetMinutes":         30,
			"retryIntervalSeconds": 60,
			"checkpoint": map[string]interface{}{
				"enabled":         true,
				"thresholds":      []int{25, 50, 75},
				"turnThreshold":   15,
				"minTokensForGen": 10000,
			},
			"compaction": map[string]interface{}{
				"reserveTokens":         4000,
				"maxMessages":           100,
				"preferCheckpoint":      true,
				"keepPercent":           50,
				"minMessages":           20,
				"freshTailCount":        0,
				"freshTailMaxTokens":    0,
				"leafMinFanout":         4,
				"condensedMinFanout":    4,
				"incrementalMaxDepth":   2,
				"leafTargetTokens":      800,
				"condensedTargetTokens": 1200,
				"lcm": map[string]interface{}{
					"enabled":                  true,
					"preset":                   session.LCMPresetBalanced,
					"summaryInjectionMode":     session.LCMSummaryInjectionModeFrontier,
					"maxInjectedSummaryTokens": 2500,
					"summaryMaxOverageFactor":  2,
				},
			},
		},
		"memoryFlush": map[string]interface{}{
			"enabled": true,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/setup/api/section/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	section := FindSection(EditorSectionsForMode(false), "session")
	if section == nil {
		t.Fatal("session section not found")
	}
	api.saveSectionConfig(rec, req, section)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rawData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(rawData, &raw); err != nil {
		t.Fatalf("unmarshal raw config: %v", err)
	}
	sessionMap, ok := raw["session"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected session object in raw config, got %#v", raw["session"])
	}
	summarization, ok := sessionMap["summarization"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summarization object in raw config, got %#v", sessionMap["summarization"])
	}
	compaction, ok := summarization["compaction"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected compaction object in raw config, got %#v", summarization["compaction"])
	}
	lcm, ok := compaction["lcm"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected raw config to persist lcm object, got %#v", compaction["lcm"])
	}
	if got := lcm["preset"]; got != session.LCMPresetBalanced {
		t.Fatalf("expected balanced preset in raw config, got %#v", got)
	}
	if got := lcm["maxInjectedSummaryTokens"]; got != float64(4000) {
		t.Fatalf("expected balanced budget 4000 in raw config, got %#v", got)
	}
	if got := lcm["summaryMaxOverageFactor"]; got != float64(3) {
		t.Fatalf("expected balanced overage factor 3 in raw config, got %#v", got)
	}
}
