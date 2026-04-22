# Changelog

All notable changes to GoClaw will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.16] stable - 2026-04-22

- tools: new `document_extract` turns uploaded PDFs, Office docs, EPUBs, and HTML into markdown via [go-markitdown](https://github.com/roelfdiedericks/go-markitdown); embedded images and scanned pages go through the agent vision chain for OCR, and the tool returns a short preview while caching the full output for `read`
- gateway: file-attachment summaries hint at `document_extract` for supported document types so the agent picks it up without being told
- media: new `extracted` category (30 day TTL, 2 GB quota) caches extracted document markdown; configurable in the web wizard and TUI alongside the other ephemeral categories
- memory: routine memories take structured recurrence (days, time, location, person, start/end dates, skip dates) instead of free-text, and today's occurrences surface in a new "Today's Schedule" bulletin section
- memory: due routines wake the agent on your primary session at the scheduled time; it can nudge you or stay quiet, and replies fan out to every active channel
- memory: `memtrigger` is now a configurable LLM purpose chain (falls back to `agent`); fixes a crash on routine fires and sharpens the wake preamble so the agent responds instead of hedging
- memory: routine recurrence shows inline on `memory_graph_query` / `memory_graph_recall` results — a compact `recurrence:` line with cadence, location, person, and next occurrence
- memory: `memory_graph_query` gains `mode: "triggers"` to read the routine-fire audit log (filter by memory, outcome, time range) so the agent can answer "did I remind them?"
- memory: "Today's Schedule" bulletin annotates routines that fired silently, were skipped as stale, or errored (`[silent]`, `[skipped]`, `[err]`)

## [0.1.15] stable - 2026-04-21

- session: GoClaw now remembers long conversations, similar in spirit to OpenClaw's `lossless-claw` plugin. When the chat grows past the context window, older parts get rolled up into searchable summaries instead of being dropped, and GoClaw pulls the exact original messages back whenever it actually needs the detail — so a decision from three months ago stays recoverable instead of being hallucinated
- session: four recall presets (`balanced` default, `aggressive`, `long_term_memory`, `recall_heavy`) trade off how much history GoClaw carries forward against prompt size and cost; configure under `session.summarization.compaction.lcm.preset`
- session: existing installs with large chat histories catch up automatically after upgrade — no manual migration. `/session` shows the catch-up progress while it runs
- tools: the `transcript.stats` agent tool surfaces the same long-memory picture plus preset-tuning hints, so GoClaw's own agent can diagnose its recall behavior
- security: bumped `golang.org/x/image` (GO-2026-4961) — fixes a panic on 32-bit builds when decoding large WEBP images via `web_fetch` / media optimization

## [0.1.14] stable - 2026-04-19

- coding agents/acp: expand Cursor ACP integration, startup/session handling, interactive prompt plumbing, and related config/editor support
- a2a/libp2p: add the first libp2p-based A2A runtime with bootstrap discovery, rendezvous, relay-first messaging, NAT traversal groundwork, and infra visibility tooling
- agent tools: add native owner-only A2A tool support for structured status, peer/task inspection, pairing payloads, ping, and remote task operations
- a2a/libp2p: optional background attempts to open a direct path to peers you already reach via relay (config under `libp2p.relay`; relay stays the default)
- localllm/llamacpp: turn on llama-server metrics for managed installs and record prompt-cache reuse plus basic server stats in GoClaw metrics
- localllm: add curated Qwen3 Coder 30B A3B managed-model variants from mradermacher (`Q2_K`, `Q3_K_S`, `Q4_K_S`, `Q4_K_M`)
- localllm: add Qwen3 Coder Next (Q8_0) to the managed local model list; text-only models no longer require a vision sidecar file
- localllm: resolve ROCm managed-runtime artifact on Debian trixie (same upstream `ubuntu-rocm-7.2-x64` tarball as generic Linux)
- gateway/context: context token usage and memory graph bulletins no longer default inside the main system prompt—they show as their own system notes
- llm/llamacpp: pin chat requests to stable llama-server slots when possible so prompt caching can stick across turns, with a safe fallback when slots are full

## [0.1.13] stable - 2026-04-03

- setup/runtime: auto-refresh stock workspace identity templates on startup when the workspace copy still matches a known stock version, while preserving customized files and excluding `BOOTSTRAP.md`

## [0.1.12] stable - 2026-04-03

- setup/runtime: create missing workspace templates during onboarding and self-heal older installs without recreating `BOOTSTRAP.md` after `SOUL.md` exists
- coding agents/acp: move Cursor ACP preferences into a dedicated top-level config section, add standalone web/TUI editor support, live model refresh, and compatibility mapping from legacy `gateway.acpCursorModel`
- setup web UI: add Coding Agents and Web Search quick-task dashboard shortcuts, including deep-link expansion into `Tools -> Web Search`

## [0.1.11] stable - 2026-04-02

- acp: add initial local stdio ACP session support with a first Cursor driver and goacp-backed extension probing
- acp tools: add agent-facing `acp_attach`, `acp_info`, `acp_respond` and `acp_cancel` workflows
- cursor acp: surface `ask_question`, `create_plan`, `update_todos`, `task` and `generate_image` events across HTTP, Telegram and TUI with shared handoff/cancellation handling for interactive prompts
- telegram/http UX: add native Telegram poll handling for single-question multi-select asks, synthetic `Other...` escape hatches, and stale interactive-state cleanup when the user continues in chat

## [0.1.10] stable - 2026-03-31

- runtime: add `goclaw status --field` for shell-safe machine-readable checks and structured `configured=false` status when no config exists yet
- installer: improve post-install update guidance for already-configured and already-running GoClaw installs, and trim a few brittle shell parsing paths
- cli: add `goclaw restart` as a simple daemon restart helper
- audit: add a Go proxy-backed dependency age check with TOML policy support to everyday `make audit`
- release tooling: make `make changelog` promote `Unreleased` notes into the new release entry and recreate a blank `Unreleased` section

## [0.1.9] stable - 2026-03-30
- setup: add Telegram/WhatsApp owner pairing flows across the browser wizard, browser editor, TUI wizard, and TUI editor with staged owner identity saves
- channels: add shared setup pairing contracts plus channel-owned Telegram OTP and WhatsApp QR pairing backends
- setup web UI: improve blocked-next guidance for consent and pairing steps, and add cache-busting for setup static assets during iteration
- setup TUI: align onboarding step flow with the browser wizard and require explicit preset warning acknowledgement before advancing
- installer: guide fresh installs toward `goclaw onboard` and point existing installs at both guided setup and `goclaw setup edit`
- deps: bump `golang.org/x/image` to v0.38.0 to address the reachable TIFF decoding vulnerability flagged by `make audit`

## [0.1.8] stable - 2026-03-25
- media: add an agent-facing `media` tool with live storage info, quotas, retention and category warnings
- voicellm: bring the web config page to parity for audio effects presets and custom controls, including Battlestar Galactica
- voice web UI:  broaden embedded `/js/` static assets for related media/favicons
- memory prompt: make real-time memory formation a required recall/store workflow instead of advisory guidance
- memory graph: add structured `happens_at` scheduling with bulletin/query support and agent prompt/tool guidance for future events and deadlines

## [0.1.7] stable - 2026-03-24

- browser: remote CDP profiles and HTTP discovery
- browser: console/network capture, tracing and emulation controls
- browser: MCP-style action aliases, drag support and related docs/tests/config UI
- memory graph: coordinate agent-driven and background extraction with shared dedupe and an agent-first handoff delay
- subagent_fanout: make partial failures explicit and skip extra summaries when worker outcomes are unhealthy
- runners web UI: stabilize live updates and add split-pane detail/transcript inspection


## [0.1.6] stable - 2026-03-24

- delegated subagents and fanout
- delegated runner registry, return routing and cancellation
- runners web UI and APIs
- delegated run visibility improvements in Telegram and TUI
- subagent UX cleanup
- delegated runs docs, tests and config rollout


## [0.1.5] stable - 2026-03-21

- web_search: add multi-provider backend support (brave, grok/xai, perplexity, gemini) with a shared driver interface
- web_search: add provider retry and fallback chain handling for transient upstream failures without changing agent/tool parallelism
- web_search: add provider-aware result metadata so the agent can see which provider produced results
- config: add full tools.web.search config-form support (default provider, fallbacks, retry tuning, per-provider keys)
- config: add legacy brave key compatibility mapping into the new provider config path for smoother migration
- http ui: fix run-scoped debug/tool panel lifecycle issues that caused duplicate or disappearing tool bubbles at turn completion
- telegram: add structured streaming tool activity summary (thinking -> tools -> final response flow) with compact post-run success display
- telegram: preserve clickable links for markdown tables by rendering table refs in `<pre>` and appending a clickable `Links:` section
- tests/docs: add web_search resolver/tool unit coverage and update web/tooling docs for new provider configuration

## [0.1.4] stable - 2026-03-21
- add binary file contentguard
- http channel ui rewrite
- sort out binary tool output compaction issues
- better safety word handling, shutdown safety word implementation
- expose contextWindow size in UI for customising on unknown models

## [0.1.3] stable - 2026-03-17
- major sandbox reworking
- wizard/ onboarding updates
- documentation updates
- model fallback chains
- sandbox exec testing tool

## [0.1.2] stable - 2026-03-14
- macOS now properly tested
- browser based setup and config wizard
- cron refactoring

## [0.1.1] stable - 2026-03-06
- First real release

## [0.1.0] stable - 2026-02-17

- Initial release
- Self-update mechanism with checksum verification
- GitHub Actions release workflow with auto-generated notes
- Debian package (.deb) distribution
- Docker images on GHCR
- user_auth tool update, docs and exampless

