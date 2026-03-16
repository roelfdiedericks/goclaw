---
title: "Voice"
description: "Real-time voice conversations with GoClaw"
section: "Channels"
weight: 25
---

# Voice

GoClaw supports real-time voice conversations through the HTTP Voice channel. Talk to your agent naturally using your microphone and hear responses spoken aloud.

## Overview

Voice uses the **xAI Realtime Voice API** to provide:

- **Real-time conversation** — Low-latency voice interaction
- **Natural interruption** — Speak while the agent is talking to interrupt
- **Voice activity detection** — Automatic detection of when you start/stop speaking
- **Tool support** — Agent can use tools during voice conversations

## Prerequisites

1. **xAI API key** with realtime voice access
2. **Modern browser** with microphone support (Chrome, Firefox, Safari, Edge)
3. **HTTPS** — Microphone access requires secure context (localhost works for development)

## Setup

### 1. Configure VoiceLLM

In `goclaw.json`:

```json
{
  "voicellm": {
    "enabled": true,
    "default": "xai",
    "serverVAD": true,
    "idleTimeout": 300,
    "providers": {
      "xai": {
        "driver": "xai",
        "apiKey": "xai-...",
        "voice": "Eve"
      }
    }
  }
}
```

Or use the setup wizard (initial setup includes a voice step):

```bash
goclaw setup        # Initial setup includes voice configuration
goclaw setup edit   # Edit existing config
```

### 2. Enable HTTP Channel

Voice is accessed through the HTTP channel, which must be enabled:

```json
{
  "channels": {
    "http": {
      "enabled": true,
      "port": 1337
    }
  }
}
```

### 3. Access Voice Interface

Open your browser to:

```
http://localhost:1337/voice
```

Click the microphone button and start talking.

## Configuration

### Global Settings

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable voice functionality |
| `default` | - | Default provider name |
| `serverVAD` | `true` | Server-side voice activity detection |
| `idleTimeout` | `300` | Session timeout in seconds |

### Provider Settings

| Field | Default | Description |
|-------|---------|-------------|
| `driver` | - | `xai` |
| `apiKey` | - | xAI API key |
| `voice` | `Eve` | Voice to use |
| `sampleRate` | `48000` | Audio sample rate in Hz |

### Available Voices (xAI)

| Voice | Description |
|-------|-------------|
| `Eve` | Female, energetic (default) |
| `Ara` | Female, warm and friendly |
| `Rex` | Male, confident and clear |
| `Sal` | Neutral, smooth and balanced |
| `Leo` | Male, authoritative |

## Voice Activity Detection

GoClaw supports two VAD modes:

**Server VAD** (`serverVAD: true`, default)
- xAI detects when you start/stop speaking
- Lower latency
- Works best with clear audio

**Client VAD** (`serverVAD: false`)
- Browser detects voice activity
- More control over sensitivity
- Useful if server VAD has issues with your microphone

## Prompt Customization

Customize how the agent responds in voice conversations:

```json
{
  "voicellm": {
    "prompt": {
      "language": "English",
      "maxSentences": 3,
      "pronunciations": {
        "GoClaw": "go-claw",
        "API": "A.P.I."
      },
      "additionalInstructions": "Speak casually and use contractions."
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `language` | - | Preferred response language |
| `maxSentences` | `3` | Keep responses concise for voice |
| `pronunciations` | - | Custom word pronunciations |
| `additionalInstructions` | - | Extra instructions for voice mode |

## How It Works

```
Browser                        GoClaw                       xAI
   │                             │                            │
   │ ◄─── WebSocket ───────────► │                            │
   │     (audio chunks)          │ ◄─── WebSocket ──────────► │
   │                             │     (xAI realtime API)     │
   │                             │                            │
   │  mic audio ────────────────►│ ───────────────────────────►
   │ ◄───────────────────────────│ ◄────────────────────────── 
   │  speaker audio              │         agent audio        │
```

1. Browser captures microphone audio and sends to GoClaw via WebSocket
2. GoClaw forwards audio to xAI's realtime API
3. xAI processes speech, generates response, returns audio
4. GoClaw streams response audio back to browser
5. Browser plays audio through speakers

## Interruption

Voice supports natural interruption:

- Start speaking while the agent is talking
- Agent stops its current response
- Your speech is processed immediately
- Agent responds to your interruption

This creates natural, conversational flow.

## Tools in Voice Mode

The agent can use tools during voice conversations. When a tool is called:

1. Agent announces what it's doing (e.g., "Let me check that...")
2. Tool executes
3. Agent speaks the result

Tool calls may cause brief pauses in the conversation.

## Browser Requirements

| Browser | Support |
|---------|---------|
| Chrome | Full support |
| Firefox | Full support |
| Safari | Full support (macOS/iOS) |
| Edge | Full support |

**Required permissions:**
- Microphone access
- Audio playback (usually automatic)

## Security

- Voice sessions use the same authentication as HTTP chat
- Only authenticated users can access `/voice`
- Audio is streamed directly to xAI — not stored by GoClaw
- Session ends after `idleTimeout` seconds of inactivity

---

## See Also

- [Web UI](web-ui.md) — HTTP channel and chat interface
- [Channels](channels.md) — Channel overview
- [Configuration](configuration.md) — Full config reference
