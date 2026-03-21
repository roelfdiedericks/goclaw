---
title: "Web Tools"
description: "Search the web and fetch page content"
section: "Tools"
weight: 50
---

# Web Tools

Search the web and fetch page content.

## web_search

Search the web using one of these providers:

- `grok` (xAI)
- `brave`
- `perplexity`
- `gemini`

```json
{
  "query": "golang concurrency patterns"
}
```

**Output:** Search results with titles, URLs, and snippets.

### Configuration

```json
{
  "tools": {
    "web": {
      "braveApiKey": "YOUR_BRAVE_API_KEY",
      "search": {
        "enabled": true,
        "provider": "auto",
        "fallbackProviders": [],
        "maxFallbackAttempts": 3,
        "retry": {
          "enabled": true,
          "maxAttemptsPerProvider": 2,
          "baseBackoffMs": 500,
          "maxBackoffMs": 5000
        },
        "providers": {
          "brave": {
            "apiKey": "YOUR_BRAVE_API_KEY"
          },
          "grok": {
            "apiKey": "YOUR_XAI_API_KEY"
          },
          "perplexity": {
            "apiKey": "YOUR_PERPLEXITY_API_KEY",
            "baseUrl": "https://api.perplexity.ai",
            "model": "sonar"
          },
          "gemini": {
            "apiKey": "YOUR_GEMINI_API_KEY",
            "model": "gemini-2.5-flash"
          }
        }
      }
    }
  }
}
```

### Provider selection and fallback

- `provider: "auto"` uses order: `grok`, `brave`, `perplexity`, `gemini`.
- `fallbackProviders` overrides fallback order when set.
- Retryable errors (`429`, `408`, timeouts, `5xx`) retry per provider, then can fall back.
- Non-retryable errors (`401`, `403`, invalid request) fail that provider immediately.

### Key resolution

For each provider, API key resolution order is:

1. `tools.web.search.providers.<provider>.apiKey`
2. matching existing `llm.providers` key (when available)
3. `tools.web.braveApiKey` (Brave only, compatibility)

GoClaw reads keys from `goclaw.json`. If you use `${VAR}` placeholders there, GoClaw expands them during config load.

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `query` | Yes | Search query |
| `count` | No | Results requested (default 5, max 20) |

### Example Output

```
1. Concurrency Patterns in Go - GoLang Docs
   https://go.dev/doc/effective_go#concurrency
   Learn about goroutines, channels, and common concurrency patterns...

2. Go Concurrency Patterns - Google I/O
   https://talks.golang.org/2012/concurrency.slide
   Rob Pike's classic talk on Go concurrency...
```

---

## web_fetch

Fetch a web page and extract readable text content.

```json
{
  "url": "https://example.com/article"
}
```

**Output:** Extracted text content from the page.

### Configuration

```json
{
  "tools": {
    "web": {
      "useBrowser": "auto",
      "profile": "default",
      "headless": true
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `braveApiKey` | - | Legacy Brave key fallback |
| `search.provider` | `"auto"` | `auto`, `grok`, `brave`, `perplexity`, `gemini` |
| `search.fallbackProviders` | `[]` | Explicit fallback chain override |
| `search.maxFallbackAttempts` | `3` | Max providers to attempt in one request |
| `search.retry.enabled` | `true` | Enable retry logic |
| `search.retry.maxAttemptsPerProvider` | `2` | Retry attempts per provider |
| `search.retry.baseBackoffMs` | `500` | Retry backoff start |
| `search.retry.maxBackoffMs` | `5000` | Retry backoff cap |
| `useBrowser` | `"auto"` | Browser fallback mode for `web_fetch` (`auto`, `always`, `never`) |
| `profile` | `"default"` | Browser profile used for fetch fallback |
| `headless` | `true` | Run browser fallback in headless mode |

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `url` | Yes | URL to fetch |

### Limitations

Some sites block automated requests:
- Cloudflare-protected sites may return errors
- Login-required pages won't work
- Heavy JavaScript sites may have incomplete content

**Fallback options:**
- Use `web_search` for snippets
- Use the [Browser Tool](browser.md) for JavaScript-rendered pages

---

## See Also

- [Browser Tool](browser.md) — Full browser automation
- [Tools](../tools.md) — Tool overview
- [Configuration](../configuration.md) — Full config reference
