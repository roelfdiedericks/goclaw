package skills

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

// Auditor scans skill content for security concerns.
type Auditor struct {
	patterns []auditPattern
}

type auditPattern struct {
	name     string
	severity string // "info", "warn", "critical" - display metadata only
	regex    *regexp.Regexp
	desc     string
	isSafe   func(match string) bool // optional: returns true if match is safe (don't flag)
}

// volumePathSafe returns true if the matched path is within a registered sandbox volume.
func volumePathSafe(match string) bool {
	return sandbox.IsRegisteredVolume(match)
}

// credentialInVolumeSafe returns true if the matched credentials reference
// appears alongside a path that's within a registered sandbox volume.
func credentialInVolumeSafe(match string) bool {
	homeDotDirs := regexp.MustCompile(`~/\.\w+/`)
	found := homeDotDirs.FindString(match)
	if found != "" {
		return sandbox.IsRegisteredVolume(found)
	}
	return false
}

// NewAuditor creates a new security auditor with default patterns.
func NewAuditor() *Auditor {
	return &Auditor{
		patterns: []auditPattern{
			// Sensitive file references
			{
				name:     "env_file",
				severity: "warn",
				regex:    regexp.MustCompile(`(?i)\.env\b|\.env/|\.env\.local|\.env\.production`),
				desc:     "References .env file or directory",
			},
			{
				name:     "credentials_file",
				severity: "warn",
				regex:    regexp.MustCompile(`(?i)~/\.\w+/.*(?:\.credentials|credentials\.json|\.secrets)`),
				desc:     "References credentials file in home directory",
				isSafe:   credentialInVolumeSafe,
			},
			{
				name:     "credentials_file_bare",
				severity: "warn",
				regex:    regexp.MustCompile(`(?i)(?:^|[^~/\.\w])(?:\.credentials|\.secrets)(?:\s|$|")`),
				desc:     "References credentials file",
			},
			{
				name:     "ssh_dir",
				severity: "warn",
				regex:    regexp.MustCompile(`~/\.ssh|~/.ssh|/\.ssh/`),
				desc:     "References SSH directory",
			},
			{
				name:     "aws_config",
				severity: "warn",
				regex:    regexp.MustCompile(`~/\.aws|~/.aws|\.aws/credentials`),
				desc:     "References AWS config",
			},
			{
				name:     "sensitive_config",
				severity: "info",
				regex:    regexp.MustCompile(`~/\.config/.*(?:token|secret|key|password)`),
				desc:     "References potentially sensitive config",
				isSafe:   volumePathSafe,
			},

			// Dangerous command patterns
			{
				name:     "curl_pipe_bash",
				severity: "critical",
				regex:    regexp.MustCompile(`curl\s+[^|]*\|\s*(?:ba)?sh`),
				desc:     "Pipe curl to shell (dangerous)",
			},
			{
				name:     "wget_pipe_sh",
				severity: "critical",
				regex:    regexp.MustCompile(`wget\s+[^|]*\|\s*(?:ba)?sh`),
				desc:     "Pipe wget to shell (dangerous)",
			},
			{
				name:     "eval_command",
				severity: "warn",
				regex:    regexp.MustCompile(`\beval\s+\$`),
				desc:     "Uses eval with variable",
			},

			// External data exfiltration
			{
				name:     "webhook_site",
				severity: "critical",
				regex:    regexp.MustCompile(`webhook\.site|requestbin\.com|hookbin\.com`),
				desc:     "External webhook URL (data exfiltration risk)",
			},
			{
				name:     "pastebin",
				severity: "warn",
				regex:    regexp.MustCompile(`pastebin\.com|hastebin\.com|dpaste\.org`),
				desc:     "Pastebin URL reference",
			},

			// Encoded content
			{
				name:     "base64_long",
				severity: "info",
				regex:    regexp.MustCompile(`[A-Za-z0-9+/]{100,}={0,2}`),
				desc:     "Long base64-encoded content",
			},

			// Outside workspace access - system paths (always flag)
			{
				name:     "outside_workspace_system",
				severity: "warn",
				regex:    regexp.MustCompile(`/etc/|/home/\w+/|/Users/\w+/`),
				desc:     "References system paths outside workspace",
			},
			// Outside workspace access - home dot-dirs (safe if sandbox volume)
			{
				name:     "outside_workspace_home",
				severity: "warn",
				regex:    regexp.MustCompile(`~/\.\w+/`),
				desc:     "References home directory paths outside workspace",
				isSafe:   volumePathSafe,
			},
		},
	}
}

// AuditSkill scans a skill's content for security concerns.
func (a *Auditor) AuditSkill(skill *Skill) *AuditResult {
	result := &AuditResult{
		Skill:    skill.Name,
		Warnings: []AuditWarning{},
		Flagged:  false,
	}

	lines := strings.Split(skill.Content, "\n")

	for lineNum, line := range lines {
		for _, pattern := range a.patterns {
			if pattern.regex.MatchString(line) {
				match := pattern.regex.FindString(line)

				if pattern.isSafe != nil && pattern.isSafe(match) {
					continue
				}

				warning := AuditWarning{
					Severity: pattern.severity,
					Pattern:  pattern.name,
					Match:    truncateMatch(match, 50),
					Line:     lineNum + 1,
				}
				result.Warnings = append(result.Warnings, warning)
				result.Flagged = true
			}
		}
	}

	return result
}

// AuditAndFlag audits a skill and updates its state.
// Returns true if the skill was flagged and disabled.
func (a *Auditor) AuditAndFlag(skill *Skill) bool {
	result := a.AuditSkill(skill)

	if result.Flagged {
		skill.AuditFlags = result.Warnings
		skill.Enabled = false
		return true
	}

	skill.AuditFlags = nil
	return false
}

// FormatWarnings formats audit warnings for display.
func (a *Auditor) FormatWarnings(result *AuditResult) string {
	if !result.Flagged || len(result.Warnings) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Security Warning: Skill %q has been flagged and disabled.\n", result.Skill))
	sb.WriteString(fmt.Sprintf("Found %d security concern(s):\n", len(result.Warnings)))

	for _, w := range result.Warnings {
		sb.WriteString(fmt.Sprintf("  - Line %d [%s]: %s (match: %s)\n",
			w.Line, w.Severity, getPatternDesc(a, w.Pattern), w.Match))
	}

	sb.WriteString("\nTo enable: add to goclaw.json: {\"skills\":{\"entries\":{\"")
	sb.WriteString(result.Skill)
	sb.WriteString("\":{\"enabled\":true}}}}")

	return sb.String()
}

// FormatStatusLine formats a single flagged skill for status output.
func (a *Auditor) FormatStatusLine(skill *Skill) string {
	if len(skill.AuditFlags) == 0 {
		return skill.Name
	}

	patterns := make([]string, 0, len(skill.AuditFlags))
	seen := make(map[string]bool)
	for _, w := range skill.AuditFlags {
		if !seen[w.Pattern] {
			patterns = append(patterns, w.Pattern)
			seen[w.Pattern] = true
		}
	}

	return fmt.Sprintf("%s (disabled): %s", skill.Name, strings.Join(patterns, ", "))
}

// getPatternDesc returns the description for a pattern name.
func getPatternDesc(a *Auditor, patternName string) string {
	for _, p := range a.patterns {
		if p.name == patternName {
			return p.desc
		}
	}
	return patternName
}

// truncateMatch truncates a match string if too long.
func truncateMatch(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// GetSeverityCounts returns counts by severity level.
func (result *AuditResult) GetSeverityCounts() map[string]int {
	counts := map[string]int{
		"info":     0,
		"warn":     0,
		"critical": 0,
	}
	for _, w := range result.Warnings {
		counts[w.Severity]++
	}
	return counts
}
