---
title: "xAI Imagine"
description: "Generate images using xAI's Grok image generation"
section: "Tools"
weight: 90
---

# xAI Imagine Tool

Generate images using xAI's Grok image generation. Supports text-to-image and image-to-image transformation.

## Features

- **Text-to-image** — Generate images from text prompts
- **Image-to-image** — Transform existing images with prompts
- **Multiple aspect ratios** — 1:1, 16:9, 9:16, 4:3, 3:4
- **High resolution** — Up to 2K (~2048px)
- **Batch generation** — Up to 4 images per request
- **Automatic delivery** — Images saved and delivered via Telegram/channels

## Configuration

```json
{
  "tools": {
    "xai_imagine": {
      "enabled": true,
      "apiKey": "YOUR_XAI_API_KEY",
      "model": "grok-2-image",
      "resolution": "1K",
      "saveToMedia": true
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable the tool |
| `apiKey` | string | - | xAI API key (can share with provider) |
| `model` | string | grok-2-image | Image generation model |
| `resolution` | string | 1K | Default resolution (1K or 2K) |
| `saveToMedia` | bool | true | Save images to media store for delivery |

## Usage

### Basic Generation

```json
{
  "prompt": "A futuristic city at sunset with flying cars"
}
```

### With Parameters

```json
{
  "prompt": "A serene mountain landscape",
  "aspectRatio": "16:9",
  "resolution": "2K",
  "count": 2
}
```

### Image-to-Image

Transform an existing image:

```json
{
  "prompt": "Make this image look like a watercolor painting",
  "inputImage": "https://example.com/photo.jpg"
}
```

The `inputImage` must be a publicly accessible URL.

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `prompt` | string | **Yes** | Image description |
| `model` | string | No | Model override |
| `aspectRatio` | string | No | 1:1, 16:9, 9:16, 4:3, 3:4 |
| `resolution` | string | No | 1K (~1024px) or 2K (~2048px) |
| `count` | int | No | Number of images (1-4) |
| `saveToMedia` | bool | No | Save to media store |
| `inputImage` | string | No | URL for image-to-image |

## Aspect Ratios

| Ratio | Use Case |
|-------|----------|
| 1:1 | Square — avatars, icons, social posts |
| 16:9 | Landscape — desktop wallpapers, presentations |
| 9:16 | Portrait — phone wallpapers, stories |
| 4:3 | Classic — photos, documents |
| 3:4 | Portrait classic — print photos |

## Response

Returns generated image URLs:

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

The `revisedPrompt` shows how xAI interpreted your prompt.

## Examples

**"Draw a cute robot reading a book"**

Agent calls xai_imagine, image is generated and delivered to your Telegram.

**"Generate a 16:9 banner image for my blog post about AI"**

```json
{
  "prompt": "Minimalist banner illustration of neural network patterns, blue gradient, modern tech aesthetic",
  "aspectRatio": "16:9",
  "resolution": "2K"
}
```

**"Turn this photo into pixel art"** (with attached image)

```json
{
  "prompt": "Convert to 16-bit pixel art style, retro gaming aesthetic",
  "inputImage": "https://uploaded-image-url.jpg"
}
```

## Limitations

- Subject to xAI content policies
- Image URLs are temporary (saved to media store by default)
- Maximum 4 images per request
- Image-to-image requires publicly accessible source URL

## Troubleshooting

### No Images Delivered

Check that `saveToMedia` is true (default) and your channel supports media.

### "API key required"

Set `apiKey` in the tool config or share from the xAI provider.

### Low Quality Results

- Be more descriptive in prompts
- Use 2K resolution for higher quality
- Specify style explicitly (e.g., "photorealistic", "digital art", "oil painting")

---

## See Also

- [xAI Provider](../providers/xai.md) — xAI configuration
- [Tools](../tools.md) — Tool overview
