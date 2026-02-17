# xAI Image Handling Issue

## Problem

When sending images to GoClaw via Telegram with xAI/Grok as the provider, the model does not analyze the image content. Instead, it responds as if no image was sent, or tries to use tools like `xai_imagine` or `write` instead of actually seeing the image.

With Anthropic/Opus, image analysis works perfectly - the model describes what's in the image.

## What We Know

### xAI Models DO Support Vision

According to xAI's official docs (https://docs.x.ai/docs/models):

| Model | Modalities | Capabilities |
|-------|-----------|--------------|
| grok-4-fast-non-reasoning | text, image → text | functions, structured |
| grok-4-fast-reasoning | text, image → text | functions, structured, reasoning |
| grok-4-1-fast-non-reasoning | text, image → text | functions, structured |
| grok-2-vision | text, image → text | functions, structured |

**All grok-4 models support both vision AND tools (functions).**

### xai-go Library Supports Images

The `xai-go` library (v0.4.0) has proper image support:

```go
// In chat_request.go
type UserContent struct {
    Text     string
    ImageURL string // optional - for vision models
}

// UserMessage adds image when ImageURL is set
func (r *ChatRequest) UserMessage(content UserContent) *ChatRequest {
    // ...
    if content.ImageURL != "" {
        msg.Content = append(msg.Content, &v1.Content{
            Content: &v1.Content_ImageUrl{ImageUrl: &v1.ImageUrlContent{ImageUrl: content.ImageURL}},
        })
    }
    // ...
}
```

### GoClaw's xai.go Implementation

In `internal/llm/xai.go`, the `addMessageToRequest()` function handles images:

```go
case "user":
    content := xai.UserContent{Text: msg.Content}
    // Add first image if present (xai-go UserContent only supports one image)
    // Convert base64 data to data URL format
    if len(msg.Images) > 0 && msg.Images[0].Data != "" {
        content.ImageURL = "data:" + msg.Images[0].MimeType + ";base64," + msg.Images[0].Data
        L_trace("xai: user message with image", "mimeType", msg.Images[0].MimeType)
    }
    req.UserMessage(content)
```

This looks correct - it creates a data URL from the base64 image data.

### Anthropic's Implementation (Works)

In `internal/llm/anthropic.go`:

```go
for _, img := range msg.Images {
    imageBlock := anthropic.NewImageBlockBase64(img.MimeType, img.Data)
    contentBlocks = append(contentBlocks, imageBlock)
    L_trace("added image block to message", "mimeType", img.MimeType, "source", img.Source)
}
```

### Message Flow (Images Should Reach Provider)

1. **Telegram** receives photo → downloads → optimizes → creates `ImageAttachment`
2. **Gateway** receives request with `Images` slice → calls `sess.AddUserMessageWithImages()`
3. **Session** stores message with `Images` field
4. **Provider** (xai.go) receives messages → should attach images to request

Relevant code paths:
- `internal/gateway/gateway.go:1074-1080` - Images copied to request
- `internal/gateway/gateway.go:1396-1397` - AddUserMessageWithImages called
- `internal/session/session.go:158-171` - AddUserMessageWithImages implementation

## What We Haven't Verified

1. **Are images actually present in `msg.Images` when `addMessageToRequest()` is called?**
   - The logging is at TRACE level, so we can't see it with DEBUG
   - Need to add DEBUG logging to confirm

2. **Is the data URL format correct for xAI?**
   - Format used: `data:image/jpeg;base64,<base64data>`
   - xAI docs show: `"url": "data:image/jpeg;base64, <base64>"` (note the space after comma)
   - Might need to verify exact format expected

3. **Is there an issue with how images are retrieved from session storage?**
   - Images might be stored but not loaded back when building message history

## Logs from Failed Attempt

```
2026/02/15 03:10:12 DEBU <telegram/bot.go:394> telegram: downloading photo fileID=... width=305 height=288
2026/02/15 03:10:12 DEBU <telegram/bot.go:401> telegram: photo optimized originalSize=24098 optimizedSize=24098 dimensions=305x288
2026/02/15 03:10:12 DEBU <gateway/gateway.go:1395> RunAgent: adding user message session=primary source=telegram msgLen=21
...
2026/02/15 03:10:12 DEBU <gateway/gateway.go:1574> invoking LLM provider=xai model=grok-4-fast-non-reasoning messages=139 tools=17
```

Note: No log showing "xai: user message with image" (it's at TRACE level).

## Other Issues Found During Investigation

### 1. `storeResponses` Config Not Being Read

The debug log shows:
```
xai: store mode config configValue=<nil> storeMode=true
```

Even though `goclaw.json` has `"storeResponses": false`, the value is nil. This is a separate bug - the `*bool` field isn't being parsed correctly from JSON.

### 2. Incremental Mode Disabled

We commented out incremental mode in xai.go for testing:
```go
// DISABLED: incremental mode - always send full history for testing
// if storeMode && p.responseID != "" ...
usedPreviousResponseId := false
```

### 3. grok-2-vision Doesn't Support Server Tools

Error when trying grok-2-vision:
```
the model grok-2-vision is not supported when using server-side tools, only the grok-4 family of models are supported
```

This is about xAI's server-side tools (web_search, x_search, etc.), not function calling.

## Next Steps

1. **Add DEBUG logging** in `xai.go` `addMessageToRequest()` to show:
   - Whether `msg.Images` has any items
   - The length of image data
   - The mime type

2. **Check session image loading** - Verify images are loaded from DB when rebuilding message history

3. **Verify data URL format** - Check if xAI expects a specific format (with/without space after comma)

4. **Test with a simple standalone script** - Call xai-go directly with an image to verify the library works

## Current Config

```json
"xai": {
  "type": "xai",
  "apiKey": "...",
  "serverToolsAllowed": [],
  "storeResponses": false
}

"agent": {
  "models": ["xai/grok-2-vision", ...]  // Currently set to grok-2-vision but should use grok-4
}
```

**Recommended model:** `xai/grok-4-fast-non-reasoning` (has both vision and tools)

## Files to Investigate

- `internal/llm/xai.go` - Provider implementation, `addMessageToRequest()` function
- `internal/session/sqlite_store.go` - How images are stored/loaded from DB
- `internal/session/types.go` - Message and ImageAttachment types
- `internal/gateway/gateway.go` - How images flow from request to session
- `internal/telegram/bot.go` - How images are received and attached
