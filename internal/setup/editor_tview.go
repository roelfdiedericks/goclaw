// Package setup - tview-based configuration editor
package setup

import (
	"encoding/json"
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/telegram"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
)

// EditorTview is the tview-based configuration editor
type EditorTview struct {
	app        *forms.TviewApp
	configPath string
	cfg        *config.Config
	dirty      bool // tracks if config has been modified
}

// NewEditorTview creates a new tview editor
func NewEditorTview(configPath string) *EditorTview {
	return &EditorTview{
		configPath: configPath,
	}
}

// Run executes the editor
func (e *EditorTview) Run() error {
	// Load config
	if err := e.loadConfig(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Register command handlers
	telegram.RegisterCommands()
	llm.RegisterCommands()
	// Note: transcript actions are registered elsewhere or not yet implemented

	// Create UI
	e.app = forms.NewTviewApp("GoClaw Configuration")

	// Show main menu
	e.showMainMenu()

	e.app.SetOnEscape(func() {
		e.confirmExit()
	})

	// Run
	if err := e.app.RunWithCleanup(); err != nil {
		return err
	}

	// Print config on exit (for testing)
	e.printConfig()

	return nil
}

// loadConfig loads the configuration file
func (e *EditorTview) loadConfig() error {
	loadResult, err := config.Load()
	if err != nil {
		// No config found - use empty config (will need to be configured)
		L_info("editor: no config found, using empty config")
		e.cfg = &config.Config{}
		return nil
	}

	e.cfg = loadResult.Config
	e.configPath = loadResult.SourcePath
	L_info("editor: loaded config", "path", e.configPath)
	return nil
}

// createMenu creates the main menu
func (e *EditorTview) createMenu() *forms.MenuListResult {
	// Set breadcrumbs for main menu
	e.app.SetBreadcrumbs([]string{"GoClaw Configuration"})
	e.app.SetStatusText(forms.StatusMenu)

	items := []forms.MenuItem{
		{Label: "LLM Configuration", OnSelect: e.editLLM},
		{Label: "Transcript Indexing", OnSelect: e.editTranscript},
		{Label: "Telegram Bot", OnSelect: e.editTelegram},
		{Label: "Users", OnSelect: func() {
			L_info("editor: users - not implemented yet")
		}},
		{Label: "Browser", OnSelect: func() {
			L_info("editor: browser - not implemented yet")
		}},
		{IsSeparator: true},
		{Label: "Save Changes", OnSelect: e.saveConfig},
		{Label: "Exit", OnSelect: e.confirmExit},
	}

	return forms.NewMenuList(forms.MenuListConfig{
		Items:    items,
		OnBack:   e.confirmExit,
		ShowBack: false, // Exit is already in the menu
	})
}

// showMainMenu displays the main menu with proper focus
func (e *EditorTview) showMainMenu() {
	e.app.SetMenuContent(e.createMenu())
}

// editLLM opens the LLM configuration menu
func (e *EditorTview) editLLM() {
	L_info("editor: opening LLM config")

	llmEditor := NewLLMEditor(e.app, &e.cfg.LLM,
		func() { e.dirty = true },
		e.showMainMenu,
	)

	llmEditor.Show()
}

// editTranscript opens the transcript configuration form
func (e *EditorTview) editTranscript() {
	L_info("editor: opening transcript config")

	// Get the transcript config from main config
	transcriptCfg := e.cfg.Transcript

	// Get form definition (needs config for nested form initialization)
	formDef := transcript.ConfigFormDef(transcriptCfg)

	// Build inline form content
	content, err := forms.BuildFormContent(formDef, &transcriptCfg, "transcript", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Transcript = transcriptCfg
			e.dirty = true
			L_info("editor: transcript config updated")
		} else {
			L_info("editor: transcript config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: transcript form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Transcripts"})
	e.app.SetFormContent(content)
}

// editTelegram opens the telegram configuration form
func (e *EditorTview) editTelegram() {
	L_info("editor: opening telegram config")

	// Get the telegram config from main config
	telegramCfg := e.cfg.Telegram

	// Get form definition
	formDef := telegram.ConfigFormDef()

	// Build inline form content
	content, err := forms.BuildFormContent(formDef, &telegramCfg, "telegram", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Telegram = telegramCfg
			e.dirty = true
			L_info("editor: telegram config updated")
		} else {
			L_info("editor: telegram config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: telegram form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Telegram"})
	e.app.SetFormContent(content)
}

// saveConfig saves the configuration (placeholder - just logs for now)
func (e *EditorTview) saveConfig() {
	if !e.dirty {
		L_info("editor: no changes to save")
		return
	}

	// TODO: Implement actual save with backup
	// For now, just mark as saved
	L_info("editor: save requested (not implemented - will print on exit)")
	L_info("editor: would save to", "path", e.configPath)
}

// confirmExit handles exit with unsaved changes check
func (e *EditorTview) confirmExit() {
	if e.dirty {
		L_warn("editor: exiting with unsaved changes")
	}
	e.app.Stop()
}

// printConfig prints the current config as JSON (for testing)
func (e *EditorTview) printConfig() {
	if !e.dirty {
		fmt.Println("\nNo changes made.")
		return
	}

	fmt.Println("\n=== Configuration (would be saved) ===")

	data, err := json.MarshalIndent(e.cfg, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling config: %v\n", err)
		return
	}

	fmt.Println(string(data))
	fmt.Println("\n(Note: Not actually saved during testing)")
}

// RunEditorTview is the entry point for the tview editor
func RunEditorTview() error {
	configPath := findExistingConfig()
	editor := NewEditorTview(configPath)
	return editor.Run()
}
