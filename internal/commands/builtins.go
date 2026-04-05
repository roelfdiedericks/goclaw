package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/acp"
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
		Name:        "/stop",
		Description: "Stop all running agent tasks",
		Handler:     handleStop,
	})

	m.Register(&Command{
		Name:        "/resume",
		Description: "Resume tasks after /stop",
		Handler:     handleResume,
	})

	m.Register(&Command{
		Name:        "/shutdown",
		Description: "Gracefully shutdown GoClaw (owner only)",
		Handler:     handleShutdown,
		OwnerOnly:   true,
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
		Usage:       "[debug|info|subs]",
		Handler:     handleHass,
	})

	m.Register(&Command{
		Name:        "/llm",
		Description: "LLM provider status and cooldown management",
		Usage:       "[status|reset]",
		Handler:     handleLLM,
	})

	m.Register(&Command{
		Name:        "/embeddings",
		Description: "Embeddings status and rebuild",
		Usage:       "[status|rebuild]",
		Handler:     handleEmbeddings,
	})

	m.Register(&Command{
		Name:        "/acp",
		Description: "Attach, inspect, and control ACP sessions",
		Usage:       "attach [driver] [--cwd /path] [--mode mode] | detach | status | close | cancel | mode <agent|plan|ask> | model <list|friendly-id> | steer <message>",
		Handler:     handleACP,
	})

	m.Register(&Command{
		Name:        "/a2a",
		Description: "Inspect A2A transport, peers, pairing payloads, and ping",
		Usage:       "status | peers [all|connected|trusted|authorized|discovered|relayed|disconnected] [list] | tasks [all|active|resumable|failed|inbound|outbound] [peer <peer>] | pair | ping <peer> | submit <peer> <message> | resume <peer> <task-id> | cancel <peer> <task-id>",
		Handler:     handleA2A,
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

func handleACP(ctx context.Context, args *CommandArgs) *CommandResult {
	raw := strings.TrimSpace(args.RawArgs)
	if raw == "" {
		return &CommandResult{
			Text:     "Usage: /acp " + args.Usage,
			Markdown: "Usage: `/acp " + args.Usage + "`",
		}
	}

	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return &CommandResult{Text: "Usage: /acp " + args.Usage, Markdown: "Usage: `/acp " + args.Usage + "`"}
	}
	action := strings.ToLower(parts[0])
	rest := parts[1:]

	switch action {
	case "attach":
		driver := "cursor"
		cwd := ""
		mode := ""
		sessionID := ""
		positional := []string{}
		for i := 0; i < len(rest); i++ {
			part := rest[i]
			switch {
			case strings.HasPrefix(part, "--cwd="):
				cwd = strings.TrimPrefix(part, "--cwd=")
			case part == "--cwd" && i+1 < len(rest):
				i++
				cwd = rest[i]
			case strings.HasPrefix(part, "--mode="):
				mode = strings.TrimPrefix(part, "--mode=")
			case part == "--mode" && i+1 < len(rest):
				i++
				mode = rest[i]
			case strings.HasPrefix(part, "--session="):
				sessionID = strings.TrimPrefix(part, "--session=")
			case part == "--session" && i+1 < len(rest):
				i++
				sessionID = rest[i]
			default:
				positional = append(positional, part)
			}
		}
		if len(positional) > 0 {
			driver = positional[0]
		}
		info, err := args.Provider.ACPAttach(ctx, args.SessionKey, args.UserID, driver, cwd, mode, sessionID)
		if err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP attach failed: %s", err), Markdown: fmt.Sprintf("ACP attach failed: `%s`", err), Error: err}
		}
		return acpInfoResult("ACP attached.", info)
	case "detach":
		info, err := args.Provider.ACPDetach(args.SessionKey)
		if err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP detach failed: %s", err), Markdown: fmt.Sprintf("ACP detach failed: `%s`", err), Error: err}
		}
		return acpInfoResult("ACP detached.", info)
	case "status":
		info, err := args.Provider.ACPInspect(args.SessionKey)
		if err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP status failed: %s", err), Markdown: fmt.Sprintf("ACP status failed: `%s`", err), Error: err}
		}
		return acpInfoResult("ACP status.", info)
	case "close":
		if err := args.Provider.ACPClose(ctx, args.SessionKey); err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP close failed: %s", err), Markdown: fmt.Sprintf("ACP close failed: `%s`", err), Error: err}
		}
		return &CommandResult{Text: "ACP session closed.", Markdown: "ACP session closed."}
	case "cancel":
		if err := args.Provider.ACPCancel(ctx, args.SessionKey); err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP cancel failed: %s", err), Markdown: fmt.Sprintf("ACP cancel failed: `%s`", err), Error: err}
		}
		return &CommandResult{Text: "ACP session cancelled.", Markdown: "ACP session cancelled."}
	case "mode":
		if len(rest) == 0 {
			return &CommandResult{Text: "Usage: /acp mode <agent|plan|ask>", Markdown: "Usage: `/acp mode <agent|plan|ask>`"}
		}
		info, err := args.Provider.ACPSetMode(ctx, args.SessionKey, rest[0])
		if err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP mode failed: %s", err), Markdown: fmt.Sprintf("ACP mode failed: `%s`", err), Error: err}
		}
		return acpInfoResult("ACP mode updated.", info)
	case "model":
		if len(rest) == 0 {
			return &CommandResult{Text: "Usage: /acp model <list|friendly-id>", Markdown: "Usage: `/acp model <list|friendly-id>`"}
		}
		if strings.EqualFold(rest[0], "list") {
			models, err := args.Provider.ACPListModels(ctx, args.SessionKey)
			if err != nil {
				return &CommandResult{Text: fmt.Sprintf("ACP model list failed: %s", err), Markdown: fmt.Sprintf("ACP model list failed: `%s`", err), Error: err}
			}
			return acpModelListResult(models)
		}
		info, err := args.Provider.ACPSetModel(ctx, args.SessionKey, strings.Join(rest, " "))
		if err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP model update failed: %s", err), Markdown: fmt.Sprintf("ACP model update failed: `%s`", err), Error: err}
		}
		return acpInfoResult("ACP model updated.", info)
	case "steer":
		if len(rest) == 0 {
			return &CommandResult{Text: "Usage: /acp steer [--stay-attached] <message>", Markdown: "Usage: `/acp steer [--stay-attached] <message>`"}
		}
		stayAttached := false
		messageParts := make([]string, 0, len(rest))
		for _, part := range rest {
			switch part {
			case "--stay-attached", "--stay_attached":
				stayAttached = true
			default:
				messageParts = append(messageParts, part)
			}
		}
		if len(messageParts) == 0 {
			return &CommandResult{Text: "Usage: /acp steer [--stay-attached] <message>", Markdown: "Usage: `/acp steer [--stay-attached] <message>`"}
		}
		result, err := args.Provider.ACPSteer(ctx, args.SessionKey, strings.Join(messageParts, " "), stayAttached)
		if err != nil {
			return &CommandResult{Text: fmt.Sprintf("ACP steer failed: %s", err), Markdown: fmt.Sprintf("ACP steer failed: `%s`", err), Error: err}
		}
		return &CommandResult{Text: result.FinalText, Markdown: result.FinalText}
	default:
		return &CommandResult{
			Text:     "Unknown /acp action. Usage: /acp " + args.Usage,
			Markdown: "Unknown `/acp` action. Usage: `/acp " + args.Usage + "`",
		}
	}
}

func handleA2A(ctx context.Context, args *CommandArgs) *CommandResult {
	parts := strings.Fields(strings.TrimSpace(args.RawArgs))
	if len(parts) == 0 || parts[0] == "status" {
		return a2aStatusResult(args.Provider.GetA2AStatus())
	}
	switch parts[0] {
	case "peers":
		filter := "all"
		if len(parts) > 1 && !strings.EqualFold(parts[1], "list") {
			filter = parts[1]
		}
		return a2aPeersResult(args.Provider.ListA2APeers(filter), filter)
	case "tasks":
		filter := "all"
		peer := ""
		if len(parts) > 1 && !strings.EqualFold(parts[1], "peer") {
			filter = parts[1]
		}
		for i := 1; i < len(parts); i++ {
			if strings.EqualFold(parts[i], "peer") && i+1 < len(parts) {
				peer = parts[i+1]
				break
			}
		}
		return a2aTasksResult(args.Provider.ListA2ATasks(filter, peer), filter, peer)
	case "pair":
		return a2aPairingResult(args.Provider.GetA2APairingPayload())
	case "ping":
		if len(parts) < 2 {
			return &CommandResult{
				Text:     "Usage: /a2a ping <peer>",
				Markdown: "Usage: `/a2a ping <peer>`",
			}
		}
		result, err := args.Provider.PingA2APeer(ctx, parts[1])
		if err != nil {
			return &CommandResult{
				Text:     fmt.Sprintf("A2A ping failed: %s", err),
				Markdown: fmt.Sprintf("A2A ping failed: `%s`", err),
				Error:    err,
			}
		}
		return &CommandResult{
			Text:     fmt.Sprintf("A2A ping %s: %s (%s)", result.PeerID, result.Message, result.Latency),
			Markdown: fmt.Sprintf("A2A ping `%s`: %s (`%s`)", result.PeerID, result.Message, result.Latency),
		}
	case "submit", "send":
		if len(parts) < 3 {
			return &CommandResult{
				Text:     "Usage: /a2a submit <peer> <message>",
				Markdown: "Usage: `/a2a submit <peer> <message>`",
			}
		}
		taskID, updates, err := args.Provider.SubmitA2ATask(ctx, parts[1], strings.Join(parts[2:], " "))
		if err != nil {
			return &CommandResult{
				Text:     fmt.Sprintf("A2A submit failed: %s", err),
				Markdown: fmt.Sprintf("A2A submit failed: `%s`", err),
				Error:    err,
			}
		}
		return a2aTaskResult(taskID, parts[1], drainA2ATaskSnapshots(ctx, updates))
	case "resume":
		if len(parts) < 3 {
			return &CommandResult{
				Text:     "Usage: /a2a resume <peer> <task-id>",
				Markdown: "Usage: `/a2a resume <peer> <task-id>`",
			}
		}
		updates, err := args.Provider.ResumeA2ATask(ctx, parts[1], parts[2])
		if err != nil {
			return &CommandResult{
				Text:     fmt.Sprintf("A2A resume failed: %s", err),
				Markdown: fmt.Sprintf("A2A resume failed: `%s`", err),
				Error:    err,
			}
		}
		return a2aTaskResult(parts[2], parts[1], drainA2ATaskSnapshots(ctx, updates))
	case "cancel":
		if len(parts) < 3 {
			return &CommandResult{
				Text:     "Usage: /a2a cancel <peer> <task-id>",
				Markdown: "Usage: `/a2a cancel <peer> <task-id>`",
			}
		}
		snapshot, err := args.Provider.CancelA2ATask(ctx, parts[1], parts[2])
		if err != nil {
			return &CommandResult{
				Text:     fmt.Sprintf("A2A cancel failed: %s", err),
				Markdown: fmt.Sprintf("A2A cancel failed: `%s`", err),
				Error:    err,
			}
		}
		return a2aTaskResult(parts[2], parts[1], snapshot)
	default:
		return &CommandResult{
			Text:     "Usage: /a2a " + args.Usage,
			Markdown: "Usage: `/a2a " + args.Usage + "`",
		}
	}
}

func a2aStatusResult(status a2a.Status) *CommandResult {
	var text strings.Builder
	text.WriteString("A2A Status\n")
	text.WriteString(fmt.Sprintf("  Enabled: %t\n", status.Enabled))
	text.WriteString(fmt.Sprintf("  Transport: %s\n", status.ActiveTransport))
	text.WriteString(fmt.Sprintf("  Lifecycle: %s\n", status.LifecycleState))
	text.WriteString(fmt.Sprintf("  Ready: %t\n", status.Ready))
	text.WriteString(fmt.Sprintf("  Warmup complete: %t\n", status.WarmupComplete))
	text.WriteString(fmt.Sprintf("  Mode: %s\n", status.RuntimeMode))
	if status.LocalPeerID != "" {
		text.WriteString(fmt.Sprintf("  PeerID: %s\n", status.LocalPeerID))
	}
	text.WriteString(fmt.Sprintf("  Bootstrap peers: %d\n", status.BootstrapPeers))
	text.WriteString(fmt.Sprintf("  Trusted peers: %d\n", status.TrustedPeers))
	text.WriteString(fmt.Sprintf("  Known peers: %d\n", status.KnownPeers))
	text.WriteString(fmt.Sprintf("  Discovered peers: %d\n", status.DiscoveredPeers))
	text.WriteString(fmt.Sprintf("  Connected peers: %d\n", status.ConnectedPeers))
	text.WriteString(fmt.Sprintf("  Retained tasks: %d\n", status.RecentTaskCount))
	text.WriteString(fmt.Sprintf("  State retention: %ds\n", status.StateRetentionSecs))
	text.WriteString(fmt.Sprintf("  Relay client: %t\n", status.RelayClientEnabled))
	text.WriteString(fmt.Sprintf("  Relay server: %t\n", status.RelayServerEnabled))
	text.WriteString(fmt.Sprintf("  Rendezvous: %t\n", status.RendezvousEnabled))
	if status.StartedAt != nil {
		text.WriteString(fmt.Sprintf("  Started: %s\n", status.StartedAt.Format(time.RFC3339)))
	}
	if status.RendezvousNamespace != "" {
		text.WriteString(fmt.Sprintf("  Rendezvous namespace: %s\n", status.RendezvousNamespace))
	}
	for _, addr := range status.ListenAddrs {
		text.WriteString(fmt.Sprintf("  Listen: %s\n", addr))
	}
	for _, addr := range status.AdvertisedAddrs {
		text.WriteString(fmt.Sprintf("  Advertise: %s\n", addr))
	}
	if len(status.PeerStateCounts) > 0 {
		text.WriteString("  Peer states:\n")
		keys := make([]string, 0, len(status.PeerStateCounts))
		for state := range status.PeerStateCounts {
			keys = append(keys, state)
		}
		sort.Strings(keys)
		for _, state := range keys {
			text.WriteString(fmt.Sprintf("    %s: %d\n", state, status.PeerStateCounts[state]))
		}
	}
	if status.LastError != "" {
		text.WriteString(fmt.Sprintf("  Last error: %s\n", status.LastError))
	}
	return &CommandResult{Text: text.String(), Markdown: text.String()}
}

func a2aTasksResult(tasks []a2a.TaskSummary, filter, peer string) *CommandResult {
	if len(tasks) == 0 {
		message := fmt.Sprintf("No retained A2A tasks for filter %q.", filter)
		if peer != "" {
			message = fmt.Sprintf("No retained A2A tasks for filter %q and peer %q.", filter, peer)
		}
		return &CommandResult{
			Text:     message,
			Markdown: message,
		}
	}
	var text strings.Builder
	text.WriteString("A2A Tasks\n")
	for _, task := range tasks {
		text.WriteString(fmt.Sprintf("  %s - %s [%s]", task.TaskID, task.State, task.Direction))
		if task.PeerID != "" {
			text.WriteString(fmt.Sprintf(" peer=%s", task.PeerID))
		}
		if task.Resumable {
			text.WriteString(" resumable=yes")
		} else {
			text.WriteString(" resumable=no")
		}
		if !task.UpdatedAt.IsZero() {
			text.WriteString(fmt.Sprintf(" updated=%s", task.UpdatedAt.Format(time.RFC3339)))
		}
		text.WriteString("\n")
		if task.SessionKey != "" {
			text.WriteString(fmt.Sprintf("    session=%s\n", task.SessionKey))
		}
		if task.ContextID != "" {
			text.WriteString(fmt.Sprintf("    context=%s\n", task.ContextID))
		}
		if task.LocalUser != "" {
			text.WriteString(fmt.Sprintf("    user=%s\n", task.LocalUser))
		}
		if task.LastError != "" {
			text.WriteString(fmt.Sprintf("    error=%s\n", task.LastError))
		}
	}
	return &CommandResult{Text: text.String(), Markdown: text.String()}
}

func a2aPeersResult(peers []a2a.PeerRecord, filter string) *CommandResult {
	if len(peers) == 0 {
		return &CommandResult{
			Text:     fmt.Sprintf("No A2A peers for filter %q.", filter),
			Markdown: fmt.Sprintf("No A2A peers for filter `%s`.", filter),
		}
	}
	var text strings.Builder
	text.WriteString("A2A Peers\n")
	for _, peer := range peers {
		alias := peer.Alias
		if alias == "" {
			alias = peer.PeerID
		}
		text.WriteString(fmt.Sprintf("  %s - %s", alias, peer.State))
		if peer.LocalUser != "" {
			text.WriteString(fmt.Sprintf(" (user=%s)", peer.LocalUser))
		}
		if peer.Relayed {
			text.WriteString(" [relayed]")
		}
		if !peer.Connected && peer.LastDisconnectAt.IsZero() && !peer.LastSeen.IsZero() {
			text.WriteString(" [seen]")
		}
		text.WriteString("\n")
	}
	return &CommandResult{Text: text.String(), Markdown: text.String()}
}

func a2aPairingResult(payload a2a.PairingPayload) *CommandResult {
	if payload.PeerID == "" {
		return &CommandResult{
			Text:     "A2A runtime not ready yet.",
			Markdown: "A2A runtime not ready yet.",
		}
	}
	var text strings.Builder
	text.WriteString("A2A Pairing Payload\n")
	text.WriteString(fmt.Sprintf("  PeerID: %s\n", payload.PeerID))
	for _, addr := range payload.Addrs {
		text.WriteString(fmt.Sprintf("  Addr: %s\n", addr))
	}
	return &CommandResult{Text: text.String(), Markdown: text.String()}
}

func drainA2ATaskSnapshots(ctx context.Context, updates <-chan a2a.TaskSnapshot) a2a.TaskSnapshot {
	var latest a2a.TaskSnapshot
	for {
		select {
		case <-ctx.Done():
			if latest.TaskID == "" {
				return a2a.TaskSnapshot{
					State:     a2a.TaskStateFailed,
					Error:     ctx.Err().Error(),
					UpdatedAt: time.Now(),
				}
			}
			latest.Error = ctx.Err().Error()
			latest.UpdatedAt = time.Now()
			return latest
		case snapshot, ok := <-updates:
			if !ok {
				return latest
			}
			latest = snapshot
		}
	}
}

func a2aTaskResult(taskID, peer string, snapshot a2a.TaskSnapshot) *CommandResult {
	if snapshot.TaskID == "" {
		snapshot.TaskID = taskID
	}
	var text strings.Builder
	text.WriteString("A2A Task\n")
	text.WriteString(fmt.Sprintf("  Peer: %s\n", peer))
	text.WriteString(fmt.Sprintf("  TaskID: %s\n", snapshot.TaskID))
	text.WriteString(fmt.Sprintf("  State: %s\n", snapshot.State))
	text.WriteString(fmt.Sprintf("  Resumable: %t\n", a2aTaskResumable(snapshot.State)))
	if snapshot.SessionKey != "" {
		text.WriteString(fmt.Sprintf("  Session: %s\n", snapshot.SessionKey))
	}
	if snapshot.ContextID != "" {
		text.WriteString(fmt.Sprintf("  Context: %s\n", snapshot.ContextID))
	}
	if snapshot.Error != "" {
		text.WriteString(fmt.Sprintf("  Error: %s\n", snapshot.Error))
	}
	if strings.TrimSpace(snapshot.Content) != "" {
		text.WriteString("  Output:\n")
		for _, line := range strings.Split(snapshot.Content, "\n") {
			text.WriteString("    " + line + "\n")
		}
	}
	return &CommandResult{Text: text.String(), Markdown: text.String()}
}

func a2aTaskResumable(state a2a.TaskState) bool {
	return state != a2a.TaskStateCompleted && state != a2a.TaskStateFailed && state != a2a.TaskStateCancelled
}

func acpInfoResult(prefix string, info *acp.AttachmentInfo) *CommandResult {
	if info == nil {
		return &CommandResult{Text: prefix, Markdown: prefix}
	}
	var text strings.Builder
	text.WriteString(prefix)
	text.WriteString("\n")
	text.WriteString(fmt.Sprintf("  Session key: %s\n", info.SessionKey))
	text.WriteString(fmt.Sprintf("  Attached: %t\n", info.Attached))
	text.WriteString(fmt.Sprintf("  ACP session: %s\n", info.SessionID))
	text.WriteString(fmt.Sprintf("  Driver: %s\n", info.Driver))
	text.WriteString(fmt.Sprintf("  Transport: %s\n", info.Transport))
	text.WriteString(fmt.Sprintf("  Mode: %s\n", info.Mode))
	text.WriteString(fmt.Sprintf("  CWD: %s\n", info.CWD))
	if info.CurrentModel != "" {
		text.WriteString(fmt.Sprintf("  Model: %s\n", info.CurrentModel))
	}
	text.WriteString(fmt.Sprintf("  State: %s\n", info.CurrentState))
	text.WriteString(fmt.Sprintf("  Buffered events: %d\n", info.BufferedEvents))
	if !info.LastActivity.IsZero() {
		text.WriteString(fmt.Sprintf("  Last activity: %s\n", info.LastActivity.Format(time.RFC3339)))
	}
	if info.LastPlanName != "" {
		text.WriteString(fmt.Sprintf("  Last plan: %s\n", info.LastPlanName))
	}
	if info.LastPlanOverview != "" {
		text.WriteString(fmt.Sprintf("  Last plan overview: %s\n", info.LastPlanOverview))
	}
	if info.LastQuestion != "" {
		text.WriteString(fmt.Sprintf("  Last question: %s\n", info.LastQuestion))
	}
	if len(info.Todos) > 0 {
		text.WriteString("  Todos:\n")
		for _, todo := range info.Todos {
			text.WriteString(fmt.Sprintf("    - [%s] %s\n", todo.Status, todo.Content))
		}
	}
	if len(info.PendingRequests) > 0 {
		text.WriteString("  Pending interactive requests:\n")
		for _, pending := range info.PendingRequests {
			text.WriteString(fmt.Sprintf("    - [%s] %s (%s", pending.Driver, pending.Method, pending.SemanticKind))
			if pending.ToolCallID != "" {
				text.WriteString(", tool=" + pending.ToolCallID)
			}
			text.WriteString(")\n")
		}
	}
	if len(info.RecentExtensions) > 0 {
		text.WriteString("  Recent driver extensions:\n")
		for _, ext := range info.RecentExtensions {
			text.WriteString(fmt.Sprintf("    - [%s] %s (%s", ext.Driver, ext.Method, ext.SemanticKind))
			if ext.ToolCallID != "" {
				text.WriteString(", tool=" + ext.ToolCallID)
			}
			if ext.Summary != "" {
				text.WriteString(": " + ext.Summary)
			}
			text.WriteString(")\n")
		}
	}
	return &CommandResult{
		Text:     text.String(),
		Markdown: text.String(),
	}
}

func acpModelListResult(models []acp.ACPModelOption) *CommandResult {
	if len(models) == 0 {
		return &CommandResult{
			Text:     "No ACP models are available for this session.",
			Markdown: "No ACP models are available for this session.",
		}
	}
	var text strings.Builder
	text.WriteString("ACP models:\n")
	for _, model := range models {
		prefix := "  - "
		if model.Current {
			prefix = "  * "
		}
		text.WriteString(prefix)
		text.WriteString(model.FriendlyID)
		if model.Name != "" {
			text.WriteString(" - ")
			text.WriteString(model.Name)
		}
		text.WriteString("\n")
	}
	return &CommandResult{
		Text:     text.String(),
		Markdown: text.String(),
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

// handleStop cancels all running agent sessions for the calling user
func handleStop(ctx context.Context, args *CommandArgs) *CommandResult {
	cancelled, err := args.Provider.StopAllUserSessions(args.UserID)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Stop failed: %s", err),
			Markdown: fmt.Sprintf("Stop failed: `%s`", err),
			Error:    err,
		}
	}

	if cancelled == 0 {
		return &CommandResult{
			Text:     "Nothing running.",
			Markdown: "Nothing running.",
		}
	}

	return &CommandResult{
		Text:     "Stopping all tasks. Send /resume to continue.",
		Markdown: "Stopping all tasks. Send `/resume` to continue.",
	}
}

func handleResume(ctx context.Context, args *CommandArgs) *CommandResult {
	resumed, err := args.Provider.ResumeAllUserSessions(args.UserID)
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Resume failed: %s", err),
			Markdown: fmt.Sprintf("Resume failed: `%s`", err),
			Error:    err,
		}
	}
	if resumed == 0 {
		return &CommandResult{
			Text:     "No paused sessions.",
			Markdown: "No paused sessions.",
		}
	}
	return &CommandResult{
		Text:     "Resumed. You can continue.",
		Markdown: "Resumed. You can continue.",
	}
}

func handleShutdown(ctx context.Context, args *CommandArgs) *CommandResult {
	if err := args.Provider.RequestShutdown(args.UserID); err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Shutdown denied: %s", err),
			Markdown: fmt.Sprintf("Shutdown denied: `%s`", err),
			Error:    err,
		}
	}
	return &CommandResult{
		Text:     "Shutting down now.",
		Markdown: "Shutting down now.",
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
		if cmd.Usage != "" {
			usageLine := fmt.Sprintf("%s %s", cmd.Name, cmd.Usage)
			text.WriteString(fmt.Sprintf("    %s\n", usageLine))
			md.WriteString(fmt.Sprintf("  `%s`\n", usageLine))
		}
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
		usage := fmt.Sprintf("/hass %s", args.Usage)
		return &CommandResult{
			Text:     fmt.Sprintf("Unknown subcommand: %s\nUsage: %s", parts[0], usage),
			Markdown: fmt.Sprintf("Unknown subcommand: `%s`\nUsage: `%s`", parts[0], usage),
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

// handleLLM handles the /llm command and subcommands
func handleLLM(ctx context.Context, args *CommandArgs) *CommandResult {
	parts := strings.Fields(args.RawArgs)

	if len(parts) == 0 || parts[0] == "status" {
		return llmStatus(args)
	}

	switch parts[0] {
	case "reset":
		return llmReset(args)
	default:
		usage := fmt.Sprintf("/llm %s", args.Usage)
		return &CommandResult{
			Text:     fmt.Sprintf("Unknown subcommand: %s\nUsage: %s", parts[0], usage),
			Markdown: fmt.Sprintf("Unknown subcommand: `%s`\nUsage: `%s`", parts[0], usage),
		}
	}
}

// llmStatus shows LLM provider status
func llmStatus(args *CommandArgs) *CommandResult {
	status := args.Provider.GetLLMProviderStatus()
	if status == nil {
		return &CommandResult{
			Text:     "LLM registry not available",
			Markdown: "LLM registry not available",
		}
	}

	var text strings.Builder
	var md strings.Builder

	text.WriteString("LLM Provider Status\n\n")
	md.WriteString("**LLM Provider Status**\n\n")

	// Provider status
	for _, p := range status.Providers {
		if p.InCooldown {
			remaining := time.Until(p.Until).Round(time.Second)
			text.WriteString(fmt.Sprintf("  ❌ %s - cooldown until %s (%s, %d failures)\n",
				p.Alias, p.Until.Format("15:04:05"), p.Reason, p.ErrorCount))
			md.WriteString(fmt.Sprintf("❌ **%s** - cooldown until %s (%s, %d failures, %s remaining)\n",
				p.Alias, p.Until.Format("15:04:05"), p.Reason, p.ErrorCount, remaining))
		} else {
			text.WriteString(fmt.Sprintf("  ✓ %s - available\n", p.Alias))
			md.WriteString(fmt.Sprintf("✓ **%s** - available\n", p.Alias))
		}
	}

	// Model chains
	if len(status.AgentChain) > 0 {
		text.WriteString(fmt.Sprintf("\nAgent chain: %s\n", strings.Join(status.AgentChain, " → ")))
		md.WriteString(fmt.Sprintf("\n**Agent chain:** %s\n", strings.Join(status.AgentChain, " → ")))
	}

	if len(status.SummarizationChain) > 0 {
		text.WriteString(fmt.Sprintf("Summarization chain: %s\n", strings.Join(status.SummarizationChain, " → ")))
		md.WriteString(fmt.Sprintf("**Summarization chain:** %s\n", strings.Join(status.SummarizationChain, " → ")))
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// llmReset clears all LLM provider cooldowns
func llmReset(args *CommandArgs) *CommandResult {
	count := args.Provider.ResetLLMCooldowns()

	if count == 0 {
		return &CommandResult{
			Text:     "No cooldowns to clear.",
			Markdown: "No cooldowns to clear.",
		}
	}

	return &CommandResult{
		Text:     fmt.Sprintf("Cleared cooldowns for %d providers.", count),
		Markdown: fmt.Sprintf("Cleared cooldowns for **%d** providers.", count),
	}
}

// handleEmbeddings handles the /embeddings command and subcommands
func handleEmbeddings(ctx context.Context, args *CommandArgs) *CommandResult {
	parts := strings.Fields(args.RawArgs)

	if len(parts) == 0 || parts[0] == "status" {
		return embeddingsStatus(args)
	}

	switch parts[0] {
	case "rebuild":
		return embeddingsRebuild(args)
	default:
		usage := fmt.Sprintf("/embeddings %s", args.Usage)
		return &CommandResult{
			Text:     fmt.Sprintf("Unknown subcommand: %s\nUsage: %s", parts[0], usage),
			Markdown: fmt.Sprintf("Unknown subcommand: `%s`\nUsage: `%s`", parts[0], usage),
		}
	}
}

// embeddingsStatus shows embeddings status
func embeddingsStatus(args *CommandArgs) *CommandResult {
	status := args.Provider.GetEmbeddingsStatus()
	if status == nil || !status.Configured {
		return &CommandResult{
			Text:     "Embeddings not configured (no models in llm.embeddings.models)",
			Markdown: "Embeddings not configured (no models in `llm.embeddings.models`)",
		}
	}

	var text strings.Builder
	var md strings.Builder

	text.WriteString("📊 Embeddings Status\n\n")
	md.WriteString("**📊 Embeddings Status**\n\n")

	// Configuration
	autoRebuildStr := "✓ enabled"
	if !status.AutoRebuild {
		autoRebuildStr = "disabled"
	}
	text.WriteString(fmt.Sprintf("Primary model: %s\n", status.PrimaryModel))
	text.WriteString(fmt.Sprintf("Auto-rebuild: %s\n\n", autoRebuildStr))
	md.WriteString(fmt.Sprintf("Primary model: `%s`\n", status.PrimaryModel))
	md.WriteString(fmt.Sprintf("Auto-rebuild: %s\n\n", autoRebuildStr))

	// Models in DB
	text.WriteString("In DB:\n")
	md.WriteString("**In DB:**\n")
	for _, m := range status.Models {
		if m.IsPrimary {
			text.WriteString(fmt.Sprintf("  ✓ %s: %d chunks\n", m.Model, m.Count))
			md.WriteString(fmt.Sprintf("✓ %s: %d chunks\n", m.Model, m.Count))
		} else {
			text.WriteString(fmt.Sprintf("  ⚠ %s: %d chunks (needs rebuild)\n", m.Model, m.Count))
			md.WriteString(fmt.Sprintf("⚠ %s: %d chunks _(needs rebuild)_\n", m.Model, m.Count))
		}
	}
	text.WriteString("\n")
	md.WriteString("\n")

	// Transcript
	text.WriteString(fmt.Sprintf("Transcripts: %d chunks\n", status.TranscriptTotal))
	md.WriteString(fmt.Sprintf("**Transcripts:** %d chunks\n", status.TranscriptTotal))
	if status.TranscriptTotal > 0 {
		text.WriteString(fmt.Sprintf("  ✓ %d primary\n", status.TranscriptPrimary))
		md.WriteString(fmt.Sprintf("  ✓ %d primary\n", status.TranscriptPrimary))
		if status.TranscriptNeedsRebuild > 0 {
			text.WriteString(fmt.Sprintf("  ⚠ %d needs rebuild\n", status.TranscriptNeedsRebuild))
			md.WriteString(fmt.Sprintf("  ⚠ %d needs rebuild\n", status.TranscriptNeedsRebuild))
		}
	}
	text.WriteString("\n")
	md.WriteString("\n")

	// Memory
	text.WriteString(fmt.Sprintf("Memory: %d chunks\n", status.MemoryTotal))
	md.WriteString(fmt.Sprintf("**Memory:** %d chunks\n", status.MemoryTotal))
	if status.MemoryTotal > 0 {
		text.WriteString(fmt.Sprintf("  ✓ %d primary\n", status.MemoryPrimary))
		md.WriteString(fmt.Sprintf("  ✓ %d primary\n", status.MemoryPrimary))
		if status.MemoryNeedsRebuild > 0 {
			text.WriteString(fmt.Sprintf("  ⚠ %d needs rebuild\n", status.MemoryNeedsRebuild))
			md.WriteString(fmt.Sprintf("  ⚠ %d needs rebuild\n", status.MemoryNeedsRebuild))
		}
	}

	return &CommandResult{
		Text:     text.String(),
		Markdown: md.String(),
	}
}

// embeddingsRebuild triggers a rebuild
func embeddingsRebuild(args *CommandArgs) *CommandResult {
	status := args.Provider.GetEmbeddingsStatus()
	if status == nil || !status.Configured {
		return &CommandResult{
			Text:     "Embeddings not configured",
			Markdown: "Embeddings not configured",
		}
	}

	needsRebuild := status.TranscriptNeedsRebuild + status.MemoryNeedsRebuild
	if needsRebuild == 0 {
		return &CommandResult{
			Text:     "Nothing to rebuild - all chunks use primary model.",
			Markdown: "Nothing to rebuild - all chunks use primary model.",
		}
	}

	err := args.Provider.TriggerEmbeddingsRebuild()
	if err != nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Failed to start rebuild: %s", err),
			Markdown: fmt.Sprintf("Failed to start rebuild: `%s`", err),
			Error:    err,
		}
	}

	return &CommandResult{
		Text:     fmt.Sprintf("Rebuild starting. %d chunks to process.\nUse /embeddings status to monitor.", needsRebuild),
		Markdown: fmt.Sprintf("Rebuild starting. **%d** chunks to process.\nUse `/embeddings status` to monitor.", needsRebuild),
	}
}
