# MESSAGE_TOOL.md — Channel Messaging Tool

## Overview

The `message` tool allows the agent to proactively send messages to channels (Telegram, etc.) rather than just replying in-context. Critical for sending media files captured by other tools (browser screenshots, camera snapshots, etc.).

---

## Why It's Needed

**Problem observed:** GoClaw took a browser screenshot but couldn't send it to Telegram. It:
1. Described the screenshot contents instead of sending it
2. Invented a non-existent `sendToChannel: true` flag on browser tool
3. Didn't know browser + message are separate tools

**The pattern:** Tools that capture (browser, camera, exec) save files to disk. The message tool sends those files to channels.

```
browser(screenshot) → /path/to/file.jpg → message(send, filePath) → Telegram
```

---

## Tool Schema

```go
type MessageToolParams struct {
    // Required
    Action  string `json:"action"`  // "send", "edit", "delete", "react"
    
    // For send/edit
    Channel string `json:"channel,omitempty"` // "telegram" (default from session)
    To      string `json:"to,omitempty"`      // Chat/user ID
    Message string `json:"message,omitempty"` // Text content
    
    // For media
    FilePath string `json:"filePath,omitempty"` // Path to file on disk
    Media    string `json:"media,omitempty"`    // URL or MEDIA:/path
    Caption  string `json:"caption,omitempty"`  // Caption for media
    
    // For edit/delete/react
    MessageId string `json:"messageId,omitempty"` // Target message ID
    Emoji     string `json:"emoji,omitempty"`     // For reactions
}
```

---

## Actions

### send

Send a message or media to a channel.

**Text message:**
```json
{
  "action": "send",
  "channel": "telegram",
  "to": "123456789",
  "message": "Hello from GoClaw!"
}
```

**Media with caption:**
```json
{
  "action": "send",
  "channel": "telegram",
  "to": "123456789",
  "filePath": "/home/openclaw/.openclaw/media/browser/screenshot.jpg",
  "caption": "News24 frontpage"
}
```

**Media from URL:**
```json
{
  "action": "send",
  "channel": "telegram", 
  "to": "123456789",
  "media": "https://example.com/image.jpg",
  "caption": "Image from web"
}
```

### edit

Edit a previously sent message.

```json
{
  "action": "edit",
  "channel": "telegram",
  "messageId": "12345",
  "message": "Updated text"
}
```

### delete

Delete a message.

```json
{
  "action": "delete",
  "channel": "telegram",
  "messageId": "12345"
}
```

### react

Add a reaction to a message.

```json
{
  "action": "react",
  "channel": "telegram",
  "messageId": "12345",
  "emoji": "👍"
}
```

---

## Implementation

### File Structure

```
internal/tools/
├── message.go       # Message tool implementation
└── message_test.go  # Tests
```

### Core Implementation

```go
// internal/tools/message.go

package tools

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    
    tele "gopkg.in/telebot.v4"
)

type MessageTool struct {
    bot      *tele.Bot
    registry *Registry
}

type MessageParams struct {
    Action    string `json:"action"`
    Channel   string `json:"channel,omitempty"`
    To        string `json:"to,omitempty"`
    Message   string `json:"message,omitempty"`
    FilePath  string `json:"filePath,omitempty"`
    Media     string `json:"media,omitempty"`
    Caption   string `json:"caption,omitempty"`
    MessageId string `json:"messageId,omitempty"`
    Emoji     string `json:"emoji,omitempty"`
}

func (t *MessageTool) Name() string {
    return "message"
}

func (t *MessageTool) Description() string {
    return "Send, edit, delete, and manage messages via channel plugins."
}

func (t *MessageTool) Schema() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "action": map[string]any{
                "type": "string",
                "enum": []string{"send", "edit", "delete", "react"},
                "description": "Action to perform",
            },
            "channel": map[string]any{
                "type": "string",
                "description": "Channel name (telegram). Defaults to current session channel.",
            },
            "to": map[string]any{
                "type": "string",
                "description": "Target chat/user ID",
            },
            "message": map[string]any{
                "type": "string",
                "description": "Text message content",
            },
            "filePath": map[string]any{
                "type": "string",
                "description": "Path to local file to send as media",
            },
            "media": map[string]any{
                "type": "string",
                "description": "URL or MEDIA:/path for media",
            },
            "caption": map[string]any{
                "type": "string",
                "description": "Caption for media",
            },
            "messageId": map[string]any{
                "type": "string",
                "description": "Message ID for edit/delete/react",
            },
            "emoji": map[string]any{
                "type": "string",
                "description": "Emoji for reactions",
            },
        },
        "required": []string{"action"},
    }
}

func (t *MessageTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    var params MessageParams
    if err := json.Unmarshal(input, &params); err != nil {
        return "", fmt.Errorf("invalid params: %w", err)
    }
    
    switch params.Action {
    case "send":
        return t.send(ctx, params)
    case "edit":
        return t.edit(ctx, params)
    case "delete":
        return t.delete(ctx, params)
    case "react":
        return t.react(ctx, params)
    default:
        return "", fmt.Errorf("unknown action: %s", params.Action)
    }
}

func (t *MessageTool) send(ctx context.Context, params MessageParams) (string, error) {
    chatID, err := strconv.ParseInt(params.To, 10, 64)
    if err != nil {
        return "", fmt.Errorf("invalid chat ID: %w", err)
    }
    
    chat := &tele.Chat{ID: chatID}
    
    // Send media if filePath or media provided
    if params.FilePath != "" {
        // Check file exists
        if _, err := os.Stat(params.FilePath); err != nil {
            return "", fmt.Errorf("file not found: %s", params.FilePath)
        }
        
        photo := &tele.Photo{
            File:    tele.FromDisk(params.FilePath),
            Caption: params.Caption,
        }
        
        msg, err := t.bot.Send(chat, photo)
        if err != nil {
            return "", fmt.Errorf("failed to send photo: %w", err)
        }
        
        return fmt.Sprintf(`{"ok":true,"messageId":"%d","chatId":"%d"}`, msg.ID, chatID), nil
    }
    
    // Send text message
    if params.Message != "" {
        msg, err := t.bot.Send(chat, params.Message)
        if err != nil {
            return "", fmt.Errorf("failed to send message: %w", err)
        }
        
        return fmt.Sprintf(`{"ok":true,"messageId":"%d","chatId":"%d"}`, msg.ID, chatID), nil
    }
    
    return "", fmt.Errorf("no message or media to send")
}

// edit, delete, react implementations...
```

---

## System Prompt Guidance

Add to GoClaw's system prompt (or tool description):

```
## message tool

Use `message` to send text or media to channels proactively.

**Important:** Other tools (browser, camera) save files to disk. 
Use `message` with `filePath` to send those files to Telegram.

Example workflow - sending a screenshot:
1. browser(action=screenshot) → saves to /path/to/file.jpg
2. message(action=send, to=chatId, filePath=/path/to/file.jpg, caption="Screenshot")

Do NOT assume browser/camera tools send to channels directly.
```

---

## Media Type Detection

```go
func detectMediaType(path string) string {
    ext := strings.ToLower(filepath.Ext(path))
    switch ext {
    case ".jpg", ".jpeg", ".png", ".gif", ".webp":
        return "photo"
    case ".mp4", ".mov", ".avi", ".webm":
        return "video"
    case ".mp3", ".ogg", ".wav", ".m4a":
        return "audio"
    case ".pdf", ".doc", ".docx", ".txt":
        return "document"
    default:
        return "document"
    }
}
```

Use this to send appropriate Telegram media type:
- Photos: `bot.Send(chat, &tele.Photo{...})`
- Videos: `bot.Send(chat, &tele.Video{...})`
- Audio: `bot.Send(chat, &tele.Audio{...})`
- Documents: `bot.Send(chat, &tele.Document{...})`

---

## Session Context Integration

The message tool needs access to:

1. **Current session's channel** — Default `channel` if not specified
2. **Current chat ID** — Default `to` if not specified  
3. **Bot instance** — For sending messages

```go
type MessageTool struct {
    bot        *tele.Bot
    getSession func() *Session  // Get current session for defaults
}

func (t *MessageTool) send(ctx context.Context, params MessageParams) (string, error) {
    // Default to current session's chat
    if params.To == "" {
        sess := t.getSession()
        if sess != nil {
            params.To = sess.ChatID
        }
    }
    // ...
}
```

---

## Response Format

Successful send:
```json
{
  "ok": true,
  "messageId": "12345",
  "chatId": "123456789"
}
```

Error:
```json
{
  "ok": false,
  "error": "file not found: /path/to/file.jpg"
}
```

---

## Testing

```go
func TestMessageTool_SendPhoto(t *testing.T) {
    // Create temp image file
    tmpFile, _ := os.CreateTemp("", "test-*.jpg")
    defer os.Remove(tmpFile.Name())
    
    // Mock bot
    mockBot := &MockBot{}
    
    tool := &MessageTool{bot: mockBot}
    
    result, err := tool.Execute(ctx, json.RawMessage(`{
        "action": "send",
        "to": "12345",
        "filePath": "`+tmpFile.Name()+`",
        "caption": "Test image"
    }`))
    
    assert.NoError(t, err)
    assert.Contains(t, result, `"ok":true`)
}
```

---

## Priority

**High** — Without this tool, GoClaw cannot:
- Send browser screenshots
- Send camera captures  
- Send any media proactively
- Send messages to channels other than the current one

This is a core capability gap.

---

## Related

- `browser` tool — Captures screenshots (needs message tool to send them)
- `tts` tool — Generates audio files (needs message tool to send them)
- SESSION_CONTEXT.md — Context management
- SESSION_PERSISTENCE.md — Session storage
