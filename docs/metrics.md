# Metrics

GoClaw exposes metrics for monitoring and observability.

## Endpoints

### JSON Metrics

```
GET /api/metrics
```

Returns all metrics as JSON:

```json
{
  "metrics": [
    {
      "path": "llm.requests",
      "type": "counter",
      "health": 0,
      "data": {"value": 1234}
    }
  ]
}
```

### Prometheus Metrics

```
GET /metrics
```

Returns metrics in Prometheus text format for scraping.

## Metric Types

| Type | Description |
|------|-------------|
| `counter` | Incrementing count |
| `gauge` | Value that can increase or decrease |
| `timing` | Duration measurements with percentiles |
| `hit_miss` | Cache hit/miss ratios |
| `success_fail` | Success/failure counts with rates |
| `outcome` | Multiple possible outcomes |
| `error` | Error tracking by type |
| `condition` | Boolean state tracking |
| `threshold` | Values against thresholds |

## Available Metrics

### LLM Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `llm.requests` | counter | Total LLM API requests |
| `llm.latency` | timing | Request latency |
| `llm.tokens.input` | counter | Input tokens used |
| `llm.tokens.output` | counter | Output tokens generated |
| `llm.cache` | hit_miss | Prompt cache performance |

### Session Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `session.messages` | gauge | Current message count |
| `session.tokens` | gauge | Current token count |
| `session.compactions` | counter | Compaction count |

### Tool Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `tools.calls` | counter | Total tool invocations |
| `tools.latency` | timing | Tool execution time |
| `tools.errors` | error | Tool errors by type |

## Health Status

Each metric includes a health status:

| Status | Value | Meaning |
|--------|-------|---------|
| Good | 0 | Normal operation |
| Warning | 1 | Needs attention |
| Critical | 2 | Action required |

## Configuration

No configuration required. Metrics are enabled when the HTTP channel is active.

## Prometheus Integration

Add GoClaw as a Prometheus scrape target:

```yaml
scrape_configs:
  - job_name: 'goclaw'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: /metrics
```

## Example Queries

### Request Rate (Prometheus)

```promql
rate(goclaw_llm_requests_total[5m])
```

### Average Latency

```promql
rate(goclaw_llm_latency_sum[5m]) / rate(goclaw_llm_latency_count[5m])
```

### Token Usage

```promql
increase(goclaw_llm_tokens_input_total[1h])
```

---

## See Also

- [Web UI](web-ui.md) — HTTP endpoints
- [Configuration](configuration.md) — Full config reference
- [Advanced](advanced.md) — Debugging and monitoring
