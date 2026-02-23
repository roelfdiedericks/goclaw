// Package setup - tview-based configuration editor
package setup

import (
	"encoding/json"
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/auth"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	goclawhttp "github.com/roelfdiedericks/goclaw/internal/http"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/tui"
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

	// Register command handlers for all components
	telegram.RegisterCommands()
	llm.RegisterCommands()
	media.RegisterCommands()
	tui.RegisterCommands()
	goclawhttp.RegisterCommands()
	session.RegisterCommands()
	skills.RegisterCommands()
	cron.RegisterCommands()
	auth.RegisterCommands()
	gateway.RegisterCommands()
	transcript.RegisterCommands()

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
		{Label: "Gateway Settings", OnSelect: e.editGateway},
		{Label: "Session Management", OnSelect: e.editSession},
		{IsSeparator: true, Label: "Channels"},
		{Label: "Telegram Bot", OnSelect: e.editTelegram},
		{Label: "HTTP Server", OnSelect: e.editHTTP},
		{IsSeparator: true, Label: "Services"},
		{Label: "Transcript Indexing", OnSelect: e.editTranscript},
		{Label: "Skills", OnSelect: e.editSkills},
		{Label: "Cron Jobs", OnSelect: e.editCron},
		{IsSeparator: true, Label: "System"},
		{Label: "Media Storage", OnSelect: e.editMedia},
		{Label: "TUI Settings", OnSelect: e.editTUI},
		{Label: "Auth Settings", OnSelect: e.editAuth},
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

// editHTTP opens the HTTP server configuration form
func (e *EditorTview) editHTTP() {
	L_info("editor: opening HTTP config")

	httpCfg := e.cfg.HTTP
	formDef := goclawhttp.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &httpCfg, "http", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.HTTP = httpCfg
			e.dirty = true
			L_info("editor: HTTP config updated")
		} else {
			L_info("editor: HTTP config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: HTTP form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "HTTP Server"})
	e.app.SetFormContent(content)
}

// editMedia opens the media storage configuration form
func (e *EditorTview) editMedia() {
	L_info("editor: opening media config")

	mediaCfg := e.cfg.Media
	formDef := media.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &mediaCfg, "media", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Media = mediaCfg
			e.dirty = true
			L_info("editor: media config updated")
		} else {
			L_info("editor: media config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: media form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Media Storage"})
	e.app.SetFormContent(content)
}

// editTUI opens the TUI settings configuration form
func (e *EditorTview) editTUI() {
	L_info("editor: opening TUI config")

	tuiCfg := e.cfg.TUI
	formDef := tui.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &tuiCfg, "tui", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.TUI = tuiCfg
			e.dirty = true
			L_info("editor: TUI config updated")
		} else {
			L_info("editor: TUI config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: TUI form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "TUI Settings"})
	e.app.SetFormContent(content)
}

// editSession opens the session management configuration form
func (e *EditorTview) editSession() {
	L_info("editor: opening session config")

	sessionCfg := e.cfg.Session
	formDef := session.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &sessionCfg, "session", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Session = sessionCfg
			e.dirty = true
			L_info("editor: session config updated")
		} else {
			L_info("editor: session config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: session form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Session Management"})
	e.app.SetFormContent(content)
}

// editSkills opens the skills configuration form
func (e *EditorTview) editSkills() {
	L_info("editor: opening skills config")

	skillsCfg := e.cfg.Skills
	formDef := skills.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &skillsCfg, "skills", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Skills = skillsCfg
			e.dirty = true
			L_info("editor: skills config updated")
		} else {
			L_info("editor: skills config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: skills form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Skills"})
	e.app.SetFormContent(content)
}

// editCron opens the cron jobs configuration form
func (e *EditorTview) editCron() {
	L_info("editor: opening cron config")

	cronCfg := e.cfg.Cron
	formDef := cron.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &cronCfg, "cron", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Cron = cronCfg
			e.dirty = true
			L_info("editor: cron config updated")
		} else {
			L_info("editor: cron config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: cron form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Cron Jobs"})
	e.app.SetFormContent(content)
}

// editAuth opens the auth settings configuration form
func (e *EditorTview) editAuth() {
	L_info("editor: opening auth config")

	authCfg := e.cfg.Auth
	formDef := auth.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &authCfg, "auth", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Auth = authCfg
			e.dirty = true
			L_info("editor: auth config updated")
		} else {
			L_info("editor: auth config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: auth form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Auth Settings"})
	e.app.SetFormContent(content)
}

// editGateway opens the gateway settings configuration form
func (e *EditorTview) editGateway() {
	L_info("editor: opening gateway config")

	// Gateway config is a bundle of multiple settings
	gatewayCfg := gateway.GatewayConfigBundle{
		Gateway:     e.cfg.Gateway,
		Agent:       e.cfg.Agent,
		PromptCache: e.cfg.PromptCache,
		Supervision: e.cfg.Supervision,
	}
	formDef := gateway.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &gatewayCfg, "gateway", func(result forms.TviewResult) {
		if result == forms.ResultSaved {
			e.cfg.Gateway = gatewayCfg.Gateway
			e.cfg.Agent = gatewayCfg.Agent
			e.cfg.PromptCache = gatewayCfg.PromptCache
			e.cfg.Supervision = gatewayCfg.Supervision
			e.dirty = true
			L_info("editor: gateway config updated")
		} else {
			L_info("editor: gateway config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: gateway form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Gateway Settings"})
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
