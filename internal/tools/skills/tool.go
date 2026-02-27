package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	skillspkg "github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Tool provides agent access to the skills registry
type Tool struct {
	manager    *skillspkg.Manager
	installCfg skillspkg.SkillInstallConfig
}

// NewTool creates a new skills tool
func NewTool(manager *skillspkg.Manager) *Tool {
	return &Tool{manager: manager}
}

// SetInstallConfig sets the installation configuration for the tool
func (t *Tool) SetInstallConfig(cfg skillspkg.SkillInstallConfig) {
	t.installCfg = cfg
}

func (t *Tool) Name() string {
	return "skills"
}

func (t *Tool) Description() string {
	return "Manage skills: list installed, search catalog, get info, check eligibility, and install. Works on both installed skills AND embedded catalog. Note: embedded/remote skills are not filesystem-accessible - use this tool instead of reading or grepping skill files."
}

func (t *Tool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list", "info", "check", "install", "search", "sources", "reload"},
				"description": "Action to perform: 'list' installed skills, 'info' details, 'check' eligibility, 'install' from source, 'search' available, 'sources' list repos, 'reload' rescan directories",
			},
			"skill": map[string]interface{}{
				"type":        "string",
				"description": "Skill name (required for 'info', 'check', and 'install' actions)",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query for 'search' action (optional, empty lists all)",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"embedded", "clawhub", "local"},
				"description": "Source to install from (required for 'install' action, default: 'embedded')",
			},
			"filter": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"all", "eligible", "ineligible", "flagged", "whitelisted"},
				"description": "Filter for 'list' action (default: 'all')",
			},
			"verbose": map[string]interface{}{
				"type":        "boolean",
				"description": "Include full details in list output (default: false)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Action  string `json:"action"`
		Skill   string `json:"skill"`
		Query   string `json:"query"`
		Source  string `json:"source"`
		Filter  string `json:"filter"`
		Verbose bool   `json:"verbose"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if t.manager == nil {
		return nil, fmt.Errorf("skills manager not available")
	}

	L_debug("skills tool: executing", "action", params.Action, "skill", params.Skill, "filter", params.Filter)

	var result string
	var err error

	switch params.Action {
	case "list":
		result, err = t.executeList(params.Filter, params.Verbose)
	case "info":
		if params.Skill == "" {
			return nil, fmt.Errorf("skill name required for 'info' action")
		}
		result, err = t.executeInfo(params.Skill)
	case "check":
		if params.Skill == "" {
			return nil, fmt.Errorf("skill name required for 'check' action")
		}
		result, err = t.executeCheck(params.Skill)
	case "install":
		if params.Skill == "" {
			return nil, fmt.Errorf("skill name required for 'install' action")
		}
		result, err = t.executeInstall(ctx, params.Skill, params.Source)
	case "search":
		result, err = t.executeSearch(params.Query)
	case "sources":
		result, err = t.executeSources()
	case "reload":
		result, err = t.executeReload()
	default:
		return nil, fmt.Errorf("unknown action: %s (valid: list, info, check, install, search, sources, reload)", params.Action)
	}

	if err != nil {
		return nil, err
	}
	return types.TextResult(result), nil
}

func (t *Tool) executeList(filter string, verbose bool) (string, error) {
	allSkills := t.manager.GetAllSkills()

	var eligibleCount, ineligibleCount, flaggedCount, whitelistedCount int
	for _, s := range allSkills {
		switch t.deriveStatus(s) {
		case "eligible":
			eligibleCount++
		case "ineligible":
			ineligibleCount++
		case "flagged":
			flaggedCount++
		case "whitelisted":
			whitelistedCount++
		}
	}

	var filtered []*skillspkg.Skill
	for _, s := range allSkills {
		status := t.deriveStatus(s)
		switch filter {
		case "eligible":
			if status == "eligible" || status == "whitelisted" {
				filtered = append(filtered, s)
			}
		case "ineligible":
			if status == "ineligible" {
				filtered = append(filtered, s)
			}
		case "flagged":
			if status == "flagged" {
				filtered = append(filtered, s)
			}
		case "whitelisted":
			if status == "whitelisted" {
				filtered = append(filtered, s)
			}
		default:
			filtered = append(filtered, s)
		}
	}

	eligCtx := skillspkg.EligibilityContext{
		OS: runtime.GOOS,
	}

	type requiresInfo struct {
		Bins []string `json:"bins,omitempty"`
		Env  []string `json:"env,omitempty"`
		OS   []string `json:"os,omitempty"`
	}

	type missingInfo struct {
		Bins []string `json:"bins,omitempty"`
		Env  []string `json:"env,omitempty"`
	}

	type skillEntry struct {
		Name        string        `json:"name"`
		Emoji       string        `json:"emoji,omitempty"`
		Description string        `json:"description"`
		Status      string        `json:"status"`
		Reasons     []string      `json:"reasons,omitempty"`
		Source      string        `json:"source"`
		Path        string        `json:"path,omitempty"`
		Requires    *requiresInfo `json:"requires,omitempty"`
		Missing     *missingInfo  `json:"missing,omitempty"`
	}

	type summaryInfo struct {
		Total       int `json:"total"`
		Eligible    int `json:"eligible"`
		Ineligible  int `json:"ineligible"`
		Flagged     int `json:"flagged"`
		Whitelisted int `json:"whitelisted"`
	}

	type listResponse struct {
		Summary summaryInfo  `json:"summary"`
		Filter  string       `json:"filter"`
		Count   int          `json:"count"`
		Skills  []skillEntry `json:"skills"`
	}

	resp := listResponse{
		Summary: summaryInfo{
			Total:       len(allSkills),
			Eligible:    eligibleCount,
			Ineligible:  ineligibleCount,
			Flagged:     flaggedCount,
			Whitelisted: whitelistedCount,
		},
		Count:  len(filtered),
		Filter: filter,
		Skills: make([]skillEntry, 0, len(filtered)),
	}

	if filter == "" {
		resp.Filter = "all"
	}

	for _, s := range filtered {
		status := t.deriveStatus(s)
		entry := skillEntry{
			Name:        s.Name,
			Description: s.Description,
			Status:      status,
			Source:      string(s.Source),
		}

		if s.Metadata != nil && s.Metadata.Emoji != "" {
			entry.Emoji = s.Metadata.Emoji
		}

		if status == "ineligible" || status == "flagged" {
			entry.Reasons = t.getReasons(s, eligCtx)
		}

		if status == "ineligible" {
			missingBins, missingEnv := t.getMissing(s, eligCtx)
			if len(missingBins) > 0 || len(missingEnv) > 0 {
				entry.Missing = &missingInfo{
					Bins: missingBins,
					Env:  missingEnv,
				}
			}
		}

		if verbose {
			entry.Path = s.Location
			if s.Metadata != nil && s.Metadata.Requires != nil {
				entry.Requires = &requiresInfo{
					Bins: s.Metadata.Requires.Bins,
					Env:  s.Metadata.Requires.Env,
				}
			}
			if s.Metadata != nil && len(s.Metadata.OS) > 0 {
				if entry.Requires == nil {
					entry.Requires = &requiresInfo{}
				}
				entry.Requires.OS = s.Metadata.OS
			}
		}

		resp.Skills = append(resp.Skills, entry)
	}

	L_info("skills tool: list", "filter", resp.Filter, "count", resp.Count)

	result, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}
	return string(result), nil
}

func (t *Tool) executeInfo(skillName string) (string, error) {
	skill := t.manager.GetSkill(skillName)
	fromEmbedded := false

	// If not installed, check embedded catalog
	if skill == nil {
		if skillspkg.SkillExistsInCatalog(skillName) {
			content, err := skillspkg.GetEmbeddedSkillContent(skillName)
			if err != nil {
				L_warn("skills tool: failed to get embedded content", "skill", skillName, "error", err)
				return "", fmt.Errorf("skill not found: %s", skillName)
			}
			skill, err = skillspkg.ParseSkillContent(content, skillName, skillspkg.SourceBundled)
			if err != nil {
				L_warn("skills tool: failed to parse embedded skill", "skill", skillName, "error", err)
				return "", fmt.Errorf("skill not found: %s", skillName)
			}
			fromEmbedded = true
			L_debug("skills tool: loaded from embedded catalog", "skill", skillName)
		} else {
			L_warn("skills tool: skill not found", "skill", skillName)
			return "", fmt.Errorf("skill not found: %s", skillName)
		}
	}

	eligCtx := skillspkg.EligibilityContext{
		OS: runtime.GOOS,
	}

	missing := skill.GetMissingRequirements(eligCtx)
	installOpts := skill.GetInstallOptions()

	var status string
	if fromEmbedded {
		status = "available"
	} else {
		status = t.deriveStatus(skill)
	}

	type requiresInfo struct {
		Bins []string `json:"bins,omitempty"`
		Env  []string `json:"env,omitempty"`
		OS   []string `json:"os,omitempty"`
	}

	type installOption struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Label   string `json:"label,omitempty"`
		Command string `json:"command,omitempty"`
	}

	type auditFlag struct {
		Pattern  string `json:"pattern"`
		Severity string `json:"severity"`
		Line     int    `json:"line,omitempty"`
	}

	type infoResponse struct {
		Name        string          `json:"name"`
		Emoji       string          `json:"emoji,omitempty"`
		Description string          `json:"description"`
		Status      string          `json:"status"`
		Path        string          `json:"path,omitempty"`
		Source      string          `json:"source"`
		Requires    *requiresInfo   `json:"requires,omitempty"`
		Missing     []string        `json:"missing,omitempty"`
		Install     []installOption `json:"install,omitempty"`
		AuditFlags  []auditFlag     `json:"audit_flags,omitempty"`
	}

	resp := infoResponse{
		Name:        skill.Name,
		Description: skill.Description,
		Status:      status,
		Path:        skill.Location,
		Source:      string(skill.Source),
		Missing:     missing,
	}

	if skill.Metadata != nil {
		if skill.Metadata.Emoji != "" {
			resp.Emoji = skill.Metadata.Emoji
		}
		if skill.Metadata.Requires != nil {
			resp.Requires = &requiresInfo{
				Bins: skill.Metadata.Requires.Bins,
				Env:  skill.Metadata.Requires.Env,
			}
		}
		if len(skill.Metadata.OS) > 0 {
			if resp.Requires == nil {
				resp.Requires = &requiresInfo{}
			}
			resp.Requires.OS = skill.Metadata.OS
		}
	}

	for _, opt := range installOpts {
		io := installOption{
			ID:    opt.ID,
			Kind:  opt.Kind,
			Label: opt.Label,
		}
		switch opt.Kind {
		case "brew":
			io.Command = fmt.Sprintf("brew install %s", opt.Formula)
		case "go":
			io.Command = fmt.Sprintf("go install %s@latest", opt.Module)
		case "uv":
			io.Command = fmt.Sprintf("uv tool install %s", opt.Package)
		case "cargo":
			io.Command = fmt.Sprintf("cargo install %s", opt.Package)
		}
		resp.Install = append(resp.Install, io)
	}

	for _, flag := range skill.AuditFlags {
		resp.AuditFlags = append(resp.AuditFlags, auditFlag{
			Pattern:  flag.Pattern,
			Severity: flag.Severity,
			Line:     flag.Line,
		})
	}

	L_info("skills tool: info", "skill", skillName, "status", status)

	result, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}
	return string(result), nil
}

func (t *Tool) executeCheck(skillName string) (string, error) {
	skill := t.manager.GetSkill(skillName)
	if skill == nil {
		L_warn("skills tool: skill not found for check", "skill", skillName)
		return "", fmt.Errorf("skill not found: %s", skillName)
	}

	eligCtx := skillspkg.EligibilityContext{
		OS: runtime.GOOS,
	}

	missing := skill.GetMissingRequirements(eligCtx)

	var reasons []string
	var fixes []string

	if skill.Metadata != nil && len(skill.Metadata.OS) > 0 {
		osOK := false
		for _, os := range skill.Metadata.OS {
			if os == runtime.GOOS {
				osOK = true
				break
			}
		}
		if !osOK {
			reasons = append(reasons, fmt.Sprintf("Requires %s (current: %s)", strings.Join(skill.Metadata.OS, " or "), runtime.GOOS))
		}
	}

	for _, m := range missing {
		reasons = append(reasons, m)
	}

	if len(skill.AuditFlags) > 0 && !skill.Whitelisted {
		for _, flag := range skill.AuditFlags {
			reasons = append(reasons, fmt.Sprintf("Security flag: %s (%s)", flag.Pattern, flag.Severity))
		}
		fixes = append(fixes, fmt.Sprintf("Whitelist in config: {\"skills\":{\"entries\":{\"%s\":{\"enabled\":true}}}}", skillName))
	}

	installOpts := skill.GetInstallOptions()
	for _, opt := range installOpts {
		switch opt.Kind {
		case "brew":
			fixes = append(fixes, fmt.Sprintf("brew install %s", opt.Formula))
		case "go":
			fixes = append(fixes, fmt.Sprintf("go install %s@latest", opt.Module))
		case "uv":
			fixes = append(fixes, fmt.Sprintf("uv tool install %s", opt.Package))
		case "cargo":
			fixes = append(fixes, fmt.Sprintf("cargo install %s", opt.Package))
		case "apt":
			fixes = append(fixes, fmt.Sprintf("apt install %s", opt.Package))
		}
	}

	type checkResponse struct {
		Name     string   `json:"name"`
		Eligible bool     `json:"eligible"`
		Reasons  []string `json:"reasons"`
		Fixes    []string `json:"fixes"`
	}

	resp := checkResponse{
		Name:     skill.Name,
		Eligible: skill.Eligible && skill.Enabled,
		Reasons:  reasons,
		Fixes:    fixes,
	}

	if resp.Reasons == nil {
		resp.Reasons = []string{}
	}
	if resp.Fixes == nil {
		resp.Fixes = []string{}
	}

	L_info("skills tool: check", "skill", skillName, "eligible", resp.Eligible, "reasons", len(resp.Reasons))

	result, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}
	return string(result), nil
}

func (t *Tool) executeInstall(ctx context.Context, skillName, sourceStr string) (string, error) {
	// Default to embedded if not specified
	if sourceStr == "" {
		sourceStr = "embedded"
	}

	source := skillspkg.SourceType(sourceStr)

	// Validate source type
	switch source {
	case skillspkg.SourceTypeEmbedded, skillspkg.SourceTypeClawHub, skillspkg.SourceTypeLocal:
		// Valid
	default:
		return "", fmt.Errorf("invalid source: %s (valid: embedded, clawhub, local)", sourceStr)
	}

	L_info("skills tool: installing", "skill", skillName, "source", source)

	result, err := t.manager.InstallSkill(ctx, skillName, source, t.installCfg)
	if err != nil {
		return "", err
	}

	type installResponse struct {
		Success   bool     `json:"success"`
		SkillName string   `json:"skill_name"`
		Source    string   `json:"source"`
		Message   string   `json:"message"`
		Flagged   bool     `json:"flagged,omitempty"`
		Warnings  []string `json:"warnings,omitempty"`
	}

	resp := installResponse{
		Success:   result.Success,
		SkillName: result.SkillName,
		Source:    string(result.Source),
		Message:   result.Message,
		Flagged:   result.Flagged,
	}

	// Convert AuditWarning to strings for output
	for _, w := range result.Warnings {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("[%s] %s: %s (line %d)", w.Severity, w.Pattern, w.Match, w.Line))
	}

	output, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(output), nil
}

func (t *Tool) executeSearch(query string) (string, error) {
	L_info("skills tool: search", "query", query)

	result, err := t.manager.SearchSkills(query, t.installCfg)
	if err != nil {
		return "", err
	}

	type searchResponse struct {
		Query   string                            `json:"query"`
		Results map[string][]skillspkg.SkillMatch `json:"results"`
		Total   int                               `json:"total"`
		Hints   []string                          `json:"hints,omitempty"`
	}

	resp := searchResponse{
		Query:   result.Query,
		Results: make(map[string][]skillspkg.SkillMatch),
		Hints:   result.Hints,
	}

	// Convert SourceType keys to strings
	total := 0
	for source, matches := range result.Results {
		resp.Results[string(source)] = matches
		total += len(matches)
	}
	resp.Total = total

	output, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(output), nil
}

func (t *Tool) executeSources() (string, error) {
	L_info("skills tool: listing sources")

	sources := t.manager.GetSources(t.installCfg)

	type sourceEntry struct {
		Type        string `json:"type"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Enabled     bool   `json:"enabled"`
	}

	type sourcesResponse struct {
		Sources []sourceEntry `json:"sources"`
		Hints   []string      `json:"hints,omitempty"`
	}

	resp := sourcesResponse{
		Sources: make([]sourceEntry, 0, len(sources)),
	}

	for _, s := range sources {
		resp.Sources = append(resp.Sources, sourceEntry{
			Type:        string(s.Type),
			Name:        s.Name,
			Description: s.Description,
			Enabled:     s.Enabled,
		})

		// Add hints for disabled sources
		if !s.Enabled {
			switch s.Type {
			case skillspkg.SourceTypeClawHub:
				resp.Hints = append(resp.Hints, "ClawHub is disabled. Enable in config to access public skill repository.")
			case skillspkg.SourceTypeLocal:
				resp.Hints = append(resp.Hints, "Local path installation is disabled (security risk). Enable only if necessary.")
			}
		}
	}

	output, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(output), nil
}

func (t *Tool) executeReload() (string, error) {
	L_info("skills tool: reloading")

	if err := t.manager.Reload(); err != nil {
		return "", fmt.Errorf("reload failed: %w", err)
	}

	stats := t.manager.GetStats()

	type reloadResponse struct {
		Success  bool   `json:"success"`
		Message  string `json:"message"`
		Total    int    `json:"total"`
		Eligible int    `json:"eligible"`
		Flagged  int    `json:"flagged"`
	}

	resp := reloadResponse{
		Success:  true,
		Message:  "Skills reloaded successfully",
		Total:    stats.TotalSkills,
		Eligible: stats.EligibleSkills,
		Flagged:  stats.FlaggedSkills,
	}

	output, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(output), nil
}

// deriveStatus returns the status string for a skill based on its flags.
func (t *Tool) deriveStatus(skill *skillspkg.Skill) string {
	if skill.Whitelisted {
		return "whitelisted"
	}
	if !skill.Eligible {
		return "ineligible"
	}
	if len(skill.AuditFlags) > 0 && !skill.Enabled {
		return "flagged"
	}
	if skill.Eligible && skill.Enabled {
		return "eligible"
	}
	return "disabled"
}

func (t *Tool) getReasons(skill *skillspkg.Skill, ctx skillspkg.EligibilityContext) []string {
	var reasons []string

	missing := skill.GetMissingRequirements(ctx)
	for _, m := range missing {
		reasons = append(reasons, m)
	}

	for _, flag := range skill.AuditFlags {
		reasons = append(reasons, fmt.Sprintf("Security flag: %s (%s)", flag.Pattern, flag.Severity))
	}

	return reasons
}

func (t *Tool) getMissing(skill *skillspkg.Skill, ctx skillspkg.EligibilityContext) (bins []string, env []string) {
	missing := skill.GetMissingRequirements(ctx)

	for _, m := range missing {
		if strings.HasPrefix(m, "binary: ") {
			bins = append(bins, strings.TrimPrefix(m, "binary: "))
		} else if strings.HasPrefix(m, "env: ") {
			env = append(env, strings.TrimPrefix(m, "env: "))
		}
	}

	return bins, env
}
