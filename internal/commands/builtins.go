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
		Name:        "/cleartool",
		Description: "Delete all tool messages (fixes corruption)",
		Handler:     handleClearTool,
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

	m.Register(&Command{
		Name:        "/hass",
		Description: "Home Assistant status and debug",
		Handler:     handleHass,
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
	if compStatus.ClientAvailable {
		text.WriteString("  LLM: available\n")
	} else {
		text.WriteString("  LLM: unavailable\n")
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
	if compStatus.ClientAvailable {
		md.WriteString("LLM: available\n")
	} else {
		md.WriteString("LLM: _unavailable_\n")
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

	// Determine source type for display
	sourceType := "LLM"
	if result.FromCheckpoint {
		sourceType = "checkpoint"
	} else if result.Model == "pending" {
		sourceType = "async (generating)"
	} else if result.UsedFallback {
		sourceType = "fallback"
	}

	// Build model display string
	modelDisplay := result.Model
	if modelDisplay == "" {
		modelDisplay = "unknown"
	}

	// Calculate reduction percentage
	reduction := 0.0
	if result.TokensBefore > 0 {
		reduction = float64(result.TokensBefore-result.TokensAfter) / float64(result.TokensBefore) * 100
	}

	var text strings.Builder
	text.WriteString("Compaction completed!\n")
	text.WriteString(fmt.Sprintf("  Tokens: %d → %d (%.0f%% reduction)\n", result.TokensBefore, result.TokensAfter, reduction))
	text.WriteString(fmt.Sprintf("  Messages after: %d\n", result.MessagesAfter))
	text.WriteString(fmt.Sprintf("  Summary: %s (%s)\n", sourceType, modelDisplay))

	var md strings.Builder
	md.WriteString("*Compaction completed!*\n")
	md.WriteString(fmt.Sprintf("Tokens: %d → %d (%.0f%% reduction)\n", result.TokensBefore, result.TokensAfter, reduction))
	md.WriteString(fmt.Sprintf("Messages after: %d\n", result.MessagesAfter))
	md.WriteString(fmt.Sprintf("Summary: _%s_ (`%s`)\n", sourceType, modelDisplay))

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

// handleClearTool removes recent tool_use/tool_result messages to fix corruption
func handleClearTool(ctx context.Context, args *CommandArgs) *CommandResult {
	deleted, err := args.Provider.CleanOrphanedToolMessages(ctx, args.SessionKey)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Failed to clean tool messages: %s", err),
			Markdown: fmt.Sprintf("Failed to clean tool messages: `%s`", err),
			Error:    err,
		}
	}

	if deleted == 0 {
		return &CommandResult{
			Text:     "No tool messages found.",
			Markdown: "No tool messages found.",
		}
	}

	return &CommandResult{
		Text:     fmt.Sprintf("Deleted %d recent tool messages.", deleted),
		Markdown: fmt.Sprintf("Deleted **%d** recent tool messages.", deleted),
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

// handleHass handles the /hass command and subcommands
func handleHass(ctx context.Context, args *CommandArgs) *CommandResult {
	info := args.Provider.GetHassInfo()
	if !info.Configured {
		return &CommandResult{
			Text:     "Home Assistant not configured",
			Markdown: "Home Assistant not configured",
		}
	}

	parts := strings.Fields(args.RawArgs)
	if len(parts) == 0 {
		// Default to info
		return hassInfo(info)
	}

	switch parts[0] {
	case "debug":
		return hassDebug(args, parts[1:])
	case "info":
		return hassInfo(info)
	case "subs":
		return hassSubs(args)
	default:
		return &CommandResult{
			Text:     fmt.Sprintf("Unknown subcommand: %s\nUsage: /hass [debug|info|subs]", parts[0]),
			Markdown: fmt.Sprintf("Unknown subcommand: `%s`\nUsage: `/hass [debug|info|subs]`", parts[0]),
		}
	}
}

// hassInfo shows Home Assistant connection status
func hassInfo(info *HassInfo) *CommandResult {
	var text strings.Builder
	var md strings.Builder

	text.WriteString("Home Assistant Status\n")
	md.WriteString("**Home Assistant Status**\n\n")

	text.WriteString(fmt.Sprintf("  State: %s\n", info.State))
	md.WriteString(fmt.Sprintf("State: %s\n", info.State))

	text.WriteString(fmt.Sprintf("  Endpoint: %s\n", info.Endpoint))
	md.WriteString(fmt.Sprintf("Endpoint: %s\n", info.Endpoint))

	if info.Uptime > 0 {
		text.WriteString(fmt.Sprintf("  Uptime: %s\n", formatDuration(info.Uptime)))
		md.WriteString(fmt.Sprintf("Uptime: %s\n", formatDuration(info.Uptime)))
	}

	if info.LastError != "" {
		text.WriteString(fmt.Sprintf("  Last Error: %s\n", info.LastError))
		md.WriteString(fmt.Sprintf("Last Error: %s\n", info.LastError))
	}

	text.WriteString(fmt.Sprintf("  Reconnects: %d\n", info.Reconnects))
	md.WriteString(fmt.Sprintf("Reconnects: %d\n", info.Reconnects))

	text.WriteString(fmt.Sprintf("  Subscriptions: %d\n", info.Subscriptions))
	md.WriteString(fmt.Sprintf("Subscriptions: %d\n", info.Subscriptions))

	debugStr := "off"
	if info.Debug {
		debugStr = "on"
	}
	text.WriteString(fmt.Sprintf("  Debug: %s\n", debugStr))
	md.WriteString(fmt.Sprintf("Debug: %s\n", debugStr))

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// hassDebug toggles or sets HASS debug mode
func hassDebug(args *CommandArgs, subArgs []string) *CommandResult {
	info := args.Provider.GetHassInfo()
	currentDebug := info.Debug

	if len(subArgs) == 0 {
		// Toggle
		newState := !currentDebug
		args.Provider.SetHassDebug(newState)
		if newState {
			return &CommandResult{
				Text:     "HASS debug enabled - will show status for events",
				Markdown: "HASS debug **enabled** - will show status for events",
			}
		}
		return &CommandResult{
			Text:     "HASS debug disabled",
			Markdown: "HASS debug **disabled**",
		}
	}

	switch strings.ToLower(subArgs[0]) {
	case "on", "true", "1":
		args.Provider.SetHassDebug(true)
		return &CommandResult{
			Text:     "HASS debug enabled",
			Markdown: "HASS debug **enabled**",
		}
	case "off", "false", "0":
		args.Provider.SetHassDebug(false)
		return &CommandResult{
			Text:     "HASS debug disabled",
			Markdown: "HASS debug **disabled**",
		}
	default:
		return &CommandResult{
			Text:     "Usage: /hass debug [on|off]",
			Markdown: "Usage: `/hass debug [on|off]`",
		}
	}
}

// hassSubs lists active HASS subscriptions
func hassSubs(args *CommandArgs) *CommandResult {
	subs := args.Provider.ListHassSubscriptions()

	if len(subs) == 0 {
		return &CommandResult{
			Text:     "No subscriptions",
			Markdown: "No subscriptions",
		}
	}

	var text strings.Builder
	var md strings.Builder

	text.WriteString(fmt.Sprintf("Subscriptions (%d)\n\n", len(subs)))
	md.WriteString(fmt.Sprintf("**Subscriptions** (%d)\n\n", len(subs)))

	for _, sub := range subs {
		pattern := sub.Pattern
		if pattern == "" {
			pattern = sub.Regex
		}
		shortID := sub.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		// Show enabled/disabled status
		statusIcon := "✓"
		statusText := ""
		if !sub.Enabled {
			statusIcon = "○"
			statusText = " [disabled]"
		}

		text.WriteString(fmt.Sprintf("%s %s (ID: %s)%s\n", statusIcon, pattern, shortID, statusText))
		md.WriteString(fmt.Sprintf("%s **%s** (ID: `%s`)%s\n", statusIcon, pattern, shortID, statusText))

		if sub.Prompt != "" {
			promptPreview := sub.Prompt
			if len(promptPreview) > 50 {
				promptPreview = promptPreview[:50] + "..."
			}
			text.WriteString(fmt.Sprintf("    Prompt: %s\n", promptPreview))
			md.WriteString(fmt.Sprintf("  Prompt: %s\n", promptPreview))
		}

		wakeStr := "no"
		if sub.Wake {
			wakeStr = "yes"
		}
		text.WriteString(fmt.Sprintf("    Wake: %s, Interval: %ds, Debounce: %ds\n", wakeStr, sub.Interval, sub.Debounce))
		md.WriteString(fmt.Sprintf("  Wake: %s, Interval: %ds, Debounce: %ds\n", wakeStr, sub.Interval, sub.Debounce))
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}
