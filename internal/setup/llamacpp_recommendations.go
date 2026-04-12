package setup

import (
	"fmt"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
)

var detectLocalModelProfile = localllm.DetectSystemProfile

const (
	LLMChoiceLocalGemma         = "local_gemma"
	LLMChoiceCloudProvider      = "cloud"
	LLMChoiceExistingLlamaCpp   = "existing_llamacpp"
	LLMProviderManagedLlamaCpp  = "llamacpp-managed"
	LLMProviderEndpointLlamaCpp = "llamacpp-endpoint"
)

type LocalModelOption struct {
	Spec            localllm.ManagedModelSpec
	Recommended     bool
	DefaultSelected bool
	Viable          bool
	Reason          string
}

type LocalModelRecommendations struct {
	Profile        localllm.SystemProfile
	Options        []LocalModelOption
	RecommendedID  string
	DefaultModelID string
	Summary        string
}

func BuildLocalModelRecommendations() LocalModelRecommendations {
	return BuildLocalModelRecommendationsForProfile(detectLocalModelProfile())
}

func BuildLocalModelRecommendationsForProfile(profile localllm.SystemProfile) LocalModelRecommendations {
	catalog := localllm.ManagedModelCatalog()
	options := make([]LocalModelOption, 0, len(catalog))

	defaultID := ""
	for i, spec := range catalog {
		option := LocalModelOption{
			Spec:            spec,
			Viable:          profile.TotalRAMBytes == 0 || profile.TotalRAMBytes >= spec.RecommendedMinRAMBytes,
			DefaultSelected: i == 0,
		}
		if option.DefaultSelected {
			defaultID = spec.ID
		}
		option.Reason = buildLocalModelReason(profile, spec, option.Viable)
		options = append(options, option)
	}

	recommendedID := defaultID
	if hasAccelerator(profile) {
		for i := len(options) - 1; i >= 0; i-- {
			if options[i].Viable {
				recommendedID = options[i].Spec.ID
				break
			}
		}
	}
	for i := range options {
		options[i].Recommended = options[i].Spec.ID == recommendedID
	}

	return LocalModelRecommendations{
		Profile:        profile,
		Options:        options,
		RecommendedID:  recommendedID,
		DefaultModelID: defaultID,
		Summary:        summarizeLocalProfile(profile, defaultID, recommendedID),
	}
}

func ConfigureWizardForManagedLlamaCpp(data *WizardData, modelID string) {
	recs := BuildLocalModelRecommendations()
	if strings.TrimSpace(modelID) == "" {
		modelID = recs.DefaultModelID
	}
	preset := LlamaCppManagedPreset()

	data.LLMOnboardingChoice = LLMChoiceLocalGemma
	data.LLMProviderID = preset.Key
	data.LLMProviderName = preset.Name
	data.LLMDriver = preset.Driver
	data.LLMBaseURL = preset.BaseURL
	data.LLMAPIKey = ""
	data.LLMModel = "managed"
	data.LLMManagedModelID = modelID
	data.MarkDirty(
		"LLMOnboardingChoice",
		"LLMProviderID",
		"LLMDriver",
		"LLMBaseURL",
		"LLMAPIKey",
		"LLMModel",
		"LLMManagedModelID",
	)
}

func ConfigureWizardForLlamaCppEndpoint(data *WizardData) {
	data.LLMOnboardingChoice = LLMChoiceExistingLlamaCpp
	data.LLMProviderID = LLMProviderEndpointLlamaCpp
	data.LLMProviderName = "Existing llama.cpp server"
	data.LLMDriver = "llamacpp"
	if strings.TrimSpace(data.LLMBaseURL) == "" {
		data.LLMBaseURL = "http://127.0.0.1:8080"
	}
	if strings.TrimSpace(data.LLMModel) == "" || data.LLMModel == "managed" {
		data.LLMModel = ""
	}
	data.MarkDirty("LLMOnboardingChoice", "LLMProviderID", "LLMDriver", "LLMBaseURL", "LLMModel")
}

func ConfigureWizardForCloudProvider(data *WizardData, preset *ProviderPreset) {
	data.LLMOnboardingChoice = LLMChoiceCloudProvider
	if preset == nil {
		data.MarkDirty("LLMOnboardingChoice")
		return
	}
	data.LLMProviderID = preset.Key
	data.LLMProviderName = preset.Name
	data.LLMDriver = preset.Driver
	data.LLMBaseURL = preset.BaseURL
	data.LLMAPIKey = ""
	data.LLMManagedModelID = ""
	data.LLMModel = preset.DefaultModel
	data.MarkDirty(
		"LLMOnboardingChoice",
		"LLMProviderID",
		"LLMDriver",
		"LLMBaseURL",
		"LLMAPIKey",
		"LLMModel",
		"LLMManagedModelID",
	)
}

func ResolveWizardManagedModel(data *WizardData) (localllm.ManagedModelSpec, error) {
	modelID := strings.TrimSpace(data.LLMManagedModelID)
	if modelID == "" {
		modelID = BuildLocalModelRecommendations().DefaultModelID
	}
	return localllm.ManagedModelByID(modelID)
}

func WizardLLMModelDisplay(data *WizardData) string {
	if data == nil {
		return ""
	}
	if data.LLMOnboardingChoice == LLMChoiceLocalGemma || data.LLMProviderID == LLMProviderManagedLlamaCpp {
		spec, err := ResolveWizardManagedModel(data)
		if err == nil {
			return spec.Label
		}
	}
	return strings.TrimSpace(data.LLMModel)
}

func buildLocalModelReason(profile localllm.SystemProfile, spec localllm.ManagedModelSpec, viable bool) string {
	ramGB := bytesToApproxGB(profile.TotalRAMBytes)
	minGB := bytesToApproxGB(spec.RecommendedMinRAMBytes)
	accel := "CPU-focused default"
	if hasAccelerator(profile) {
		accel = fmt.Sprintf("accelerator detected (%s)", strings.ToUpper(string(profile.Recommended)))
	}
	if viable {
		return fmt.Sprintf("%s, approx %d GB download, recommend >= %d GB RAM", accel, bytesToApproxGB(spec.ApproxDownloadBytes), minGB)
	}
	if ramGB > 0 {
		return fmt.Sprintf("heavy for this machine (%d GB RAM seen, recommend >= %d GB)", ramGB, minGB)
	}
	return fmt.Sprintf("heavy option, recommend >= %d GB RAM", minGB)
}

func summarizeLocalProfile(profile localllm.SystemProfile, defaultID, recommendedID string) string {
	parts := []string{
		fmt.Sprintf("%s/%s", strings.ToLower(string(profile.OSFlavor)), string(profile.Arch)),
	}
	if profile.TotalRAMBytes > 0 {
		parts = append(parts, fmt.Sprintf("%d GB RAM", bytesToApproxGB(profile.TotalRAMBytes)))
	}
	if len(profile.AvailableBackends) > 0 {
		backends := make([]string, 0, len(profile.AvailableBackends))
		for _, backend := range profile.AvailableBackends {
			backends = append(backends, string(backend))
		}
		parts = append(parts, "backends: "+strings.Join(backends, ", "))
	}
	parts = append(parts, "default: "+defaultID)
	if recommendedID != "" && recommendedID != defaultID {
		parts = append(parts, "recommended: "+recommendedID)
	}
	return strings.Join(parts, " | ")
}

func bytesToApproxGB(n uint64) int {
	if n == 0 {
		return 0
	}
	return int((n + (1024*1024*1024)/2) / (1024 * 1024 * 1024))
}

func hasAccelerator(profile localllm.SystemProfile) bool {
	for _, backend := range profile.AvailableBackends {
		if backend != localllm.BackendCPU {
			return true
		}
	}
	return false
}

func cloudProviderPresets() []ProviderPreset {
	presets := BuildPresets()
	out := make([]ProviderPreset, 0, len(presets))
	for _, preset := range presets {
		if preset.Driver == "llamacpp" {
			continue
		}
		out = append(out, preset)
	}
	return out
}

func isLlamaCppManagedConfig(cfg llm.LLMProviderConfig) bool {
	return cfg.Driver == "llamacpp" && cfg.LlamaCpp != nil && cfg.LlamaCpp.Mode == llm.LlamaCppModeManaged
}

func isLlamaCppEndpointConfig(cfg llm.LLMProviderConfig) bool {
	return cfg.Driver == "llamacpp" && (cfg.LlamaCpp == nil || cfg.LlamaCpp.Mode == "" || cfg.LlamaCpp.Mode == llm.LlamaCppModeEndpoint)
}
