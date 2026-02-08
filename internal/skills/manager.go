package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Manager coordinates skill loading, auditing, and prompt generation.
type Manager struct {
	loader    *Loader
	auditor   *Auditor
	installer *Installer
	watcher   *Watcher

	// Configuration
	bundledDir   string
	managedDir   string
	workspaceDir string
	extraDirs    []string
	configKeys   map[string]bool
	skillConfigs map[string]*SkillEntryConfig

	// State
	mu             sync.RWMutex
	flaggedSkills  []*Skill // Skills disabled due to security warnings
	startupWarning string   // Warning message to show on session start
}

// ManagerConfig contains configuration for the skill manager.
type ManagerConfig struct {
	Enabled       bool
	BundledDir    string   // GoClaw bundled skills (default: <exe_dir>/skills)
	ManagedDir    string   // User-installed skills (gateway: ~/.openclaw/skills or ~/.goclaw/skills based on workspace)
	WorkspaceDir  string   // Workspace skills (default: <workspace>/skills)
	ExtraDirs     []string // Additional directories
	WatchEnabled  bool
	WatchDebounce int                          // ms
	ConfigKeys    map[string]bool              // Available config keys for eligibility
	SkillConfigs  map[string]*SkillEntryConfig // Per-skill configuration
}

// NewManager creates a new skill manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	L_debug("skills: creating manager")

	// Resolve default paths
	bundledDir := cfg.BundledDir
	if bundledDir == "" {
		// Default to skills/ relative to executable
		exe, err := os.Executable()
		if err == nil {
			bundledDir = filepath.Join(filepath.Dir(exe), "skills")
		}
	}

	managedDir := cfg.ManagedDir
	if managedDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			managedDir = filepath.Join(home, ".goclaw", "skills")
		}
	}

	L_debug("skills: directories configured",
		"bundled", bundledDir,
		"managed", managedDir,
		"workspace", cfg.WorkspaceDir,
		"extraDirs", cfg.ExtraDirs)

	m := &Manager{
		bundledDir:   bundledDir,
		managedDir:   managedDir,
		workspaceDir: cfg.WorkspaceDir,
		extraDirs:    cfg.ExtraDirs,
		configKeys:   cfg.ConfigKeys,
		skillConfigs: cfg.SkillConfigs,
		auditor:      NewAuditor(),
		installer:    NewInstaller(""),
	}

	// Create loader
	m.loader = NewLoader(bundledDir, managedDir, cfg.WorkspaceDir, cfg.ExtraDirs)

	// Set up watcher if enabled
	if cfg.WatchEnabled {
		watcher, err := NewWatcher(cfg.WatchDebounce, m.onSkillsChanged)
		if err != nil {
			L_warn("failed to create skill watcher", "error", err)
		} else {
			m.watcher = watcher
		}
	}

	return m, nil
}

// Load loads all skills synchronously. Call during initialization.
func (m *Manager) Load() error {
	if err := m.Reload(); err != nil {
		return fmt.Errorf("failed to load skills: %w", err)
	}
	return nil
}

// StartWatcher starts the file watcher for live reloads.
// This runs in a background goroutine internally.
// Call after Load() during startup.
func (m *Manager) StartWatcher() {
	if m.watcher == nil {
		return
	}

	dirs := m.loader.WatchedDirs()
	if err := m.watcher.WatchDirs(dirs); err != nil {
		L_warn("failed to watch skill directories", "error", err)
		return
	}
	m.watcher.Start() // This spawns a goroutine internally
	L_debug("skills: watcher started", "dirs", len(dirs))
}

// Stop shuts down the manager.
func (m *Manager) Stop() error {
	if m.watcher != nil {
		return m.watcher.Stop()
	}
	return nil
}

// Reload reloads all skills from disk.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Load skills
	skills, err := m.loader.LoadAll()
	if err != nil {
		return err
	}

	// Clear previous state
	m.flaggedSkills = nil

	// Check eligibility and audit each skill
	ctx := EligibilityContext{
		OS:         runtime.GOOS,
		ConfigKeys: m.configKeys,
	}

	eligibleCount := 0
	ineligibleCount := 0

	for _, skill := range skills {
		// Check per-skill config
		var skillCfg *SkillEntryConfig
		if cfg, ok := m.skillConfigs[skill.Name]; ok {
			skillCfg = cfg
			ctx.SkillConfig = cfg
		} else {
			ctx.SkillConfig = nil
		}

		// Check eligibility
		skill.IsEligible(ctx)

		if skill.Eligible {
			eligibleCount++
			// Audit for security concerns (only if eligible)
			if m.auditor.AuditAndFlag(skill) {
				// Skill was flagged - check if whitelisted in config
				if skillCfg != nil && skillCfg.Enabled {
					skill.Enabled = true
					skill.Whitelisted = true
					L_info("skills: whitelisted flagged skill", "skill", skill.Name)
				} else {
					m.flaggedSkills = append(m.flaggedSkills, skill)
				}
			}
		} else {
			ineligibleCount++
			// Log why the skill is ineligible
			missing := skill.GetMissingRequirements(ctx)
			reason := "unknown"
			if len(missing) > 0 {
				reason = missing[0]
				if len(missing) > 1 {
					reason += fmt.Sprintf(" (+%d more)", len(missing)-1)
				}
			} else if !skill.Enabled {
				reason = "disabled"
			} else if skill.Metadata == nil {
				reason = "no metadata (should be eligible?)"
			}
			L_trace("skills: ineligible", "skill", skill.Name, "reason", reason)
		}
	}

	// Log summary of ineligible skills at INFO only if there are some
	if ineligibleCount > 0 {
		L_info("skills: eligibility check",
			"eligible", eligibleCount,
			"ineligible", ineligibleCount)
	}

	// Generate startup warning if there are flagged skills
	m.generateStartupWarning()

	return nil
}

// onSkillsChanged is called when skill files change.
func (m *Manager) onSkillsChanged() {
	if err := m.Reload(); err != nil {
		L_error("failed to reload skills", "error", err)
	}
}

// generateStartupWarning creates a warning message for flagged skills.
func (m *Manager) generateStartupWarning() {
	if len(m.flaggedSkills) == 0 {
		m.startupWarning = ""
		return
	}

	var msg string
	if len(m.flaggedSkills) == 1 {
		skill := m.flaggedSkills[0]
		result := &AuditResult{
			Skill:    skill.Name,
			Warnings: skill.AuditFlags,
			Flagged:  true,
		}
		msg = m.auditor.FormatWarnings(result)
	} else {
		msg = fmt.Sprintf("Security Warning: %d skills have been flagged and disabled.\n\n", len(m.flaggedSkills))
		for _, skill := range m.flaggedSkills {
			msg += m.auditor.FormatStatusLine(skill) + "\n"
		}
		msg += "\nTo enable: add to goclaw.json skills.entries.<name>.enabled = true"
	}

	m.startupWarning = msg
}

// GetEligibleSkills returns skills that are eligible and enabled.
func (m *Manager) GetEligibleSkills() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ctx := EligibilityContext{
		OS:         runtime.GOOS,
		ConfigKeys: m.configKeys,
	}

	var eligible []*Skill
	for _, skill := range m.loader.GetAllSkills() {
		if skill.Eligible && skill.Enabled {
			eligible = append(eligible, skill)
		}
	}

	// Re-check with current context
	_ = ctx // Context already applied during Reload

	return eligible
}

// GetAllSkills returns all loaded skills.
func (m *Manager) GetAllSkills() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loader.GetAllSkills()
}

// GetSkill returns a specific skill by name.
func (m *Manager) GetSkill(name string) *Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loader.GetSkill(name)
}

// GetStats returns statistics about loaded skills.
func (m *Manager) GetStats() ManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loader.GetStats()
}

// GetStartupWarning returns the startup warning message (if any).
func (m *Manager) GetStartupWarning() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.startupWarning
}

// GetFlaggedSkills returns skills that were disabled due to security concerns.
func (m *Manager) GetFlaggedSkills() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy
	result := make([]*Skill, len(m.flaggedSkills))
	copy(result, m.flaggedSkills)
	return result
}

// FormatPrompt generates the skills section for the system prompt.
func (m *Manager) FormatPrompt() string {
	skills := m.GetEligibleSkills()
	return FormatSkillsPrompt(skills)
}

// Install attempts to install a skill's dependencies.
func (m *Manager) Install(ctx context.Context, skillName string, specID string) (*InstallResult, error) {
	skill := m.GetSkill(skillName)
	if skill == nil {
		return nil, fmt.Errorf("skill not found: %s", skillName)
	}

	options := skill.GetInstallOptions()
	if len(options) == 0 {
		return nil, fmt.Errorf("skill has no installation options: %s", skillName)
	}

	// Find the requested spec
	var spec *InstallSpec
	for i := range options {
		if options[i].ID == specID || specID == "" {
			spec = &options[i]
			break
		}
	}

	if spec == nil {
		return nil, fmt.Errorf("install spec not found: %s", specID)
	}

	return m.installer.Install(ctx, *spec)
}

// FormatStatusSection generates the skills section for /status output.
func (m *Manager) FormatStatusSection() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := m.loader.GetStats()

	result := fmt.Sprintf("Skills: %d total, %d eligible", stats.TotalSkills, stats.EligibleSkills)

	if stats.FlaggedSkills > 0 {
		result += fmt.Sprintf("\nFlagged Skills: %d", stats.FlaggedSkills)
		for _, skill := range m.flaggedSkills {
			result += fmt.Sprintf("\n  - %s", m.auditor.FormatStatusLine(skill))
		}
	}

	return result
}
