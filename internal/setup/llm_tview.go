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

	items := []forms.MenuItem{
		{Label: fmt.Sprintf("Providers (%d configured)", providerCount), OnSelect: e.showProviderList},
		{Label: fmt.Sprintf("Agent (%d models)", agentModels), OnSelect: func() { e.editPurpose("agent", &e.cfg.Agent) }},
		{Label: fmt.Sprintf("Summarization (%d models)", summarizationModels), OnSelect: func() { e.editPurpose("summarization", &e.cfg.Summarization) }},
		{Label: fmt.Sprintf("Embeddings (%d models)", embeddingModels), OnSelect: func() { e.editPurpose("embeddings", &e.cfg.Embeddings) }},
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
			Label:   fmt.Sprintf("%s (%s)", providerName, cfg.Type),
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
	lines = append(lines, fmt.Sprintf("[yellow]Type:[white] %s", cfg.Type))
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

	formDef := llm.ProviderConfigFormDef()

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

// selectProviderType shows provider type selection
func (e *LLMEditor) selectProviderType(cfg *llm.LLMProviderConfig) {
	// Set breadcrumbs
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", "Providers", "Add Provider"})
	e.app.SetStatusText(forms.StatusMenu)

	items := []forms.MenuItem{
		{Label: "Anthropic (Claude)", OnSelect: func() {
			cfg.Type = "anthropic"
			e.selectProviderSubtype(cfg)
		}},
		{Label: "OpenAI Compatible", OnSelect: func() {
			cfg.Type = "openai"
			e.selectProviderSubtype(cfg)
		}},
		{Label: "Ollama (Local)", OnSelect: func() {
			cfg.Type = "ollama"
			cfg.URL = "http://localhost:11434"
			e.selectProviderSubtype(cfg)
		}},
		{Label: "xAI (Grok)", OnSelect: func() {
			cfg.Type = "xai"
			e.selectProviderSubtype(cfg)
		}},
	}

	menu := forms.NewMenuList(forms.MenuListConfig{
		Title:     "Select Provider Type",
		Items:     items,
		OnBack:    e.showProviderList,
		BackLabel: "Cancel",
	})

	e.app.SetMenuContent(menu)
}

// selectProviderSubtype shows subtype selection for providers that have subtypes
func (e *LLMEditor) selectProviderSubtype(cfg *llm.LLMProviderConfig) {
	// Get subtypes from provider
	provider, err := llm.NewProvider("temp", *cfg)
	if err != nil {
		L_warn("llm editor: failed to create provider for subtypes", "error", err)
		e.promptProviderName(cfg)
		return
	}

	subtypeProvider, ok := provider.(llm.SubtypeProvider)
	if !ok {
		e.promptProviderName(cfg)
		return
	}

	subtypes := subtypeProvider.GetSubtypes()
	if len(subtypes) == 0 {
		e.promptProviderName(cfg)
		return
	}

	// Set breadcrumbs
	e.app.SetBreadcrumbs([]string{llmBreadcrumbBase, "LLM Configuration", "Providers", "Add Provider", "Select Service"})
	e.app.SetStatusText(forms.StatusMenu)

	items := make([]forms.MenuItem, 0, len(subtypes))
	for _, st := range subtypes {
		subtype := st
		items = append(items, forms.MenuItem{
			Label: subtype.Name,
			OnSelect: func() {
				cfg.Subtype = subtype.ID
				if subtype.DefaultBaseURL != "" {
					cfg.BaseURL = subtype.DefaultBaseURL
				}
				e.promptProviderName(cfg)
			},
		})
	}

	menu := forms.NewMenuList(forms.MenuListConfig{
		Title:     "Select Service",
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
	defaultName := cfg.Type
	if cfg.Subtype != "" && cfg.Subtype != cfg.Type {
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
	L_info("llm editor: finishing add provider", "name", name, "type", cfg.Type)

	formDef := llm.ProviderConfigFormDef()

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

// editPurpose opens the purpose configuration editor
func (e *LLMEditor) editPurpose(name string, cfg *llm.LLMPurposeConfig) {
	L_info("llm editor: editing purpose", "name", name)

	var formDef forms.FormDef
	switch name {
	case "agent":
		formDef = llm.AgentPurposeFormDef()
	case "summarization":
		formDef = llm.SummarizationPurposeFormDef()
	case "embeddings":
		formDef = llm.EmbeddingsPurposeFormDef()
	default:
		L_error("llm editor: unknown purpose", "name", name)
		return
	}

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

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "LLM Configuration", name})
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
