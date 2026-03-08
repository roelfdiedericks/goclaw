// Package setup - VoiceLLM configuration editor
package setup

import (
	"fmt"
	"sort"
	"strconv"
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

	// Effects status - show preset name, "custom", or "disabled"
	effectsStr := "disabled"
	if e.cfg.Effects.Mode != "" && e.cfg.Effects.Mode != "none" {
		formData := e.cfg.Effects.ToEffectsFormData()
		presetName := formData.DetectPreset()
		if presetName == "custom" {
			effectsStr = "custom"
		} else if preset := voicellm.GetEffectsPreset(presetName); preset != nil {
			effectsStr = preset.Label
		} else {
			effectsStr = "custom"
		}
	}

	items := []forms.MenuItem{
		{Label: fmt.Sprintf("Providers (%d configured)", providerCount), OnSelect: e.showProviderList},
		{Label: fmt.Sprintf("Voice Settings (%s)", enabledStr), OnSelect: e.editSettings},
		{Label: fmt.Sprintf("Audio Effects (%s)", effectsStr), OnSelect: e.editEffects},
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

// editEffects opens the audio effects form with preset support
func (e *VoiceLLMEditor) editEffects() {
	L_info("voicellm editor: editing effects")

	// Convert to form-friendly format
	data := e.cfg.Effects.ToEffectsFormData()

	e.app.SetBreadcrumbs([]string{voiceLLMBreadcrumbBase, "VoiceLLM Configuration", "Audio Effects"})
	e.app.SetStatusText(forms.StatusForm)

	form := tview.NewForm()
	form.SetBorder(false)

	// Field references for in-place updates
	var modeDropdown *tview.DropDown
	var carrierField, mixField *tview.InputField
	var bitDepthField, downsampleField *tview.InputField

	// Color constants
	activeColor := tcell.ColorWhite
	inactiveColor := tcell.ColorGray

	// Helper to update field colors based on mode
	updateFieldColors := func() {
		ringActive := data.Mode == "ring" || data.Mode == "both"
		bitcrushActive := data.Mode == "bitcrush" || data.Mode == "both"

		if carrierField != nil {
			if ringActive {
				carrierField.SetLabelColor(activeColor)
				carrierField.SetFieldTextColor(activeColor)
			} else {
				carrierField.SetLabelColor(inactiveColor)
				carrierField.SetFieldTextColor(inactiveColor)
			}
		}
		if mixField != nil {
			if ringActive {
				mixField.SetLabelColor(activeColor)
				mixField.SetFieldTextColor(activeColor)
			} else {
				mixField.SetLabelColor(inactiveColor)
				mixField.SetFieldTextColor(inactiveColor)
			}
		}
		if bitDepthField != nil {
			if bitcrushActive {
				bitDepthField.SetLabelColor(activeColor)
				bitDepthField.SetFieldTextColor(activeColor)
			} else {
				bitDepthField.SetLabelColor(inactiveColor)
				bitDepthField.SetFieldTextColor(inactiveColor)
			}
		}
		if downsampleField != nil {
			if bitcrushActive {
				downsampleField.SetLabelColor(activeColor)
				downsampleField.SetFieldTextColor(activeColor)
			} else {
				downsampleField.SetLabelColor(inactiveColor)
				downsampleField.SetFieldTextColor(inactiveColor)
			}
		}
	}

	// Helper to update all field values from data (for preset changes)
	updateFieldValues := func() {
		if modeDropdown != nil {
			modeValues := []string{"none", "ring", "bitcrush", "both"}
			for i, v := range modeValues {
				if v == data.Mode {
					modeDropdown.SetCurrentOption(i)
					break
				}
			}
		}
		if carrierField != nil {
			carrierField.SetText(fmt.Sprintf("%.0f", data.CarrierFreq))
		}
		if mixField != nil {
			mixField.SetText(fmt.Sprintf("%.1f", data.Mix))
		}
		if bitDepthField != nil {
			bitDepthField.SetText(fmt.Sprintf("%d", data.BitDepth))
		}
		if downsampleField != nil {
			downsampleField.SetText(fmt.Sprintf("%d", data.Downsample))
		}
		updateFieldColors()
	}

	// Build preset options
	presetOptions := make([]string, len(voicellm.EffectsPresets))
	presetIndex := 0
	for i, p := range voicellm.EffectsPresets {
		presetOptions[i] = p.Label
		if p.Name == data.Preset {
			presetIndex = i
		}
	}

	// Re-entrancy guard for dropdown callbacks (see forms/AGENTS.md)
	initializing := true

	// Preset dropdown
	form.AddDropDown("Preset", presetOptions, presetIndex, func(option string, index int) {
		if initializing {
			return
		}
		if index >= 0 && index < len(voicellm.EffectsPresets) {
			selectedPreset := voicellm.EffectsPresets[index].Name
			if selectedPreset != "custom" {
				data.ApplyPreset(selectedPreset)
			} else {
				data.Preset = "custom"
			}
			updateFieldValues()
		}
	})

	// Mode dropdown
	modeOptions := []string{"None", "Ring Modulation", "Bitcrush", "Both"}
	modeValues := []string{"none", "ring", "bitcrush", "both"}
	modeIndex := 0
	for i, v := range modeValues {
		if v == data.Mode {
			modeIndex = i
			break
		}
	}
	form.AddDropDown("Mode", modeOptions, modeIndex, func(option string, index int) {
		if initializing {
			return
		}
		if index >= 0 && index < len(modeValues) {
			data.Mode = modeValues[index]
			data.Preset = "custom"
			updateFieldColors()
		}
	})
	modeDropdown, _ = form.GetFormItem(form.GetFormItemCount() - 1).(*tview.DropDown)

	// Section: Ring Modulation
	form.AddTextView("", "── Ring Modulation ──", 30, 1, false, false)

	form.AddInputField("Carrier Freq (Hz)", fmt.Sprintf("%.0f", data.CarrierFreq), 10, nil, func(text string) {
		if initializing {
			return
		}
		if v, err := strconv.ParseFloat(text, 64); err == nil {
			data.CarrierFreq = v
			data.Preset = "custom"
		}
	})
	carrierField, _ = form.GetFormItem(form.GetFormItemCount() - 1).(*tview.InputField)

	form.AddInputField("Mix (0-1)", fmt.Sprintf("%.1f", data.Mix), 10, nil, func(text string) {
		if initializing {
			return
		}
		if v, err := strconv.ParseFloat(text, 64); err == nil {
			data.Mix = v
			data.Preset = "custom"
		}
	})
	mixField, _ = form.GetFormItem(form.GetFormItemCount() - 1).(*tview.InputField)

	// Section: Bitcrush
	form.AddTextView("", "── Bitcrush ──", 30, 1, false, false)

	form.AddInputField("Bit Depth", fmt.Sprintf("%d", data.BitDepth), 10, nil, func(text string) {
		if initializing {
			return
		}
		if v, err := strconv.Atoi(text); err == nil {
			data.BitDepth = v
			data.Preset = "custom"
		}
	})
	bitDepthField, _ = form.GetFormItem(form.GetFormItemCount() - 1).(*tview.InputField)

	form.AddInputField("Downsample", fmt.Sprintf("%d", data.Downsample), 10, nil, func(text string) {
		if initializing {
			return
		}
		if v, err := strconv.Atoi(text); err == nil {
			data.Downsample = v
			data.Preset = "custom"
		}
	})
	downsampleField, _ = form.GetFormItem(form.GetFormItemCount() - 1).(*tview.InputField)

	// Apply initial colors
	updateFieldColors()

	// Done initializing - enable callbacks
	initializing = false

	// Buttons
	form.AddButton("Save", func() {
		e.cfg.Effects = data.ToEffectsConfig()
		e.onSave()
		L_info("voicellm editor: effects updated", "preset", data.Preset, "mode", e.cfg.Effects.Mode)
		e.Show()
	})
	form.AddButton("Cancel", func() {
		e.Show()
	})

	// Wrap in flex with header
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Audio Effects[white]\n\nSelect a preset or tweak individual values.\nInactive fields are dimmed based on Mode.")
	header.SetBorder(false)

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 5, 0, false).
		AddItem(form, 0, 1, true)

	e.app.SetContent(flex)
}
