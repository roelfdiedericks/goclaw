// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

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
		fmt.Println("Default locations searched:")
		fmt.Println("  ./goclaw.json")
		fmt.Println("  ~/.goclaw/goclaw.json")
		fmt.Println("  ~/.openclaw/goclaw/goclaw.json")
		return nil
	}

	fmt.Println(configPath)
	return nil
}

// findExistingConfig searches for an existing goclaw.json
func findExistingConfig() string {
	home, _ := os.UserHomeDir()

	// Priority order (highest first)
	paths := []string{
		"goclaw.json",                                        // current directory
		filepath.Join(home, ".goclaw", "goclaw.json"),        // ~/.goclaw/
		filepath.Join(home, ".openclaw", "goclaw", "goclaw.json"), // ~/.openclaw/goclaw/
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
// based on whether OpenClaw exists (side-by-side) or fresh install
func GetConfigPath(openclawImport bool) string {
	home, _ := os.UserHomeDir()

	if openclawImport {
		// Side-by-side with OpenClaw
		return filepath.Join(home, ".openclaw", "goclaw", "goclaw.json")
	}

	// Fresh install
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
