---
title: "Environment variables and secrets"
description: "Why GoClaw uses config file only for secrets; env var risks and best practice"
section: "Security"
weight: 10
---

# Environment variables and secrets

GoClaw does **not** read API keys or tokens from environment variables at runtime. Secrets and settings are read only from `goclaw.json` (and `users.json`). This page explains the rationale: unintended behaviour when env and file both apply, security concerns, and how GoClaw’s approach aligns with common best practice.

For where config is stored and how it is protected from the agent, see [Configuration — Security: config file and credentials](configuration.md#security-config-file-and-credentials).

---

## GoClaw’s behaviour

- **At runtime:** The only source of secrets is the config file. No environment variable overrides.
- **At setup:** The setup wizard can copy API keys from your environment (e.g. `ANTHROPIC_API_KEY`, `TELEGRAM_BOT_TOKEN`, `BRAVE_API_KEY`) or from existing auth-profiles into `goclaw.json`. After that, runtime uses only the config file.

So you get a single, explicit source of truth and no ambiguity about which value is used.

---

## Unintended behaviour when env and file both apply

If an application supports both a config file and environment variables for the same setting, several problems often arise:

- **Precedence** — The order of resolution must be defined (e.g. “env overrides file” or “file overrides env”). Without a clear rule, behaviour depends on load order and is hard to reason about. Even with a rule, users must remember which source wins, and debugging “which key is actually used?” becomes harder when env is set in a parent process, a systemd unit, or a container entrypoint they forgot about.
- **Visibility** — Environment variables are invisible in the repo and often in the process list. It is easy to assume “nothing is configured” when in fact env is set elsewhere, leading to surprises when moving to another machine or another user.
- **Scope** — Env is usually process-wide. You cannot easily have “key A for this run, key B for that run” without wrapping in scripts that set env per invocation. A config file can be swapped or its path overridden more explicitly.

GoClaw avoids these by having a single source at runtime: the config file.

---

## Security concerns with environment variables

- **Cleartext in process** — Environment variables live in process memory and in `/proc/<pid>/environ` (readable by the same user, and often by root). Any child process inherits them. So env is effectively cleartext storage from a process perspective (see CWE-526: cleartext storage of sensitive information in an environment variable).
- **Leakage** — Env often ends up in crash dumps, debug logs, and “print env for support” outputs. With a file, you control one path and its permissions; with env, any tool that logs or dumps the environment can leak secrets.
- **Injection** — If env values are ever built from or mixed with untrusted input and then used in shell commands or passed to other programs, they can contribute to injection (e.g. Shellshock-style issues, or command injection when values are used in shell commands). That risk is about how env is used, but env spreads values into many code paths, increasing the attack surface.
- **Parsing and format** — Env is stringly-typed; newlines and special characters can be mis-parsed when values are written into scripts or configs. A structured config format (e.g. JSON) has clearer escaping and structure.

Storing secrets in a file has downsides too (backups, permissions), but the file is at a fixed path, can be permission-restricted (`chmod 0600`), and in GoClaw is explicitly excluded from agent tool access. So from a security standpoint, a single, well-protected config file is generally preferable to env for secrets.

---

## Best practice

Common guidance (e.g. OWASP, CNCF) advises:

- Do not treat environment variables as a first-class secrets store; prefer explicit injection at process start (e.g. from a vault or secret manager) or a single, well-defined config file with strict permissions.
- Use env for *non-secret* configuration (e.g. `LOG_LEVEL`, `PORT`).
- If you do use env for secrets: set it at process start, avoid logging or dumping the environment, and do not mix untrusted input into env. Even then, precedence and “which env is set where” remain operational headaches.

GoClaw’s choice — one source of truth (the config file), no env at runtime for secrets — matches this: predictable behaviour, one place to rotate and audit, and the setup wizard provides a one-time migration path from env or existing auth-profiles into `goclaw.json`.

---

## Summary

| Aspect | Env vars at runtime | Config file only (GoClaw) |
|--------|---------------------|---------------------------|
| Precedence | File vs env must be defined; easy to get wrong | Single source; no precedence rules |
| Visibility | Hidden; differs per environment | Explicit path; same format everywhere |
| Security | Process dump, inheritance, injection surface | One path; `chmod 0600`; sandbox denies agent access |
| Operational clarity | “Which key?” depends on where env is set | One place to rotate and audit |
| Migration | — | Wizard can copy from env or auth-profiles once |

---

- [Security](security.md) — Security overview  
- [Configuration](configuration.md) — Config file location, sandbox, and credentials
