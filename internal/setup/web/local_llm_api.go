package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/jobs"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	setupcore "github.com/roelfdiedericks/goclaw/internal/setup"
)

var (
	localLLMLatestRuntimeVersionFunc = localllm.LatestRuntimeVersion
	localLLMLatestRuntimeCacheMu     sync.Mutex
	localLLMLatestRuntimeCache       = struct {
		Value     string
		Err       string
		FetchedAt time.Time
	}{}
)

type localLLMActionRequest struct {
	Action         string `json:"action"`
	ModelID        string `json:"modelID,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	JobID          string `json:"jobID,omitempty"`
}

func (a *API) HandleLocalLLM(w http.ResponseWriter, r *http.Request) {
	if !a.ensureStrictContracts(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.handleGetLocalLLM(w)
	case http.MethodPost:
		a.handleLocalLLMAction(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Success: false,
			Message: "Method not allowed",
		})
	}
}

func (a *API) handleGetLocalLLM(w http.ResponseWriter) {
	result, err := a.loadConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	status := localllm.GetManager().Status()
	recs := setupcore.BuildLocalModelRecommendations()
	if isZeroLocalLLMProfile(status.SystemProfile) {
		status.SystemProfile = recs.Profile
	}
	managedProviders, defaultSpec, defaultAlias := managedLocalLLMProviders(result.Config)
	models := buildLocalLLMModels(status, recs)
	latestVersion, latestErr := cachedLocalLLMLatestRuntimeVersion()
	selectedModelID := preferredLocalLLMModelID(status, defaultSpec)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]any{
			"status": localLLMStatusPayload(status),
			"jobs":   jobs.GetManager().List("local_llm"),
			"models": models,
			"recommendations": map[string]any{
				"summary": recs.Summary,
				"profile": localLLMProfilePayload(recs.Profile),
			},
			"managedProviders":   managedProviders,
			"defaultSpec":        managedSpecPayload(defaultSpec),
			"defaultProvider":    defaultAlias,
			"hasManagedProvider": len(managedProviders) > 0,
			"wiring":             localLLMWiringPayload(result.Config, defaultAlias, selectedModelID),
			"runtimeVersion": map[string]any{
				"installed":            status.RuntimeVersion,
				"configured":           defaultSpec.RuntimeVersion,
				"latest":               latestVersion,
				"usingLatestByDefault": strings.TrimSpace(defaultSpec.RuntimeVersion) == "",
				"effective":            effectiveLocalLLMRuntimeVersion(status.RuntimeVersion, defaultSpec.RuntimeVersion, latestVersion),
				"latestLookupError":    latestErr,
			},
		},
	})
}

func (a *API) handleLocalLLMAction(w http.ResponseWriter, r *http.Request) {
	var req localLLMActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Invalid JSON payload",
		})
		return
	}

	result, err := a.loadConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Success: false,
			Message: "Failed to load configuration",
		})
		return
	}

	spec := localLLMActionSpec(result.Config, localllm.GetManager().Status(), req)

	var cmdResult bus.CommandResult
	switch strings.TrimSpace(req.Action) {
	case "ensure_runtime":
		cmdResult = bus.SendCommandWithSource("local_llm", "ensure_runtime", spec, "web", "")
	case "ensure_latest_runtime":
		if strings.TrimSpace(spec.RuntimeVersion) == "" {
			latestVersion, err := cachedLocalLLMLatestRuntimeVersion()
			if err != "" {
				writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err})
				return
			}
			spec.RuntimeVersion = latestVersion
		}
		cmdResult = bus.SendCommandWithSource("local_llm", "ensure_runtime", spec, "web", "")
	case "download_model":
		if strings.TrimSpace(spec.ModelID) == "" {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "modelID is required"})
			return
		}
		cmdResult = bus.SendCommandWithSource("local_llm", "download_model", spec, "web", "")
	case "start":
		cmdResult = bus.SendCommandWithSource("local_llm", "start", spec, "web", "")
	case "stop":
		cmdResult = bus.SendCommandWithSource("local_llm", "stop", nil, "web", "")
	case "select_model":
		if strings.TrimSpace(spec.ModelID) == "" {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "modelID is required"})
			return
		}
		cmdResult = bus.SendCommandWithSource("local_llm", "select_model", localllm.ManagedSpec{ModelID: spec.ModelID}, "web", "")
	case "cancel_job":
		if strings.TrimSpace(req.JobID) == "" {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "jobID is required"})
			return
		}
		cmdResult = bus.SendCommandWithSource("jobs", "cancel", jobs.CancelRequest{JobID: strings.TrimSpace(req.JobID)}, "web", "")
	case "configure_managed_provider":
		data, message, err := a.configureManagedLocalProvider(result, spec, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: message, Data: data})
		return
	case "add_managed_provider_to_agent_chain":
		data, message, err := a.configureManagedLocalProvider(result, spec, true)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: message, Data: data})
		return
	case "use_for_agent":
		data, message, err := a.configureManagedLocalProvider(result, spec, true)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: message, Data: data})
		return
	default:
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Unknown local LLM action",
		})
		return
	}

	if cmdResult.Error != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: cmdResult.Message,
		})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: cmdResult.Message,
		Data:    cmdResult.Data,
	})
}

func managedLocalLLMSpecFromConfig(cfg *config.Config) (localllm.ManagedSpec, bool) {
	providers, defaultSpec, _ := managedLocalLLMProviders(cfg)
	if len(providers) == 0 {
		return localllm.ManagedSpec{}, false
	}
	return defaultSpec, true
}

func managedLocalLLMProviders(cfg *config.Config) ([]map[string]any, localllm.ManagedSpec, string) {
	defaultSpec := localllm.ManagedSpec{}
	defaultAlias := ""
	if cfg == nil {
		return nil, defaultSpec, defaultAlias
	}

	agentAlias := ""
	if len(cfg.LLM.Agent.Models) > 0 {
		parts := strings.SplitN(cfg.LLM.Agent.Models[0], "/", 2)
		if len(parts) == 2 {
			agentAlias = strings.TrimSpace(parts[0])
		}
	}

	aliases := make([]string, 0, len(cfg.LLM.Providers))
	for alias := range cfg.LLM.Providers {
		aliases = append(aliases, alias)
	}
	sortStrings(aliases)

	providers := make([]map[string]any, 0, len(aliases))
	for _, alias := range aliases {
		provider := cfg.LLM.Providers[alias]
		if provider.Driver != "llamacpp" || provider.LlamaCpp == nil || provider.LlamaCpp.Mode != llm.LlamaCppModeManaged {
			continue
		}
		spec := localllm.ManagedSpec{
			RuntimeVersion: provider.LlamaCpp.RuntimeVersion,
			ModelID:        provider.LlamaCpp.ManagedModelID,
			Host:           provider.LlamaCpp.Host,
			Port:           provider.LlamaCpp.Port,
			ModelAlias:     provider.LlamaCpp.ModelAlias,
		}
		if defaultAlias == "" || alias == agentAlias {
			defaultAlias = alias
			defaultSpec = spec
		}
		providers = append(providers, map[string]any{
			"alias":          alias,
			"isAgentDefault": alias == agentAlias,
			"driver":         provider.Driver,
			"managedModelID": provider.LlamaCpp.ManagedModelID,
			"runtimeVersion": provider.LlamaCpp.RuntimeVersion,
			"host":           provider.LlamaCpp.Host,
			"port":           provider.LlamaCpp.Port,
			"modelAlias":     provider.LlamaCpp.ModelAlias,
		})
	}

	return providers, defaultSpec, defaultAlias
}

func localLLMWiringPayload(cfg *config.Config, defaultAlias, selectedModelID string) map[string]any {
	recommendedAlias := recommendedManagedLocalLLMAlias(cfg)
	agentRef := ""
	providerInAgentChain := false
	if strings.TrimSpace(defaultAlias) != "" && cfg != nil {
		agentRef, providerInAgentChain = managedProviderRefInChain(cfg.LLM.Agent.Models, defaultAlias)
	}
	return map[string]any{
		"selectedModelID":      strings.TrimSpace(selectedModelID),
		"defaultProvider":      strings.TrimSpace(defaultAlias),
		"recommendedAlias":     recommendedAlias,
		"providerInAgentChain": providerInAgentChain,
		"agentModelRef":        agentRef,
	}
}

func preferredLocalLLMModelID(status localllm.ManagerStatus, defaultSpec localllm.ManagedSpec) string {
	if modelID := strings.TrimSpace(status.ModelID); modelID != "" {
		return modelID
	}
	if modelID := strings.TrimSpace(defaultSpec.ModelID); modelID != "" {
		return modelID
	}
	return defaultManagedLocalModelID()
}

func localLLMActionSpec(cfg *config.Config, status localllm.ManagerStatus, req localLLMActionRequest) localllm.ManagedSpec {
	spec, _ := managedLocalLLMSpecFromConfig(cfg)
	if spec.Host == "" {
		spec.Host = "127.0.0.1"
	}
	if spec.Port == 0 {
		spec.Port = 8080
	}
	if strings.TrimSpace(spec.ModelID) == "" {
		spec.ModelID = preferredLocalLLMModelID(status, spec)
	}
	if strings.TrimSpace(req.ModelID) != "" {
		spec.ModelID = strings.TrimSpace(req.ModelID)
	}
	if strings.TrimSpace(req.RuntimeVersion) != "" {
		spec.RuntimeVersion = strings.TrimSpace(req.RuntimeVersion)
	}
	return spec
}

func defaultManagedLocalModelID() string {
	catalog := localllm.ManagedModelCatalog()
	if len(catalog) == 0 {
		return ""
	}
	return strings.TrimSpace(catalog[0].ID)
}

func managedProviderRefInChain(models []string, alias string) (string, bool) {
	trimmedAlias := strings.TrimSpace(alias)
	for _, ref := range models {
		parts := strings.SplitN(strings.TrimSpace(ref), "/", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == trimmedAlias {
			return strings.TrimSpace(ref), true
		}
	}
	return "", false
}

func recommendedManagedLocalLLMAlias(cfg *config.Config) string {
	const base = "local-llm"
	if cfg == nil || cfg.LLM.Providers == nil {
		return base
	}
	if _, exists := cfg.LLM.Providers[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		alias := base + "-" + strconv.Itoa(i)
		if _, exists := cfg.LLM.Providers[alias]; !exists {
			return alias
		}
	}
}

func upsertManagedLocalLLMProvider(cfg *config.Config, spec localllm.ManagedSpec) (string, llm.LLMProviderConfig) {
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = map[string]llm.LLMProviderConfig{}
	}

	_, _, alias := managedLocalLLMProviders(cfg)
	if strings.TrimSpace(alias) == "" {
		alias = recommendedManagedLocalLLMAlias(cfg)
	}

	provider := cfg.LLM.Providers[alias]
	provider.Driver = "llamacpp"
	if strings.TrimSpace(provider.Subtype) == "" {
		provider.Subtype = "llamacpp-managed"
	}
	provider.APIKey = ""
	provider.BaseURL = ""
	if provider.LlamaCpp == nil {
		provider.LlamaCpp = &llm.LlamaCppProviderConfig{}
	}
	provider.LlamaCpp.Mode = llm.LlamaCppModeManaged
	if strings.TrimSpace(provider.LlamaCpp.Host) == "" {
		provider.LlamaCpp.Host = "127.0.0.1"
	}
	if provider.LlamaCpp.Port == 0 {
		provider.LlamaCpp.Port = 8080
	}
	if strings.TrimSpace(spec.Host) != "" {
		provider.LlamaCpp.Host = strings.TrimSpace(spec.Host)
	}
	if spec.Port != 0 {
		provider.LlamaCpp.Port = spec.Port
	}
	if strings.TrimSpace(provider.LlamaCpp.ManagedModelID) == "" {
		provider.LlamaCpp.ManagedModelID = defaultManagedLocalModelID()
	}
	if strings.TrimSpace(spec.ModelID) != "" {
		provider.LlamaCpp.ManagedModelID = strings.TrimSpace(spec.ModelID)
	}
	if strings.TrimSpace(spec.RuntimeVersion) != "" {
		provider.LlamaCpp.RuntimeVersion = strings.TrimSpace(spec.RuntimeVersion)
	}
	if strings.TrimSpace(spec.ModelAlias) != "" {
		provider.LlamaCpp.ModelAlias = strings.TrimSpace(spec.ModelAlias)
	}

	cfg.LLM.Providers[alias] = provider
	return alias, provider
}

func ensureManagedProviderInAgentChain(cfg *config.Config, alias string) string {
	modelRef := strings.TrimSpace(alias) + "/managed"
	models := cfg.LLM.Agent.Models
	for i, ref := range models {
		parts := strings.SplitN(strings.TrimSpace(ref), "/", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) != strings.TrimSpace(alias) {
			continue
		}
		cfg.LLM.Agent.Models[i] = modelRef
		return modelRef
	}
	cfg.LLM.Agent.Models = append(cfg.LLM.Agent.Models, modelRef)
	return modelRef
}

func (a *API) saveConfigResult(result *config.LoadResult) error {
	savePath := a.resolveSavePath(result)
	if err := config.BackupAndWriteJSON(savePath, result.Config, config.DefaultBackupCount); err != nil {
		L_error("setup: failed to save local llm config", "error", err, "path", savePath)
		return err
	}
	return nil
}

func (a *API) configureManagedLocalProvider(result *config.LoadResult, spec localllm.ManagedSpec, addToAgent bool) (map[string]any, string, error) {
	alias, provider := upsertManagedLocalLLMProvider(result.Config, spec)
	agentModelRef := ""
	if addToAgent {
		agentModelRef = ensureManagedProviderInAgentChain(result.Config, alias)
	}
	if err := a.saveConfigResult(result); err != nil {
		return nil, "", err
	}

	L_info("setup: local llm provider configured",
		"action", map[bool]string{true: "use_for_agent", false: "configure_managed_provider"}[addToAgent],
		"alias", alias,
		"modelID", provider.LlamaCpp.ManagedModelID,
		"agentModelRef", agentModelRef,
	)

	message := "Managed provider configured as `" + alias + "`."
	if addToAgent {
		message = "Managed provider `" + alias + "` is now wired into the agent chain."
	}
	return map[string]any{
		"configUpdated":  true,
		"providerAlias":  alias,
		"providerConfig": provider,
		"agentModelRef":  agentModelRef,
	}, message, nil
}

func buildLocalLLMModels(status localllm.ManagerStatus, recs setupcore.LocalModelRecommendations) []map[string]any {
	items := make([]map[string]any, 0, len(recs.Options))
	for _, option := range recs.Options {
		modelPath, _ := localllm.ManagedModelPath(option.Spec)
		mmprojPath, _ := localllm.ManagedModelMMProjPath(option.Spec)
		installed := pathExists(modelPath)
		if strings.TrimSpace(option.Spec.MMProjFilename) != "" {
			installed = installed && pathExists(mmprojPath)
		}
		items = append(items, map[string]any{
			"id":                     option.Spec.ID,
			"label":                  option.Spec.Label,
			"family":                 option.Spec.Family,
			"hfRepo":                 option.Spec.HFRepo,
			"preferredQuant":         option.Spec.PreferredQuant,
			"preferredFilename":      option.Spec.PreferredFilename,
			"mmprojFilename":         option.Spec.MMProjFilename,
			"fallbackContextTokens":  option.Spec.FallbackContextTokens,
			"approxDownloadBytes":    option.Spec.ApproxDownloadBytes,
			"recommendedMinRAMBytes": option.Spec.RecommendedMinRAMBytes,
			"installed":              installed,
			"selected":               status.ModelID == option.Spec.ID,
			"recommended":            option.Recommended,
			"defaultSelected":        option.DefaultSelected,
			"viable":                 option.Viable,
			"reason":                 option.Reason,
		})
	}
	return items
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func sortStrings(items []string) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j] < items[i] {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func localLLMStatusPayload(status localllm.ManagerStatus) map[string]any {
	return map[string]any{
		"configured":           status.Configured,
		"runtimeVersion":       status.RuntimeVersion,
		"modelID":              status.ModelID,
		"modelPath":            status.ModelPath,
		"mmprojPath":           status.MMProjPath,
		"runtimePath":          status.RuntimePath,
		"backend":              status.Backend,
		"effectiveContextSize": status.EffectiveContextSize,
		"systemProfile":        localLLMProfilePayload(status.SystemProfile),
		"lastError":            status.LastError,
		"server": map[string]any{
			"state":        status.Server.State,
			"endpoint":     status.Server.Endpoint,
			"healthy":      status.Server.Healthy,
			"pid":          status.Server.PID,
			"restartCount": status.Server.RestartCount,
			"lastError":    status.Server.LastError,
			"recentLogs":   status.Server.RecentLogs,
		},
	}
}

func localLLMProfilePayload(profile localllm.SystemProfile) map[string]any {
	return map[string]any{
		"osFlavor":          profile.OSFlavor,
		"arch":              profile.Arch,
		"totalRAMBytes":     profile.TotalRAMBytes,
		"availableBackends": profile.AvailableBackends,
		"recommended":       profile.Recommended,
	}
}

func managedSpecPayload(spec localllm.ManagedSpec) map[string]any {
	return map[string]any{
		"runtimeVersion": spec.RuntimeVersion,
		"modelID":        spec.ModelID,
		"host":           spec.Host,
		"port":           spec.Port,
		"contextSize":    spec.ContextSize,
		"modelAlias":     spec.ModelAlias,
	}
}

func isZeroLocalLLMProfile(profile localllm.SystemProfile) bool {
	return profile.OSFlavor == "" && profile.Arch == "" && profile.TotalRAMBytes == 0 && len(profile.AvailableBackends) == 0 && profile.Recommended == ""
}

func cachedLocalLLMLatestRuntimeVersion() (string, string) {
	localLLMLatestRuntimeCacheMu.Lock()
	defer localLLMLatestRuntimeCacheMu.Unlock()

	now := time.Now()
	if now.Sub(localLLMLatestRuntimeCache.FetchedAt) < 5*time.Minute {
		return localLLMLatestRuntimeCache.Value, localLLMLatestRuntimeCache.Err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := localLLMLatestRuntimeVersionFunc(ctx)
	localLLMLatestRuntimeCache.FetchedAt = now
	localLLMLatestRuntimeCache.Value = strings.TrimSpace(value)
	if err != nil {
		localLLMLatestRuntimeCache.Err = err.Error()
	} else {
		localLLMLatestRuntimeCache.Err = ""
	}
	return localLLMLatestRuntimeCache.Value, localLLMLatestRuntimeCache.Err
}

func effectiveLocalLLMRuntimeVersion(installed, configured, latest string) string {
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured)
	}
	if strings.TrimSpace(latest) != "" {
		return strings.TrimSpace(latest)
	}
	return strings.TrimSpace(installed)
}
