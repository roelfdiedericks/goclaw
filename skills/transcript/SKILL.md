---
name: transcript
description: Search conversation history in GoClaw sessions.db - find messages, detect sleep gaps, track chat time.
metadata: { "openclaw": { "emoji": "📜", "requires": { "bins": ["sqlite3"] } } }
---

# Transcript Search Skill

Search through conversation history stored in the GoClaw sessions database.

## When to Use

- Finding when something was discussed
- Checking when the user last messaged
- Calculating time gaps (e.g., "how long did I sleep?")
- Finding specific conversations by keyword
- Reviewing what was decided/discussed on a topic

## Database Location

```
/home/openclaw/.openclaw/goclaw/sessions.db
```

## Schema Overview

**messages** table:
- `id` - unique message ID
- `session_key` - which session this belongs to
- `timestamp` - Unix timestamp (seconds)
- `role` - 'user', 'assistant', or 'tool'
- `content` - the message text
- `tool_name` - if role='tool', which tool was called
- `source` - origin (e.g., 'telegram', 'tui')
- `channel_id`, `user_id` - channel metadata

**sessions** table:
- `key` - session identifier
- `created_at`, `updated_at` - timestamps
- `model`, `thinking_level` - config

## Common Queries

### Last N user messages (excluding heartbeats)
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
SELECT datetime(timestamp, 'unixepoch', '+2 hours') as sast, 
       substr(content, 1, 100) as preview 
FROM messages 
WHERE role='user' 
  AND content NOT LIKE '%HEARTBEAT%' 
  AND content NOT LIKE '%heartbeat%'
  AND content NOT LIKE '%Memory checkpoint%'
ORDER BY timestamp DESC 
LIMIT 10;"
```

### Search by keyword
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
SELECT datetime(timestamp, 'unixepoch', '+2 hours') as sast,
       role,
       substr(content, 1, 150) as preview
FROM messages 
WHERE content LIKE '%KEYWORD%'
ORDER BY timestamp DESC 
LIMIT 20;"
```

### Messages in a time range (SAST)
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
SELECT datetime(timestamp, 'unixepoch', '+2 hours') as sast,
       role,
       substr(content, 1, 100) as preview
FROM messages 
WHERE timestamp BETWEEN strftime('%s', '2026-02-03 00:00:00', '-2 hours') 
                    AND strftime('%s', '2026-02-03 12:00:00', '-2 hours')
  AND role='user'
ORDER BY timestamp;"
```

### Time since last real user message
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
SELECT 
  datetime(timestamp, 'unixepoch', '+2 hours') as last_msg_sast,
  (strftime('%s','now') - timestamp) / 60 as minutes_ago,
  (strftime('%s','now') - timestamp) / 3600 as hours_ago,
  substr(content, 1, 80) as preview
FROM messages 
WHERE role='user' 
  AND content NOT LIKE '%HEARTBEAT%'
  AND content NOT LIKE '%heartbeat%'
  AND content NOT LIKE '%Memory checkpoint%'
ORDER BY timestamp DESC 
LIMIT 1;"
```

### Gap between two messages (sleep detection)
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
WITH user_msgs AS (
  SELECT timestamp, content,
         LAG(timestamp) OVER (ORDER BY timestamp) as prev_timestamp
  FROM messages 
  WHERE role='user' 
    AND content NOT LIKE '%HEARTBEAT%'
    AND content NOT LIKE '%heartbeat%'
    AND content NOT LIKE '%Memory checkpoint%'
)
SELECT 
  datetime(prev_timestamp, 'unixepoch', '+2 hours') as from_time,
  datetime(timestamp, 'unixepoch', '+2 hours') as to_time,
  (timestamp - prev_timestamp) / 3600.0 as hours_gap,
  substr(content, 1, 60) as woke_up_with
FROM user_msgs
WHERE prev_timestamp IS NOT NULL
ORDER BY timestamp DESC
LIMIT 10;"
```

### Count messages by day
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
SELECT date(timestamp, 'unixepoch', '+2 hours') as day,
       COUNT(*) as total,
       SUM(CASE WHEN role='user' THEN 1 ELSE 0 END) as user_msgs,
       SUM(CASE WHEN role='assistant' THEN 1 ELSE 0 END) as assistant_msgs
FROM messages
GROUP BY day
ORDER BY day DESC
LIMIT 14;"
```

### Full message by ID or content match
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
SELECT datetime(timestamp, 'unixepoch', '+2 hours') as sast,
       role,
       content
FROM messages 
WHERE content LIKE '%specific phrase%'
ORDER BY timestamp DESC 
LIMIT 1;"
```

## Tips

1. **Timezone**: Database stores UTC timestamps. Add `'+2 hours'` for SAST.

2. **Filtering noise**: Exclude heartbeats and memory checkpoints for "real" conversations:
   ```sql
   AND content NOT LIKE '%HEARTBEAT%'
   AND content NOT LIKE '%Memory checkpoint%'
   ```

3. **Performance**: The `messages` table has indexes on `(session_key, timestamp)` and `(session_key, role)`.

4. **Full content**: Use `content` not `substr(content, ...)` when you need the full message.

5. **Session isolation**: Add `AND session_key = 'xxx'` to limit to a specific session.

## Example Workflow: "How much sleep did I have?"

1. Find the last real user message before a gap:
```bash
sqlite3 /home/openclaw/.openclaw/goclaw/sessions.db "
WITH user_msgs AS (
  SELECT timestamp, content,
         LEAD(timestamp) OVER (ORDER BY timestamp) as next_timestamp
  FROM messages 
  WHERE role='user' 
    AND content NOT LIKE '%HEARTBEAT%'
    AND content NOT LIKE '%Memory checkpoint%'
)
SELECT 
  datetime(timestamp, 'unixepoch', '+2 hours') as went_to_bed,
  datetime(next_timestamp, 'unixepoch', '+2 hours') as woke_up,
  ROUND((next_timestamp - timestamp) / 3600.0, 1) as hours_sleep
FROM user_msgs
WHERE (next_timestamp - timestamp) > 3600  -- gaps > 1 hour
ORDER BY timestamp DESC
LIMIT 5;"
```

This shows recent gaps in conversation that likely represent sleep or away time.
