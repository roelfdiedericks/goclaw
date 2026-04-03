# Changelog

All notable changes to GoClaw will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

