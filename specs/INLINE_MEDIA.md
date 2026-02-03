# Inline Media Rendering Specification

## Overview

Enable GoClaw to render media inline within assistant responses across all channels.

## Syntax

Generic media reference:

```
{{media:path/to/file.ext}}
```

Path is relative to the media root. Works for any file type - images, video, audio, documents.

Example:
```
Here's the driveway camera:

{{media:inbound/driveway2.jpg}}

C63 is parked on the left, gate closed.

And here's the voice note you sent:

{{media:inbound/voice_note.mp3}}
```

## Flow

1. Agent writes `{{media:path}}` in response
2. Gateway intercepts before sending to channel
3. Resolves full path from media root
4. Detects mimetype
5. Passes to channel adapter with metadata
6. Channel renders based on mimetype

```go
type MediaRef struct {
    RelPath  string // "inbound/file_1.jpg"
    FullPath string // resolved absolute path
    MimeType string // "image/jpeg"
    Size     int64
}

func (g *Gateway) expandMediaRefs(content string) (string, []MediaRef) {
    pattern := regexp.MustCompile(`\{\{media:([^}]+)\}\}`)
    // Find refs, resolve paths, detect mimetypes
    // Return modified content + metadata for channel adapters
}
```

## Channel Rendering

Each channel renders based on mimetype:

| Mimetype | HTTP | Telegram | TUI |
|----------|------|----------|-----|
| image/* | `<img>` tag | sendPhoto | `[image: path]` |
| video/* | `<video>` tag | sendVideo | `[video: path]` |
| audio/* | `<audio>` tag | sendVoice/Audio | `[audio: path]` |
| application/pdf | `<embed>` or link | sendDocument | `[doc: path]` |
| * (other) | download link | sendDocument | `[file: path]` |

### HTTP Channel

```javascript
function renderMediaRef(path, mimeType) {
    const url = '/media?path=' + encodeURIComponent(path);
    
    if (mimeType.startsWith('image/')) {
        return '<img src="' + url + '" class="chat-media">';
    } else if (mimeType.startsWith('video/')) {
        return '<video src="' + url + '" controls class="chat-media"></video>';
    } else if (mimeType.startsWith('audio/')) {
        return '<audio src="' + url + '" controls></audio>';
    } else {
        return '<a href="' + url + '" download>Download file</a>';
    }
}
```

### Telegram Channel

```go
func (c *TelegramChannel) renderMedia(ref MediaRef, chatID string) {
    switch {
    case strings.HasPrefix(ref.MimeType, "image/"):
        c.bot.SendPhoto(chatID, ref.FullPath)
    case strings.HasPrefix(ref.MimeType, "video/"):
        c.bot.SendVideo(chatID, ref.FullPath)
    case strings.HasPrefix(ref.MimeType, "audio/"):
        c.bot.SendAudio(chatID, ref.FullPath)
    default:
        c.bot.SendDocument(chatID, ref.FullPath)
    }
}
```

### TUI Channel

```go
func (c *TUIChannel) renderMedia(ref MediaRef) string {
    typeLabel := strings.Split(ref.MimeType, "/")[0]
    return fmt.Sprintf("[%s: %s]", typeLabel, ref.RelPath)
}
```

## Backend: Media Endpoint

HTTP channel needs endpoint to serve media files:

```
GET /media?path=inbound/file_1.jpg
```

- Auth required (same as all endpoints)
- Path resolved relative to media root
- Security: validate path doesn't escape media root
- Serve with correct Content-Type header

```go
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
    relPath := r.URL.Query().Get("path")
    
    // Clean and validate
    cleaned := filepath.Clean(relPath)
    if strings.HasPrefix(cleaned, "..") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }
    
    fullPath := filepath.Join(s.mediaRoot, cleaned)
    
    // Verify still within media root after resolution
    if !strings.HasPrefix(fullPath, s.mediaRoot) {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }
    
    http.ServeFile(w, r, fullPath)
}
```

## Message Tool Enhancement

Also enhance message tool for explicit mixed-content sends:

```json
{
  "action": "send",
  "content": [
    {"type": "text", "text": "Here's the driveway:"},
    {"type": "media", "path": "inbound/driveway.jpg"},
    {"type": "text", "text": "C63 on the left."}
  ]
}
```

Use message tool when you need:
- Specific channel/chat targeting
- Explicit delivery confirmation
- Programmatic sends (cron jobs)

Prefer inline `{{media:}}` for conversational flow.

## Agent Instructions

Add to system prompt:

```markdown
## Media References

Reference media files inline with:

    {{media:path/to/file.ext}}

Path is relative to media root. Gateway renders appropriately per channel based on mimetype:
- HTTP: inline player/image
- Telegram: native media message
- TUI: text placeholder

When tools save media (screenshots, captures, etc.), they return the path to use.

Prefer inline media for conversational flow. Use message tool for explicit sends to specific channels/chats.
```

## Security

1. **Path validation**: Clean paths, reject `..` traversal
2. **Media root sandboxing**: Only serve from within media root
3. **Auth required**: /media endpoint uses same auth as chat
4. **Mimetype detection**: Don't trust extensions, detect from content

## Implementation Phases

### Phase 1: Core (MVP)
- Gateway regex to find `{{media:...}}` refs
- Path resolution + mimetype detection
- HTTP /media endpoint
- HTTP frontend rendering (img, video, audio tags)
- Telegram channel media sends

### Phase 2: Polish
- Click to enlarge modal (HTTP)
- Loading states
- Error handling (missing files)
- TUI better formatting

### Phase 3: Message Tool
- Mixed content support in message tool
- Content array with text/media items

## Future: User-Uploaded Images

The HTTP chat currently shows user-pasted images via base64 data URLs. Once this media
system is implemented, a cleaner flow would be:

1. User pastes image in HTTP chat
2. Frontend uploads to `/api/upload` → media manager stores in `inbound/`
3. Returns path like `inbound/paste_1738456789.png`
4. Frontend displays via `/media?path=inbound/paste_1738456789.png`
5. Message to agent includes `{{media:inbound/paste_1738456789.png}}` reference

Benefits:
- No base64 bloat in chat history
- Image persists across page refreshes (currently lost)
- Agent can reference the same image later
- Consistent handling with other inbound media

This would require:
- `POST /api/upload` endpoint (auth required, stores to media manager)
- Frontend change to upload on paste instead of base64 inline

## Out of Scope

- Base64 inline (bloats responses) - current HTTP paste preview is temporary
- External URLs (use browser tool)
- Media directory structure (managed by media manager)
- Persistence categories (future enhancement)
