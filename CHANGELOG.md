# Changelog

All notable changes to GoClaw will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

- delegated runs: add end-to-end reinjection hardening coverage (`subagent_spawn` completion -> synthetic requester `tool_use/tool_result`) including HTTP request-handler path
- delegated runs: add cron delegated-path delivery semantics tests to ensure `store_only`/`deliver`/`handoff_main` behavior parity
- delegated runs: add runners API tests for snapshot + SSE resume reconciliation (`since` / `Last-Event-ID`)
- delegated runs: add `internal/delegatedrun` unit/concurrency coverage for completion/failed/cancel/timeout state transitions, mixed parallel outcomes, bus event schema payloads, and registry consistency
- subagent_cancel: add integration test covering parent cascade cancellation through tool path
- visibility: add concise Telegram delegated lifecycle summaries for owner and compact delegated run snapshot in TUI logs pane
- delegated runs: auto-propagate `parentRunID` from current session run when `subagent_spawn` omits explicit lineage
- delegated runs: harden `return_to_requester` retries by advancing persisted completion dispatch sequence after dispatch failure
- delegated runs: add dedicated concurrency lane scheduler (`maxConcurrentRuns`) so excess runs remain queued and are admitted as slots free up
- delegated runs: add `subagent_fanout` coordinator with bounded parallel child spawning and deterministic aggregation ordered by input index
- delegated runs: add optional model-mediated synthesis pass for `subagent_fanout` (`synthesize` / `synthesisPrompt`) layered on top of deterministic fanout reduction
- delegated runs: add fanout synthesis guardrails with bounded synthesis input payload + truncation metadata and per-synthesis timeout override (`synthesisTimeoutSeconds`)
- runners dashboard: add "Include completed" toggle for active-focused triage view while preserving full history on demand
- docs: add dedicated `docs/delegated-runs.md` architecture reference and link it from architecture/tools docs
- docs: add delegated-runs concept section in `docs/concepts.md` and README feature/docs links for delegated architecture
- gateway config: enable delegated runs by default (`gateway.delegatedRuns.enabled=true`) and clarify delegated lane capacity wording in setup form
- docs: extend configuration reference with delegated runs and subagent tool knobs (`gateway.delegatedRuns.*`, `tools.subagent`)
- tools: register subagent tool set only when both `tools.subagent.enabled` and `gateway.delegatedRuns.enabled` are true; log warning on invalid partial enablement
- tools config: set delegated subagent tool toggle default to enabled and document dependency on delegated infrastructure toggle
- setup UI: split delegated settings into a dedicated "Delegated Runs" section under Gateway config form
- delegated runs: add central timeout policy knobs (`defaultTimeoutSeconds`, `maxTimeoutSeconds`) and enforce defaults/caps in delegated start path
- subagent spawn/fanout: avoid auto-linking `parentRunID` from non-delegated main-session run IDs (prevents false "parent run not found" denials)






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

