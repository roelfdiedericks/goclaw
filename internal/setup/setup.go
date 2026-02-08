// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// BackupCount is the number of backup versions to keep
const BackupCount = 5

// escKeyMap returns a keymap with Escape added to Quit and proper help text
func escKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	// Add escape to quit binding with help text
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "go back"))
	// Update submit/next help text to include esc hint
	km.Input.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", "submit • esc go back"))
	km.Input.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit • esc go back"))
	km.Select.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", "select • esc go back"))
	km.Select.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit • esc go back"))
	km.MultiSelect.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", "confirm • esc go back"))
	km.MultiSelect.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm • esc go back"))
	km.MultiSelect.Toggle = key.NewBinding(key.WithKeys(" ", "x"), key.WithHelp("space", "toggle"))
	km.Confirm.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", "next • esc go back"))
	km.Confirm.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit • esc go back"))
	return km
}

// newForm wraps huh.NewForm and applies escape-to-quit keymap with help shown
func newForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithKeyMap(escKeyMap()).WithShowHelp(true)
}

// suppressLogs sets log level to error-only and returns the previous level
func suppressLogs() int {
	level := logging.GetLevel()
	logging.SetLevel(logging.LevelError)
	return level
}

// restoreLogs restores the log level to the given value
func restoreLogs(level int) {
	logging.SetLevel(level)
}

// RunAuto detects mode based on existing config
func RunAuto() error {
	configPath := findExistingConfig()
	if configPath != "" {
		L_debug("setup: existing config found, running edit mode", "path", configPath)
		return RunEdit()
	}
	L_debug("setup: no config found, running wizard")
	return RunWizard()
}

// RunWizard runs the full setup wizard
func RunWizard() error {
	w := NewWizard()
	return w.Run()
}

// RunEdit runs the edit menu (requires existing config)
func RunEdit() error {
	configPath := findExistingConfig()
	if configPath == "" {
		return fmt.Errorf("no existing config found - run 'goclaw setup' to create one")
	}

	e := NewEditor(configPath)
	return e.Run()
}

// ShowConfig displays the current configuration
func ShowConfig() error {
	configPath := findExistingConfig()
	if configPath == "" {
		fmt.Println("No GoClaw configuration found.")
		fmt.Println()
		fmt.Println("Run 'goclaw setup' to create a configuration interactively.")
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	// Pretty print the JSON
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		// If not valid JSON, just print raw
		fmt.Println(string(data))
		return nil
	}

	pretty, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Configuration: %s\n\n", configPath)
	fmt.Println(string(pretty))
	return nil
}

// ShowConfigPath displays the path to the configuration file
func ShowConfigPath() error {
	configPath := findExistingConfig()
	if configPath == "" {
		fmt.Println("No GoClaw configuration found.")
		fmt.Println()
		fmt.Println("Locations searched:")
		fmt.Println("  ./goclaw.json")
		fmt.Println("  ~/.goclaw/goclaw.json")
		return nil
	}

	fmt.Println(configPath)
	return nil
}

// findExistingConfig searches for an existing goclaw.json
func findExistingConfig() string {
	home, _ := os.UserHomeDir()

	// Search locations
	paths := []string{
		"goclaw.json",                                 // current directory
		filepath.Join(home, ".goclaw", "goclaw.json"), // ~/.goclaw/
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath
		}
	}

	return ""
}

// GetConfigPath returns the path where config should be saved
// GoClaw always uses its own directory, regardless of OpenClaw presence
func GetConfigPath(openclawImport bool) string {
	home, _ := os.UserHomeDir()
	// Always use ~/.goclaw/ for GoClaw's own config
	// OpenClaw integration is via config options (inherit), not directory nesting
	return filepath.Join(home, ".goclaw", "goclaw.json")
}

// GetUsersPath returns the path where users.json should be saved
// Always alongside goclaw.json
func GetUsersPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "users.json")
}

// EnsureConfigDir creates the directory for the config file if needed
func EnsureConfigDir(configPath string) error {
	dir := filepath.Dir(configPath)
	return os.MkdirAll(dir, 0755)
}

// BackupFile creates a backup of the file with rotation.
// Keeps up to BackupCount versions:
//   - .bak.4 gets deleted (oldest)
//   - .bak.3 → .bak.4
//   - .bak.2 → .bak.3
//   - .bak.1 → .bak.2
//   - .bak → .bak.1
//   - current → .bak
func BackupFile(path string) error {
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // Nothing to backup
	}

	// Rotate existing backups
	rotateBackups(path)

	// Copy current to .bak
	backupPath := path + ".bak"
	if err := copyFile(path, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	L_debug("setup: created backup", "path", backupPath)
	return nil
}

// rotateBackups rotates backup files
func rotateBackups(path string) {
	if BackupCount <= 1 {
		return
	}

	backupBase := path + ".bak"
	maxIndex := BackupCount - 1 // 4

	// Delete oldest
	oldestPath := fmt.Sprintf("%s.%d", backupBase, maxIndex)
	if err := os.Remove(oldestPath); err != nil && !os.IsNotExist(err) {
		L_trace("setup: failed to remove oldest backup", "path", oldestPath, "error", err)
	}

	// Rotate: .bak.3 → .bak.4, .bak.2 → .bak.3, .bak.1 → .bak.2
	for i := maxIndex - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", backupBase, i)
		dst := fmt.Sprintf("%s.%d", backupBase, i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			L_trace("setup: failed to rotate backup", "src", src, "dst", dst, "error", err)
		}
	}

	// .bak → .bak.1
	if err := os.Rename(backupBase, backupBase+".1"); err != nil && !os.IsNotExist(err) {
		L_trace("setup: failed to rotate .bak to .bak.1", "error", err)
	}
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Get source file info for permissions
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
