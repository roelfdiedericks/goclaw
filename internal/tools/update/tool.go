package update

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	updatepkg "github.com/roelfdiedericks/goclaw/internal/update"
)

// Tool provides the goclaw_update tool for agents.
type Tool struct {
	currentVersion string
}

// NewTool creates a new update tool.
func NewTool(currentVersion string) *Tool {
	return &Tool{
		currentVersion: currentVersion,
	}
}

func (t *Tool) Name() string {
	return "goclaw_update"
}

func (t *Tool) Description() string {
	return "Check for GoClaw updates and optionally install them. Use action='check' to see if updates are available, action='update' to install. This tool runs outside the sandbox and can update the GoClaw binary."
}

func (t *Tool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"check", "update"},
				"description": "Action to perform: 'check' to see available updates, 'update' to download and install",
			},
			"channel": map[string]any{
				"type":        "string",
				"enum":        []string{"stable", "beta"},
				"default":     "stable",
				"description": "Release channel to check/update from",
			},
			"force": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Force reinstall even if already on latest version",
			},
		},
		"required": []string{"action"},
	}
}

type updateInput struct {
	Action  string `json:"action"`
	Channel string `json:"channel"`
	Force   bool   `json:"force"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params updateInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// Default channel
	if params.Channel == "" {
		params.Channel = "stable"
	}

	L_info("goclaw_update: tool invoked",
		"action", params.Action,
		"channel", params.Channel,
		"force", params.Force,
		"currentVersion", t.currentVersion,
	)

	// Check if system-managed
	if updatepkg.IsSystemManaged() {
		exePath, _ := updatepkg.GetExecutablePath()
		L_info("goclaw_update: system-managed installation, self-update disabled", "path", exePath)
		return fmt.Sprintf("GoClaw is installed at a system-managed location (%s).\n\n"+
			"Self-update is disabled to prevent conflicts with the package manager.\n\n"+
			"To update, use the system package manager or download the latest .deb from:\n"+
			"https://github.com/roelfdiedericks/goclaw/releases/latest", exePath), nil
	}

	updater := updatepkg.NewUpdater(t.currentVersion)

	// Check for updates
	info, err := updater.CheckForUpdate(params.Channel)
	if err != nil {
		L_error("goclaw_update: check failed", "error", err)
		return "", fmt.Errorf("failed to check for updates: %w", err)
	}

	var result strings.Builder

	result.WriteString(fmt.Sprintf("Current version: %s\n", info.CurrentVersion))
	result.WriteString(fmt.Sprintf("Latest %s version: %s\n", params.Channel, info.NewVersion))

	if !info.IsNewer && !params.Force {
		L_info("goclaw_update: already up to date",
			"current", info.CurrentVersion,
			"latest", info.NewVersion,
		)
		result.WriteString("\nGoClaw is already up to date.")
		return result.String(), nil
	}

	if info.IsNewer {
		L_info("goclaw_update: update available",
			"current", info.CurrentVersion,
			"new", info.NewVersion,
			"channel", info.Channel,
		)
		result.WriteString("\nA new version is available!\n")
	} else if params.Force {
		L_info("goclaw_update: force reinstall requested", "version", info.NewVersion)
		result.WriteString("\nForce reinstall requested.\n")
	}

	// Show changelog
	if info.Changelog != "" {
		result.WriteString("\nChangelog:\n")
		result.WriteString("----------\n")
		changelog := info.Changelog
		if len(changelog) > 2000 {
			changelog = changelog[:2000] + "\n..."
		}
		result.WriteString(changelog)
		result.WriteString("\n")
	}

	if params.Action == "check" {
		L_debug("goclaw_update: check-only mode, not installing")
		result.WriteString("\nTo install this update, call goclaw_update with action='update'.")
		return result.String(), nil
	}

	// Action is "update" - proceed with download and install
	L_info("goclaw_update: starting download", "version", info.NewVersion)
	result.WriteString("\nDownloading update...\n")

	binaryPath, err := updater.Download(info, nil)
	if err != nil {
		L_error("goclaw_update: download failed", "error", err)
		return "", fmt.Errorf("download failed: %w", err)
	}

	result.WriteString("Download complete. Checksum verified.\n")
	result.WriteString("Installing update...\n")

	L_info("goclaw_update: applying update", "binaryPath", binaryPath)

	// Apply update (will restart the process)
	// Note: This will replace the current process, so we won't return
	if err := updater.Apply(binaryPath, false); err != nil {
		L_error("goclaw_update: apply failed", "error", err)
		return "", fmt.Errorf("failed to apply update: %w", err)
	}

	// This line is reached only if noRestart was true (which it's not in this case)
	result.WriteString("Update installed! GoClaw will restart.\n")
	return result.String(), nil
}
