// Package setup - LLM configuration editor
package setup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metadata"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// breadcrumbBase is the base path for LLM navigation
const llmBreadcrumbBase = "GoClaw Configuration"

// LLMEditor handles LLM configuration editing
type LLMEditor struct {
	app    *forms.TviewApp
	cfg    *llm.LLMConfig
	onSave func()
	onBack func()
}

// NewLLMEditor creates a new LLM editor
func NewLLMEditor(app *forms.TviewApp, cfg *llm.LLMConfig, onSave func(), onBack func()) *LLMEditor {
	return &LLMEditor{
		app:    app,
		cfg:    cfg,
		onSave: onSave,
		onBack: onBack,
	}
}

// Show displays the LLM configuration menu
func (e *LLMEditor) Show() {
	e.app.SetMenuContent(e.createMenu())
}

// createMenu creates the LLM submenu
func (e *LLMEditor) createMenu() *forms.MenuListResult {
	// Set breadcrumbs
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration"})
	e.app.SetStatusText(forms.StatusMenu)

	providerCount := len(e.cfg.Providers)
	agentModels := len(e.cfg.Agent.Models)
	summarizationModels := len(e.cfg.Summarization.Models)
	embeddingModels := len(e.cfg.Embeddings.Models)

	purposeLabel := func(name string, count int) string {
		if count == 0 {
			return fmt.Sprintf("%s (uses agent chain)", name)
		}
		return fmt.Sprintf("%s (%d models)", name, count)
	}

	items := []forms.MenuItem{
		{Label: fmt.Sprintf("Providers (%d configured)", providerCount), OnSelect: e.showProviderList},
		{Label: fmt.Sprintf("Agent (%d models)", agentModels), OnSelect: func() { e.editPurpose("agent", &e.cfg.Agent) }},
		{Label: fmt.Sprintf("Summarization (%d models)", summarizationModels), OnSelect: func() { e.editPurpose("summarization", &e.cfg.Summarization) }},
		{Label: fmt.Sprintf("Embeddings (%d models)", embeddingModels), OnSelect: func() { e.editPurpose("embeddings", &e.cfg.Embeddings) }},
		{Label: purposeLabel("Heartbeat", len(e.cfg.Heartbeat.Models)), OnSelect: func() { e.editPurpose("heartbeat", &e.cfg.Heartbeat) }},
		{Label: purposeLabel("Cron", len(e.cfg.Cron.Models)), OnSelect: func() { e.editPurpose("cron", &e.cfg.Cron) }},
		{Label: purposeLabel("Hass", len(e.cfg.Hass.Models)), OnSelect: func() { e.editPurpose("hass", &e.cfg.Hass) }},
		{Label: "System Prompt", OnSelect: e.editSystemPrompt},
		{Label: "Extended Thinking", OnSelect: e.editThinking},
	}

	return forms.NewMenuList(forms.MenuListConfig{
		Items:  items,
		OnBack: e.onBack,
	})
}

// showProviderList shows the list of configured providers with preview
func (e *LLMEditor) showProviderList() {
	L_info("llm editor: showing provider list")

	// Set breadcrumbs
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", "Providers"})
	e.app.SetStatusText(forms.StatusList)

	// Build sorted list of provider names for stable ordering
	providerNames := make([]string, 0, len(e.cfg.Providers))
	for name := range e.cfg.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	// Build split pane items
	items := make([]forms.SplitItem, 0, len(providerNames)+1)

	for _, name := range providerNames {
		providerName := name
		cfg := e.cfg.Providers[name]
		preview := e.buildProviderPreview(providerName, cfg)

		items = append(items, forms.SplitItem{
			Label:   fmt.Sprintf("%s (%s)", providerName, cfg.Driver),
			Preview: preview,
			OnSelect: func() {
				provCfg := e.cfg.Providers[providerName]
				e.editProvider(providerName, &provCfg)
			},
			OnDelete: func() {
				e.deleteProvider(providerName)
			},
			OnRename: func() {
				e.renameProvider(providerName)
			},
		})
	}

	// Add "Add Provider" option
	items = append(items, forms.SplitItem{
		Label:   "[+] Add Provider",
		Preview: "Configure a new LLM provider connection.",
		OnSelect: func() {
			e.addProvider()
		},
	})

	splitPane := forms.NewSplitPane(forms.SplitPaneConfig{
		Title:     "Providers",
		Items:     items,
		OnBack:    e.Show,
		ListWidth: 30,
	})
	splitPane.SetPreviewTitle("Provider Details")

	e.app.SetSplitPaneContent(splitPane)
}

// buildProviderPreview generates the preview text for a provider
func (e *LLMEditor) buildProviderPreview(name string, cfg llm.LLMProviderConfig) string {
	var lines []string

	// Type
	lines = append(lines, fmt.Sprintf("[yellow]Driver:[white] %s", cfg.Driver))
	if cfg.Subtype != "" {
		lines = append(lines, fmt.Sprintf("[yellow]Subtype:[white] %s", cfg.Subtype))
	}

	// URL/BaseURL
	if cfg.URL != "" {
		lines = append(lines, fmt.Sprintf("[yellow]URL:[white] %s", cfg.URL))
	} else if cfg.BaseURL != "" {
		lines = append(lines, fmt.Sprintf("[yellow]Base URL:[white] %s", cfg.BaseURL))
	} else {
		lines = append(lines, "[yellow]URL:[white] (default)")
	}

	lines = append(lines, "")

	// Features
	if cfg.PromptCaching {
		lines = append(lines, "[yellow]Prompt Caching:[white] enabled")
	}
	if cfg.ThinkingLevel != "" && cfg.ThinkingLevel != "none" {
		lines = append(lines, fmt.Sprintf("[yellow]Thinking Level:[white] %s", cfg.ThinkingLevel))
	}
	if cfg.MaxTokens > 0 {
		lines = append(lines, fmt.Sprintf("[yellow]Max Tokens:[white] %d", cfg.MaxTokens))
	}

	return strings.Join(lines, "\n")
}

// findProviderReferences finds all references to a provider in purpose configs
func (e *LLMEditor) findProviderReferences(providerName string) []string {
	var refs []string
	prefix := providerName + "/"

	// Check agent models
	for _, model := range e.cfg.Agent.Models {
		if strings.HasPrefix(model, prefix) {
			refs = append(refs, fmt.Sprintf("Agent: %s", model))
		}
	}

	// Check summarization models
	for _, model := range e.cfg.Summarization.Models {
		if strings.HasPrefix(model, prefix) {
			refs = append(refs, fmt.Sprintf("Summarization: %s", model))
		}
	}

	// Check embeddings models
	for _, model := range e.cfg.Embeddings.Models {
		if strings.HasPrefix(model, prefix) {
			refs = append(refs, fmt.Sprintf("Embeddings: %s", model))
		}
	}

	return refs
}

// updateProviderReferences updates all references from oldName to newName
func (e *LLMEditor) updateProviderReferences(oldName, newName string) {
	oldPrefix := oldName + "/"
	newPrefix := newName + "/"

	updateModels := func(models []string) []string {
		result := make([]string, len(models))
		for i, model := range models {
			if strings.HasPrefix(model, oldPrefix) {
				result[i] = newPrefix + strings.TrimPrefix(model, oldPrefix)
			} else {
				result[i] = model
			}
		}
		return result
	}

	e.cfg.Agent.Models = updateModels(e.cfg.Agent.Models)
	e.cfg.Summarization.Models = updateModels(e.cfg.Summarization.Models)
	e.cfg.Embeddings.Models = updateModels(e.cfg.Embeddings.Models)
}

// deleteProvider handles provider deletion with confirmation
func (e *LLMEditor) deleteProvider(name string) {
	L_info("llm editor: delete provider requested", "name", name)

	refs := e.findProviderReferences(name)

	// Build confirmation message
	var msg string
	if len(refs) > 0 {
		msg = fmt.Sprintf("Delete provider '%s'?\n\nWARNING: This provider is referenced by:\n", name)
		for _, ref := range refs {
			msg += fmt.Sprintf("  - %s\n", ref)
		}
		msg += "\nThese references will become invalid!"
	} else {
		msg = fmt.Sprintf("Delete provider '%s'?", name)
	}

	// Show confirmation modal
	modal := tview.NewModal().
		SetText(msg).
		AddButtons([]string{"Delete", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Delete" {
				delete(e.cfg.Providers, name)
				e.onSave()
				L_info("llm editor: provider deleted", "name", name)
			}
			e.showProviderList()
		})

	e.app.SetContent(modal)
}

// renameProvider handles provider renaming
func (e *LLMEditor) renameProvider(oldName string) {
	L_info("llm editor: rename provider requested", "name", oldName)

	// Set breadcrumbs
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", "Providers", "Rename"})
	e.app.SetStatusText(forms.StatusForm)

	refs := e.findProviderReferences(oldName)

	input := tview.NewInputField().
		SetLabel("New name: ").
		SetText(oldName).
		SetFieldWidth(40)

	// Info text
	var infoText string
	if len(refs) > 0 {
		infoText = fmt.Sprintf("Renaming '%s' will update %d reference(s)", oldName, len(refs))
	} else {
		infoText = fmt.Sprintf("Rename provider '%s'", oldName)
	}

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			newName := strings.TrimSpace(input.GetText())
			if newName == "" || newName == oldName {
				e.showProviderList()
				return
			}
			// Check for duplicate
			if _, exists := e.cfg.Providers[newName]; exists {
				L_warn("llm editor: provider name already exists", "name", newName)
				e.showProviderList()
				return
			}

			// Perform rename
			cfg := e.cfg.Providers[oldName]
			delete(e.cfg.Providers, oldName)
			e.cfg.Providers[newName] = cfg

			// Update references
			e.updateProviderReferences(oldName, newName)

			e.onSave()
			L_info("llm editor: provider renamed", "old", oldName, "new", newName, "refsUpdated", len(refs))
			e.showProviderList()
		} else if key == tcell.KeyEscape {
			e.showProviderList()
		}
	})

	form := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetText(infoText), 1, 0, false).
		AddItem(nil, 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(nil, 0, 1, false)

	e.app.SetContent(form)
}

// editProvider opens the provider edit form
func (e *LLMEditor) editProvider(name string, cfg *llm.LLMProviderConfig) {
	L_info("llm editor: editing provider", "name", name)

	e.resolveProviderID(name, cfg)
	formDef := llm.ProviderConfigFormDef(buildSubtypeOptions(cfg.Driver))
	formDef.Title = fmt.Sprintf("Provider: %s", name)

	content, err := forms.BuildFormContent(formDef, cfg, "llm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Providers[name] = *cfg
			e.onSave()
			L_info("llm editor: provider updated", "name", name)
		}
		e.showProviderList()
	}, e.app.App())
	if err != nil {
		L_error("llm editor: form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "LLM Configuration", "Providers", name})
	e.app.SetFormContent(content)
}

// addProvider shows the add provider wizard
func (e *LLMEditor) addProvider() {
	L_info("llm editor: adding new provider")

	// Create a new provider config
	newCfg := llm.LLMProviderConfig{}

	// First, select provider type
	e.selectProviderType(&newCfg)
}

// selectProviderType shows a flat list of all known providers from models.json
// plus a Custom Endpoint escape hatch.
func (e *LLMEditor) selectProviderType(cfg *llm.LLMProviderConfig) {
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", "Providers", "Add Provider"})
	e.app.SetStatusText(forms.StatusMenu)

	meta := metadata.Get()
	providerIDs := meta.ModelProviderIDs()

	var items []forms.MenuItem

	for _, pid := range providerIDs {
		providerID := pid
		prov, ok := meta.GetModelProvider(providerID)
		if !ok {
			continue
		}

		items = append(items, forms.MenuItem{
			Label: prov.Name,
			OnSelect: func() {
				cfg.Subtype = providerID
				if prov.Driver == "ollama" {
					cfg.Driver = "ollama"
					cfg.URL = prov.APIEndpoint
				} else {
					cfg.Driver = prov.Driver
					cfg.BaseURL = prov.APIEndpoint
				}
				e.promptProviderName(cfg)
			},
		})
	}

	items = append(items, forms.MenuItem{IsSeparator: true})
	items = append(items, forms.MenuItem{
		Label: "Custom Endpoint",
		OnSelect: func() {
			cfg.Driver = "openai"
			cfg.Subtype = "custom"
			e.promptProviderName(cfg)
		},
	})

	menu := forms.NewMenuList(forms.MenuListConfig{
		Title:     "Select Provider",
		Items:     items,
		OnBack:    e.showProviderList,
		BackLabel: "Cancel",
	})

	e.app.SetMenuContent(menu)
}

// promptProviderName prompts for provider alias name
func (e *LLMEditor) promptProviderName(cfg *llm.LLMProviderConfig) {
	// Set breadcrumbs
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", "Providers", "Add Provider", "Name"})
	e.app.SetStatusText(forms.StatusForm)

	// Default name based on type/subtype
	defaultName := cfg.Driver
	if cfg.Subtype != "" && cfg.Subtype != cfg.Driver {
		defaultName = cfg.Subtype
	}

	input := tview.NewInputField().
		SetLabel("Provider Name: ").
		SetText(defaultName).
		SetFieldWidth(40)

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			name := input.GetText()
			if name == "" {
				return
			}
			// Check for duplicate
			if _, exists := e.cfg.Providers[name]; exists {
				L_warn("llm editor: provider name already exists", "name", name)
				return
			}
			e.finishAddProvider(name, cfg)
		} else if key == tcell.KeyEscape {
			e.showProviderList()
		}
	})

	form := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetText("Enter a unique name for this provider:"), 1, 0, false).
		AddItem(nil, 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(nil, 0, 1, false)

	e.app.SetContent(form)
}

// finishAddProvider completes adding a new provider
func (e *LLMEditor) finishAddProvider(name string, cfg *llm.LLMProviderConfig) {
	L_info("llm editor: finishing add provider", "name", name, "driver", cfg.Driver)

	formDef := llm.ProviderConfigFormDef(buildSubtypeOptions(cfg.Driver))

	content, err := forms.BuildFormContent(formDef, cfg, "llm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			if e.cfg.Providers == nil {
				e.cfg.Providers = make(map[string]llm.LLMProviderConfig)
			}
			e.cfg.Providers[name] = *cfg
			e.onSave()
			L_info("llm editor: provider added", "name", name)
		}
		e.showProviderList()
	}, e.app.App())
	if err != nil {
		L_error("llm editor: form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "LLM Configuration", "Providers", name + " (new)"})
	e.app.SetFormContent(content)
}

// editPurpose opens the purpose configuration editor.
// Agent and summarization get the model chain picker; embeddings stays as a form.
func (e *LLMEditor) editPurpose(name string, cfg *llm.LLMPurposeConfig) {
	L_info("llm editor: editing purpose", "name", name)

	if name != "embeddings" {
		e.showModelChain(name, cfg)
		return
	}

	// Embeddings: keep the old form-based editor
	formDef := llm.EmbeddingsPurposeFormDef()
	content, err := forms.BuildFormContent(formDef, cfg, "llm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.onSave()
			L_info("llm editor: purpose updated", "name", name)
		}
		e.Show()
	}, e.app.App())
	if err != nil {
		L_error("llm editor: purpose form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", name})
	e.app.SetFormContent(content)
}

// --- Model chain picker (agent/summarization) ---

// showModelChain displays the model chain as a split pane with preview.
func (e *LLMEditor) showModelChain(purpose string, cfg *llm.LLMPurposeConfig, focusIndex ...int) {
	title := cases.Title(language.English).String(purpose)
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", title})
	e.app.SetStatusText("d=Delete  Shift+Up/Down=Reorder")

	var items []forms.SplitItem

	for i, ref := range cfg.Models {
		idx := i
		modelRef := ref
		preview := e.buildChainEntryPreview(modelRef, purpose)

		label := modelRef
		if idx == 0 {
			label = modelRef + " [primary]"
		}

		// Parse alias from the model reference for the replace flow
		parts := strings.SplitN(modelRef, "/", 2)
		refAlias := ""
		if len(parts) == 2 {
			refAlias = parts[0]
		}

		items = append(items, forms.SplitItem{
			Label:   label,
			Preview: preview,
			OnSelect: func() {
				if refAlias == "" {
					return
				}
				provCfg, ok := e.cfg.Providers[refAlias]
				if !ok {
					return
				}
				e.pickModelFromProvider(refAlias, provCfg, purpose, cfg, func(newRef string) {
					cfg.Models[idx] = newRef
				})
			},
			OnDelete: func() {
				cfg.Models = append(cfg.Models[:idx], cfg.Models[idx+1:]...)
				e.onSave()
				e.showModelChain(purpose, cfg)
			},
		})
	}

	items = append(items, forms.SplitItem{IsSeparator: true})

	items = append(items, forms.SplitItem{
		Label:   "[+] Add Model",
		Preview: "Add a model to the chain.\nFirst model is primary, rest are fallbacks.",
		OnSelect: func() {
			e.addModelToChain(purpose, cfg)
		},
	})

	if purpose == "summarization" {
		settingsLabel := fmt.Sprintf("[settings] Max Input Tokens: %d", cfg.MaxInputTokens)
		settingsPreview := "Input limit for summarization.\n0 = use model context - buffer."

		items = append(items, forms.SplitItem{
			Label:   settingsLabel,
			Preview: settingsPreview,
			OnSelect: func() {
				e.editPurposeSettings(purpose, cfg)
			},
		})
	}

	pane := forms.NewSplitPane(forms.SplitPaneConfig{
		Title:     title + " Model Chain",
		Items:     items,
		OnBack:    e.Show,
		ListWidth: 40,
	})
	pane.SetPreviewTitle("Model Details")

	// Add move up/down key handling
	if list, ok := pane.Focusable().(*tview.List); ok {
		origCapture := list.GetInputCapture()
		list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			idx := list.GetCurrentItem()
			chainLen := len(cfg.Models)

			if event.Modifiers()&tcell.ModShift != 0 {
				switch event.Key() {
				case tcell.KeyUp:
					if idx > 0 && idx < chainLen {
						cfg.Models[idx], cfg.Models[idx-1] = cfg.Models[idx-1], cfg.Models[idx]
						e.onSave()
						e.showModelChain(purpose, cfg, idx-1)
						return nil
					}
				case tcell.KeyDown:
					if idx >= 0 && idx < chainLen-1 {
						cfg.Models[idx], cfg.Models[idx+1] = cfg.Models[idx+1], cfg.Models[idx]
						e.onSave()
						e.showModelChain(purpose, cfg, idx+1)
						return nil
					}
				}
			}

			if origCapture != nil {
				return origCapture(event)
			}
			return event
		})

		if len(focusIndex) > 0 && focusIndex[0] >= 0 {
			list.SetCurrentItem(focusIndex[0])
		}
	}

	e.app.SetSplitPaneContent(pane)
}

// buildSubtypeOptions returns form options for the subtype dropdown,
// filtered to providers that use the same driver as the given config.
func buildSubtypeOptions(driverType string) []forms.Option {
	meta := metadata.Get()
	var options []forms.Option

	for _, pid := range meta.ModelProviderIDs() {
		prov, ok := meta.GetModelProvider(pid)
		if !ok {
			continue
		}
		if prov.Driver == driverType {
			options = append(options, forms.Option{Label: prov.Name, Value: pid})
		}
	}

	options = append(options, forms.Option{Label: "Custom", Value: "custom"})
	return options
}

// resolveProviderID returns the models.json provider ID for a configured provider.
// If subtype is missing, infers it and persists to config.
func (e *LLMEditor) resolveProviderID(alias string, provCfg *llm.LLMProviderConfig) string {
	resolved := metadata.Get().ResolveProvider(provCfg.Subtype, provCfg.Driver, provCfg.BaseURL)

	if provCfg.Subtype == "" && resolved != provCfg.Driver {
		provCfg.Subtype = resolved
		e.cfg.Providers[alias] = *provCfg
		e.onSave()
		L_info("llm editor: inferred subtype from URL", "alias", alias, "subtype", resolved)
	}

	return resolved
}

// buildChainEntryPreview builds a preview string for a model chain entry.
func (e *LLMEditor) buildChainEntryPreview(modelRef, purpose string) string {
	parts := strings.SplitN(modelRef, "/", 2)
	if len(parts) != 2 {
		return fmt.Sprintf("[red]Invalid format:[white] %s\nExpected: provider/model", modelRef)
	}

	alias := parts[0]
	modelID := parts[1]

	provCfg, ok := e.cfg.Providers[alias]
	if !ok {
		return fmt.Sprintf("[red]Unknown provider:[white] %s\nThis provider is not configured.", alias)
	}

	providerID := e.resolveProviderID(alias, &provCfg)
	return buildModelPreview(providerID, modelID, purpose)
}

// buildModelPreview formats model metadata from models.json into a preview string.
func buildModelPreview(providerID, modelID, purpose string) string {
	meta := metadata.Get()
	matchedID, model, ok := meta.ResolveModel(providerID, modelID)
	if !ok {
		return fmt.Sprintf("[green::b]%s[-:-:-]\n[yellow]Provider:[white] %s\n\nModel not found in metadata.\nThis may be a custom or dynamically discovered model.", modelID, providerID)
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("[green::b]%s[-:-:-] [dim](%s)[-]", model.Name, matchedID))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("[yellow]Context:[white]  %dk tokens", model.ContextWindow/1000))
	lines = append(lines, fmt.Sprintf("[yellow]Output:[white]   %dk tokens", model.MaxOutputTokens/1000))
	lines = append(lines, "")

	// Capabilities with purpose-aware warnings
	cap := model.Capabilities
	var capFlags []string

	visionStr := "No"
	if cap.Vision {
		visionStr = "[green]Yes[-]"
	} else if purpose == "agent" {
		visionStr = "[red]No (required for agent)[-]"
	}
	capFlags = append(capFlags, fmt.Sprintf("[yellow]Vision:[white]    %s", visionStr))

	toolStr := "No"
	if cap.ToolUse {
		toolStr = "[green]Yes[-]"
	} else if purpose == "agent" {
		toolStr = "[red]No (required for agent)[-]"
	}
	capFlags = append(capFlags, fmt.Sprintf("[yellow]Tool Use:[white]  %s", toolStr))

	if cap.Reasoning {
		reasonStr := "[green]Yes[-]"
		if cap.DefaultReasoningEffort != "" {
			reasonStr += fmt.Sprintf(" (default: %s)", cap.DefaultReasoningEffort)
		}
		capFlags = append(capFlags, fmt.Sprintf("[yellow]Reasoning:[white] %s", reasonStr))
	}
	if cap.StructuredOutput {
		capFlags = append(capFlags, "[yellow]Structured Output:[white] [green]Yes[-]")
	}

	lines = append(lines, capFlags...)
	lines = append(lines, "")

	// Cost
	cost := model.Cost
	costLine := fmt.Sprintf("[yellow]Cost:[white]     $%.2f / $%.2f per 1M tokens", cost.Input, cost.Output)
	if cost.CacheRead > 0 {
		costLine += fmt.Sprintf("\n[yellow]Cache:[white]    read $%.2f / write $%.2f", cost.CacheRead, cost.CacheWrite)
	}
	lines = append(lines, costLine)

	// Metadata
	if model.Metadata.KnowledgeCutoff != "" {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("[yellow]Knowledge:[white] %s", model.Metadata.KnowledgeCutoff))
	}

	return strings.Join(lines, "\n")
}

// addModelToChain shows the provider selection step for adding a model.
func (e *LLMEditor) addModelToChain(purpose string, cfg *llm.LLMPurposeConfig) {
	title := cases.Title(language.English).String(purpose)
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", title, "Add Model"})
	e.app.SetStatusText(forms.StatusMenu)

	providerNames := make([]string, 0, len(e.cfg.Providers))
	for name := range e.cfg.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	if len(providerNames) == 0 {
		modal := tview.NewModal().
			SetText("No providers configured.\nAdd a provider first.").
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(int, string) { e.showModelChain(purpose, cfg) })
		e.app.SetContent(modal)
		return
	}

	meta := metadata.Get()
	var items []forms.MenuItem

	for _, name := range providerNames {
		alias := name
		provCfg := e.cfg.Providers[alias]
		providerID := e.resolveProviderID(alias, &provCfg)

		label := alias
		if prov, ok := meta.GetModelProvider(providerID); ok {
			label = fmt.Sprintf("%s (%s)", alias, prov.Name)
		}

		items = append(items, forms.MenuItem{
			Label: label,
			OnSelect: func() {
				e.pickModelFromProvider(alias, provCfg, purpose, cfg, func(ref string) {
					cfg.Models = append(cfg.Models, ref)
				})
			},
		})
	}

	menu := forms.NewMenuList(forms.MenuListConfig{
		Title:     "Select Provider",
		Items:     items,
		OnBack:    func() { e.showModelChain(purpose, cfg) },
		BackLabel: "Cancel",
	})

	e.app.SetMenuContent(menu)
}

// pickModelFromProvider shows available models for a provider with purpose filtering.
// onPick is called with the selected model reference before saving. Use it to either
// append (for add) or replace (for swap) the chain entry.
func (e *LLMEditor) pickModelFromProvider(alias string, provCfg llm.LLMProviderConfig, purpose string, cfg *llm.LLMPurposeConfig, onPick func(ref string)) {
	title := cases.Title(language.English).String(purpose)
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", title, "Select Model", alias})

	providerID := e.resolveProviderID(alias, &provCfg)

	meta := metadata.Get()
	models := meta.GetModels(providerID)

	if len(models) == 0 {
		e.freeTextModelInput(alias, purpose, cfg, onPick)
		return
	}

	modelIDs := meta.GetKnownChatModels(providerID)

	var items []forms.SplitItem
	for _, mid := range modelIDs {
		modelID := mid
		model := models[modelID]

		label := modelID
		if purpose == "agent" && model != nil {
			if !model.Capabilities.Vision || !model.Capabilities.ToolUse {
				label = "⚠ " + label
			}
		}

		preview := buildModelPreview(providerID, modelID, purpose)

		items = append(items, forms.SplitItem{
			Label:   label,
			Preview: preview,
			OnSelect: func() {
				ref := alias + "/" + modelID
				onPick(ref)
				e.onSave()
				L_info("llm editor: model selected", "purpose", purpose, "model", ref)
				e.showModelChain(purpose, cfg)
			},
		})
	}

	pane := forms.NewSplitPane(forms.SplitPaneConfig{
		Title:     alias + ": Select Model",
		Items:     items,
		OnBack:    func() { e.showModelChain(purpose, cfg) },
		ListWidth: 35,
	})
	pane.SetPreviewTitle("Model Details")

	e.app.SetSplitPaneContent(pane)
}

// freeTextModelInput shows a text input for typing a model name manually.
func (e *LLMEditor) freeTextModelInput(alias, purpose string, cfg *llm.LLMPurposeConfig, onPick func(ref string)) {
	title := cases.Title(language.English).String(purpose)
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", title, "Select Model", alias, "Model Name"})
	e.app.SetStatusText(forms.StatusForm)

	input := tview.NewInputField().
		SetLabel("Model ID: ").
		SetFieldWidth(50)

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			modelID := strings.TrimSpace(input.GetText())
			if modelID == "" {
				return
			}
			ref := alias + "/" + modelID
			onPick(ref)
			e.onSave()
			L_info("llm editor: model selected (manual)", "purpose", purpose, "model", ref)
			e.showModelChain(purpose, cfg)
		} else if key == tcell.KeyEscape {
			e.showModelChain(purpose, cfg)
		}
	})

	info := fmt.Sprintf("Enter model ID for provider '%s'.\nNo known models in metadata (custom/local provider).", alias)

	form := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetText(info), 2, 0, false).
		AddItem(nil, 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(nil, 0, 1, false)

	e.app.SetContent(form)
}

// editPurposeSettings opens a small form for purpose-specific settings.
func (e *LLMEditor) editPurposeSettings(purpose string, cfg *llm.LLMPurposeConfig) {
	title := cases.Title(language.English).String(purpose)
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", title, "Settings"})

	formDef := forms.FormDef{
		Title: title + " Settings",
		Sections: []forms.Section{{
			Fields: []forms.Field{
				{
					Name:  "maxInputTokens",
					Title: "Max Input Tokens",
					Desc:  "Input limit for summarization (0 = context - buffer)",
					Type:  forms.Number,
					Min:   0,
					Max:   2000000,
				},
			},
		}},
	}

	content, err := forms.BuildFormContent(formDef, cfg, "llm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.onSave()
		}
		e.showModelChain(purpose, cfg)
	}, e.app.App())
	if err != nil {
		L_error("llm editor: settings form error", "error", err)
		return
	}

	e.app.SetFormContent(content)
}

// editSystemPrompt opens the system prompt editor
func (e *LLMEditor) editSystemPrompt() {
	L_info("llm editor: editing system prompt")

	// Wrap systemPrompt in a struct for the form
	type promptWrapper struct {
		SystemPrompt string `json:"systemPrompt"`
	}
	wrapper := &promptWrapper{SystemPrompt: e.cfg.SystemPrompt}

	formDef := llm.SystemPromptFormDef()

	content, err := forms.BuildFormContent(formDef, wrapper, "llm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.SystemPrompt = wrapper.SystemPrompt
			e.onSave()
			L_info("llm editor: system prompt updated")
		}
		e.Show()
	}, e.app.App())
	if err != nil {
		L_error("llm editor: system prompt form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "LLM Configuration", "System Prompt"})
	e.app.SetFormContent(content)
}

// editThinking opens the thinking configuration editor
func (e *LLMEditor) editThinking() {
	L_info("llm editor: editing thinking config")

	formDef := llm.ThinkingFormDef()

	content, err := forms.BuildFormContent(formDef, &e.cfg.Thinking, "llm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.onSave()
			L_info("llm editor: thinking config updated")
		}
		e.Show()
	}, e.app.App())
	if err != nil {
		L_error("llm editor: thinking form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "LLM Configuration", "Thinking"})
	e.app.SetFormContent(content)
}
