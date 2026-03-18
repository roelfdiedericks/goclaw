---
title: "xAI Video"
description: "Generate videos using xAI's Grok video generation"
section: "Tools"
weight: 95
---

# xAI Video Tool

Generate videos using xAI's Grok video generation. Supports text-to-video, image-to-video (animate still images), and video editing.

## Features

- **Text-to-video** — Generate videos from text prompts
- **Image-to-video** — Animate still images into videos
- **Video editing** — Modify existing videos with prompts
- **Multiple aspect ratios** — 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3
- **HD resolution** — Up to 720p
- **Configurable duration** — 1-15 seconds
- **Automatic delivery** — Videos saved and delivered via Telegram/channels

## Configuration

```json
{
  "tools": {
    "xaiVideo": {
      "enabled": true,
      "apiKey": "YOUR_XAI_API_KEY",
      "model": "grok-imagine-video",
      "resolution": "480p",
      "duration": 5,
      "saveToMedia": true,
      "pollInterval": 5,
      "timeout": 600
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable the tool |
| `apiKey` | string | - | xAI API key |
| `model` | string | grok-imagine-video | Video generation model |
| `resolution` | string | 480p | Default resolution (480p or 720p) |
| `duration` | int | 5 | Default video duration in seconds (1-15) |
| `saveToMedia` | bool | true | Save videos to media store for delivery |
| `pollInterval` | int | 5 | Seconds between status polls |
| `timeout` | int | 600 | Maximum wait time in seconds (10 minutes) |

## Usage

### Basic Generation

```json
{
  "prompt": "A cat walking across a sunny room"
}
```

### With Parameters

```json
{
  "prompt": "Ocean waves crashing on a beach at sunset",
  "duration": 10,
  "aspectRatio": "16:9",
  "resolution": "720p"
}
```

### Image-to-Video

Animate a still image:

```json
{
  "prompt": "Make the person wave and smile",
  "imageUrl": "https://example.com/photo.jpg"
}
```

The `imageUrl` must be a publicly accessible URL.

### Video Editing

Modify an existing video:

```json
{
  "prompt": "Add a hat to the person",
  "videoUrl": "https://example.com/video.mp4"
}
```

Video editing ignores `duration` and `resolution` parameters — output matches the input video.

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `prompt` | string | **Yes** | Video description with motion and action |
| `duration` | int | No | Length in seconds (1-15) |
| `aspectRatio` | string | No | 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3 |
| `resolution` | string | No | 480p or 720p |
| `imageUrl` | string | No | URL for image-to-video |
| `videoUrl` | string | No | URL for video editing (max 8.7s input) |

## Aspect Ratios

| Ratio | Use Case |
|-------|----------|
| 1:1 | Square — social posts, thumbnails |
| 16:9 | Landscape — YouTube, presentations |
| 9:16 | Portrait — TikTok, Stories, Reels |
| 4:3 | Classic — presentations |
| 3:4 | Portrait classic |
| 3:2 | Photo landscape |
| 2:3 | Photo portrait |

## Response

Returns generated video path:

```json
{
  "video": "./media/generated/video/abc123.mp4",
  "duration": 5,
  "prompt": "A cat walking across a sunny room"
}
```

If saving fails, returns URL instead:

```json
{
  "url": "https://vidgen.x.ai/.../video.mp4",
  "duration": 5,
  "prompt": "A cat walking across a sunny room"
}
```

## Examples

**"Create a 5 second video of a rocket launching"**

Agent calls xai_video, waits for generation (1-5 minutes), video is delivered to your Telegram.

**"Animate this image of a landscape"** (with attached image)

```json
{
  "prompt": "Add gentle movement to the clouds and trees swaying in the wind",
  "imageUrl": "https://uploaded-image-url.jpg"
}
```

**"Make this video look like it's raining"**

```json
{
  "prompt": "Add rain effects with water droplets",
  "videoUrl": "https://uploaded-video-url.mp4"
}
```

## Processing Time

Video generation is asynchronous and typically takes **1-5 minutes** depending on:

- **Duration** — Longer videos take more time
- **Resolution** — 720p takes longer than 480p
- **Prompt complexity** — Detailed scenes need more processing
- **Video editing** — Editing existing videos adds overhead

The tool polls for status every 5 seconds (configurable) and times out after 10 minutes by default.

## Limitations

- Maximum 15 seconds for generation
- Maximum 8.7 seconds input for video editing
- Video editing doesn't support custom duration/resolution
- Subject to xAI content policies
- Video URLs are temporary (saved to media store by default)

## Troubleshooting

### "API key required"

Set `apiKey` in the tool config.

### Timeout Errors

Increase `timeout` in config. Complex videos may take longer than 10 minutes.

### Videos Not Delivered

Check that `saveToMedia` is true (default) and your channel supports video.

### Generation Failed/Expired

The request may have been rejected by content moderation or timed out on xAI's side. Try a different prompt or shorter duration.

---

## See Also

- [xAI Imagine](xai-imagine.md) — Image generation
- [Tools](../tools.md) — Tool overview
