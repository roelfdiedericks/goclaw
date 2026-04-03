package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// CreateWorkspace initializes a new workspace at the given path
func CreateWorkspace(wsPath string) error {
	L_info("setup: ensuring workspace", "path", wsPath)

	// BOOTSTRAP.md is only for a fresh identity. If SOUL.md already exists
	// before we start repairing the workspace, do not recreate BOOTSTRAP.md.
	hadSoulAtStart := fileExists(filepath.Join(wsPath, "SOUL.md"))

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

	if err := createMissingWorkspaceTemplates(wsPath, hadSoulAtStart); err != nil {
		L_warn("setup: failed while creating missing templates", "path", wsPath, "error", err)
	}
	if err := CheckUpdateWorkspace(wsPath); err != nil {
		L_warn("setup: failed while checking template updates", "path", wsPath, "error", err)
	}

	L_info("setup: workspace ensured successfully", "path", wsPath, "bootstrapCreated", !hadSoulAtStart)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

func createMissingWorkspaceTemplates(wsPath string, hadSoulAtStart bool) error {
	var firstErr error

	for _, name := range templateFiles {
		if name == bootstrapTemplateName && hadSoulAtStart {
			L_debug("setup: bootstrap skipped because soul exists", "path", filepath.Join(wsPath, name))
			continue
		}
		destPath := filepath.Join(wsPath, name)
		if err := writeTemplateIfMissing(destPath, name); err != nil {
			L_warn("setup: failed to write template", "file", name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

func CheckUpdateWorkspace(wsPath string) error {
	var firstErr error

	for _, spec := range autoUpdateTemplateSpecs() {
		destPath := filepath.Join(wsPath, spec.Name)
		templateContent, err := LoadTemplateStripped(spec.Name)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("load template %s: %w", spec.Name, err)
			}
			L_warn("setup: failed to load template for update check", "file", spec.Name, "error", err)
			continue
		}

		latestChecksum := checksumString(templateContent)
		currentBytes, err := os.ReadFile(destPath)
		if err != nil {
			if os.IsNotExist(err) {
				if err := writeTemplate(destPath, spec.Name, templateContent); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					L_warn("setup: failed to recreate missing template", "file", spec.Name, "error", err)
					continue
				}
				L_debug("setup: template decision",
					"status", "created_missing_template",
					"file", spec.Name,
				)
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("read workspace template %s: %w", spec.Name, err)
			}
			L_warn("setup: failed to read workspace template", "file", spec.Name, "error", err)
			continue
		}

		currentChecksum := checksumBytes(currentBytes)
		if currentChecksum == latestChecksum {
			L_debug("setup: template decision",
				"status", "unchanged_current_template",
				"file", spec.Name,
			)
			continue
		}

		if !templateHasKnownChecksum(spec.Name, currentChecksum) {
			L_debug("setup: template decision",
				"status", "skipped_customized_template",
				"file", spec.Name,
			)
			continue
		}

		if err := writeTemplate(destPath, spec.Name, templateContent); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			L_warn("setup: failed to update stock template", "file", spec.Name, "error", err)
			continue
		}

		L_debug("setup: template decision",
			"status", "updated_stock_template",
			"file", spec.Name,
		)
	}

	return firstErr
}

func writeTemplate(destPath, templateName, content string) error {
	if content == "" {
		var err error
		content, err = LoadTemplateStripped(templateName)
		if err != nil {
			return fmt.Errorf("failed to load template %s: %w", templateName, err)
		}
	}

	if err := os.WriteFile(destPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", destPath, err)
	}

	L_debug("setup: wrote template", "file", destPath)
	return nil
}

func checksumBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func checksumString(content string) string {
	return checksumBytes([]byte(content))
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
