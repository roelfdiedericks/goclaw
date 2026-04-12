package setup

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/jobs"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
)

type localLLMTUIViewState struct {
	Status           localllm.ManagerStatus
	Jobs             []jobs.Status
	Recommendations  LocalModelRecommendations
	ManagedProviders []localLLMTUIManagedProvider
	DefaultSpec      localllm.ManagedSpec
	DefaultProvider  string
	RecommendedAlias string
	AgentModelRef    string
	ProviderInChain  bool
}

type localLLMTUIManagedProvider struct {
	Alias          string
	IsAgentDefault bool
	ManagedModelID string
}

func (e *EditorTview) editLocalLLM() {
	statusView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	statusView.SetBorder(true).SetTitle(" Local LLM Status ").SetTitleAlign(tview.AlignLeft)

	modelList := tview.NewList().
		ShowSecondaryText(true)
	modelList.SetBorder(true).SetTitle(" Managed Models ").SetTitleAlign(tview.AlignLeft)

	selectedModelID := ""
	state := localLLMTUIViewState{}

	refresh := func() {
		state = e.localLLMState()
		selectedModelID = chooseLocalLLMModelID(selectedModelID, state)
		renderLocalLLMStatusView(statusView, state, selectedModelID)
		renderLocalLLMModelList(modelList, state, selectedModelID, func(modelID string) {
			selectedModelID = modelID
			renderLocalLLMStatusView(statusView, state, selectedModelID)
		})
	}

	stopPolling := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopPolling:
				return
			case <-ticker.C:
				if !hasRunningLocalLLMJobs(state.Jobs) {
					continue
				}
				e.app.App().QueueUpdateDraw(refresh)
			}
		}
	}()

	closeScreen := func() {
		close(stopPolling)
		e.showMainMenu()
	}

	actionSpec := func() localllm.ManagedSpec {
		spec := state.DefaultSpec
		if spec.Host == "" {
			spec.Host = "127.0.0.1"
		}
		if spec.Port == 0 {
			spec.Port = 8080
		}
		if selectedModelID != "" {
			spec.ModelID = selectedModelID
		}
		if strings.TrimSpace(spec.ModelID) == "" {
			spec.ModelID = defaultManagedLocalModelIDForEditor(state.Recommendations)
		}
		return spec
	}

	runAsync := func(command string, spec localllm.ManagedSpec) {
		res := bus.SendCommandWithSource("local_llm", command, spec, "tui", "")
		if res.Error != nil {
			e.app.SetStatusText(res.Message)
			return
		}
		e.app.SetStatusText(res.Message)
		refresh()
	}

	buttons := tview.NewForm()
	buttons.SetBorder(false)
	buttons.AddButton("Ensure Runtime", func() {
		runAsync("ensure_runtime", actionSpec())
	})
	buttons.AddButton("Download Model", func() {
		spec := actionSpec()
		if strings.TrimSpace(spec.ModelID) == "" {
			e.app.SetStatusText("Select a model first")
			return
		}
		runAsync("download_model", spec)
	})
	buttons.AddButton("Select Model", func() {
		spec := actionSpec()
		if strings.TrimSpace(spec.ModelID) == "" {
			e.app.SetStatusText("Select a model first")
			return
		}
		res := bus.SendCommandWithSource("local_llm", "select_model", localllm.ManagedSpec{ModelID: spec.ModelID}, "tui", "")
		if res.Error != nil {
			e.app.SetStatusText(res.Message)
			return
		}
		e.dirty = true
		e.app.SetStatusText(res.Message)
		refresh()
	})
	buttons.AddButton("Configure Provider", func() {
		spec := actionSpec()
		if strings.TrimSpace(spec.ModelID) == "" {
			e.app.SetStatusText("Select a model first")
			return
		}
		alias, _, changed := configureManagedLocalProviderForEditor(e.cfg, spec, false)
		if changed {
			e.dirty = true
		}
		refresh()
		e.app.SetStatusText(fmt.Sprintf("Staged managed provider %q. Save Changes to persist.", alias))
	})
	buttons.AddButton("Use For Agent", func() {
		spec := actionSpec()
		if strings.TrimSpace(spec.ModelID) == "" {
			e.app.SetStatusText("Select a model first")
			return
		}
		alias, agentRef, changed := configureManagedLocalProviderForEditor(e.cfg, spec, true)
		if changed {
			e.dirty = true
		}
		refresh()
		e.app.SetStatusText(fmt.Sprintf("Staged %q for agent as %s. Save Changes to persist.", alias, agentRef))
	})
	buttons.AddButton("Start Server", func() {
		runAsync("start", actionSpec())
	})
	buttons.AddButton("Stop Server", func() {
		res := bus.SendCommandWithSource("local_llm", "stop", nil, "tui", "")
		if res.Error != nil {
			e.app.SetStatusText(res.Message)
			return
		}
		e.app.SetStatusText(res.Message)
		refresh()
	})
	buttons.AddButton("Cancel Job", func() {
		for _, job := range state.Jobs {
			if job.State != jobs.StateRunning {
				continue
			}
			res := bus.SendCommandWithSource("jobs", "cancel", jobs.CancelRequest{JobID: job.JobID}, "tui", "")
			if res.Error != nil {
				e.app.SetStatusText(res.Message)
				return
			}
			e.app.SetStatusText("Local LLM job cancel requested")
			refresh()
			return
		}
		e.app.SetStatusText("No running local LLM job")
	})
	buttons.AddButton("Refresh", refresh)
	buttons.AddButton("Back", closeScreen)

	refresh()

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(buttons, 3, 0, true).
		AddItem(statusView, 0, 2, false).
		AddItem(modelList, 0, 3, false)

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Local LLM"})
	e.app.SetContent(layout)
}

func (e *EditorTview) localLLMState() localLLMTUIViewState {
	recs := BuildLocalModelRecommendations()
	status := localllm.GetManager().Status()
	providers, defaultSpec, defaultAlias, agentRef, inChain := managedLocalLLMSpecFromEditorConfig(e.cfg)
	return localLLMTUIViewState{
		Status:           status,
		Jobs:             jobs.GetManager().List("local_llm"),
		Recommendations:  recs,
		ManagedProviders: providers,
		DefaultSpec:      defaultSpec,
		DefaultProvider:  defaultAlias,
		RecommendedAlias: recommendedManagedLocalLLMAliasForEditor(e.cfg),
		AgentModelRef:    agentRef,
		ProviderInChain:  inChain,
	}
}

func managedLocalLLMSpecFromEditorConfig(cfg *config.Config) ([]localLLMTUIManagedProvider, localllm.ManagedSpec, string, string, bool) {
	if cfg == nil {
		return nil, localllm.ManagedSpec{}, "", "", false
	}
	agentAlias := ""
	if len(cfg.LLM.Agent.Models) > 0 {
		parts := strings.SplitN(cfg.LLM.Agent.Models[0], "/", 2)
		if len(parts) == 2 {
			agentAlias = strings.TrimSpace(parts[0])
		}
	}

	aliases := make([]string, 0, len(cfg.LLM.Providers))
	for alias, provider := range cfg.LLM.Providers {
		if provider.Driver != "llamacpp" || provider.LlamaCpp == nil || provider.LlamaCpp.Mode != llm.LlamaCppModeManaged {
			continue
		}
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	providers := make([]localLLMTUIManagedProvider, 0, len(aliases))
	defaultAlias := ""
	defaultSpec := localllm.ManagedSpec{}
	for _, alias := range aliases {
		provider := cfg.LLM.Providers[alias]
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
		providers = append(providers, localLLMTUIManagedProvider{
			Alias:          alias,
			IsAgentDefault: alias == agentAlias,
			ManagedModelID: provider.LlamaCpp.ManagedModelID,
		})
	}

	agentRef := ""
	inChain := false
	if defaultAlias != "" {
		agentRef, inChain = managedProviderRefInChainForEditor(cfg.LLM.Agent.Models, defaultAlias)
	}
	return providers, defaultSpec, defaultAlias, agentRef, inChain
}

func chooseLocalLLMModelID(current string, state localLLMTUIViewState) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	if strings.TrimSpace(state.Status.ModelID) != "" {
		return state.Status.ModelID
	}
	if strings.TrimSpace(state.DefaultSpec.ModelID) != "" {
		return state.DefaultSpec.ModelID
	}
	return defaultManagedLocalModelIDForEditor(state.Recommendations)
}

func renderLocalLLMStatusView(view *tview.TextView, state localLLMTUIViewState, selectedModelID string) {
	lines := []string{
		fmt.Sprintf("Server: %s", strings.TrimSpace(state.Status.Server.State)),
		fmt.Sprintf("Endpoint: %s", fallbackString(state.Status.Server.Endpoint, "not started")),
		fmt.Sprintf("Healthy: %t", state.Status.Server.Healthy),
		fmt.Sprintf("Runtime: %s", fallbackString(state.Status.RuntimeVersion, "-")),
		fmt.Sprintf("Backend: %s", fallbackString(string(state.Status.Backend), "-")),
		fmt.Sprintf("Selected model: %s", fallbackString(selectedModelID, "-")),
		fmt.Sprintf("Machine: %s", state.Recommendations.Summary),
	}

	if len(state.ManagedProviders) > 0 {
		lines = append(lines, "", "Managed providers:")
		for _, provider := range state.ManagedProviders {
			label := provider.Alias
			if provider.IsAgentDefault {
				label += " (agent default)"
			}
			lines = append(lines, fmt.Sprintf("- %s -> %s", label, fallbackString(provider.ManagedModelID, "-")))
		}
	} else {
		lines = append(lines, "", "Managed providers:", "- none configured")
		lines = append(lines, fmt.Sprintf("Suggested alias: %s", fallbackString(state.RecommendedAlias, "local-llm")))
	}

	if state.AgentModelRef != "" {
		lines = append(lines, "", fmt.Sprintf("Agent chain ref: %s", state.AgentModelRef))
	} else if state.DefaultProvider != "" {
		lines = append(lines, "", fmt.Sprintf("Agent chain ref: %s/managed (not yet staged)", state.DefaultProvider))
	}

	if state.Status.LastError != "" {
		lines = append(lines, "", fmt.Sprintf("Last error: %s", state.Status.LastError))
	}

	activeJobs := make([]string, 0, len(state.Jobs))
	for _, job := range state.Jobs {
		if job.State != jobs.StateRunning {
			continue
		}
		activeJobs = append(activeJobs, fmt.Sprintf("- %s: %s (%d%%)", job.OwnerAction, fallbackString(job.Message, job.Phase), job.ProgressPercent))
	}
	if len(activeJobs) > 0 {
		lines = append(lines, "", "Active jobs:")
		lines = append(lines, activeJobs...)
	}

	lines = append(lines, "", "Provider wiring changes are staged here. Use Save Changes from the main menu to persist them.")

	view.SetText(strings.Join(lines, "\n"))
}

func renderLocalLLMModelList(list *tview.List, state localLLMTUIViewState, selectedModelID string, onSelect func(string)) {
	list.Clear()
	for _, option := range state.Recommendations.Options {
		spec := option.Spec
		badges := make([]string, 0, 4)
		if localLLMModelInstalled(spec) {
			badges = append(badges, "installed")
		}
		if spec.ID == state.Status.ModelID {
			badges = append(badges, "active")
		}
		if option.Recommended {
			badges = append(badges, "recommended")
		}
		if !option.Viable {
			badges = append(badges, "heavy")
		}
		title := spec.Label
		if len(badges) > 0 {
			title = fmt.Sprintf("%s [%s]", spec.Label, strings.Join(badges, ", "))
		}
		modelID := spec.ID
		list.AddItem(title, option.Reason, 0, func() {
			if onSelect != nil {
				onSelect(modelID)
			}
		})
		if spec.ID == selectedModelID {
			list.SetCurrentItem(list.GetItemCount() - 1)
		}
	}
}

func localLLMModelInstalled(spec localllm.ManagedModelSpec) bool {
	modelPath, err := localllm.ManagedModelPath(spec)
	if err != nil || strings.TrimSpace(modelPath) == "" {
		return false
	}
	if _, modelErr := os.Stat(modelPath); modelErr != nil {
		return false
	}
	if strings.TrimSpace(spec.MMProjFilename) == "" {
		return true
	}
	mmprojPath, err := localllm.ManagedModelMMProjPath(spec)
	if err != nil || strings.TrimSpace(mmprojPath) == "" {
		return false
	}
	_, mmprojErr := os.Stat(mmprojPath)
	return mmprojErr == nil
}

func hasRunningLocalLLMJobs(items []jobs.Status) bool {
	for _, item := range items {
		if item.State == jobs.StateRunning {
			return true
		}
	}
	return false
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultManagedLocalModelIDForEditor(recs LocalModelRecommendations) string {
	if strings.TrimSpace(recs.DefaultModelID) != "" {
		return strings.TrimSpace(recs.DefaultModelID)
	}
	options := recs.Options
	if len(options) > 0 {
		return strings.TrimSpace(options[0].Spec.ID)
	}
	catalog := localllm.ManagedModelCatalog()
	if len(catalog) == 0 {
		return ""
	}
	return strings.TrimSpace(catalog[0].ID)
}

func recommendedManagedLocalLLMAliasForEditor(cfg *config.Config) string {
	const base = "local-llm"
	if cfg == nil || cfg.LLM.Providers == nil {
		return base
	}
	if _, exists := cfg.LLM.Providers[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		alias := fmt.Sprintf("%s-%d", base, i)
		if _, exists := cfg.LLM.Providers[alias]; !exists {
			return alias
		}
	}
}

func managedProviderRefInChainForEditor(models []string, alias string) (string, bool) {
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

func upsertManagedLocalLLMProviderForEditor(cfg *config.Config, spec localllm.ManagedSpec) (string, llm.LLMProviderConfig, bool) {
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = map[string]llm.LLMProviderConfig{}
	}

	_, _, alias, _, _ := managedLocalLLMSpecFromEditorConfig(cfg)
	if strings.TrimSpace(alias) == "" {
		alias = recommendedManagedLocalLLMAliasForEditor(cfg)
	}

	provider := cfg.LLM.Providers[alias]
	changed := false
	if provider.Driver != "llamacpp" {
		provider.Driver = "llamacpp"
		changed = true
	}
	if strings.TrimSpace(provider.Subtype) == "" {
		provider.Subtype = "llamacpp-managed"
		changed = true
	}
	if provider.APIKey != "" {
		provider.APIKey = ""
		changed = true
	}
	if provider.BaseURL != "" {
		provider.BaseURL = ""
		changed = true
	}
	if provider.LlamaCpp == nil {
		provider.LlamaCpp = &llm.LlamaCppProviderConfig{}
		changed = true
	}
	if provider.LlamaCpp.Mode != llm.LlamaCppModeManaged {
		provider.LlamaCpp.Mode = llm.LlamaCppModeManaged
		changed = true
	}
	if strings.TrimSpace(provider.LlamaCpp.Host) == "" {
		provider.LlamaCpp.Host = "127.0.0.1"
		changed = true
	}
	if provider.LlamaCpp.Port == 0 {
		provider.LlamaCpp.Port = 8080
		changed = true
	}
	if strings.TrimSpace(spec.Host) != "" && provider.LlamaCpp.Host != strings.TrimSpace(spec.Host) {
		provider.LlamaCpp.Host = strings.TrimSpace(spec.Host)
		changed = true
	}
	if spec.Port != 0 && provider.LlamaCpp.Port != spec.Port {
		provider.LlamaCpp.Port = spec.Port
		changed = true
	}
	if strings.TrimSpace(provider.LlamaCpp.ManagedModelID) == "" {
		provider.LlamaCpp.ManagedModelID = defaultManagedLocalModelIDForEditor(LocalModelRecommendations{})
		changed = true
	}
	if strings.TrimSpace(spec.ModelID) != "" && provider.LlamaCpp.ManagedModelID != strings.TrimSpace(spec.ModelID) {
		provider.LlamaCpp.ManagedModelID = strings.TrimSpace(spec.ModelID)
		changed = true
	}
	if strings.TrimSpace(spec.RuntimeVersion) != "" && provider.LlamaCpp.RuntimeVersion != strings.TrimSpace(spec.RuntimeVersion) {
		provider.LlamaCpp.RuntimeVersion = strings.TrimSpace(spec.RuntimeVersion)
		changed = true
	}
	if strings.TrimSpace(spec.ModelAlias) != "" && provider.LlamaCpp.ModelAlias != strings.TrimSpace(spec.ModelAlias) {
		provider.LlamaCpp.ModelAlias = strings.TrimSpace(spec.ModelAlias)
		changed = true
	}

	if _, ok := cfg.LLM.Providers[alias]; !ok {
		changed = true
	}
	if changed {
		cfg.LLM.Providers[alias] = provider
	}
	return alias, provider, changed
}

func ensureManagedProviderInAgentChainForEditor(cfg *config.Config, alias string) (string, bool) {
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
		if cfg.LLM.Agent.Models[i] != modelRef {
			cfg.LLM.Agent.Models[i] = modelRef
			return modelRef, true
		}
		return modelRef, false
	}
	cfg.LLM.Agent.Models = append(cfg.LLM.Agent.Models, modelRef)
	return modelRef, true
}

func configureManagedLocalProviderForEditor(cfg *config.Config, spec localllm.ManagedSpec, addToAgent bool) (string, string, bool) {
	alias, _, changed := upsertManagedLocalLLMProviderForEditor(cfg, spec)
	agentModelRef := ""
	if addToAgent {
		var chainChanged bool
		agentModelRef, chainChanged = ensureManagedProviderInAgentChainForEditor(cfg, alias)
		changed = changed || chainChanged
	}
	return alias, agentModelRef, changed
}
