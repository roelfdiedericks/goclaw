# xAI Imagine Tool

Generate images using xAI's Grok image generation.

## Usage

```json
{
  "prompt": "A futuristic city at sunset with flying cars"
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `prompt` | Yes | Image description |

## Configuration

Requires an xAI provider configured:

```json
{
  "llm": {
    "providers": {
      "xai": {
        "type": "xai",
        "apiKey": "YOUR_XAI_API_KEY"
      }
    }
  }
}
```

## Response

Returns URLs to generated images:

```json
{
  "images": [
    {
      "url": "https://...",
      "revisedPrompt": "A detailed futuristic cityscape..."
    }
  ]
}
```

## Use Cases

- Create illustrations for documents
- Generate diagrams and visualizations
- Create artwork and concept images
- Produce thumbnails and icons

## Limitations

- Subject to xAI content policies
- Image URLs are temporary
- Quality depends on prompt clarity

---

## See Also

- [xAI Provider](../providers/xai.md) — xAI configuration
- [Tools](../tools.md) — Tool overview
