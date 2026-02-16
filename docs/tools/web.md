---
title: "Web Tools"
description: "Search the web and fetch page content"
section: "Tools"
weight: 50
---

# Web Tools

Search the web and fetch page content.

## web_search

Search the web using Brave Search API.

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
      "braveApiKey": "YOUR_BRAVE_API_KEY"
    }
  }
}
```

Get an API key at [brave.com/search/api](https://brave.com/search/api/).

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `query` | Yes | Search query |

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
      "useJina": false
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `braveApiKey` | - | Brave Search API key |
| `useJina` | false | Use Jina for content extraction |

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
