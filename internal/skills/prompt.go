package skills

import (
	"fmt"
	"html"
	"strings"
)

// FormatSkillsPrompt generates the skills section for the system prompt.
// hasSkillsTool indicates whether the user has access to the skills management tool.
// Returns empty string if no skills are eligible.
func FormatSkillsPrompt(skills []*Skill, hasSkillsTool bool) string {
	if len(skills) == 0 && !hasSkillsTool {
		return ""
	}

	var sb strings.Builder

	// Header
	sb.WriteString("<agent_skills>\n")
	sb.WriteString("When users ask you to perform tasks, check if any of the available skills below can help complete the task more effectively. ")
	sb.WriteString("Skills provide specialized capabilities and domain knowledge. ")
	sb.WriteString("To use a skill, read the skill file at the provided absolute path using the Read tool, then follow the instructions within. ")
	sb.WriteString("When a skill is relevant, read and follow it IMMEDIATELY as your first action. ")
	sb.WriteString("NEVER just announce or mention a skill without actually reading and following it. ")
	sb.WriteString("Only use skills listed below.\n\n")

	if len(skills) > 0 {
		// Skills list
		sb.WriteString("<available_skills description=\"Skills the agent can use. Use the Read tool with the provided absolute path to fetch full contents.\">\n")

		for _, skill := range skills {
			sb.WriteString(formatSkillXML(skill))
			sb.WriteString("\n")
		}

		sb.WriteString("</available_skills>\n\n")
	}

	// Skills tool guidance
	if hasSkillsTool {
		sb.WriteString("To discover and install additional skills, use the skills tool (action='search' to find, action='info' to preview, action='install' to add). ")
		sb.WriteString("Embedded and remote skills are not accessible via the filesystem - use the skills tool to inspect them.\n")
	} else {
		sb.WriteString("You cannot install or manage additional skills. Only the skills listed above are available.\n")
	}

	sb.WriteString("</agent_skills>")

	return sb.String()
}

// formatSkillXML formats a single skill as an XML element.
func formatSkillXML(skill *Skill) string {
	// Build description - use skill description or generate one
	desc := skill.Description
	if desc == "" {
		desc = fmt.Sprintf("Skill: %s", skill.Name)
	}

	// Escape XML entities
	desc = html.EscapeString(desc)
	location := html.EscapeString(skill.Location)

	// Add emoji if present
	emoji := ""
	if skill.Metadata != nil && skill.Metadata.Emoji != "" {
		emoji = skill.Metadata.Emoji + " "
	}

	return fmt.Sprintf(`<agent_skill fullPath="%s">%s%s</agent_skill>`,
		location, emoji, desc)
}

// FormatSkillsList formats skills as a simple list (for /status etc).
func FormatSkillsList(skills []*Skill) string {
	if len(skills) == 0 {
		return "No skills loaded"
	}

	var sb strings.Builder
	for i, skill := range skills {
		if i > 0 {
			sb.WriteString("\n")
		}

		// Add emoji if present
		if skill.Metadata != nil && skill.Metadata.Emoji != "" {
			sb.WriteString(skill.Metadata.Emoji)
			sb.WriteString(" ")
		}

		sb.WriteString(skill.Name)

		// Add source
		sb.WriteString(" (")
		sb.WriteString(string(skill.Source))
		sb.WriteString(")")

		// Add eligibility status
		if !skill.Eligible {
			sb.WriteString(" [ineligible]")
		}
		if !skill.Enabled {
			sb.WriteString(" [disabled]")
		}
	}

	return sb.String()
}

// FormatSkillsTable formats skills as a table (for TUI/terminal).
func FormatSkillsTable(skills []*Skill) string {
	if len(skills) == 0 {
		return "No skills loaded"
	}

	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("%-20s %-10s %-10s %s\n", "Name", "Source", "Status", "Description"))
	sb.WriteString(strings.Repeat("-", 70))
	sb.WriteString("\n")

	for _, skill := range skills {
		name := skill.Name
		if len(name) > 20 {
			name = name[:17] + "..."
		}

		source := string(skill.Source)

		status := "ready"
		if !skill.Eligible {
			status = "ineligible"
		} else if !skill.Enabled {
			status = "disabled"
		}

		desc := skill.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}

		sb.WriteString(fmt.Sprintf("%-20s %-10s %-10s %s\n", name, source, status, desc))
	}

	return sb.String()
}

// FormatSkillsMarkdown formats skills as markdown (for Telegram/Discord).
func FormatSkillsMarkdown(skills []*Skill) string {
	if len(skills) == 0 {
		return "No skills loaded"
	}

	var sb strings.Builder
	sb.WriteString("**Available Skills:**\n\n")

	// Group by source
	bySource := make(map[Source][]*Skill)
	for _, skill := range skills {
		bySource[skill.Source] = append(bySource[skill.Source], skill)
	}

	// Output in precedence order
	sources := []Source{SourceWorkspace, SourceManaged, SourceBundled, SourceExtra}
	for _, source := range sources {
		srcSkills := bySource[source]
		if len(srcSkills) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("**%s:**\n", formatSourceName(source)))
		for _, skill := range srcSkills {
			emoji := ""
			if skill.Metadata != nil && skill.Metadata.Emoji != "" {
				emoji = skill.Metadata.Emoji + " "
			}

			status := ""
			if !skill.Eligible {
				status = " _(ineligible)_"
			} else if !skill.Enabled {
				status = " _(disabled)_"
			}

			sb.WriteString(fmt.Sprintf("- %s%s%s\n", emoji, skill.Name, status))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatSourceName returns a human-readable source name.
func formatSourceName(source Source) string {
	switch source {
	case SourceBundled:
		return "Bundled"
	case SourceManaged:
		return "Managed"
	case SourceWorkspace:
		return "Workspace"
	case SourceExtra:
		return "Extra"
	default:
		return string(source)
	}
}
