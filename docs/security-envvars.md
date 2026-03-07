---
title: "Environment variables and secrets"
description: "Why GoClaw uses config file as single source of truth, with optional explicit env var references"
section: "Security"
weight: 10
---

# Environment variables and secrets

GoClaw uses `goclaw.json` as the single source of truth for configuration. It does **not** automatically scan environment variables for API keys or tokens. However, you can explicitly reference environment variables using `${VAR_NAME}` syntax when needed.

This page explains:
- How to use environment variable references in config
- Why GoClaw doesn't auto-scan env vars
- Security considerations and best practices

For where config is stored and how it is protected from the agent, see [Configuration — Security: config file and credentials](configuration.md#security-config-file-and-credentials).

---

## Explicit environment variable references

GoClaw supports referencing environment variables in `goclaw.json` using `${VAR_NAME}` syntax:

```json
{
  "llm": {
    "providers": {
      "anthropic": {
        "apiKey": "${ANTHROPIC_API_KEY}"
      },
      "openai": {
        "apiKey": "${OPENAI_API_KEY}"
      }
    }
  },
  "channels": {
    "telegram": {
      "botToken": "${TELEGRAM_BOT_TOKEN}"
    }
  }
}
```

### When expansion happens

- **At runtime** — When starting the gateway (`goclaw gateway`, `goclaw start`), CLI commands, and browser/embeddings/graph operations, `${VAR}` references are expanded to their environment values.
- **In setup wizard/editor** — The literal `${VAR_NAME}` text is preserved. You see and edit the placeholder, not the resolved value.

### Missing variables

If a referenced environment variable is not set, GoClaw fails with a clear error:

```
config: environment variable ${ANTHROPIC_API_KEY} is not set
```

This fail-fast behaviour prevents silent failures from missing secrets.

### Use cases

This feature is useful for:

- **Kubernetes** — Secrets mounted as environment variables
- **Docker Compose** — Secrets in `environment:` section
- **CI/CD pipelines** — Secrets injected by the pipeline
- **Process supervisors** — Environment injection at service start
- **Vault/SOPS** — Secrets decrypted to env at runtime

---

## Why not auto-scan environment variables?

GoClaw deliberately does not automatically read `ANTHROPIC_API_KEY` or similar from the environment. You must explicitly write `${ANTHROPIC_API_KEY}` in the config to use it. This is intentional.

### Precedence confusion

If an application supports both a config file and environment variables for the same setting:

- **Which wins?** The order of resolution must be defined (env overrides file, or file overrides env). Without a clear rule, behaviour depends on load order and is hard to reason about.
- **Debugging** — "Which key is actually used?" becomes harder when env might be set in a parent process, a shell profile, or a container entrypoint you forgot about.

With explicit `${VAR}` references, the config file is always the source of truth. It just happens to reference an external value.

### Visibility

Environment variables are invisible in the config file and often in the process list. It's easy to assume "nothing is configured" when in fact env is set elsewhere.

With `${VAR}` syntax, the config file explicitly shows which variables are expected. You can see at a glance what external dependencies exist.

### Agent exposure

If GoClaw auto-scanned all environment variables, the agent could potentially observe which secrets are available (even if it couldn't read the values directly). With explicit references, only variables you deliberately include in config are relevant.

---

## Security considerations

### Environment variables are not secure storage

- **Cleartext in process** — Environment variables live in process memory and in `/proc/<pid>/environ` (readable by the same user). Any child process inherits them. (See CWE-526)
- **Leakage** — Env often ends up in crash dumps, debug logs, and "print env for support" outputs.
- **Injection surface** — Env values used in shell commands can contribute to injection attacks.

### Config file advantages

Storing secrets in a file has its own considerations (backups, permissions), but:

- **Single path** — One location to secure (`chmod 0600`)
- **Agent-blocked** — GoClaw's sandbox denies agent access to `goclaw.json`
- **Auditable** — One place to rotate and review credentials

### Recommendation

Use `${VAR}` references when your deployment system requires env-based secret injection (Kubernetes, Docker, CI/CD). For local development or standalone deployments, hardcoded values in `goclaw.json` with proper file permissions are simpler and equally secure.

---

## Best practice

Common guidance (e.g. OWASP, CNCF) advises:

- Do not treat environment variables as a first-class secrets store; prefer explicit injection at process start (from a vault or secret manager) or a config file with strict permissions.
- Use env for *non-secret* configuration (e.g. `LOG_LEVEL`, `PORT`).
- If you use env for secrets: set it at process start, avoid logging the environment, and do not mix untrusted input into env.

GoClaw's approach — explicit `${VAR}` references rather than auto-scanning — aligns with this: you control exactly which env vars are used, the config file remains the single source of truth, and the setup wizard preserves placeholders for editing.

---

## Summary

| Aspect | Auto env scanning | Explicit `${VAR}` (GoClaw) |
|--------|-------------------|----------------------------|
| Precedence | File vs env must be defined | Config is source of truth |
| Visibility | Hidden; env set "somewhere" | Config shows which vars used |
| Agent exposure | Could observe all env vars | Only referenced vars matter |
| Kubernetes/Docker | Works automatically | Works with explicit syntax |
| Setup wizard | Would show resolved values | Shows `${VAR}` placeholders |

---

## See Also

- [Security](security.md) — Security overview
- [Configuration](configuration.md) — Config file location and structure
