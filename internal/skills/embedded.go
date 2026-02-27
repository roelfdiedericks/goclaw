package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

//go:embed catalog/*
var catalogFS embed.FS

// ListEmbedded returns the names of all skills in the embedded catalog.
func ListEmbedded() ([]string, error) {
	entries, err := catalogFS.ReadDir("catalog")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded catalog: %w", err)
	}

	var skills []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if it has a SKILL.md file
			skillPath := filepath.Join("catalog", entry.Name(), "SKILL.md")
			if _, err := catalogFS.Open(skillPath); err == nil {
				skills = append(skills, entry.Name())
			}
		}
	}

	L_debug("embedded: listed skills", "count", len(skills))
	return skills, nil
}

// GetEmbeddedSkillContent reads the SKILL.md content for an embedded skill.
func GetEmbeddedSkillContent(name string) (string, error) {
	skillPath := filepath.Join("catalog", name, "SKILL.md")
	data, err := catalogFS.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("skill not found in catalog: %s", name)
	}
	return string(data), nil
}

// ExtractSkill extracts an embedded skill to the destination directory.
// Creates destDir/skillName/ with all skill files.
func ExtractSkill(name, destDir string) error {
	skillDir := filepath.Join("catalog", name)

	// Check skill exists
	if _, err := catalogFS.ReadDir(skillDir); err != nil {
		return fmt.Errorf("skill not found in catalog: %s", name)
	}

	// Create destination directory
	targetDir := filepath.Join(destDir, name)
	if err := os.MkdirAll(targetDir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Walk and copy all files
	err := fs.WalkDir(catalogFS, skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from skill directory
		relPath, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(targetDir, relPath)

		if d.IsDir() {
			if relPath != "." {
				return os.MkdirAll(targetPath, 0750)
			}
			return nil
		}

		// Read and write file
		data, err := catalogFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		if err := os.WriteFile(targetPath, data, 0600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", targetPath, err)
		}

		L_trace("embedded: extracted file", "file", targetPath)
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to extract skill %s: %w", name, err)
	}

	L_debug("embedded: extracted skill", "skill", name, "dest", targetDir)
	return nil
}

// SkillExistsInCatalog checks if a skill exists in the embedded catalog.
func SkillExistsInCatalog(name string) bool {
	skillPath := filepath.Join("catalog", name, "SKILL.md")
	_, err := catalogFS.Open(skillPath)
	return err == nil
}
