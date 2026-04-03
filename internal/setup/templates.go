// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"embed"
	"regexp"
	"slices"
	"strings"
)

//go:embed templates/*.md
var templatesFS embed.FS

const bootstrapTemplateName = "BOOTSTRAP.md"

type WorkspaceTemplateSpec struct {
	Name       string
	AutoUpdate bool
}

type TemplateManifestEntry struct {
	Current string
	Known   []string
}

var workspaceTemplateSpecs = []WorkspaceTemplateSpec{
	{Name: "AGENTS.md", AutoUpdate: true},
	{Name: "SOUL.md", AutoUpdate: true},
	{Name: bootstrapTemplateName, AutoUpdate: false},
	{Name: "IDENTITY.md", AutoUpdate: true},
	{Name: "USER.md", AutoUpdate: true},
	{Name: "TOOLS.md", AutoUpdate: true},
	{Name: "HEARTBEAT.md", AutoUpdate: true},
}

// templateManifest is populated by generated code.
var templateManifest = map[string]TemplateManifestEntry{}

// templateFiles lists all workspace template files.
var templateFiles = templateSpecNames(workspaceTemplateSpecs)

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

func templateSpecNames(specs []WorkspaceTemplateSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func autoUpdateTemplateSpecs() []WorkspaceTemplateSpec {
	specs := make([]WorkspaceTemplateSpec, 0, len(workspaceTemplateSpecs))
	for _, spec := range workspaceTemplateSpecs {
		if spec.AutoUpdate {
			specs = append(specs, spec)
		}
	}
	return specs
}

func templateHasKnownChecksum(name, checksum string) bool {
	entry, ok := templateManifest[name]
	if !ok {
		return false
	}
	if entry.Current == checksum {
		return true
	}
	return slices.Contains(entry.Known, checksum)
}
