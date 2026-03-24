# Changelog

All notable changes to GoClaw will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]




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

