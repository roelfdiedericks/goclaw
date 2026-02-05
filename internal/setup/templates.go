// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"embed"
	"regexp"
	"strings"
)

//go:embed templates/*.md
var templatesFS embed.FS

// templateFiles lists all workspace template files
var templateFiles = []string{
	"AGENTS.md",
	"SOUL.md",
	"BOOTSTRAP.md",
	"IDENTITY.md",
	"USER.md",
	"TOOLS.md",
	"HEARTBEAT.md",
}

// frontmatterRegex matches YAML frontmatter at the start of a file
var frontmatterRegex = regexp.MustCompile(`(?s)^---\n.*?\n---\n*`)

// LoadTemplate reads a template file from the embedded filesystem
func LoadTemplate(name string) (string, error) {
	data, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// StripFrontmatter removes YAML frontmatter from markdown content
func StripFrontmatter(content string) string {
	return strings.TrimLeft(frontmatterRegex.ReplaceAllString(content, ""), "\n")
}

// LoadTemplateStripped reads a template and strips frontmatter
func LoadTemplateStripped(name string) (string, error) {
	content, err := LoadTemplate(name)
	if err != nil {
		return "", err
	}
	return StripFrontmatter(content), nil
}
