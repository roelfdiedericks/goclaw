package skills

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNodeBlocked is returned when a node/npm install is requested
var ErrNodeBlocked = errors.New("Node.js package installation is not supported for security reasons. Please install manually")

// Installer handles skill dependency installation.
type Installer struct {
	workDir string // Working directory for installs
}

// NewInstaller creates a new installer.
func NewInstaller(workDir string) *Installer {
	return &Installer{
		workDir: workDir,
	}
}

// Install attempts to install a skill's dependencies using the given spec.
func (i *Installer) Install(ctx context.Context, spec InstallSpec) (*InstallResult, error) {
	// Check OS compatibility
	if len(spec.OS) > 0 {
		compatible := false
		for _, os := range spec.OS {
			if os == runtime.GOOS {
				compatible = true
				break
			}
		}
		if !compatible {
			return &InstallResult{
				Success: false,
				Message: fmt.Sprintf("Install spec %q not compatible with %s", spec.ID, runtime.GOOS),
			}, nil
		}
	}

	switch spec.Kind {
	case "brew":
		return i.installBrew(ctx, spec)
	case "go":
		return i.installGo(ctx, spec)
	case "uv":
		return i.installUV(ctx, spec)
	case "download":
		return i.installDownload(ctx, spec)
	case "node", "npm", "pnpm", "yarn":
		return nil, ErrNodeBlocked
	default:
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("Unknown install kind: %s", spec.Kind),
		}, nil
	}
}

// installBrew installs using Homebrew.
func (i *Installer) installBrew(ctx context.Context, spec InstallSpec) (*InstallResult, error) {
	if spec.Formula == "" {
		return &InstallResult{
			Success: false,
			Message: "No formula specified for brew install",
		}, nil
	}

	// Check if brew exists
	if _, err := exec.LookPath("brew"); err != nil {
		return &InstallResult{
			Success: false,
			Message: "Homebrew not installed",
		}, nil
	}

	cmd := exec.CommandContext(ctx, "brew", "install", spec.Formula) //nolint:gosec // G204: dead code, needs security review before enabling
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("brew install failed: %s", strings.TrimSpace(string(output))),
			Error:   err,
		}, nil
	}

	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("Installed %s via brew", spec.Formula),
	}, nil
}

// installGo installs using go install.
func (i *Installer) installGo(ctx context.Context, spec InstallSpec) (*InstallResult, error) {
	if spec.Module == "" {
		return &InstallResult{
			Success: false,
			Message: "No module specified for go install",
		}, nil
	}

	// Check if go exists
	if _, err := exec.LookPath("go"); err != nil {
		return &InstallResult{
			Success: false,
			Message: "Go not installed",
		}, nil
	}

	module := spec.Module
	if !strings.Contains(module, "@") {
		module += "@latest"
	}

	cmd := exec.CommandContext(ctx, "go", "install", module) //nolint:gosec // G204: dead code, needs security review before enabling
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("go install failed: %s", strings.TrimSpace(string(output))),
			Error:   err,
		}, nil
	}

	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("Installed %s via go install", spec.Module),
	}, nil
}

// installUV installs using uv tool install.
func (i *Installer) installUV(ctx context.Context, spec InstallSpec) (*InstallResult, error) {
	if spec.Package == "" {
		return &InstallResult{
			Success: false,
			Message: "No package specified for uv install",
		}, nil
	}

	// Check if uv exists
	if _, err := exec.LookPath("uv"); err != nil {
		return &InstallResult{
			Success: false,
			Message: "uv not installed (install with: curl -LsSf https://astral.sh/uv/install.sh | sh)",
		}, nil
	}

	cmd := exec.CommandContext(ctx, "uv", "tool", "install", spec.Package) //nolint:gosec // G204: dead code, needs security review before enabling
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("uv tool install failed: %s", strings.TrimSpace(string(output))),
			Error:   err,
		}, nil
	}

	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("Installed %s via uv", spec.Package),
	}, nil
}

// installDownload downloads and extracts a tarball.
func (i *Installer) installDownload(ctx context.Context, spec InstallSpec) (*InstallResult, error) {
	if spec.URL == "" {
		return &InstallResult{
			Success: false,
			Message: "No URL specified for download",
		}, nil
	}

	// For now, just return instructions - actual download is complex
	return &InstallResult{
		Success: false,
		Message: fmt.Sprintf("Manual download required: %s", spec.URL),
	}, nil
}

// InstallSkillFiles installs a skill by extracting its files from a source to the destination directory.
// It validates the skill exists, extracts it, and runs the auditor.
func (i *Installer) InstallSkillFiles(ctx context.Context, skillName string, source SourceType, destDir string, auditor *Auditor) (*SkillInstallResult, error) {
	// Get the appropriate fetcher
	fetcher, err := GetFetcher(source, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get fetcher: %w", err)
	}

	// Check skill exists in source
	if !fetcher.Exists(skillName) {
		return &SkillInstallResult{
			Success:   false,
			SkillName: skillName,
			Source:    source,
			Message:   fmt.Sprintf("skill not found in %s", source),
		}, nil
	}

	// Extract to destination
	if err := fetcher.FetchTo(skillName, destDir); err != nil {
		return &SkillInstallResult{
			Success:   false,
			SkillName: skillName,
			Source:    source,
			Message:   fmt.Sprintf("failed to extract skill: %s", err),
		}, nil
	}

	// Parse the installed skill to audit it
	skillPath := destDir + "/" + skillName + "/SKILL.md"
	skill, err := ParseSkillFile(skillPath, SourceWorkspace)
	if err != nil {
		return &SkillInstallResult{
			Success:   true,
			SkillName: skillName,
			Source:    source,
			Message:   fmt.Sprintf("installed but failed to parse for audit: %s", err),
		}, nil
	}

	// Audit the skill
	var warnings []AuditWarning
	if auditor != nil && auditor.AuditAndFlag(skill) {
		warnings = skill.AuditFlags
	}

	return &SkillInstallResult{
		Success:   true,
		SkillName: skillName,
		Source:    source,
		Message:   fmt.Sprintf("installed %s from %s", skillName, source),
		Warnings:  warnings,
		Flagged:   len(warnings) > 0,
	}, nil
}

// SkillInstallResult contains the result of a skill file installation
type SkillInstallResult struct {
	Success             bool           `json:"success"`
	SkillName           string         `json:"skillName"`
	Source              SourceType     `json:"source"`
	Message             string         `json:"message"`
	Warnings            []AuditWarning `json:"warnings,omitempty"`
	Flagged             bool           `json:"flagged,omitempty"`
	Eligible            bool           `json:"eligible"`
	MissingRequirements []string       `json:"missingRequirements,omitempty"`
}

// CanInstall checks if a specific install kind is supported on this system.
func CanInstall(kind string) (bool, string) {
	switch kind {
	case "brew":
		if _, err := exec.LookPath("brew"); err != nil {
			return false, "Homebrew not installed"
		}
		return true, ""
	case "go":
		if _, err := exec.LookPath("go"); err != nil {
			return false, "Go not installed"
		}
		return true, ""
	case "uv":
		if _, err := exec.LookPath("uv"); err != nil {
			return false, "uv not installed"
		}
		return true, ""
	case "download":
		return true, ""
	case "node", "npm", "pnpm", "yarn":
		return false, "Node.js installation blocked for security"
	default:
		return false, fmt.Sprintf("Unknown install kind: %s", kind)
	}
}
