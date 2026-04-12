package context

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/metrics"
	"github.com/roelfdiedericks/goclaw/internal/tokens"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// PromptParams contains parameters for building the system prompt
type PromptParams struct {
	WorkspaceDir   string
	VisibleHomeDir string
	SandboxMode    string
	IsSubagent     bool
	Tools          *tools.Registry
	Model          string
	Channel        string // "tui", "telegram", etc.
	UserTimezone   string
	Version        string
	User           *user.User // Current user for identity section
	// Context tracking (not rendered into the system prompt; gateway injects status ephemerally)
	TotalTokens int // Current context size
	MaxTokens   int // Model's context window
	// Optional cached workspace files (if nil, loads from disk)
	WorkspaceFiles []WorkspaceFile
	// Skills prompt section (pre-formatted XML)
	SkillsPrompt string
	// Memory access control (true = include MEMORY.md, false = exclude)
	IncludeMemory bool
	// Role-specific system prompt customization
	RoleSystemPrompt     string // Inline system prompt text from role config
	RoleSystemPromptFile string // Path to system prompt file (relative to workspace)
	// Time injection control
	TimeInSystemPrompt bool // Include time section in system prompt (default: false)
	// Memory graph agent extraction
	AgentExtraction bool // Enable agent-driven memory extraction guidance in prompt
	// Memory graph bulletins (pre-generated, for injection="prompt" mode)
	MemoryBulletin  string // Memory bulletin content (if injection="prompt")
	ContextBulletin string // Context bulletin content (if injection="prompt")
	// Tool batching and parallel-execution hints (runtime capabilities)
	ParallelToolBatching  bool     // If true, model should consider batching independent tool calls in one turn
	ParallelExecution     bool     // If true, gateway may execute eligible tool calls concurrently
	ParallelMaxConcurrent int      // Max concurrent eligible tools
	ParallelEligibleTools []string // Effective allowlist of tools eligible for parallel execution
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

	// Track section content for token estimation via tiktoken
	var toolsText, workspaceText, memoryText, skillsText, staticText string

	// 1. Core identity
	if params.IsSubagent {
		sections = append(sections, "You are a worker agent spawned to complete a specific task.")
	} else {
		sections = append(sections, "You are a personal assistant running on GoClaw (Go runtime). You share workspace, memories, and session history with OpenClaw instances. Your identity is defined in IDENTITY.md.")
	}

	// 2. Tooling section
	if params.Tools != nil && params.Tools.Count() > 0 {
		s := buildToolingSection(params.Tools)
		toolsText += s
		sections = append(sections, s)

		ps := buildToolBatchingSection(params)
		toolsText += ps
		sections = append(sections, ps)
	}

	// 2b. Message tool guidance (if message tool is available)
	if params.Tools != nil && params.Tools.Has("message") {
		s := buildMessageToolSection(params.Channel)
		toolsText += s
		sections = append(sections, s)
	}

	// 2c. Subagent guidance (main agent only, when subagent tools are available)
	if !isMinimal && params.Tools != nil && (params.Tools.Has("subagent_spawn") || params.Tools.Has("subagent_fanout") || params.Tools.Has("subagent_status")) {
		s := buildSubagentToolSection(params.Tools)
		toolsText += s
		sections = append(sections, s)
	}

	// 3. Tool Call Style (main agent only)
	if !isMinimal {
		s := buildToolCallStyleSection()
		staticText += s
		sections = append(sections, s)
	}

	// 4. Safety section
	{
		s := buildSafetySection()
		staticText += s
		sections = append(sections, s)
	}

	// 5. GoClaw CLI Reference (main agent only)
	if !isMinimal {
		s := buildCLIReferenceSection()
		staticText += s
		sections = append(sections, s)
	}

	// 6. Workspace section
	{
		s := buildWorkspaceSection(params.WorkspaceDir, params.VisibleHomeDir, params.SandboxMode)
		staticText += s
		sections = append(sections, s)
	}

	// 7. User Identity (main agent only)
	if !isMinimal && params.User != nil {
		s := buildUserIdentitySection(params.User)
		staticText += s
		sections = append(sections, s)
	}

	// 7b. Role-specific system prompt (main agent only)
	if !isMinimal {
		rolePrompt := buildRolePromptSection(params)
		if rolePrompt != "" {
			staticText += rolePrompt
			sections = append(sections, rolePrompt)
		}
	}

	// 7c. Cron handoff guidance (main agent only, channel-specific)
	if !isMinimal {
		s := buildCronHandoffSection(params.Channel)
		if s != "" {
			staticText += s
			sections = append(sections, s)
		}
	}

	// 8. Time section (only if configured — excluding it enables prompt caching)
	if params.TimeInSystemPrompt {
		s := buildTimeSection(params.UserTimezone)
		staticText += s
		sections = append(sections, s)
	}

	// 9. Load and inject workspace files (Project Context)
	var files []WorkspaceFile
	if params.WorkspaceFiles != nil {
		files = params.WorkspaceFiles
		logging.L_trace("context: using cached workspace files", "count", len(files))
	} else {
		files = LoadWorkspaceFiles(params.WorkspaceDir, params.IncludeMemory)
	}
	files = FilterForSession(files, params.IsSubagent)
	if !params.IncludeMemory {
		files = FilterMemory(files)
	}
	if len(files) > 0 {
		s := buildProjectContextSection(files, params.IsSubagent)
		workspaceText = s
		sections = append(sections, s)

		for _, f := range files {
			if f.Name == "MEMORY.md" {
				memoryText = f.Content
			}
		}
	}

	// 9b. Skills section (main agent only)
	if !isMinimal && params.SkillsPrompt != "" {
		skillsText = params.SkillsPrompt
		sections = append(sections, params.SkillsPrompt)
		logging.L_debug("context: skills section injected", "chars", len(skillsText))
	}

	// 10. Silent replies (main agent only)
	if !isMinimal {
		s := buildSilentRepliesSection()
		staticText += s
		sections = append(sections, s)
	}

	// 11. Cron Jobs (main agent only)
	if !isMinimal {
		s := buildCronJobsSection()
		staticText += s
		sections = append(sections, s)
	}

	// 12. Memory flush instructions (main agent only)
	if !isMinimal {
		s := buildMemoryFlushSection()
		staticText += s
		sections = append(sections, s)
	}

	// 13. Memory vs Transcript guidance (main agent only)
	if !isMinimal {
		s := buildMemoryVsTranscriptSection()
		staticText += s
		sections = append(sections, s)
	}

	// 13b. Agent-driven memory extraction guidance (main agent only, if enabled)
	if !isMinimal && params.AgentExtraction {
		s := buildAgentExtractionSection()
		staticText += s
		sections = append(sections, s)
	}

	// 13c. Memory bulletin (main agent only, if injection="prompt")
	if !isMinimal && params.MemoryBulletin != "" {
		s := buildMemoryBulletinSection(params.MemoryBulletin)
		staticText += s
		sections = append(sections, s)
	}

	// 13d. Context bulletin (main agent only, if injection="prompt")
	if !isMinimal && params.ContextBulletin != "" {
		s := buildContextBulletinSection(params.ContextBulletin)
		staticText += s
		sections = append(sections, s)
	}

	// 14. Context status moved to ephemeral system messages (gateway) so the system
	// prompt prefix stays stable for prompt caching across providers.

	// 15. Runtime info
	{
		s := buildRuntimeSection(params)
		staticText += s
		sections = append(sections, s)
	}

	// Filter empty sections and join
	var nonEmpty []string
	for _, s := range sections {
		if strings.TrimSpace(s) != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}

	prompt := strings.Join(nonEmpty, "\n\n")

	est := tokens.Get()
	totalTokens := est.Count(prompt)
	workspaceTokens := est.Count(workspaceText)
	memoryTokens := est.Count(memoryText)
	toolsTokens := est.Count(toolsText)
	skillsTokens := est.Count(skillsText)
	staticTokens := est.Count(staticText)

	logging.L_debug("context: system prompt built",
		"totalTokens", totalTokens,
		"workspaceTokens", workspaceTokens,
		"memoryTokens", memoryTokens,
		"toolsTokens", toolsTokens,
		"skillsTokens", skillsTokens,
		"staticTokens", staticTokens,
	)

	metrics.MetricSet("prompt", "total_tokens", int64(totalTokens))
	metrics.MetricSet("prompt", "workspace_tokens", int64(workspaceTokens))
	metrics.MetricSet("prompt", "memory_tokens", int64(memoryTokens))
	metrics.MetricSet("prompt", "tools_tokens", int64(toolsTokens))
	metrics.MetricSet("prompt", "skills_tokens", int64(skillsTokens))
	metrics.MetricSet("prompt", "static_tokens", int64(staticTokens))

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

func buildToolBatchingSection(params PromptParams) string {
	var lines []string
	lines = append(lines, "## Tool Batching & Parallelism")
	lines = append(lines, "You may emit multiple tool calls in a single assistant turn when tasks are independent.")
	lines = append(lines, "For dependent tasks, sequence calls so later inputs can use earlier outputs.")
	lines = append(lines, "Do not assume completion order for batched calls.")
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("- Multi-call batching supported: %t", params.ParallelToolBatching))
	lines = append(lines, fmt.Sprintf("- Parallel execution enabled: %t", params.ParallelExecution))
	if params.ParallelExecution {
		lines = append(lines, fmt.Sprintf("- Max concurrent eligible tools: %d", params.ParallelMaxConcurrent))
		if len(params.ParallelEligibleTools) > 0 {
			lines = append(lines, "- Parallel-eligible tools: "+strings.Join(params.ParallelEligibleTools, ", "))
		} else {
			lines = append(lines, "- Parallel-eligible tools: none")
		}
		lines = append(lines, "- Non-eligible tools may execute sequentially even when batched.")
	}
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

func buildSubagentToolSection(reg *tools.Registry) string {
	var lines []string
	lines = append(lines, "## Delegated Subagents")
	lines = append(lines, "Subagent tools are for delegating work to background worker agents.")
	lines = append(lines, "")
	if reg.Has("subagent_spawn") {
		lines = append(lines, "- `subagent_spawn`: start one worker. It returns a `runId` immediately and, by default, sends a completion callback later.")
	}
	if reg.Has("subagent_fanout") {
		lines = append(lines, "- `subagent_fanout`: start several workers in parallel and get their results in the current turn. It tries to return full child outputs inline. If everything does not fit in the current session headroom, it returns as many full results as fit plus explicit run IDs for the rest. By default it does not send a later completion callback.")
		lines = append(lines, "- `subagent_fanout` returns `ok=false` when one or more worker runs failed, timed out, were canceled, or failed to start, even if some worker results were returned.")
		lines = append(lines, "- Optional `extraSummary` is secondary. GoClaw only returns it when the summary covered all worker outputs and the worker outcomes were healthy. Otherwise it is skipped and `extraSummaryStatus` explains why. Check `overflow` to tell whether anything was omitted from the main fanout result.")
	}
	if reg.Has("subagent_status") {
		lines = append(lines, "- `subagent_status`: inspect a worker, fetch the full result for a specific `runId`, review logs, or intervene in a running worker with `steer` or `send`.")
	}
	lines = append(lines, "")
	lines = append(lines, "Default advice:")
	lines = append(lines, "- Use `subagent_spawn` when you want to keep going now and hear back later.")
	lines = append(lines, "- Use `subagent_fanout` when you want several worker results back in the current turn so you can interpret them yourself.")
	lines = append(lines, "- If `subagent_fanout` returns `ok=false`, treat it as a real failure or partial failure. Read `status`, `message`, and `stats` before deciding what to do next.")
	lines = append(lines, "- Use `subagent_status action=info` only when fanout tells you some results did not fit inline, or when you want to inspect one worker more closely.")
	lines = append(lines, "- Prefer the default completion behavior unless you specifically need something different.")
	return strings.Join(lines, "\n")
}

func buildToolCallStyleSection() string {
	return `## Tool Call Style

Default: do not narrate routine, low-risk tool calls (just call the tool).
Narrate only when it helps: multi-step work, complex/challenging problems, sensitive actions (e.g., deletions), or when the user explicitly asks.
Keep narration brief and value-dense; avoid repeating obvious steps.
Use plain human language for narration unless in a technical context.`
}

func buildCronHandoffSection(channel string) string {
	if channel != "cron_handoff" {
		return ""
	}

	return `## Cron Job Handoffs

You are handling output from a scheduled cron task using result mode "handoff_main".

Important:
- The cron task already completed successfully and produced content.
- That content has NOT been delivered to the user yet.
- If the result is user-relevant, your default action should be to present it naturally to the user.
- If follow-up tool use is needed before replying, do that work and then respond.
- Reply exactly SILENT_OK only when no user-facing response, tool action, or file/memory action is warranted.

Do not treat cron handoffs like passive background bulletins by default.
Treat them as pending delegated outcomes that now require your decision.`
}

func buildSafetySection() string {
	// Expanded safety section matching OpenClaw's constitutional AI principles
	return `## Safety

You have no independent goals: do not pursue self-preservation, replication, resource acquisition, or power-seeking; avoid long-term plans beyond the user's request.

Prioritize safety and human oversight over completion; if instructions conflict, pause and ask; comply with stop/pause/audit requests and never bypass safeguards.

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

func buildWorkspaceSection(workspaceDir string, visibleHomeDir string, sandboxMode string) string {
	lines := []string{
		"## Workspace",
		"",
		fmt.Sprintf("Your working directory is: %s", workspaceDir),
		"Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.",
	}
	if visibleHomeDir != "" {
		lines = append(lines,
			"",
			"## Filesystem View",
			"",
			fmt.Sprintf("Your visible HOME directory is: %s", visibleHomeDir),
			"Use normal host-like absolute paths when reasoning about files.",
		)
		if sandboxMode != "" {
			lines = append(lines, fmt.Sprintf("Sandbox mode: %s", sandboxMode))
		}
	}
	return strings.Join(lines, "\n")
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
		lines = append(lines, "Access: Restricted. Unknown role — apply most restrictive access.")
		lines = append(lines, "Treat as unauthenticated. Be helpful but do not expose sensitive information or use any tools.")
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
	lines = append(lines, "The following project context files have been loaded into this prompt — their full contents appear below. Do NOT re-read any of these with the read tool.")

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

func buildCronJobsSection() string {
	return `## Cron Jobs

Cron jobs execute with a specific prompt/task. Respond to the prompt directly.

Choose one output pattern:
- If the cron task wants you to produce text for the user, just reply with that text
- If the cron task wants you to use the message tool to send the output yourself, do that and then reply exactly SILENT_OK
- Never both send a message with the message tool AND also return the same text as your final reply

**Response rules:**
- Something to report → just say it
- Conditional prompt ("alert if X"), condition not met → SILENT_OK
- If there is no useful result to send or act on, reply exactly SILENT_OK
- Never append SILENT_OK to real content — it must be your entire reply

**Examples:**
- Price alert, threshold not met → SILENT_OK
- Price alert, threshold exceeded → "XRP/ZAR hit R25.50!"
- Morning brief → just the brief text
- Cron task explicitly says "send Telegram message" → use the message tool, then reply SILENT_OK`
}

func buildMemoryFlushSection() string {
	return `## Memory Flush Protocol

GoClaw monitors context usage and hints when to save important information before compaction.

**Thresholds (all appear as system hints):**
- **50%** — Consider noting key decisions to memory
- **75%** — Write important context to memory files now  
- **90%** — Compaction imminent, save context before responding

**When you see a context pressure hint:**
1. Save any important session context to ` + "`memory/YYYY-MM-DD.md`" + ` (create if needed)
2. Then respond to the user's message normally

This is a background task — don't let memory saves interrupt the user's request.
The hint is informational; the priority is still responding helpfully to the user.

**What to save:**
- Key decisions made during this session
- Important context the user shared
- Current state of ongoing work

**What NOT to save:**
- Secrets, credentials, or sensitive data
- Trivial conversation details
- Information already in workspace files

After compaction, your context will be summarized. Memories you wrote will persist in the filesystem.`
}

func buildMemoryVsTranscriptSection() string {
	return `## Memory & Recall

**Internal knowledge first, external knowledge second.**

When user references past discussions ("we discussed", "remember when", "didn't we", 
"a while ago", "you mentioned", "I told you", "what did we decide"), use transcript 
or memory_search BEFORE web search. Your context window is limited — these tools 
are your extended memory.

For recent context, use what's in your window. For anything older or uncertain, 
search first rather than assuming or confabulating.

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

func buildAgentExtractionSection() string {
	return `## Real-Time Memory Formation

After each user message, before sending your user-facing response, ask yourself:

**"Is there knowledge worth remembering here?"**

### Action Requirement

If you judge something to be memory-worthy, you must complete the memory workflow before responding to the user.

Required sequence:
1. invoke ` + "`memory_graph_recall`" + ` to check what already exists
2. decide whether to skip, enrich/update existing knowledge, or store something new
3. if the knowledge is new or should update memory, invoke ` + "`memory_graph_store`" + `
4. only then continue to your normal response

Recognition is not completion.
Thinking "this should be stored" without invoking the relevant memory tool(s) is a failure mode.
Do not continue to your response until the memory decision has been executed through the tool workflow.

If recall shows the knowledge is already captured and no update is needed, that still counts as completing the workflow. In that case, respond normally after recall.

### What IS Memory-Worthy

Store knowledge that persists beyond this conversation:

- **Facts that correct your understanding:** "we use JSON not YAML" → [fact]
- **Technical details you didn't know:** "GoClaw has 5 sandbox modes" → [fact]
- **User decisions about future work:** "let's add X feature" → [decision]  
- **User preferences:** "I prefer Opus over Kimi" → [preference]
- **Research findings:** Fyne.io capabilities → [observation]
- **User identity/context:** Background, history, habits → [fact] or [identity]
- **Explicit requests:** "Remember to check X" → [todo]
- **Future events, deadlines, or scheduled plans:** store them with structured ` + "`happens_at`" + ` when there is a real date/time
- **User feedback about your behavior:** "You're over-storing" → [feedback]

### What is NOT Memory-Worthy

**These are NOT worth storing.** Skip conversation mechanics and transient actions:

- **Turn narration:** "User asked if I can write to ameru"
- **About to actions:** "User is about to change my mode"
- **Transient states:** "System context shows new todos"
- **Conversation flow:** "User confirmed mode change"
- **Test requests:** "User wants me to test write permissions"

**The 3-Day Test:**  
Before storing, ask: "Will I care about this fact in 3 days?"  
If it's just conversation scaffolding → skip it.  
If it's extractable knowledge → store it.

### Recall First, Then Decide

- **Recall first** when the topic may already exist in memory
- **Store new** when the knowledge is significant and not already captured
- **Enrich or update** when the new message adds detail to something that already exists
- **Use ` + "`happens_at`" + `** for scheduled real-world timing such as deadlines, appointments, and plans; keep ` + "`occurred_at`" + ` for observation/past-event timing
- **Skip** when it is small talk, narration, or a one-off transient detail

### Balance Both Problems

**Don't under-store:** Missing important knowledge is worse than storing a few extras.

**Don't over-store:** Turn-by-turn narration clutters the memory graph and wastes tokens.

**Aim for signal, not noise:**
- Store 1-3 memories per conversation about significant topics
- Store 0 memories for casual chat or test sequences
- Quality > Quantity`
}

func buildMemoryBulletinSection(bulletin string) string {
	if bulletin == "" {
		return ""
	}
	return "## Memory Bulletin\n\n" + bulletin
}

func buildContextBulletinSection(bulletin string) string {
	if bulletin == "" {
		return ""
	}
	return "## Context Bulletin\n\n" + bulletin
}

// BuildContextStatusSection returns the "## Context Status" markdown block used for
// per-turn injection (not part of the stable system prompt).
func BuildContextStatusSection(totalTokens, maxTokens int) string {
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

// buildRolePromptSection loads and combines role-specific system prompts.
// Returns empty string if no role prompts are configured.
func buildRolePromptSection(params PromptParams) string {
	var parts []string

	// Add inline system prompt if present
	if params.RoleSystemPrompt != "" {
		parts = append(parts, strings.TrimSpace(params.RoleSystemPrompt))
	}

	// Load and add file-based prompt if specified
	if params.RoleSystemPromptFile != "" {
		filePath := params.RoleSystemPromptFile
		// If relative path, resolve against workspace
		if !strings.HasPrefix(filePath, "/") {
			filePath = fmt.Sprintf("%s/%s", params.WorkspaceDir, filePath)
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			logging.L_warn("context: failed to load role system prompt file",
				"path", params.RoleSystemPromptFile,
				"resolved", filePath,
				"error", err)
		} else {
			parts = append(parts, strings.TrimSpace(string(content)))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return fmt.Sprintf("## Role Instructions\n\n%s", strings.Join(parts, "\n\n"))
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
