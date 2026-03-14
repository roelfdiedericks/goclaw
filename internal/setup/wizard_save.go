// Package setup provides setup wizard and configuration editing
package setup

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// SaveWizardConfig saves the wizard configuration and users to their respective files.
// This is the exported version of printWizardConfig for use by the web wizard.
func SaveWizardConfig(data *WizardData) error {
	return SaveWizardConfigToPath(data, "")
}

// SaveWizardConfigToPath saves wizard config and users using an explicit config path.
// If configPath is empty, the default path is used.
func SaveWizardConfigToPath(data *WizardData, configPath string) error {
	// Get save paths
	saveConfigPath := configPath
	if saveConfigPath == "" {
		var err error
		saveConfigPath, err = paths.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("getting config path: %w", err)
		}
	}
	usersPath, err := paths.UsersPath(saveConfigPath)
	if err != nil {
		return fmt.Errorf("getting users path: %w", err)
	}

	// Ensure parent directory exists
	if err := paths.EnsureParentDir(saveConfigPath); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// Build and save config
	cfg := buildConfigFromWizardData(data)
	if err := config.BackupAndWriteJSON(saveConfigPath, cfg, config.DefaultBackupCount); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// Build and save users
	userEntry := map[string]interface{}{
		"name": data.UserDisplayName,
		"role": data.UserRole,
	}
	if data.UserTelegramID != "" {
		userEntry["telegram_id"] = data.UserTelegramID
	}
	if data.UserPassword != "" {
		hash, err := user.HashPassword(data.UserPassword)
		if err != nil {
			return fmt.Errorf("hashing password: %w", err)
		}
		userEntry["http_password_hash"] = hash
	} else if data.UserExistingPwdHash != "" {
		userEntry["http_password_hash"] = data.UserExistingPwdHash
	}
	users := map[string]interface{}{
		data.UserName: userEntry,
	}

	if err := config.BackupAndWriteJSON(usersPath, users, config.DefaultBackupCount); err != nil {
		return fmt.Errorf("saving users: %w", err)
	}

	return nil
}
