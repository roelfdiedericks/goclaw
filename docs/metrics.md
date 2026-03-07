---
title: "Metrics"
description: "Real-time metrics, LLM cost tracking, and observability"
section: "Advanced"
weight: 55
---

# Metrics

GoClaw includes a comprehensive metrics system for monitoring performance, tracking LLM costs, and observability.

## Overview

Metrics are organized in a hierarchical tree structure by topic and function:

```
llm/
├── anthropic/
│   └── claude-sonnet-4/
│       ├── timing (p50: 2.3s, p99: 8.1s)
│       └── cost (total: $1.23)
├── ollama/
│   └── nomic-embed-text/
│       └── timing (p50: 45ms)
gateway/
├── agent_loop/
│   └── timing
└── compaction/
    └── success_fail
```

## Accessing Metrics

### Web UI

Visit `/metrics` on your HTTP channel for an interactive tree view:

```
http://localhost:1337/metrics
```

The UI auto-refreshes and allows expanding/collapsing metric branches.

### JSON API

Get a JSON snapshot via `/api/metrics`:

```bash
curl http://localhost:1337/api/metrics | jq
```

Returns all metrics with current values, organized by path.

## LLM Cost Tracking

GoClaw tracks LLM costs automatically using pricing data from the embedded `models.json`:

- **Input tokens** — Cost per million input tokens
- **Output tokens** — Cost per million output tokens
- **Cache read** — Discounted cost for cached prompt tokens (Anthropic)
- **Cache write** — One-time cost when caching new content

### How It Works

1. Each LLM call records token counts (input, output, cache read/write)
2. Pricing is looked up from `models.json` based on provider and model
3. Cost is calculated in microdollars (USD × 1,000,000) for precision
4. Metrics track: total accumulated, last request, min/max per request

### Viewing Costs

Costs appear in the metrics tree under the LLM provider path:

```
llm/anthropic/claude-sonnet-4/cost
  Total: $1.234567
  Last:  $0.002340
  Min:   $0.000120
  Max:   $0.045000
  Count: 523
```

### Custom Pricing

Override `models.json` pricing in your provider config:

```json
{
  "llm": {
    "providers": {
      "my-claude": {
        "driver": "anthropic",
        "apiKey": "...",
        "costInput": 3.0,
        "costOutput": 15.0,
        "costCacheRead": 0.3,
        "costCacheWrite": 3.75
      }
    }
  }
}
```

Values are USD per million tokens. Set to `0` to use `models.json` defaults.

## Metric Types

| Type | Description | Example |
|------|-------------|---------|
| **Timing** | Duration with percentiles (p50, p95, p99) | LLM response time |
| **Counter** | Incrementing count | Messages processed |
| **Gauge** | Point-in-time value | Active sessions |
| **HitMiss** | Cache hit/miss ratio | Prompt cache |
| **SuccessFail** | Success/failure counts | Tool execution |
| **Outcome** | Multiple outcome categories | LLM response types |
| **Error** | Error counts by type | API errors |
| **Cost** | Accumulated cost tracking | LLM spend |
| **Threshold** | Value vs threshold tracking | Context usage |

## Persistence

Metrics persist across restarts in SQLite (`~/.goclaw/sessions.db`). This enables:

- Cumulative cost tracking over time
- Historical comparison
- Survival across gateway restarts

Persistence saves every 30 seconds and on graceful shutdown.

## Key Metrics

### LLM Performance

| Path | Description |
|------|-------------|
| `llm/<provider>/<model>/timing` | Response latency |
| `llm/<provider>/<model>/cost` | Token costs |
| `llm/<provider>/<model>/tokens` | Token counts |

### Gateway

| Path | Description |
|------|-------------|
| `gateway/agent_loop/timing` | Full agent loop time |
| `gateway/compaction/timing` | Compaction duration |
| `gateway/tool/<name>/timing` | Tool execution time |

### Channels

| Path | Description |
|------|-------------|
| `telegram/messages` | Message counts |
| `http/requests` | HTTP request counts |
| `whatsapp/messages` | WhatsApp message counts |

## Programmatic Access

Use the metrics API in Go code:

```go
import . "github.com/roelfdiedericks/goclaw/internal/metrics"

// Timing
key := MetricStart("my_topic", "my_function")
// ... do work ...
MetricEnd(key)

// Or auto-timed:
defer MetricFunc("my_topic")()

// Counters
MetricInc("requests", "total")
MetricAdd("bytes", "processed", 1024)

// Cost (microdollars)
MetricCost("llm/anthropic", "claude-sonnet", 234500) // $0.2345
```

---

## See Also

- [LLM Providers](llm-providers.md) — Provider configuration
- [Deployment](deployment.md) — Monitoring setup
- [Architecture](architecture.md) — System overview
