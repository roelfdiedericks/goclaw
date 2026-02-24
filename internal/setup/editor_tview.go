// Package setup - tview-based configuration editor
package setup

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/auth"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
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

	// Register command handlers for all components
	telegramconfig.RegisterCommands()
	llm.RegisterCommands()
	media.RegisterCommands()
	tuiconfig.RegisterCommands()
	httpconfig.RegisterCommands()
	session.RegisterCommands()
	skills.RegisterCommands()
	cron.RegisterCommands()
	auth.RegisterCommands()
	gateway.RegisterCommands()
	transcript.RegisterCommands()
	stt.RegisterCommands()

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
		{Label: "Speech-to-Text (STT)", OnSelect: e.editSTT},
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
		{Label: "Backups", OnSelect: e.showBackups},
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
		if result == forms.ResultAccepted {
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
	telegramCfg := e.cfg.Channels.Telegram

	// Get form definition
	formDef := telegramconfig.ConfigFormDef()

	// Build inline form content
	content, err := forms.BuildFormContent(formDef, &telegramCfg, "channels.telegram", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Channels.Telegram = telegramCfg
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

	httpCfg := e.cfg.Channels.HTTP
	formDef := httpconfig.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &httpCfg, "channels.http", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Channels.HTTP = httpCfg
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
		if result == forms.ResultAccepted {
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

// editSTT opens the STT configuration form
func (e *EditorTview) editSTT() {
	L_info("editor: opening STT config")

	sttCfg := e.cfg.STT
	// Pass current modelsDir to scan for available whisper models
	formDef := stt.ConfigFormDef(sttCfg.WhisperCpp.ModelsDir)

	content, err := forms.BuildFormContent(formDef, &sttCfg, "stt", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.STT = sttCfg
			e.dirty = true
			L_info("editor: STT config updated")
		} else {
			L_info("editor: STT config cancelled")
		}
		e.showMainMenu()
	}, e.app.App())
	if err != nil {
		L_error("editor: STT form error", "error", err)
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Speech-to-Text"})
	e.app.SetFormContent(content)
}

// editTUI opens the TUI settings configuration form
func (e *EditorTview) editTUI() {
	L_info("editor: opening TUI config")

	tuiCfg := e.cfg.Channels.TUI
	formDef := tuiconfig.ConfigFormDef()

	content, err := forms.BuildFormContent(formDef, &tuiCfg, "channels.tui", func(result forms.TviewResult) {
		if result == forms.ResultAccepted {
			e.cfg.Channels.TUI = tuiCfg
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
		if result == forms.ResultAccepted {
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
		if result == forms.ResultAccepted {
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
		if result == forms.ResultAccepted {
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
		if result == forms.ResultAccepted {
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
		if result == forms.ResultAccepted {
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

// saveConfig saves the configuration with backup
func (e *EditorTview) saveConfig() {
	if !e.dirty {
		L_info("editor: no changes to save")
		e.app.SetStatusText("No changes to save")
		return
	}

	// Determine save path
	savePath := e.configPath
	if savePath == "" {
		var err error
		savePath, err = paths.DefaultConfigPath()
		if err != nil {
			L_error("editor: failed to get config path", "error", err)
			e.app.SetStatusText("Error: failed to get config path")
			return
		}
	}

	// Ensure parent directory exists
	if err := paths.EnsureParentDir(savePath); err != nil {
		L_error("editor: failed to create config directory", "error", err)
		e.app.SetStatusText("Error: failed to create directory")
		return
	}

	// Save with backup
	if err := config.BackupAndWriteJSON(savePath, e.cfg, config.DefaultBackupCount); err != nil {
		L_error("editor: failed to save config", "path", savePath, "error", err)
		e.app.SetStatusText("Error: failed to save config")
		return
	}

	e.dirty = false
	e.configPath = savePath
	L_info("editor: config saved", "path", savePath)
	e.app.SetStatusText("Saved to " + savePath)
}

// showBackups displays available config backups and allows restoration
func (e *EditorTview) showBackups() {
	// Determine config path
	configPath := e.configPath
	if configPath == "" {
		var err error
		configPath, err = paths.DefaultConfigPath()
		if err != nil {
			L_error("editor: failed to get config path", "error", err)
			e.app.SetStatusText("Error: failed to get config path")
			return
		}
	}

	// List available backups
	backups := config.ListBackups(configPath)
	if len(backups) == 0 {
		e.app.SetStatusText("No backups available")
		return
	}

	// Build menu items from backups
	items := make([]forms.MenuItem, 0, len(backups)+1)
	for _, backup := range backups {
		b := backup // capture for closure
		name := ".bak"
		if b.Index > 0 {
			name = fmt.Sprintf(".bak.%d", b.Index)
		}
		label := fmt.Sprintf("%s (%s)", name, b.ModTime.Format("2006-01-02 15:04:05"))
		items = append(items, forms.MenuItem{
			Label: label,
			OnSelect: func() {
				e.restoreBackup(configPath, b.Index)
			},
		})
	}
	items = append(items, forms.MenuItem{IsSeparator: true})
	items = append(items, forms.MenuItem{Label: "Back", OnSelect: e.showMainMenu})

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Backups"})
	e.app.SetStatusText("Select a backup to restore")
	e.app.SetMenuContent(forms.NewMenuList(forms.MenuListConfig{
		Items:  items,
		OnBack: e.showMainMenu,
	}))
}

// restoreBackup restores configuration from a backup file
func (e *EditorTview) restoreBackup(configPath string, backupIndex int) {
	if err := config.RestoreBackup(configPath, backupIndex); err != nil {
		L_error("editor: failed to restore backup", "index", backupIndex, "error", err)
		e.app.SetStatusText("Error: failed to restore backup")
		return
	}

	L_info("editor: backup restored", "index", backupIndex)
	e.app.SetStatusText("Backup restored - reload to see changes")

	// Reload config
	if err := e.loadConfig(); err != nil {
		L_error("editor: failed to reload config", "error", err)
		e.app.SetStatusText("Restored but failed to reload - restart editor")
		return
	}

	e.dirty = false
	e.app.SetStatusText("Backup restored successfully")
	e.showMainMenu()
}

// confirmExit handles exit with unsaved changes check
func (e *EditorTview) confirmExit() {
	if !e.dirty {
		e.app.Stop()
		return
	}

	e.app.SetBreadcrumbs([]string{"GoClaw Configuration", "Unsaved Changes"})
	e.app.SetStatusText("You have unsaved changes")
	e.app.SetMenuContent(forms.NewMenuList(forms.MenuListConfig{
		Items: []forms.MenuItem{
			{Label: "Save & Exit", OnSelect: func() {
				e.saveConfig()
				e.app.Stop()
			}},
			{Label: "Discard & Exit", OnSelect: func() {
				L_warn("editor: discarding unsaved changes")
				e.app.Stop()
			}},
			{IsSeparator: true},
			{Label: "Back", OnSelect: e.showMainMenu},
		},
		OnBack: e.showMainMenu,
	}))
}

// RunEditorTview is the entry point for the tview editor
func RunEditorTview() error {
	configPath, _ := paths.ConfigPath()
	editor := NewEditorTview(configPath)
	return editor.Run()
}
