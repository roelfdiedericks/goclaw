package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// CreateWorkspace initializes a new workspace at the given path
func CreateWorkspace(wsPath string) error {
	L_info("setup: creating workspace", "path", wsPath)

	// Create main workspace directory
	if err := os.MkdirAll(wsPath, 0750); err != nil {
		return fmt.Errorf("failed to create workspace directory: %w", err)
	}

	// Create subdirectories
	subdirs := []string{"memory", "skills", "media"}
	for _, dir := range subdirs {
		dirPath := filepath.Join(wsPath, dir)
		if err := os.MkdirAll(dirPath, 0750); err != nil {
			return fmt.Errorf("failed to create %s directory: %w", dir, err)
		}
		L_debug("setup: created directory", "path", dirPath)
	}

	// Copy template files (skip if they already exist)
	for _, name := range templateFiles {
		destPath := filepath.Join(wsPath, name)
		if err := writeTemplateIfMissing(destPath, name); err != nil {
			L_warn("setup: failed to write template", "file", name, "error", err)
			// Continue with other files, don't fail completely
		}
	}

	L_info("setup: workspace created successfully", "path", wsPath)
	return nil
}

// writeTemplateIfMissing writes a template file only if it doesn't exist
func writeTemplateIfMissing(destPath, templateName string) error {
	// Check if file already exists
	if _, err := os.Stat(destPath); err == nil {
		L_debug("setup: template already exists, skipping", "file", templateName)
		return nil
	}

	// Load and strip frontmatter
	content, err := LoadTemplateStripped(templateName)
	if err != nil {
		return fmt.Errorf("failed to load template %s: %w", templateName, err)
	}

	// Write file
	if err := os.WriteFile(destPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", destPath, err)
	}

	L_debug("setup: wrote template", "file", destPath)
	return nil
}

// ExpandPath expands ~ to home directory
func ExpandPath(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

// DefaultWorkspacePath returns the default workspace path
func DefaultWorkspacePath() string {
	p, _ := paths.DefaultWorkspace()
	return p
}

// DefaultGoclawRoot returns the default GoClaw root directory
func DefaultGoclawRoot() string {
	p, _ := paths.BaseDir()
	return p
}

// OpenClawGoclawRoot returns the path for side-by-side with OpenClaw
func OpenClawGoclawRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "goclaw")
}

// OpenClawConfigPath returns the path to OpenClaw's config file
func OpenClawConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "openclaw.json")
}

// OpenClawExists checks if OpenClaw is installed
func OpenClawExists() bool {
	_, err := os.Stat(OpenClawConfigPath())
	return err == nil
}

// GetOpenClawWorkspace returns OpenClaw's workspace path from openclaw.json
func GetOpenClawWorkspace() string {
	data, err := os.ReadFile(OpenClawConfigPath())
	if err != nil {
		return ""
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return ""
	}

	// Extract from agents.defaults.workspace
	if agents, ok := config["agents"].(map[string]interface{}); ok {
		if defaults, ok := agents["defaults"].(map[string]interface{}); ok {
			if ws, ok := defaults["workspace"].(string); ok {
				return ws
			}
		}
	}

	// Fallback to default OpenClaw workspace
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "workspace")
}
