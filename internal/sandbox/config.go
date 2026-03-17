package sandbox

import (
	"fmt"
	"runtime"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Sandbox modes
const (
	ModeEphemeral     = "ephemeral"      // No persistent home dirs - maximum security
	ModeVolumes       = "volumes"        // Specific directories persisted via isolated mounts
	ModeHome          = "home"           // Full isolated home directory - everything persists
	ModeAutoDocsRead  = "autodocs-read"  // Sandbox home plus dynamic read-only non-hidden home directories
	ModeAutoDocsWrite = "autodocs-write" // Sandbox home plus dynamic read-write non-hidden home directories
)

// Config holds top-level sandbox configuration.
type Config struct {
	General    GeneralConfig    `json:"general"`
	Bubblewrap BubblewrapConfig `json:"bubblewrap"`
	Seatbelt   SeatbeltConfig   `json:"seatbelt"`
}

// GeneralConfig holds shared sandbox policy across backends.
type GeneralConfig struct {
	Enabled          bool     `json:"enabled" default:"true"`
	Mode             string   `json:"mode" default:"home"` // "ephemeral", "volumes", "home"
	DataDir          string   `json:"dataDir"`             // Backing directory root (default: ~/.goclaw/sandbox)
	ExtraPaths       []string `json:"extraPaths"`          // Additional PATH entries for sandbox
	ExecEnabled      bool     `json:"execEnabled" default:"true"`
	BrowserEnabled   bool     `json:"browserEnabled" default:"true"`
	FileToolsEnabled bool     `json:"fileToolsEnabled" default:"true"`
}

// BubblewrapConfig holds Linux-specific bubblewrap settings.
type BubblewrapConfig struct {
	Path    string   `json:"path"`    // Custom bwrap binary path (empty = search PATH)
	Volumes []string `json:"volumes"` // Isolated mount points (runtime default)
}

// SeatbeltConfig holds Darwin-specific seatbelt settings.
type SeatbeltConfig struct {
	Path string `json:"path"` // Custom sandbox-exec path (empty = search PATH)
}

// GetMode returns the configured mode with default fallback.
func (c *Config) GetMode() string {
	if c.General.Mode == "" {
		return ModeHome
	}
	return c.General.Mode
}

// IsAutoDocsMode reports whether the config uses one of the autodocs variants.
func (c *Config) IsAutoDocsMode() bool {
	mode := c.GetMode()
	return mode == ModeAutoDocsRead || mode == ModeAutoDocsWrite
}

// IsAutoDocsWriteMode reports whether autodocs directories are writable.
func (c *Config) IsAutoDocsWriteMode() bool {
	return c.GetMode() == ModeAutoDocsWrite
}

// IsEnabled returns whether sandboxing is enabled globally.
func (c *Config) IsEnabled() bool {
	return c.General.Enabled
}

// IsExecEnabled returns whether exec sandboxing is enabled.
func (c *Config) IsExecEnabled() bool {
	return c.General.Enabled && c.General.ExecEnabled
}

// IsBrowserEnabled returns whether browser sandboxing is enabled.
func (c *Config) IsBrowserEnabled() bool {
	return c.General.Enabled && c.General.BrowserEnabled
}

// IsFileToolsEnabled returns whether file tool sandboxing is enabled.
func (c *Config) IsFileToolsEnabled() bool {
	return c.General.Enabled && c.General.FileToolsEnabled
}

// GetDataDir returns the shared sandbox data directory.
func (c *Config) GetDataDir() string {
	return c.General.DataDir
}

// GetExtraPaths returns shared extra PATH entries.
func (c *Config) GetExtraPaths() []string {
	return c.General.ExtraPaths
}

// GetVolumes returns Linux bubblewrap volume mappings.
func (c *Config) GetVolumes() []string {
	return c.Bubblewrap.Volumes
}

// GetBackendPath returns the configured backend binary path for the current platform.
func (c *Config) GetBackendPath() string {
	switch runtime.GOOS {
	case "darwin":
		return c.Seatbelt.Path
	default:
		return c.Bubblewrap.Path
	}
}

// DefaultVolumes returns the built-in sandbox volume mount points.
func DefaultVolumes() []string {
	return []string{"~/.local", "~/.config", "~/.cache"}
}

const configPath = "sandbox"

// ConfigFormDef returns the form definition for sandbox configuration.
func ConfigFormDef() forms.FormDef {
	modeOptions := SupportedModeOptions()
	backendLabel := CurrentBackendDisplayName()
	backendPathFieldName := BackendPathFieldName()
	backendPathDesc := BackendPathDescription()
	sections := []forms.Section{
		{
			Title: "General",
			Fields: []forms.Field{
				{
					Name:  "general.enabled",
					Title: "Enable Sandboxing",
					Type:  forms.Toggle,
					Desc:  "Master switch for sandboxing across exec, browser, and file tools",
				},
				{
					Name:  "general.dataDir",
					Title: "Data Directory",
					Type:  forms.Text,
					Desc:  "Backing storage root (default: ~/.goclaw/sandbox)",
				},
				{
					Name:  "general.extraPaths",
					Title: "Extra PATH Entries",
					Type:  forms.StringList,
					Desc:  "Additional directories to add to sandbox PATH",
				},
			},
		},
		{
			Title:    "Sandbox Categories",
			ShowWhen: "general.enabled=true",
			Fields: []forms.Field{
				{
					Name:  "general.execEnabled",
					Title: "Enable Exec Sandboxing",
					Type:  forms.Toggle,
					Desc:  "Apply OS sandboxing to exec tool commands",
				},
				{
					Name:  "general.browserEnabled",
					Title: "Enable Browser Sandboxing",
					Type:  forms.Toggle,
					Desc:  "Apply OS sandboxing to managed browser launches",
				},
				{
					Name:  "general.fileToolsEnabled",
					Title: "Enable File Tool Sandboxing",
					Type:  forms.Toggle,
					Desc:  "Restrict read, write, edit, and jq file-mode to sandboxed paths",
				},
			},
		},
		{
			Title:    "Sandbox Categories",
			ShowWhen: "general.enabled=false",
			Desc:     "Exec, browser, and file tool sandboxing are not applicable while sandboxing is disabled.",
		},
		{
			Title:    "Sandbox Mode",
			ShowWhen: "general.enabled=true",
			Fields: []forms.Field{
				{
					Name:    "general.mode",
					Title:   "Sandbox Mode",
					Type:    forms.Select,
					Desc:    "How home directories are handled inside the sandbox",
					Options: modeOptions,
				},
			},
		},
		{
			Title:    "Sandbox Mode",
			ShowWhen: "general.enabled=false",
			Desc:     "Sandbox mode: not applicable while sandboxing is disabled.",
		},
	}

	switch CurrentSandboxBackend() {
	case BackendBubblewrap:
		sections = append(sections,
			forms.Section{
				Title: backendLabel,
				Fields: []forms.Field{
					{
						Name:  backendPathFieldName,
						Title: "Bubblewrap Binary Path",
						Type:  forms.Text,
						Desc:  backendPathDesc,
					},
				},
			},
			forms.Section{
				Title:    "Volume Mounts",
				ShowWhen: "general.mode=volumes",
				Fields: []forms.Field{
					{
						Name:  "bubblewrap.volumes",
						Title: "Sandbox Volumes",
						Type:  forms.StringList,
						Desc:  "Home directory paths to persist (e.g., ~/.local, ~/.config)",
					},
				},
			},
		)
	case BackendSeatbelt:
		sections = append(sections, forms.Section{
			Title: backendLabel,
			Desc:  "macOS supports policy-managed Home and Autodocs modes. Volumes and Ephemeral are not available.",
			Fields: []forms.Field{
				{
					Name:  backendPathFieldName,
					Title: "Seatbelt Binary Path",
					Type:  forms.Text,
					Desc:  backendPathDesc,
				},
			},
		})
	default:
		sections = append(sections, forms.Section{
			Title: backendLabel,
			Desc:  "No managed sandbox backend is available on this platform.",
		})
	}

	return forms.FormDef{
		Title:       "Sandbox",
		Description: "Configure agent sandboxing and filesystem isolation",
		Sections:    sections,
		Actions: []forms.ActionDef{
			{Name: "apply", Label: "Apply"},
		},
	}
}

// RegisterCommands registers bus commands for sandbox config.
func RegisterCommands() {
	bus.RegisterCommand(configPath, "apply", handleApply)
}

// UnregisterCommands unregisters bus commands for sandbox config.
func UnregisterCommands() {
	bus.UnregisterComponent(configPath)
}

func handleApply(cmd bus.Command) bus.CommandResult {
	cfg, ok := cmd.Payload.(*Config)
	if !ok {
		return bus.CommandResult{
			Success: false,
			Error:   fmt.Errorf("expected *Config, got %T", cmd.Payload),
		}
	}

	L_info("sandbox: config applied",
		"backend", CurrentSandboxBackend(),
		"enabled", cfg.General.Enabled,
		"mode", cfg.GetMode(),
		"backendPath", cfg.GetBackendPath(),
		"volumes", len(cfg.Bubblewrap.Volumes))
	bus.PublishEvent(configPath+".config.applied", cfg)

	return bus.CommandResult{Success: true, Message: "Config applied"}
}
