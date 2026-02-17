# JSONL Store Removal

## Status: Completed

The `JSONLStore` (full read/write storage backend) has been removed. JSONL is now read-only for OpenClaw session inheritance.

## What Was Removed

- `internal/session/jsonl_store.go` - Deleted
- `"jsonl"` case in `NewStore()` - Removed
- `store: "jsonl"` config option - No longer supported

## What Remains

- `JSONLReader` (`jsonl.go`) - Reads OpenClaw session files for inheritance
- `BuildSessionFromRecords()` (`context.go`) - Parses JSONL records into sessions
- Record types (`types.go`) - `MessageRecord`, `CompactionRecord`, etc.

## Current Architecture

```
SQLite (primary storage)
  └── All GoClaw sessions stored here

JSONLReader (read-only)
  └── Reads existing OpenClaw sessions for inheritance
  └── Used by Manager.InheritOpenClawSession()
```

## Configuration

```json
{
  "session": {
    "storePath": "~/.goclaw/sessions.db",
    "path": "~/.openclaw/agents/ratpup/sessions"
  }
}
```

- `storePath` - SQLite database (GoClaw's storage)
- `path` - OpenClaw sessions directory (read-only inheritance)
