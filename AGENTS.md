---
description: 
alwaysApply: true
---

# GoClaw Development Guidelines

## Logging Standards

All code in GoClaw MUST include appropriate logging to aid debugging and observability. Use the logging package with dot import for convenience:

```go
import . "github.com/roelfdiedericks/goclaw/internal/logging"
```

### Log Levels

**L_trace** - Very detailed, low-level information
- Individual iterations, data transformations
- File contents being read/written
- Only visible with `-t` flag
- Example: `L_trace("parsing json field", "field", name, "value", val)`

**L_debug** - Information useful for debugging
- Function entry/exit with key parameters
- Configuration values being used
- External API calls (request/response summaries)
- State changes and decisions
- Visible with `-d` flag
- Example: `L_debug("sending request to Anthropic", "model", model, "messages", len(msgs))`

**L_info** - Normal operational information
- Service startup/shutdown
- Significant events (user authenticated, agent run started/completed)
- Always visible
- Example: `L_info("telegram bot started", "username", bot.Username)`

**L_warn** - Potential issues that don't stop execution
- Unknown users attempting access
- Retryable errors
- Deprecated feature usage
- Example: `L_warn("unknown telegram user ignored", "userID", id)`

**L_error** - Errors that affect functionality
- Failed API calls
- Configuration errors
- Tool execution failures
- Example: `L_error("failed to send message", "error", err)`

### Required Logging Points

Every package MUST log:

1. **Initialization**: Log when the component is created with key config values
2. **External calls**: Log before/after any external API or system call
3. **State changes**: Log significant state transitions
4. **Errors**: Always log errors with context
5. **User actions**: Log user-initiated actions with user identity

### Format Guidelines

- Use structured logging with key-value pairs: `L_info("message", "key1", val1, "key2", val2)`
- Keep messages lowercase and concise
- Include relevant IDs (runID, userID, sessionID) for correlation
- For sensitive data, log length not content: `"tokenLength", len(token)`
- Prefix with package/component: `"config: loading file"`, `"telegram: user authenticated"`

### Example Implementation

```go
func (b *Bot) handleMessage(c tele.Context) error {
    userID := fmt.Sprintf("%d", c.Sender().ID)
    
    L_debug("telegram: message received", "userID", userID, "chatID", c.Chat().ID)
    
    user := b.users.FromIdentity("telegram", userID)
    if user == nil {
        L_warn("telegram: unknown user ignored", "userID", userID)
        return nil
    }
    
    L_info("telegram: processing message", "user", user.Name, "role", user.Role)
    
    result, err := b.process(c.Text())
    if err != nil {
        L_error("telegram: processing failed", "error", err, "userID", userID)
        return err
    }
    
    L_debug("telegram: message processed", "responseLength", len(result))
    return nil
}
```

## Code Style

- Use meaningful variable names
- Keep functions focused and small
- Document exported functions and types
- Handle errors explicitly, never ignore them silently
- Use context.Context for cancellation and timeouts

## Build patterns

- Avoid running gofmt, and lint all the time. It can be done once after a major iteration

## Config Form Patterns

Default pattern:

- Bind web/TUI forms directly to the real runtime config structs.
- Treat this as the default for new config sections unless there is a very clear reason not to.

Allowed exception:

- Only add a separate form-data wrapper when the UI truly needs temporary or synthetic fields that must not live on the runtime struct.
- `VoiceLLM` is the model for this pattern. It is the exception, not the default.

Do not do this:

- Do not invent a parallel wrapper just to carry presets, derived values, or save-time expansions for a section that should round-trip to `goclaw.json`.
- If a selector or field must persist to disk, it belongs on the real config struct.

Preset / derived-field rule:

- For preset-backed config, store the persisted selector on the real config struct.
- Put the preset/default expansion logic in one shared normalization helper on the real config type.
- Do not duplicate that logic in separate web-only or TUI-only code paths.

Required wiring:

- If a section needs normalization, call the shared normalization helper in all relevant paths:
- Web section save handler
- TUI accept/save path
- Runtime config load/default path

Testing rule:

- For persistence bugs, add a focused regression test that inspects the raw JSON written to disk.
- Do not rely only on reload-through-`config.LoadFromPath(...)` tests, because runtime defaults and normalization can hide bad on-disk state.

Checklist before merging:

- Is the form bound to the real runtime config struct?
- If not, is there a genuine UI-only reason for a wrapper?
- Are presets/derived fields normalized by one shared helper?
- Are web, TUI, and runtime load paths all using that helper?
- Is there a raw-file persistence regression test?

---

## CHANGELOG.md updates

The CHANGELOG is **end-user facing**. It lives next to the README and gets read by people who run GoClaw, not by the engineers who wrote the code. Write accordingly.

- **This is the GoClaw project.** Always name it. Do not say "bots" or "the bot" as a generic stand-in for GoClaw — a changelog entry that works for any random chat bot isn't telling a GoClaw operator anything specific. Say "GoClaw does X" or name the concrete surface (`/session`, `transcript.stats`, the TUI, the HTTP UI, etc.).
- **Audience:** a GoClaw operator who knows what GoClaw is but has never read the source. They want to know *what changed for them*, not what was refactored internally.
- **Style:** same terse, one-line-per-entry format as prior releases (`## [0.1.14] stable`, `## [0.1.13] stable`, etc.). Look at those before writing new entries and match their voice.
- **Length:** keep the `Unreleased` section tight. **Collapse related internal changes into a single user-observable bullet.** A feature that took 10 commits to land is one bullet, not ten — unless each commit was independently user-visible. If you find yourself adding a third bullet with the same `component:` prefix in one Unreleased block, you're almost certainly listing internal steps that should be merged.
- **Plain language, not jargon:** explain feature names and acronyms the first time they appear in an Unreleased block (in parens or a short clause is fine). "**long-memory recall** — when old messages get rolled up into summaries, they stay searchable and expandable" is good. "LCM frontier injection with incremental_max_depth tuning" is not. Internal names like LCM, DAG, fanout, condensation are fine inside the GoClaw codebase but should be decoded for the changelog reader.
- **Don't reference internal identifiers** unless they are user-facing surfaces. User-facing is OK: config keys (`session.summarization.compaction.lcm.*`), CLI commands (`goclaw onboard`), slash commands (`/session`), tool names (`transcript.stats`), HTTP endpoints (`/api/metrics`), file paths in the user's home (`~/.goclaw/sessions.db`). Not OK: Go type names (`CompactionManagerConfig`), function names (`backfillCompactionDAG`), struct field paths (`SessionConfig.Summarization.Compaction.LCM.*`), SQL table names, private interface names.
- **Past-release blocks are frozen.** Do not edit `## [0.1.x]` entries unless the user explicitly asks. Only append new bullets under `## [Unreleased]`.
- **One bullet = one observable outcome.** "The thing now works" or "the `/session` command now shows X" or "four new presets exist". Not "refactored X to do Y so that Z" — that's a commit message, not a changelog line.

When in doubt, ask: *would a GoClaw operator skimming this before upgrading care?* If no, it doesn't belong in the changelog (or it should be collapsed into a more general bullet with other related work).

## Documentation updates
Follow the docs/AGENTS.md guide for changing files in the docs/ subdirectory. Ensure documentation updates are user-facing and not overly technical.

## Release readiness

When the user says "release it" (or asks whether something is ready for release), run this check — in this order — and report status. **Never cut the version, tag, or run any git write commands. The user handles the actual release.**

1. **Docs review.** Scan `docs/` for anything that could be stale because of the current changeset. Pure reliability / internal fixes usually need no user-facing doc change — the CHANGELOG is the right place. User-visible features or behavior changes usually do. Honor `docs/AGENTS.md`.
2. **`make build`** — must be clean.
3. **Targeted tests** — `go test -vet=off -count=1` against the packages touched by the changeset. Only widen if you've touched cross-cutting infrastructure.
4. **`make audit`** — full pipeline (build + golangci-lint + module audit + govulncheck + gitleaks). Must be green. If it fails, fix and re-run; don't hand off a broken audit.
5. **`git status` + `git diff --stat`** — sanity-check the changeset. Flag:
   - unrelated / collateral edits
   - accidentally included secrets (`.env`, credentials, keys)
   - new binaries or generated files that shouldn't be tracked
6. **`## [Unreleased]` in `CHANGELOG.md`** — verify entries match the `CHANGELOG.md updates` rules above (terse, user-facing, one bullet per observable outcome, no internal jargon).
7. **Report.** Summarize verification results, diff footprint, and CHANGELOG state. If a version cut is useful, *propose* the rename (`[Unreleased]` → `[0.1.x] stable - YYYY-MM-DD` plus a fresh empty `[Unreleased]`) but **wait for approval** before writing it.

Do NOT:
- Run `git add` / `commit` / `tag` / `push` / any git write command (see `ground-rules`)
- Bump the version in any file yourself
- Run `goclaw gateway` or `make run` to "verify the build works at runtime" — the user tests manually


