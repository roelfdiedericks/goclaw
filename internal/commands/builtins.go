package commands

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// registerBuiltins registers all built-in commands
func registerBuiltins(m *Manager) {
	m.Register(&Command{
		Name:        "/status",
		Description: "Show session info and compaction health",
		Handler:     handleStatus,
	})

	m.Register(&Command{
		Name:        "/skills",
		Description: "List available skills",
		Handler:     handleSkills,
	})

	m.Register(&Command{
		Name:        "/compact",
		Description: "Force context compaction",
		Handler:     handleCompact,
	})

	m.Register(&Command{
		Name:        "/clear",
		Description: "Clear conversation history",
		Aliases:     []string{"/reset"},
		Handler:     handleClear,
	})

	m.Register(&Command{
		Name:        "/help",
		Description: "Show this help",
		Handler:     handleHelp,
	})

	m.Register(&Command{
		Name:        "/heartbeat",
		Description: "Trigger heartbeat check",
		Handler:     handleHeartbeat,
	})
}

// handleStatus returns session status and compaction health
func handleStatus(ctx context.Context, args *CommandArgs) *CommandResult {
	info, err := args.Provider.GetSessionInfoForCommands(ctx, args.SessionKey)
	if err != nil {
		return &CommandResult{
			Text:  fmt.Sprintf("Error getting session info: %s", err),
			Error: err,
		}
	}

	compStatus := args.Provider.GetCompactionStatus(ctx)

	// Build plain text output
	var text strings.Builder
	text.WriteString("Session Status\n")
	text.WriteString(fmt.Sprintf("  Messages: %d\n", info.Messages))
	text.WriteString(fmt.Sprintf("  Tokens: %d / %d (%.1f%%)\n", info.TotalTokens, info.MaxTokens, info.UsagePercent))
	text.WriteString(fmt.Sprintf("  Compactions: %d\n", info.CompactionCount))

	text.WriteString("\nCompaction Health\n")
	if compStatus.OllamaConfigured {
		ollamaHealth := "healthy"
		if compStatus.UsingFallback {
			ollamaHealth = "degraded"
		} else if !compStatus.OllamaAvailable {
			ollamaHealth = "unavailable"
		}
		text.WriteString(fmt.Sprintf("  Ollama: %s (%d/%d failures)\n",
			ollamaHealth, compStatus.OllamaFailures, compStatus.OllamaThreshold))

		if compStatus.UsingFallback {
			text.WriteString("  Mode: fallback to main model\n")
			if compStatus.MinutesUntilReset > 0 {
				text.WriteString(fmt.Sprintf("  Reset in: %d min\n", compStatus.MinutesUntilReset))
			}
		} else {
			text.WriteString("  Mode: normal\n")
		}

		if !compStatus.LastOllamaAttempt.IsZero() {
			ago := time.Since(compStatus.LastOllamaAttempt)
			text.WriteString(fmt.Sprintf("  Last attempt: %s ago\n", formatDuration(ago)))
		}
	} else {
		text.WriteString("  Ollama: not configured\n")
		text.WriteString("  Mode: main model only\n")
	}

	if compStatus.PendingRetries > 0 {
		text.WriteString(fmt.Sprintf("  Pending retries: %d\n", compStatus.PendingRetries))
	}

	if compStatus.RetryInProgress {
		text.WriteString("  Status: compaction in progress\n")
	}

	// Build markdown output
	var md strings.Builder
	md.WriteString("*Session Status*\n")
	md.WriteString(fmt.Sprintf("Messages: %d\n", info.Messages))
	md.WriteString(fmt.Sprintf("Tokens: %d / %d (%.1f%%)\n", info.TotalTokens, info.MaxTokens, info.UsagePercent))
	md.WriteString(fmt.Sprintf("Compactions: %d\n", info.CompactionCount))

	md.WriteString("\n*Compaction Health*\n")
	if compStatus.OllamaConfigured {
		ollamaHealth := "healthy"
		if compStatus.UsingFallback {
			ollamaHealth = "degraded"
		} else if !compStatus.OllamaAvailable {
			ollamaHealth = "unavailable"
		}
		md.WriteString(fmt.Sprintf("Ollama: %s (%d/%d failures)\n",
			ollamaHealth, compStatus.OllamaFailures, compStatus.OllamaThreshold))

		if compStatus.UsingFallback {
			md.WriteString("Mode: _fallback to main model_\n")
			if compStatus.MinutesUntilReset > 0 {
				md.WriteString(fmt.Sprintf("Reset in: %d min\n", compStatus.MinutesUntilReset))
			}
		} else {
			md.WriteString("Mode: normal\n")
		}
	} else {
		md.WriteString("Ollama: _not configured_\n")
		md.WriteString("Mode: main model only\n")
	}

	if compStatus.PendingRetries > 0 {
		md.WriteString(fmt.Sprintf("Pending retries: %d\n", compStatus.PendingRetries))
	}

	// Add last compaction info if available
	if info.LastCompaction != nil {
		text.WriteString(fmt.Sprintf("\nLast Compaction (%s)\n", info.LastCompaction.Timestamp.Format("2006-01-02 15:04")))
		text.WriteString(fmt.Sprintf("  Tokens before: %d\n", info.LastCompaction.TokensBefore))

		md.WriteString(fmt.Sprintf("\n*Last Compaction* (%s)\n", info.LastCompaction.Timestamp.Format("2006-01-02 15:04")))
		md.WriteString(fmt.Sprintf("Tokens before: %d\n", info.LastCompaction.TokensBefore))

		// Truncate summary if too long
		summary := info.LastCompaction.Summary
		if len(summary) > 500 {
			summary = summary[:500] + "..."
		}
		if summary != "" {
			text.WriteString(fmt.Sprintf("  Summary: %s\n", summary))
			md.WriteString(fmt.Sprintf("Summary: %s\n", summary))
		}
	}

	// Add skills info
	skillsSection := args.Provider.GetSkillsStatusSection()
	if skillsSection != "" {
		text.WriteString("\n")
		text.WriteString(skillsSection)
		text.WriteString("\n")

		md.WriteString("\n*")
		md.WriteString(skillsSection)
		md.WriteString("*\n")
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// handleCompact forces compaction
func handleCompact(ctx context.Context, args *CommandArgs) *CommandResult {
	result, err := args.Provider.ForceCompact(ctx, args.SessionKey)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Compaction failed: %s", err),
			Markdown: fmt.Sprintf("Compaction failed: `%s`", err),
			Error:    err,
		}
	}

	var text strings.Builder
	text.WriteString("Compaction completed!\n")
	text.WriteString(fmt.Sprintf("  Tokens before: %d\n", result.TokensBefore))

	source := "LLM"
	if result.FromCheckpoint {
		source = "checkpoint"
	} else if result.EmergencyTruncation {
		source = "emergency truncation"
	} else if result.UsedFallback {
		source = "fallback model"
	}
	text.WriteString(fmt.Sprintf("  Summary source: %s\n", source))

	var md strings.Builder
	md.WriteString("*Compaction completed!*\n")
	md.WriteString(fmt.Sprintf("Tokens before: %d\n", result.TokensBefore))
	md.WriteString(fmt.Sprintf("Summary source: _%s_\n", source))

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// handleClear resets the session
func handleClear(ctx context.Context, args *CommandArgs) *CommandResult {
	err := args.Provider.ResetSession(args.SessionKey)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Failed to clear session: %s", err),
			Markdown: fmt.Sprintf("Failed to clear session: `%s`", err),
			Error:    err,
		}
	}

	return &CommandResult{
		Text:     "Session cleared.",
		Markdown: "Session cleared.",
	}
}

// handleSkills returns the list of available skills
func handleSkills(ctx context.Context, args *CommandArgs) *CommandResult {
	result := args.Provider.GetSkillsListForCommand()
	if result == nil {
		return &CommandResult{
			Text:     "Skills system not available",
			Markdown: "Skills system not available",
		}
	}

	var text strings.Builder
	var md strings.Builder

	// Header
	text.WriteString(fmt.Sprintf("Skills: %d total, %d eligible, %d ineligible",
		result.Total, result.Eligible, result.Ineligible))
	if result.Whitelisted > 0 {
		text.WriteString(fmt.Sprintf(", %d whitelisted", result.Whitelisted))
	}
	if result.Flagged > 0 {
		text.WriteString(fmt.Sprintf(", %d flagged", result.Flagged))
	}
	text.WriteString("\n\n")

	md.WriteString(fmt.Sprintf("**Skills:** %d total, %d eligible, %d ineligible",
		result.Total, result.Eligible, result.Ineligible))
	if result.Whitelisted > 0 {
		md.WriteString(fmt.Sprintf(", %d whitelisted", result.Whitelisted))
	}
	if result.Flagged > 0 {
		md.WriteString(fmt.Sprintf(", %d flagged", result.Flagged))
	}
	md.WriteString("\n\n")

	// Group by status
	var ready, whitelisted, ineligible, flagged []SkillInfo
	for _, s := range result.Skills {
		switch s.Status {
		case "ready":
			ready = append(ready, s)
		case "whitelisted":
			whitelisted = append(whitelisted, s)
		case "ineligible":
			ineligible = append(ineligible, s)
		case "flagged":
			flagged = append(flagged, s)
		}
	}

	// Ready skills
	if len(ready) > 0 {
		text.WriteString("Ready:\n")
		md.WriteString("**Ready:**\n")
		for _, s := range ready {
			emoji := s.Emoji
			if emoji == "" {
				emoji = "•"
			}
			text.WriteString(fmt.Sprintf("  %s %s", emoji, s.Name))
			md.WriteString(fmt.Sprintf("%s %s", emoji, s.Name))
			if s.Description != "" {
				text.WriteString(fmt.Sprintf(" - %s", truncate(s.Description, 40)))
				md.WriteString(fmt.Sprintf(" - %s", truncate(s.Description, 40)))
			}
			text.WriteString("\n")
			md.WriteString("\n")
		}
		text.WriteString("\n")
		md.WriteString("\n")
	}

	// Whitelisted skills (manually enabled despite audit flags)
	if len(whitelisted) > 0 {
		text.WriteString(fmt.Sprintf("Whitelisted (%d):\n", len(whitelisted)))
		md.WriteString(fmt.Sprintf("**✓ Whitelisted** (%d):\n", len(whitelisted)))
		for _, s := range whitelisted {
			emoji := s.Emoji
			if emoji == "" {
				emoji = "✓"
			}
			text.WriteString(fmt.Sprintf("  %s %s", emoji, s.Name))
			md.WriteString(fmt.Sprintf("%s %s", emoji, s.Name))
			if s.Reason != "" {
				text.WriteString(fmt.Sprintf(" (was: %s)", s.Reason))
				md.WriteString(fmt.Sprintf(" _(was: %s)_", s.Reason))
			}
			text.WriteString("\n")
			md.WriteString("\n")
		}
		text.WriteString("\n")
		md.WriteString("\n")
	}

	// Ineligible skills (summarized)
	if len(ineligible) > 0 {
		text.WriteString(fmt.Sprintf("Ineligible (%d):\n", len(ineligible)))
		md.WriteString(fmt.Sprintf("**Ineligible** (%d):\n", len(ineligible)))
		for _, s := range ineligible {
			text.WriteString(fmt.Sprintf("  • %s", s.Name))
			md.WriteString(fmt.Sprintf("• %s", s.Name))
			if s.Reason != "" {
				text.WriteString(fmt.Sprintf(" - %s", s.Reason))
				md.WriteString(fmt.Sprintf(" _%s_", s.Reason))
			}
			text.WriteString("\n")
			md.WriteString("\n")
		}
		text.WriteString("\n")
		md.WriteString("\n")
	}

	// Flagged skills
	if len(flagged) > 0 {
		text.WriteString(fmt.Sprintf("Flagged (%d):\n", len(flagged)))
		md.WriteString(fmt.Sprintf("**⚠️ Flagged** (%d):\n", len(flagged)))
		for _, s := range flagged {
			text.WriteString(fmt.Sprintf("  ⚠️ %s", s.Name))
			md.WriteString(fmt.Sprintf("⚠️ %s", s.Name))
			if s.Reason != "" {
				text.WriteString(fmt.Sprintf(" - %s", s.Reason))
				md.WriteString(fmt.Sprintf(" _%s_", s.Reason))
			}
			text.WriteString("\n")
			md.WriteString("\n")
		}
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// handleHelp returns available commands (generated from registry)
func handleHelp(ctx context.Context, args *CommandArgs) *CommandResult {
	mgr := GetManager()
	cmds := mgr.List()

	var text strings.Builder
	var md strings.Builder

	text.WriteString("Available commands:\n")
	md.WriteString("*Available commands:*\n")

	for _, cmd := range cmds {
		text.WriteString(fmt.Sprintf("  %s - %s\n", cmd.Name, cmd.Description))
		md.WriteString(fmt.Sprintf("%s - %s\n", cmd.Name, cmd.Description))
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// handleHeartbeat triggers a heartbeat check
func handleHeartbeat(ctx context.Context, args *CommandArgs) *CommandResult {
	err := args.Provider.TriggerHeartbeat(ctx)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Heartbeat failed: %s", err),
			Markdown: fmt.Sprintf("Heartbeat failed: `%s`", err),
			Error:    err,
		}
	}

	return &CommandResult{
		Text:     "Heartbeat triggered.",
		Markdown: "Heartbeat triggered.",
	}
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d sec", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d hr", int(d.Hours()))
}
