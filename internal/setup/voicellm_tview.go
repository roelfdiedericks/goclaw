// Package setup - VoiceLLM configuration editor
package setup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

const voiceLLMBreadcrumbBase = "GoClaw Configuration"

// VoiceLLMEditor handles VoiceLLM configuration editing
type VoiceLLMEditor struct {
	app    *forms.TviewApp
	cfg    *voicellm.Config
	onSave func()
	onBack func()
}

// NewVoiceLLMEditor creates a new VoiceLLM editor
func NewVoiceLLMEditor(app *forms.TviewApp, cfg *voicellm.Config, onSave func(), onBack func()) *VoiceLLMEditor {
	return &VoiceLLMEditor{
		app:    app,
		cfg:    cfg,
		onSave: onSave,
		onBack: onBack,
	}
}

// Show displays the VoiceLLM configuration menu
func (e *VoiceLLMEditor) Show() {
	e.app.SetMenuContent(e.createMenu())
}

// createMenu creates the VoiceLLM submenu
func (e *VoiceLLMEditor) createMenu() *forms.MenuListResult {
	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration"})
	e.app.SetStatusText(forms.StatusMenu)

	providerCount := len(e.cfg.Providers)
	enabledStr := "disabled"
	if e.cfg.Enabled {
		enabledStr = "enabled"
	}

	items := []forms.MenuItem{
		{Label: fmt.Sprintf("Providers (%d configured)", providerCount), OnSelect: e.showProviderList},
		{Label: fmt.Sprintf("Voice Settings (%s)", enabledStr), OnSelect: e.editSettings},
		{Label: "Voice Prompt", OnSelect: e.editPrompt},
	}

	return forms.NewMenuList(forms.MenuListConfig{
		Items:  items,
		OnBack: e.onBack,
	})
}

// showProviderList shows the list of configured providers with preview
func (e *VoiceLLMEditor) showProviderList() {
	L_info("voicellm editor: showing provider list")

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Providers"})
	e.app.SetStatusText(forms.StatusList)

	// Build sorted list of provider names
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
		Preview: "Configure a new VoiceLLM provider connection.",
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
func (e *VoiceLLMEditor) buildProviderPreview(name string, cfg voicellm.ProviderConfig) string {
	var lines []string

	lines = append(lines, fmt.Sprintf("[yellow]Driver:[white] %s", cfg.Driver))
	lines = append(lines, fmt.Sprintf("[yellow]Voice:[white] %s", cfg.Voice))

	if cfg.BaseURL != "" {
		lines = append(lines, fmt.Sprintf("[yellow]Base URL:[white] %s", cfg.BaseURL))
	}

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("[yellow]Sample Rate:[white] %d Hz", cfg.SampleRate))
	lines = append(lines, fmt.Sprintf("[yellow]Audio Format:[white] %s", cfg.AudioFormat))

	// Show if this is the default
	if e.cfg.Default == name {
		lines = append(lines, "")
		lines = append(lines, "[green]★ Default Provider[-]")
	}

	return strings.Join(lines, "\n")
}

// editProvider opens the provider edit form
func (e *VoiceLLMEditor) editProvider(name string, cfg *voicellm.ProviderConfig) {
	L_info("voicellm editor: editing provider", "name", name)

	formDef := voicellm.ProviderConfigFormDef()
	formDef.Title = fmt.Sprintf("Provider: %s", name)

	content, err := forms.BuildFormContent(formDef, cfg, "voicellm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Providers[name] = *cfg
			e.onSave()
			L_info("voicellm editor: provider updated", "name", name)
		}
		e.showProviderList()
	}, e.app.App())
	if err != nil {
		L_error("voicellm editor: form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Providers", name})
	e.app.SetFormContent(content)
}

// addProvider shows the add provider wizard
func (e *VoiceLLMEditor) addProvider() {
	L_info("voicellm editor: adding new provider")

	newCfg := voicellm.ProviderConfig{
		SampleRate:  48000,
		AudioFormat: "pcm",
	}

	e.selectProviderDriver(&newCfg)
}

// selectProviderDriver shows driver selection for new provider
func (e *VoiceLLMEditor) selectProviderDriver(cfg *voicellm.ProviderConfig) {
	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Providers", "Add Provider"})
	e.app.SetStatusText(forms.StatusMenu)

	items := []forms.MenuItem{
		{
			Label: "xAI Voice",
			OnSelect: func() {
				cfg.Driver = "xai"
				cfg.Voice = "Eve"
				e.promptProviderName(cfg)
			},
		},
		{
			Label: "OpenAI Realtime",
			OnSelect: func() {
				cfg.Driver = "openai"
				cfg.Voice = "alloy"
				e.promptProviderName(cfg)
			},
		},
	}

	menu := forms.NewMenuList(forms.MenuListConfig{
		Title:     "Select Provider Type",
		Items:     items,
		OnBack:    e.showProviderList,
		BackLabel: "Cancel",
	})

	e.app.SetMenuContent(menu)
}

// promptProviderName prompts for provider alias name
func (e *VoiceLLMEditor) promptProviderName(cfg *voicellm.ProviderConfig) {
	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Providers", "Add Provider", "Name"})
	e.app.SetStatusText(forms.StatusForm)

	defaultName := cfg.Driver

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
			if _, exists := e.cfg.Providers[name]; exists {
				L_warn("voicellm editor: provider name already exists", "name", name)
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
func (e *VoiceLLMEditor) finishAddProvider(name string, cfg *voicellm.ProviderConfig) {
	L_info("voicellm editor: finishing add provider", "name", name, "driver", cfg.Driver)

	formDef := voicellm.ProviderConfigFormDef()

	content, err := forms.BuildFormContent(formDef, cfg, "voicellm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			if e.cfg.Providers == nil {
				e.cfg.Providers = make(map[string]voicellm.ProviderConfig)
			}
			e.cfg.Providers[name] = *cfg
			// If this is the first provider, make it the default
			if e.cfg.Default == "" {
				e.cfg.Default = name
			}
			e.onSave()
			L_info("voicellm editor: provider added", "name", name)
		}
		e.showProviderList()
	}, e.app.App())
	if err != nil {
		L_error("voicellm editor: form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Providers", name + " (new)"})
	e.app.SetFormContent(content)
}

// deleteProvider handles provider deletion with confirmation
func (e *VoiceLLMEditor) deleteProvider(name string) {
	L_info("voicellm editor: delete provider requested", "name", name)

	msg := fmt.Sprintf("Delete provider '%s'?", name)
	if e.cfg.Default == name {
		msg += "\n\nWARNING: This is the default provider!"
	}

	modal := tview.NewModal().
		SetText(msg).
		AddButtons([]string{"Delete", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Delete" {
				delete(e.cfg.Providers, name)
				// Clear default if we deleted it
				if e.cfg.Default == name {
					e.cfg.Default = ""
				}
				e.onSave()
				L_info("voicellm editor: provider deleted", "name", name)
			}
			e.showProviderList()
		})

	e.app.SetContent(modal)
}

// renameProvider handles provider renaming
func (e *VoiceLLMEditor) renameProvider(oldName string) {
	L_info("voicellm editor: rename provider requested", "name", oldName)

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Providers", "Rename"})
	e.app.SetStatusText(forms.StatusForm)

	input := tview.NewInputField().
		SetLabel("New name: ").
		SetText(oldName).
		SetFieldWidth(40)

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			newName := strings.TrimSpace(input.GetText())
			if newName == "" || newName == oldName {
				e.showProviderList()
				return
			}
			if _, exists := e.cfg.Providers[newName]; exists {
				L_warn("voicellm editor: provider name already exists", "name", newName)
				e.showProviderList()
				return
			}

			// Perform rename
			cfg := e.cfg.Providers[oldName]
			delete(e.cfg.Providers, oldName)
			e.cfg.Providers[newName] = cfg

			// Update default reference
			if e.cfg.Default == oldName {
				e.cfg.Default = newName
			}

			e.onSave()
			L_info("voicellm editor: provider renamed", "old", oldName, "new", newName)
			e.showProviderList()
		} else if key == tcell.KeyEscape {
			e.showProviderList()
		}
	})

	form := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().SetText(fmt.Sprintf("Rename provider '%s'", oldName)), 1, 0, false).
		AddItem(nil, 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(nil, 0, 1, false)

	e.app.SetContent(form)
}

// editSettings opens the voice settings form
func (e *VoiceLLMEditor) editSettings() {
	L_info("voicellm editor: editing settings")

	// Build provider options for default dropdown
	providerOptions := []forms.Option{{Label: "(none)", Value: ""}}
	providerNames := make([]string, 0, len(e.cfg.Providers))
	for name := range e.cfg.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, name := range providerNames {
		providerOptions = append(providerOptions, forms.Option{Label: name, Value: name})
	}

	// Create a settings struct that matches the form fields
	type settingsForm struct {
		Default     string `json:"default"`
		Enabled     bool   `json:"enabled"`
		ServerVAD   bool   `json:"serverVAD"`
		IdleTimeout int    `json:"idleTimeout"`
	}
	settings := &settingsForm{
		Default:     e.cfg.Default,
		Enabled:     e.cfg.Enabled,
		ServerVAD:   e.cfg.ServerVAD,
		IdleTimeout: e.cfg.IdleTimeout,
	}

	formDef := voicellm.SettingsFormDef(providerOptions)

	content, err := forms.BuildFormContent(formDef, settings, "voicellm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Default = settings.Default
			e.cfg.Enabled = settings.Enabled
			e.cfg.ServerVAD = settings.ServerVAD
			e.cfg.IdleTimeout = settings.IdleTimeout
			e.onSave()
			L_info("voicellm editor: settings updated")
		}
		e.Show()
	}, e.app.App())
	if err != nil {
		L_error("voicellm editor: settings form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Voice Settings"})
	e.app.SetFormContent(content)
}

// editPrompt opens the voice prompt form
func (e *VoiceLLMEditor) editPrompt() {
	L_info("voicellm editor: editing prompt")

	// Convert to form-friendly format
	promptForm := e.cfg.Prompt.ToPromptConfigForm()

	formDef := voicellm.PromptConfigFormDef()

	content, err := forms.BuildFormContent(formDef, promptForm, "voicellm", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Prompt = promptForm.ToPromptConfig()
			e.onSave()
			L_info("voicellm editor: prompt updated")
		}
		e.Show()
	}, e.app.App())
	if err != nil {
		L_error("voicellm editor: prompt form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Voice Prompt"})
	e.app.SetFormContent(content)
}
