package skills

import (
	"time"
)

// Source identifies where a skill came from
type Source string

const (
	SourceExtra     Source = "extra"     // From extra directories (lowest precedence)
	SourceBundled   Source = "bundled"   // Ships with GoClaw
	SourceManaged   Source = "managed"   // User-installed via clawhub
	SourceWorkspace Source = "workspace" // Project-specific (highest precedence)
)

// Skill represents a loaded skill with all its metadata
type Skill struct {
	Name        string
	Description string
	Location    string // Full path to SKILL.md
	Source      Source
	Content     string // Raw SKILL.md content
	ContentSHA  string // SHA256 of content for change detection

	// Parsed metadata
	Metadata *OpenClawMetadata

	// Runtime state
	Eligible    bool           // Passes eligibility checks
	Enabled     bool           // Not disabled by config or audit
	Whitelisted bool           // Manually enabled via config despite audit flags
	AuditFlags  []AuditWarning // Security warnings found
	LoadedAt    time.Time
}

// Frontmatter represents the YAML frontmatter of a SKILL.md file
type Frontmatter struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Homepage    string      `yaml:"homepage,omitempty"`
	Metadata    interface{} `yaml:"metadata"` // Can be YAML map or JSON string
}

// OpenClawMetadata contains the openclaw-specific skill metadata
type OpenClawMetadata struct {
	Emoji    string             `json:"emoji,omitempty"`
	OS       []string           `json:"os,omitempty"`       // ["darwin", "linux"]
	Requires *SkillRequirements `json:"requires,omitempty"` // Runtime requirements
	Install  []InstallSpec      `json:"install,omitempty"`  // Installation options
}

// SkillRequirements defines what a skill needs to be eligible
type SkillRequirements struct {
	Bins    []string `json:"bins,omitempty"`    // All required binaries
	AnyBins []string `json:"anyBins,omitempty"` // At least one required
	Env     []string `json:"env,omitempty"`     // Required env vars
	Config  []string `json:"config,omitempty"`  // Required config keys
}

// InstallSpec describes how to install a skill's dependencies
type InstallSpec struct {
	ID      string   `json:"id,omitempty"`
	Kind    string   `json:"kind"`              // "brew", "go", "uv", "download", "node"
	Label   string   `json:"label,omitempty"`   // Human-readable description
	Bins    []string `json:"bins,omitempty"`    // Binaries this provides
	OS      []string `json:"os,omitempty"`      // OS restrictions
	Formula string   `json:"formula,omitempty"` // brew formula
	Module  string   `json:"module,omitempty"`  // go module path
	Package string   `json:"package,omitempty"` // uv/node package name
	URL     string   `json:"url,omitempty"`     // download URL
}

// AuditWarning represents a security concern found in a skill
type AuditWarning struct {
	Severity string // "info", "warn", "critical"
	Pattern  string // What pattern was matched
	Match    string // The actual matched text
	Line     int    // Line number in SKILL.md
}

// AuditResult contains the full audit output for a skill
type AuditResult struct {
	Skill    string
	Warnings []AuditWarning
	Flagged  bool // True if any warning found
}

// EligibilityContext provides runtime context for eligibility checking
type EligibilityContext struct {
	OS          string            // runtime.GOOS
	ConfigKeys  map[string]bool   // Available config keys
	SkillConfig *SkillEntryConfig // Per-skill config if exists
}

// SkillEntryConfig holds per-skill configuration from goclaw.json
type SkillEntryConfig struct {
	Enabled bool              `json:"enabled"`
	APIKey  string            `json:"apiKey,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Config  map[string]any    `json:"config,omitempty"`
}

// InstallResult reports the outcome of an install attempt
type InstallResult struct {
	Success bool
	Message string
	Error   error
}

// ManagerStats provides statistics about loaded skills
type ManagerStats struct {
	TotalSkills    int
	EligibleSkills int
	FlaggedSkills  int
	BySource       map[Source]int
}

// GetInstallOptions returns available installation options for this skill
func (s *Skill) GetInstallOptions() []InstallSpec {
	if s.Metadata == nil {
		return nil
	}
	return s.Metadata.Install
}
