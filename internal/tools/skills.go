package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/skills"
)

// SkillsTool provides agent access to the skills registry
type SkillsTool struct {
	manager *skills.Manager
}

// NewSkillsTool creates a new skills tool
func NewSkillsTool(manager *skills.Manager) *SkillsTool {
	return &SkillsTool{manager: manager}
}

func (t *SkillsTool) Name() string {
	return "skills"
}

func (t *SkillsTool) Description() string {
	return "Query the skills registry. List available skills, check eligibility, get details and install hints. Use this instead of manually reading SKILL.md files."
}

func (t *SkillsTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list", "info", "check"},
				"description": "Action to perform: 'list' all skills, 'info' for details on one skill, 'check' why a skill is ineligible",
			},
			"skill": map[string]interface{}{
				"type":        "string",
				"description": "Skill name (required for 'info' and 'check' actions)",
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

func (t *SkillsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Action  string `json:"action"`
		Skill   string `json:"skill"`
		Filter  string `json:"filter"`
		Verbose bool   `json:"verbose"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if t.manager == nil {
		return "", fmt.Errorf("skills manager not available")
	}

	L_debug("skills tool: executing", "action", params.Action, "skill", params.Skill, "filter", params.Filter)

	switch params.Action {
	case "list":
		return t.executeList(params.Filter, params.Verbose)
	case "info":
		if params.Skill == "" {
			return "", fmt.Errorf("skill name required for 'info' action")
		}
		return t.executeInfo(params.Skill)
	case "check":
		if params.Skill == "" {
			return "", fmt.Errorf("skill name required for 'check' action")
		}
		return t.executeCheck(params.Skill)
	default:
		return "", fmt.Errorf("unknown action: %s (valid: list, info, check)", params.Action)
	}
}

// executeList returns a list of skills with optional filtering
func (t *SkillsTool) executeList(filter string, verbose bool) (string, error) {
	allSkills := t.manager.GetAllSkills()
	eligibleSkills := t.manager.GetEligibleSkills(nil, nil) // No user filtering for listing
	flaggedSkills := t.manager.GetFlaggedSkills()

	// Build lookup maps
	eligibleSet := make(map[string]bool)
	for _, s := range eligibleSkills {
		eligibleSet[s.Name] = true
	}
	flaggedSet := make(map[string]bool)
	for _, s := range flaggedSkills {
		flaggedSet[s.Name] = true
	}

	// Count by status for summary
	var eligibleCount, ineligibleCount, flaggedCount, whitelistedCount int
	for _, s := range allSkills {
		status := t.getStatus(s, eligibleSet, flaggedSet)
		switch status {
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

	// Filter skills
	var filtered []*skills.Skill
	for _, s := range allSkills {
		status := t.getStatus(s, eligibleSet, flaggedSet)
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
		default: // "all" or empty
			filtered = append(filtered, s)
		}
	}

	// Build eligibility context for missing checks
	ctx := skills.EligibilityContext{
		OS: runtime.GOOS,
	}

	// Build response
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
		status := t.getStatus(s, eligibleSet, flaggedSet)
		entry := skillEntry{
			Name:        s.Name,
			Description: s.Description,
			Status:      status,
			Source:      string(s.Source),
		}

		if s.Metadata != nil && s.Metadata.Emoji != "" {
			entry.Emoji = s.Metadata.Emoji
		}

		// Add reasons for non-eligible skills
		if status == "ineligible" || status == "flagged" {
			entry.Reasons = t.getReasons(s, ctx)
		}

		// Add missing info for ineligible skills
		if status == "ineligible" {
			missingBins, missingEnv := t.getMissing(s, ctx)
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

// executeInfo returns detailed information about a specific skill
func (t *SkillsTool) executeInfo(skillName string) (string, error) {
	skill := t.manager.GetSkill(skillName)
	if skill == nil {
		L_warn("skills tool: skill not found", "skill", skillName)
		return "", fmt.Errorf("skill not found: %s", skillName)
	}

	// Build eligibility context
	ctx := skills.EligibilityContext{
		OS: runtime.GOOS,
	}

	// Get missing requirements
	missing := skill.GetMissingRequirements(ctx)

	// Get install options
	installOpts := skill.GetInstallOptions()

	// Determine status
	eligibleSkills := t.manager.GetEligibleSkills(nil, nil) // No user filtering for info
	flaggedSkills := t.manager.GetFlaggedSkills()
	eligibleSet := make(map[string]bool)
	for _, s := range eligibleSkills {
		eligibleSet[s.Name] = true
	}
	flaggedSet := make(map[string]bool)
	for _, s := range flaggedSkills {
		flaggedSet[s.Name] = true
	}
	status := t.getStatus(skill, eligibleSet, flaggedSet)

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
		Path        string          `json:"path"`
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

	// Build install options with commands
	for _, opt := range installOpts {
		io := installOption{
			ID:    opt.ID,
			Kind:  opt.Kind,
			Label: opt.Label,
		}
		// Generate command hint
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

	// Add audit flags if any
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

// executeCheck returns focused eligibility information with fix suggestions
func (t *SkillsTool) executeCheck(skillName string) (string, error) {
	skill := t.manager.GetSkill(skillName)
	if skill == nil {
		L_warn("skills tool: skill not found for check", "skill", skillName)
		return "", fmt.Errorf("skill not found: %s", skillName)
	}

	// Build eligibility context
	ctx := skills.EligibilityContext{
		OS: runtime.GOOS,
	}

	// Get missing requirements
	missing := skill.GetMissingRequirements(ctx)

	// Build reasons and fixes
	var reasons []string
	var fixes []string

	// Check OS requirement
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

	// Add missing requirements as reasons
	for _, m := range missing {
		reasons = append(reasons, m)
	}

	// Check audit flags
	if len(skill.AuditFlags) > 0 && !skill.Whitelisted {
		for _, flag := range skill.AuditFlags {
			reasons = append(reasons, fmt.Sprintf("Security flag: %s (%s)", flag.Pattern, flag.Severity))
		}
		fixes = append(fixes, fmt.Sprintf("Whitelist in config: {\"skills\":{\"entries\":{\"%s\":{\"enabled\":true}}}}", skillName))
	}

	// Generate fix suggestions from install options
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

	// Ensure non-nil slices in JSON
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

// getStatus determines the display status of a skill
func (t *SkillsTool) getStatus(skill *skills.Skill, eligibleSet, flaggedSet map[string]bool) string {
	if skill.Whitelisted {
		return "whitelisted"
	}
	if flaggedSet[skill.Name] {
		return "flagged"
	}
	if eligibleSet[skill.Name] {
		return "eligible"
	}
	return "ineligible"
}

// getReasons returns human-readable reasons why a skill is not eligible
func (t *SkillsTool) getReasons(skill *skills.Skill, ctx skills.EligibilityContext) []string {
	var reasons []string

	// Get missing requirements (handles OS, bins, env, config)
	missing := skill.GetMissingRequirements(ctx)
	for _, m := range missing {
		reasons = append(reasons, m)
	}

	// Security flags
	for _, flag := range skill.AuditFlags {
		reasons = append(reasons, fmt.Sprintf("Security flag: %s (%s)", flag.Pattern, flag.Severity))
	}

	return reasons
}

// getMissing returns which specific binaries and env vars are missing
func (t *SkillsTool) getMissing(skill *skills.Skill, ctx skills.EligibilityContext) (bins []string, env []string) {
	missing := skill.GetMissingRequirements(ctx)

	for _, m := range missing {
		// Parse the "type: value" format from GetMissingRequirements
		if strings.HasPrefix(m, "binary: ") {
			bins = append(bins, strings.TrimPrefix(m, "binary: "))
		} else if strings.HasPrefix(m, "env: ") {
			env = append(env, strings.TrimPrefix(m, "env: "))
		}
	}

	return bins, env
}
