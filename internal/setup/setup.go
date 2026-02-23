// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// RunAuto detects mode based on existing config
func RunAuto() error {
	configPath, err := paths.ConfigPath()
	if err != nil {
		return fmt.Errorf("failed to check config path: %w", err)
	}
	if configPath != "" {
		L_debug("setup: existing config found, running edit mode", "path", configPath)
		return RunEdit()
	}
	L_debug("setup: no config found, running wizard")
	return RunWizard()
}

// RunWizard runs the full setup wizard (tview-based)
func RunWizard() error {
	return RunOnboardWizardTview()
}

// RunEdit runs the edit menu (requires existing config, tview-based)
func RunEdit() error {
	configPath, err := paths.ConfigPath()
	if err != nil {
		return fmt.Errorf("failed to check config path: %w", err)
	}
	if configPath == "" {
		return fmt.Errorf("no existing config found - run 'goclaw setup' to create one")
	}

	return RunEditorTview()
}

// ShowConfig displays the current configuration
func ShowConfig() error {
	configPath, err := paths.ConfigPath()
	if err != nil {
		return fmt.Errorf("failed to check config path: %w", err)
	}
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
	configPath, err := paths.ConfigPath()
	if err != nil {
		return fmt.Errorf("failed to check config path: %w", err)
	}
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
	owner := &user.UserEntry{
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

	users := user.UsersConfig{
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

// GetConfigPath returns the path where config should be saved
// GoClaw always uses its own directory, regardless of OpenClaw presence
func GetConfigPath(openclawImport bool) string {
	// Always use ~/.goclaw/ for GoClaw's own config
	// OpenClaw integration is via config options (inherit), not directory nesting
	p, _ := paths.DefaultConfigPath()
	return p
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
