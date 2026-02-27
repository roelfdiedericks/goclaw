package skills

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Manager coordinates skill loading, auditing, and prompt generation.
type Manager struct {
	loader    *Loader
	auditor   *Auditor
	installer *Installer
	watcher   *Watcher

	// Configuration
	workspaceDir string
	extraDirs    []string
	configKeys   map[string]bool
	skillConfigs map[string]*SkillEntryConfig

	// State
	mu             sync.RWMutex
	startupWarning string // Warning message to show on session start

	// Event subscriptions
	configEventSub bus.SubscriptionID
}

// ManagerConfig contains configuration for the skill manager.
type ManagerConfig struct {
	Enabled       bool
	WorkspaceDir  string   // Workspace skills (default: <workspace>/skills)
	ExtraDirs     []string // Additional directories (power user feature)
	WatchEnabled  bool
	WatchDebounce int                          // ms
	ConfigKeys    map[string]bool              // Available config keys for eligibility
	SkillConfigs  map[string]*SkillEntryConfig // Per-skill configuration
}

// NewManager creates a new skill manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	L_debug("skills: creating manager")

	L_debug("skills: directories configured",
		"workspace", cfg.WorkspaceDir,
		"extraDirs", cfg.ExtraDirs)

	m := &Manager{
		workspaceDir: cfg.WorkspaceDir,
		extraDirs:    cfg.ExtraDirs,
		configKeys:   cfg.ConfigKeys,
		skillConfigs: cfg.SkillConfigs,
		auditor:      NewAuditor(),
		installer:    NewInstaller(""),
	}

	// Create loader
	m.loader = NewLoader(cfg.WorkspaceDir, cfg.ExtraDirs)

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

// RegisterOperationalCommands registers runtime commands and event subscriptions.
func (m *Manager) RegisterOperationalCommands() {
	bus.RegisterCommand("skills", "list", m.handleList)
	bus.RegisterCommand("skills", "reload", m.handleReload)

	// Subscribe to config changes
	m.configEventSub = bus.SubscribeEvent("skills.config.applied", m.onConfigApplied)

	L_debug("skills: operational commands registered")
}

// UnregisterOperationalCommands removes commands and subscriptions.
func (m *Manager) UnregisterOperationalCommands() {
	bus.UnregisterCommand("skills", "list")
	bus.UnregisterCommand("skills", "reload")
	if m.configEventSub != 0 {
		bus.UnsubscribeEvent(m.configEventSub)
		m.configEventSub = 0
	}
}

// onConfigApplied handles skills config changes
func (m *Manager) onConfigApplied(e bus.Event) {
	cfg, ok := e.Data.(SkillsConfig)
	if !ok {
		cfgPtr, okPtr := e.Data.(*SkillsConfig)
		if okPtr {
			cfg = *cfgPtr
			ok = true
		}
	}
	if !ok {
		L_error("skills: invalid config event data type", "type", fmt.Sprintf("%T", e.Data))
		return
	}

	L_info("skills: config changed, reloading", "enabled", cfg.Enabled)

	// Update directories if they changed
	if cfg.WorkspaceDir != "" {
		m.workspaceDir = cfg.WorkspaceDir
	}
	m.extraDirs = cfg.ExtraDirs

	// Reload skills
	if err := m.Reload(); err != nil {
		L_error("skills: reload failed after config change", "error", err)
	}
}

// handleList returns a list of loaded skills
func (m *Manager) handleList(cmd bus.Command) bus.CommandResult {
	skills := m.GetAllSkills()
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}

	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Loaded %d skills", len(skills)),
		Data:    names,
	}
}

// handleReload forces a reload of all skills
func (m *Manager) handleReload(cmd bus.Command) bus.CommandResult {
	if err := m.Reload(); err != nil {
		return bus.CommandResult{
			Error:   err,
			Message: fmt.Sprintf("Reload failed: %v", err),
		}
	}

	skills := m.GetAllSkills()
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Reloaded %d skills", len(skills)),
	}
}

// Reload reloads all skills from disk, checks eligibility, and audits each skill.
// All skills remain in the registry with their status flags set.
// Only eligible+enabled skills are injected into agent prompts.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	skills, err := m.loader.LoadAll()
	if err != nil {
		return err
	}

	ctx := EligibilityContext{
		OS:         runtime.GOOS,
		ConfigKeys: m.configKeys,
	}

	eligibleCount := 0
	ineligibleCount := 0
	flaggedCount := 0

	for _, skill := range skills {
		var skillCfg *SkillEntryConfig
		if cfg, ok := m.skillConfigs[skill.Name]; ok {
			skillCfg = cfg
			ctx.SkillConfig = cfg
		} else {
			ctx.SkillConfig = nil
		}

		// Check eligibility (sets skill.Eligible)
		skill.IsEligible(ctx)

		if !skill.Eligible {
			skill.Enabled = false
			missing := skill.GetMissingRequirements(ctx)
			reason := "unknown"
			if len(missing) > 0 {
				reason = missing[0]
			}
			L_debug("skills: ineligible", "skill", skill.Name, "reason", reason)
			ineligibleCount++
			continue
		}

		// Audit for security concerns
		if m.auditor.AuditAndFlag(skill) {
			if skillCfg != nil && skillCfg.Enabled {
				skill.Enabled = true
				skill.Whitelisted = true
				L_info("skills: whitelisted flagged skill", "skill", skill.Name)
			} else {
				skill.Enabled = false
				L_debug("skills: flagged", "skill", skill.Name)
				flaggedCount++
			}
		}

		if skill.Eligible && skill.Enabled {
			eligibleCount++
		}
	}

	L_info("skills: reload complete",
		"total", len(skills),
		"eligible", eligibleCount,
		"ineligible", ineligibleCount,
		"flagged", flaggedCount)

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
	flagged := m.getFlaggedSkillsLocked()
	if len(flagged) == 0 {
		m.startupWarning = ""
		return
	}

	var msg string
	if len(flagged) == 1 {
		skill := flagged[0]
		result := &AuditResult{
			Skill:    skill.Name,
			Warnings: skill.AuditFlags,
			Flagged:  true,
		}
		msg = m.auditor.FormatWarnings(result)
	} else {
		msg = fmt.Sprintf("Security Warning: %d skills have been flagged and disabled.\n\n", len(flagged))
		for _, skill := range flagged {
			msg += m.auditor.FormatStatusLine(skill) + "\n"
		}
		msg += "\nTo enable: add to goclaw.json skills.entries.<name>.enabled = true"
	}

	m.startupWarning = msg
}

// GetEligibleSkills returns skills that are eligible and enabled.
// If user is nil, all eligible+enabled skills are returned.
// Otherwise, skills are filtered by the user's role permissions.
func (m *Manager) GetEligibleSkills(u *user.User, rolesConfig user.RolesConfig) []*Skill {
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

	// Filter by user's role if user is provided
	if u != nil {
		resolvedRole, err := user.ResolveRole(string(u.Role), rolesConfig)
		if err != nil {
			L_warn("skills: failed to resolve role, returning all eligible", "user", u.Name, "error", err)
			return eligible
		}

		// If AllSkills, no filtering needed
		if resolvedRole.AllSkills {
			return eligible
		}

		// Filter to allowed skills
		filtered := make([]*Skill, 0, len(eligible))
		for _, skill := range eligible {
			if resolvedRole.CanUseSkill(skill.Name) {
				filtered = append(filtered, skill)
			}
		}
		L_debug("skills: filtered by role", "user", u.Name, "role", resolvedRole.Name, "total", len(eligible), "allowed", len(filtered))
		return filtered
	}

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
// Computed on the fly from the loaded skills.
func (m *Manager) GetFlaggedSkills() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getFlaggedSkillsLocked()
}

// getFlaggedSkillsLocked returns flagged skills (caller must hold lock).
func (m *Manager) getFlaggedSkillsLocked() []*Skill {
	var flagged []*Skill
	for _, skill := range m.loader.GetAllSkills() {
		if len(skill.AuditFlags) > 0 && !skill.Whitelisted {
			flagged = append(flagged, skill)
		}
	}
	return flagged
}

// FormatPrompt generates the skills section for the system prompt.
// This version returns all eligible skills (no user filtering).
func (m *Manager) FormatPrompt() string {
	skills := m.GetEligibleSkills(nil, nil)
	return FormatSkillsPrompt(skills, true)
}

// FormatPromptForUser generates the skills section filtered by user's role.
// hasSkillsTool indicates whether the user has access to the skills management tool.
func (m *Manager) FormatPromptForUser(u *user.User, rolesConfig user.RolesConfig, hasSkillsTool bool) string {
	skills := m.GetEligibleSkills(u, rolesConfig)
	return FormatSkillsPrompt(skills, hasSkillsTool)
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

	flagged := m.getFlaggedSkillsLocked()
	if len(flagged) > 0 {
		result += fmt.Sprintf("\nFlagged Skills: %d", len(flagged))
		for _, skill := range flagged {
			result += fmt.Sprintf("\n  - %s", m.auditor.FormatStatusLine(skill))
		}
	}

	return result
}

// InstallSkill installs a skill from a source to the workspace skills directory.
func (m *Manager) InstallSkill(ctx context.Context, skillName string, source SourceType, installCfg SkillInstallConfig) (*SkillInstallResult, error) {
	// Validate source is allowed
	switch source {
	case SourceTypeEmbedded:
		if !installCfg.IsEmbeddedAllowed() {
			return nil, fmt.Errorf("embedded source is not enabled in configuration")
		}
	case SourceTypeClawHub:
		if !installCfg.AllowClawHub {
			return nil, fmt.Errorf("ClawHub source is not enabled in configuration")
		}
	case SourceTypeLocal:
		if !installCfg.AllowLocal {
			return nil, fmt.Errorf("local source is not enabled in configuration (security risk)")
		}
	default:
		return nil, fmt.Errorf("unknown source type: %s", source)
	}

	// Check if already installed
	if existing := m.GetSkill(skillName); existing != nil && existing.Source == SourceWorkspace {
		return &SkillInstallResult{
			Success:   false,
			SkillName: skillName,
			Source:    source,
			Message:   "skill already installed in workspace",
		}, nil
	}

	// Install to workspace skills directory
	destDir := m.workspaceDir
	if destDir == "" {
		return nil, fmt.Errorf("workspace skills directory not configured")
	}

	result, err := m.installer.InstallSkillFiles(ctx, skillName, source, destDir, m.auditor)
	if err != nil {
		return nil, err
	}

	// Reload skills if successful and check eligibility
	if result.Success {
		if err := m.Reload(); err != nil {
			L_warn("skills: reload after install failed", "error", err)
		}

		// Check post-install eligibility
		if installed := m.GetSkill(skillName); installed != nil {
			result.Eligible = installed.Eligible && installed.Enabled
			if !installed.Eligible {
				ctx := EligibilityContext{
					OS:         runtime.GOOS,
					ConfigKeys: m.configKeys,
				}
				result.MissingRequirements = installed.GetMissingRequirements(ctx)
				result.Message = fmt.Sprintf("installed %s from %s (ineligible: missing requirements)", skillName, source)
			}
		}
	}

	return result, nil
}

// SearchSkills searches for skills in enabled sources.
// Searches both skill names and descriptions for matches.
func (m *Manager) SearchSkills(query string, installCfg SkillInstallConfig) (*SearchResult, error) {
	result := &SearchResult{
		Query:   query,
		Results: make(map[SourceType][]SkillMatch),
		Hints:   []string{},
	}

	// Search embedded catalog
	if installCfg.IsEmbeddedAllowed() {
		embedded, err := ListEmbedded()
		if err == nil {
			var matches []SkillMatch
			for _, name := range embedded {
				// Get skill content and parse it
				content, err := GetEmbeddedSkillContent(name)
				if err != nil {
					L_debug("search: failed to get content", "skill", name, "error", err)
					continue
				}

				skill, err := ParseSkillContent(content, name, SourceBundled)
				if err != nil {
					L_debug("search: failed to parse", "skill", name, "error", err)
					continue
				}

				// Check if query matches name or description
				matched, where := matchesQuery(skill.Name, skill.Description, query)
				if matched {
					emoji := ""
					if skill.Metadata != nil {
						emoji = skill.Metadata.Emoji
					}
					matches = append(matches, SkillMatch{
						Name:        name,
						Emoji:       emoji,
						Description: skill.Description,
						MatchedIn:   where,
					})
				}
			}
			if len(matches) > 0 {
				result.Results[SourceTypeEmbedded] = matches
			}
		}
	} else {
		result.Hints = append(result.Hints, "Embedded skills are disabled in configuration")
	}

	// ClawHub search (stub for now)
	if installCfg.AllowClawHub {
		// TODO: Implement ClawHub API search
		result.Hints = append(result.Hints, "ClawHub search not yet implemented")
	} else {
		result.Hints = append(result.Hints, "ClawHub is disabled - enable to search public skill repository")
	}

	return result, nil
}

// GetSources returns the available skill sources and their status.
func (m *Manager) GetSources(installCfg SkillInstallConfig) []SourceInfo {
	sources := []SourceInfo{
		{
			Type:        SourceTypeEmbedded,
			Name:        "Embedded Catalog",
			Description: "Skills bundled with GoClaw",
			Enabled:     installCfg.IsEmbeddedAllowed(),
		},
		{
			Type:        SourceTypeClawHub,
			Name:        "ClawHub",
			Description: "Public skill repository at clawhub.ai",
			Enabled:     installCfg.AllowClawHub,
		},
		{
			Type:        SourceTypeLocal,
			Name:        "Local Path",
			Description: "Install from local filesystem (security risk)",
			Enabled:     installCfg.AllowLocal,
		},
	}
	return sources
}

// matchesQuery checks if query matches name or description (case-insensitive).
// Returns (matched, where) - where is "name", "description", or "" if no match.
func matchesQuery(name, description, query string) (bool, string) {
	if query == "" {
		return true, ""
	}

	queryLower := strings.ToLower(query)
	nameLower := strings.ToLower(name)
	descLower := strings.ToLower(description)

	// Check name first (higher priority)
	if strings.Contains(nameLower, queryLower) {
		return true, "name"
	}

	// Check description
	if strings.Contains(descLower, queryLower) {
		return true, "description"
	}

	return false, ""
}

// SearchResult contains the results of a skill search.
type SearchResult struct {
	Query   string                      `json:"query"`
	Results map[SourceType][]SkillMatch `json:"results"`
	Hints   []string                    `json:"hints,omitempty"`
}

// SourceInfo describes a skill installation source.
type SourceInfo struct {
	Type        SourceType `json:"type"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Enabled     bool       `json:"enabled"`
}
