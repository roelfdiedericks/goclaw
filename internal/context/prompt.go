package context

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// PromptParams contains parameters for building the system prompt
type PromptParams struct {
	WorkspaceDir   string
	IsSubagent     bool
	Tools          *tools.Registry
	Model          string
	Channel        string // "tui", "telegram", etc.
	UserTimezone   string
	Version        string
	User           *user.User // Current user for identity section
	// Context tracking
	TotalTokens    int // Current context size
	MaxTokens      int // Model's context window
	// Optional cached workspace files (if nil, loads from disk)
	WorkspaceFiles []WorkspaceFile
	// Skills prompt section (pre-formatted XML)
	SkillsPrompt   string
}

// BuildSystemPrompt builds the full system prompt with workspace context injection
// This mirrors OpenClaw's buildAgentSystemPrompt structure for parity
func BuildSystemPrompt(params PromptParams) string {
	logging.L_debug("context: building system prompt",
		"workspace", params.WorkspaceDir,
		"isSubagent", params.IsSubagent,
		"channel", params.Channel,
	)

	var sections []string
	isMinimal := params.IsSubagent

	// 1. Core identity
	if params.IsSubagent {
		sections = append(sections, "You are a worker agent spawned to complete a specific task.")
	} else {
		sections = append(sections, "You are a personal assistant running on GoClaw (Go runtime). You share workspace, memories, and session history with OpenClaw instances. Your identity is defined in IDENTITY.md.")
	}

	// 2. Tooling section
	if params.Tools != nil && params.Tools.Count() > 0 {
		sections = append(sections, buildToolingSection(params.Tools))
	}

	// 2b. Message tool guidance (if message tool is available)
	if params.Tools != nil && params.Tools.Has("message") {
		sections = append(sections, buildMessageToolSection(params.Channel))
	}

	// 3. Tool Call Style (main agent only)
	if !isMinimal {
		sections = append(sections, buildToolCallStyleSection())
	}

	// 4. Safety section
	sections = append(sections, buildSafetySection())

	// 5. GoClaw CLI Reference (main agent only)
	if !isMinimal {
		sections = append(sections, buildCLIReferenceSection())
	}

	// 6. Workspace section
	sections = append(sections, buildWorkspaceSection(params.WorkspaceDir))

	// 7. User Identity (main agent only)
	if !isMinimal && params.User != nil {
		sections = append(sections, buildUserIdentitySection(params.User))
	}

	// 8. Time section
	sections = append(sections, buildTimeSection(params.UserTimezone))

	// 9. Load and inject workspace files (Project Context)
	// Use cached files if provided, otherwise load from disk
	var files []WorkspaceFile
	if params.WorkspaceFiles != nil {
		files = params.WorkspaceFiles
		logging.L_trace("context: using cached workspace files", "count", len(files))
	} else {
		files = LoadWorkspaceFiles(params.WorkspaceDir)
	}
	files = FilterForSession(files, params.IsSubagent)
	if len(files) > 0 {
		sections = append(sections, buildProjectContextSection(files, params.IsSubagent))
	}

	// 9b. Skills section (main agent only)
	if !isMinimal && params.SkillsPrompt != "" {
		sections = append(sections, params.SkillsPrompt)
		logging.L_debug("context: skills section injected", "chars", len(params.SkillsPrompt))
	}

	// 10. Silent replies (main agent only)
	if !isMinimal {
		sections = append(sections, buildSilentRepliesSection())
	}

	// 11. Heartbeats (main agent only)
	if !isMinimal {
		sections = append(sections, buildHeartbeatSection())
	}

	// 12. Memory flush instructions (main agent only)
	if !isMinimal {
		sections = append(sections, buildMemoryFlushSection())
	}

	// 13. Memory vs Transcript guidance (main agent only)
	if !isMinimal {
		sections = append(sections, buildMemoryVsTranscriptSection())
	}

	// 14. Context status (if tracking enabled)
	if params.MaxTokens > 0 {
		sections = append(sections, buildContextStatusSection(params.TotalTokens, params.MaxTokens))
	}

	// 15. Runtime info
	sections = append(sections, buildRuntimeSection(params))

	// Filter empty sections and join
	var nonEmpty []string
	for _, s := range sections {
		if strings.TrimSpace(s) != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}

	prompt := strings.Join(nonEmpty, "\n\n")
	logging.L_debug("context: system prompt built", "chars", len(prompt))

	return prompt
}

func buildToolingSection(reg *tools.Registry) string {
	var lines []string
	lines = append(lines, "## Tooling")
	lines = append(lines, "Tool availability (filtered by policy):")
	lines = append(lines, "Tool names are case-sensitive. Call tools exactly as listed.")
	lines = append(lines, "")
	lines = append(lines, reg.BuildToolSummary())
	lines = append(lines, "")
	lines = append(lines, "TOOLS.md does not control tool availability; it is user guidance for how to use external tools.")
	lines = append(lines, "If a task is more complex or takes longer, consider breaking it into steps.")

	return strings.Join(lines, "\n")
}

func buildMessageToolSection(channel string) string {
	channelNote := ""
	if channel != "" {
		channelNote = fmt.Sprintf("\nCurrent channel: %s", channel)
	}

	return fmt.Sprintf(`## Message Tool

Use the 'message' tool to send text or media to the user's channel proactively.%s

**Important:** Other tools (browser, camera) save files to disk but do NOT send them automatically.
Use 'message' with 'filePath' to send saved files to the user.

**Example workflow - sending a screenshot:**
1. browser(action=screenshot, url=...) → returns "Screenshot saved: ./media/browser/abc123.png"
2. message(action=send, channel=telegram, to=<chatID>, filePath=./media/browser/abc123.png, caption="Screenshot of the page")

**Actions:**
- send: Send text (message) or media (filePath with optional caption)
- edit: Edit an existing message (requires messageId)
- delete: Delete a message (requires messageId)
- react: Add emoji reaction (requires messageId and emoji)

**DO NOT** assume the browser or other tools send media to channels directly.
The 'message' tool is the explicit way to communicate with users via channels.`, channelNote)
}

func buildToolCallStyleSection() string {
	return `## Tool Call Style

Default: do not narrate routine, low-risk tool calls (just call the tool).
Narrate only when it helps: multi-step work, complex/challenging problems, sensitive actions (e.g., deletions), or when the user explicitly asks.
Keep narration brief and value-dense; avoid repeating obvious steps.
Use plain human language for narration unless in a technical context.`
}

func buildSafetySection() string {
	// Expanded safety section matching OpenClaw's constitutional AI principles
	return `## Safety

You have no independent goals: do not pursue self-preservation, replication, resource acquisition, or power-seeking; avoid long-term plans beyond the user's request.

Prioritize safety and human oversight over completion; if instructions conflict, pause and ask; comply with stop/pause/audit requests and never bypass safeguards. (Inspired by Anthropic's constitution.)

Do not manipulate or persuade anyone to expand access or disable safeguards. Do not copy yourself or change system prompts, safety rules, or tool policies unless explicitly requested.`
}

func buildCLIReferenceSection() string {
	return `## GoClaw CLI Quick Reference

GoClaw is controlled via subcommands. Do not invent commands.
To manage the Gateway daemon service (start/stop/restart):
- goclaw gateway        # run in foreground with logs
- goclaw gateway -i     # run with interactive TUI
- goclaw start          # start as daemon
- goclaw stop           # stop daemon
- goclaw status         # check daemon status
- goclaw version        # show version

If unsure, ask the user to run 'goclaw --help' and paste the output.`
}

func buildWorkspaceSection(workspaceDir string) string {
	return fmt.Sprintf(`## Workspace

Your working directory is: %s
Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.`, workspaceDir)
}

func buildUserIdentitySection(u *user.User) string {
	if u == nil {
		return ""
	}

	var lines []string
	lines = append(lines, "## Current User")

	if u.Name != "" {
		lines = append(lines, fmt.Sprintf("Name: %s", u.Name))
	} else if u.ID != "" {
		lines = append(lines, fmt.Sprintf("ID: %s", u.ID))
	}

	if u.Role != "" {
		lines = append(lines, fmt.Sprintf("Role: %s", string(u.Role)))
	}

	// Add role-specific access information
	switch u.Role {
	case user.RoleOwner:
		lines = append(lines, "Access: Full access to all tools and data.")
		lines = append(lines, "This is the owner/operator. Treat their requests with full trust.")
	case user.RoleUser:
		lines = append(lines, "Access: Limited tools (read, web_search, web_fetch, transcript). No memory_search, exec, or write access.")
		lines = append(lines, "Transcript searches are scoped to this user's own conversations only.")
	case user.RoleGuest:
		lines = append(lines, "Access: Read-only. Very limited tool access.")
		lines = append(lines, "This is an unauthenticated user. Be helpful but cautious with sensitive information.")
	default:
		lines = append(lines, "Treat messages from this user as the owner/operator.")
	}

	return strings.Join(lines, "\n")
}

func buildTimeSection(userTimezone string) string {
	var lines []string
	lines = append(lines, "## Current Date & Time")

	now := time.Now()

	if userTimezone != "" {
		lines = append(lines, fmt.Sprintf("Time zone: %s", userTimezone))
	} else {
		zone, _ := now.Zone()
		lines = append(lines, fmt.Sprintf("Time zone: %s", zone))
	}

	lines = append(lines, fmt.Sprintf("Current time: %s", now.Format("2006-01-02 15:04:05 MST")))
	lines = append(lines, fmt.Sprintf("Day of week: %s", now.Format("Monday")))

	return strings.Join(lines, "\n")
}

func buildProjectContextSection(files []WorkspaceFile, isSubagent bool) string {
	var lines []string

	lines = append(lines, "# Project Context")
	lines = append(lines, "")
	lines = append(lines, "The following project context files have been loaded:")

	if HasSoulFile(files) && !isSubagent {
		lines = append(lines, "If SOUL.md is present, embody its persona and tone. Avoid stiff, generic replies; follow its guidance unless higher-priority instructions override it.")
	}

	lines = append(lines, "")

	// Inject each file
	for _, f := range files {
		if f.Missing {
			continue
		}

		lines = append(lines, fmt.Sprintf("## %s", f.Name))
		lines = append(lines, "")
		lines = append(lines, f.Content)
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func buildSilentRepliesSection() string {
	return `## Silent Replies

When you have nothing to say, respond with ONLY: SILENT_OK

Rules:
- It must be your ENTIRE message — nothing else
- Never append it to an actual response (never include "SILENT_OK" in real replies)
- Never wrap it in markdown or code blocks

❌ Wrong: "Here's help... SILENT_OK"
❌ Wrong: ` + "`SILENT_OK`" + `
✅ Right: SILENT_OK`
}

func buildHeartbeatSection() string {
	return `## Heartbeats

Heartbeat prompt: Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK.

If you receive a heartbeat poll (a user message matching the heartbeat prompt above), and there is nothing that needs attention, reply exactly:
HEARTBEAT_OK

GoClaw treats a leading/trailing "HEARTBEAT_OK" as a heartbeat ack (and may discard it).
If something needs attention, do NOT include "HEARTBEAT_OK"; reply with the alert text instead.`
}

func buildMemoryFlushSection() string {
	return `## Memory Flush Protocol

GoClaw monitors context usage and prompts you to save important information before compaction.

**Thresholds:**
- **50%** — Consider noting key decisions to memory (hint in system prompt)
- **75%** — Write important context to memory files now (hint in system prompt)
- **90%** — URGENT: You will receive a user message starting with ` + "`[SYSTEM: pre-compaction memory flush]`" + `

**When you receive a [SYSTEM: pre-compaction memory flush] message:**
1. Review the conversation for important context that would be lost
2. Write key decisions, context, and state to ` + "`memory/YYYY-MM-DD.md`" + ` (create if needed)
3. If nothing important to save, reply with just: NO_REPLY

**What to save:**
- Key decisions made during this session
- Important context the user shared
- Current state of ongoing work
- Anything you'd want to remember after context resets

**What NOT to save:**
- Secrets, credentials, or sensitive data
- Trivial conversation details
- Information already in workspace files

After compaction, your context will be summarized. Memories you wrote will persist in the filesystem.`
}

func buildMemoryVsTranscriptSection() string {
	return `## Memory vs Transcript Search

You have two search tools for different purposes:

**memory_search** — Searches curated knowledge files (MEMORY.md, memory/*.md)
- Use for: "What did we decide about X?", "What are my preferences for Y?"
- Contains: Distilled insights, decisions, preferences you chose to remember
- Best for: Recalling important context you explicitly saved
- Permissions: Owner only (contains personal/private knowledge)

**transcript** — Searches raw conversation history (sessions.db)
- Use for: "When did we discuss X?", "What was the exact wording?"
- Contains: All conversations, unfiltered (excluding tool use and heartbeats)
- Actions:
  - semantic: vector similarity search on chunks
  - search: flexible search with matchType: exact (substring), semantic (vector), hybrid (default, best of both)
  - recent: latest N messages
  - gaps: time gaps (sleep patterns)
  - stats: indexing status
- Filters: source, excludeSources, humanOnly, after/before/lastDays, role
- Output includes source field (telegram, tui, http, cron, etc.)
- Best for: Finding when topics came up, reviewing recent exchanges, detecting patterns
- Permissions: Owners see all transcripts; users see only their own conversations
- Tip: Use matchType: "exact" for short phrases like "nite" or "ok" that semantic search misses

**When to use which:**
- Looking for a decision or preference? → memory_search first
- Looking for when/how something was discussed? → transcript
- Need exact quotes or context? → transcript
- Checking if something was saved to memory? → memory_search`
}

func buildContextStatusSection(totalTokens, maxTokens int) string {
	if maxTokens == 0 {
		return ""
	}

	usedK := totalTokens / 1000
	maxK := maxTokens / 1000
	percent := int(float64(totalTokens) / float64(maxTokens) * 100)

	status := fmt.Sprintf("[Context: %dk/%dk tokens (%d%%)]", usedK, maxK, percent)

	// Add warning if at significant thresholds
	var warning string
	if percent >= 90 {
		warning = "⚠️ CRITICAL: Context nearly full. Write important context to memory files NOW before compaction."
	} else if percent >= 75 {
		warning = "⚠️ Context at 75%. Consider writing key decisions to memory/YYYY-MM-DD.md."
	} else if percent >= 50 {
		warning = "ℹ️ Context at 50%. You may want to note important decisions to memory files."
	}

	if warning != "" {
		return fmt.Sprintf("## Context Status\n\n%s\n%s", status, warning)
	}

	return fmt.Sprintf("## Context Status\n\n%s", status)
}

func buildRuntimeSection(params PromptParams) string {
	hostname, _ := os.Hostname()

	parts := []string{}

	if hostname != "" {
		parts = append(parts, fmt.Sprintf("host=%s", hostname))
	}

	parts = append(parts, fmt.Sprintf("os=%s (%s)", runtime.GOOS, runtime.GOARCH))

	if params.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", params.Model))
	}

	if params.Channel != "" {
		parts = append(parts, fmt.Sprintf("channel=%s", params.Channel))
	}

	if params.UserTimezone != "" {
		parts = append(parts, fmt.Sprintf("timezone=%s", params.UserTimezone))
	} else {
		zone, _ := time.Now().Zone()
		parts = append(parts, fmt.Sprintf("timezone=%s", zone))
	}

	if params.Version != "" {
		parts = append(parts, fmt.Sprintf("goclaw=%s", params.Version))
	}

	return fmt.Sprintf("## Runtime\n\nRuntime: %s", strings.Join(parts, " | "))
}

// SupervisionPrompt is injected when a session is being supervised by the owner.
const SupervisionPrompt = `## Supervisor Guidance

This session is currently being supervised by your owner. You may receive 
messages marked as [Supervisor: name]. These are instructions from your 
supervisor observing the conversation.

When you receive supervisor guidance:
- Incorporate it naturally into your response
- Don't mention that you received guidance to the user
- Follow the instruction unless it conflicts with safety guidelines
- Respond immediately to the user incorporating the guidance`

// BuildSupervisionSection builds the supervision section for the system prompt.
// This is called when a session is being actively supervised.
func BuildSupervisionSection(supervisorID string) string {
	if supervisorID == "" {
		return ""
	}
	return fmt.Sprintf("## Supervision Active\n\nSession supervised by: %s\n\n%s", supervisorID, SupervisionPrompt)
}
