// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// BackupCount is the number of backup versions to keep
const BackupCount = 5

// blueTheme creates a blue-colored theme for huh forms (no pink/purple!)
func blueTheme() *huh.Theme {
	t := huh.ThemeBase()

	// Colors matching our website
	blue := lipgloss.Color("39")      // Primary blue
	cyan := lipgloss.Color("87")      // Cyan/light blue
	white := lipgloss.Color("255")    // White
	gray := lipgloss.Color("245")     // Gray
	darkGray := lipgloss.Color("240") // Dark gray

	// Focused styles - use blue/white instead of pink
	t.Focused.Title = t.Focused.Title.Foreground(cyan)
	t.Focused.Description = t.Focused.Description.Foreground(gray)
	t.Focused.Base = t.Focused.Base.BorderForeground(blue)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(white)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(white)
	t.Focused.Option = t.Focused.Option.Foreground(gray)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Background(blue).Foreground(white)
	t.Focused.BlurredButton = t.Focused.BlurredButton.Background(darkGray).Foreground(white)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(white)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(gray)

	// Blurred styles
	t.Blurred.Title = t.Blurred.Title.Foreground(gray)
	t.Blurred.Description = t.Blurred.Description.Foreground(darkGray)
	t.Blurred.SelectSelector = t.Blurred.SelectSelector.Foreground(gray)

	return t
}

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

// formKeyMap returns a keymap optimized for multi-field forms with arrow navigation
func formKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	// Escape to go back
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "back"))
	// Arrow keys + tab for field navigation
	km.Input.Prev = key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("↑/shift+tab", "prev"))
	km.Input.Next = key.NewBinding(key.WithKeys("tab", "down", "enter"), key.WithHelp("↓/tab/enter", "next"))
	km.Input.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next"))
	km.Text.Prev = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev"))
	km.Text.Next = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next"))
	km.Confirm.Prev = key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("↑", "prev"))
	km.Confirm.Next = key.NewBinding(key.WithKeys("tab", "down", "enter"), key.WithHelp("↓/enter", "next"))
	return km
}

// newForm wraps huh.NewForm and applies escape-to-quit keymap with help shown
func newForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).
		WithKeyMap(escKeyMap()).
		WithShowHelp(true).
		WithTheme(blueTheme())
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

// GenerateDefault outputs a default configuration template to stdout
func GenerateDefault() error {
	cfg := config.DefaultConfig()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

// GenerateDefaultUsers outputs a default users.json template to stdout
// If withPassword is true, generates a random password and includes the hash
func GenerateDefaultUsers(withPassword bool) error {
	owner := &config.UserEntry{
		Name: "Owner",
		Role: "owner",
	}

	if withPassword {
		// Generate random password
		password, err := generateRandomPassword(16)
		if err != nil {
			return fmt.Errorf("failed to generate password: %w", err)
		}

		// Hash it
		hash, err := user.HashPassword(password)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		owner.HTTPPasswordHash = hash

		// Print password to stderr so JSON goes to stdout
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "=== Generated Credentials ===")
		fmt.Fprintln(os.Stderr, "Username: owner")
		fmt.Fprintln(os.Stderr, "Password:", password)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Save this password - it cannot be recovered!")
		fmt.Fprintln(os.Stderr, "")
	}

	users := config.UsersConfig{
		"owner": owner,
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal users: %w", err)
	}

	fmt.Println(string(data))

	if !withPassword {
		// Print warning to stderr
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "NOTE: The generated users.json has no authentication credentials.")
		fmt.Fprintln(os.Stderr, "To enable HTTP authentication, run: goclaw user set-password owner")
		fmt.Fprintln(os.Stderr, "To enable Telegram, add your telegram_id to the owner entry.")
	}

	return nil
}

// generateRandomPassword creates a random alphanumeric password
func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}

// findExistingConfig searches for an existing goclaw.json
func findExistingConfig() string {
	home, _ := os.UserHomeDir()

	// Search locations
	paths := []string{
		"goclaw.json", // current directory
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
	return os.MkdirAll(dir, 0750)
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
