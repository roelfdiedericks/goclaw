# Troubleshooting

Common issues and solutions for GoClaw.

## Quick Diagnostics

```bash
# Run with debug logging
make debug

# Check configuration
cat goclaw.json | jq .

# Check users
cat users.json | jq .

# Check Ollama
curl http://localhost:11434/api/tags

# Check SQLite database
sqlite3 ~/.goclaw/sessions.db ".tables"
```

---

## Startup Issues

### "failed to load config"

**Symptoms:** GoClaw exits immediately with config error

**Solutions:**
1. Check `goclaw.json` exists in working directory
2. Validate JSON syntax: `cat goclaw.json | jq .`
3. Check file permissions

### "failed to create LLM client"

**Symptoms:** Error about Anthropic client

**Solutions:**
1. Check API key is set:
   ```bash
   echo $ANTHROPIC_API_KEY
   ```
2. Or set in config:
   ```json
   {"llm": {"apiKey": "sk-ant-..."}}
   ```
3. Verify API key is valid (not expired/revoked)

### "failed to create session manager"

**Symptoms:** Database initialization error

**Solutions:**
1. Check SQLite path is writable:
   ```bash
   touch ~/.goclaw/sessions.db
   ```
2. Check disk space
3. Try removing corrupted database:
   ```bash
   rm ~/.goclaw/sessions.db
   ```

---

## Telegram Issues

### Bot Not Responding

**Symptoms:** Messages sent to bot, no response

**Checklist:**
1. Is Telegram enabled?
   ```json
   {"telegram": {"enabled": true}}
   ```
2. Is bot token correct?
3. Is user authorized in `users.json`?
4. Check logs for errors:
   ```bash
   make debug 2>&1 | grep -i telegram
   ```

### "unauthorized user"

**Symptoms:** Bot ignores messages from user

**Solution:** Add user to `users.json`:
```json
{
  "users": [
    {
      "name": "TheRoDent",
      "role": "owner",
      "identities": [
        {"provider": "telegram", "id": "YOUR_USER_ID"}
      ]
    }
  ]
}
```

Find your user ID: message [@userinfobot](https://t.me/userinfobot)

### Images Not Processing

**Symptoms:** Bot receives image but doesn't process it

**Solutions:**
1. Check media directory:
   ```bash
   ls -la ~/.goclaw/media/
   ```
2. Check file size (default max: 5MB)
3. Check supported format (JPEG, PNG, GIF, WebP)

---

## Ollama Issues

### "Ollama not available"

**Symptoms:** Compaction/checkpoints fail, fallback to Anthropic

**Solutions:**
1. Check Ollama is running:
   ```bash
   curl http://localhost:11434/api/tags
   ```
2. Start Ollama:
   ```bash
   ollama serve
   ```
3. Check URL in config matches Ollama address

### "context deadline exceeded"

**Symptoms:** Ollama requests timeout

**Solutions:**
1. Increase timeout:
   ```json
   {"session": {"summarization": {"ollama": {"timeoutSeconds": 600}}}}
   ```
2. Use smaller model (`qwen2.5:7b` instead of `14b`)
3. Check Ollama server resources (CPU/GPU/memory)

### "truncating input prompt"

**Symptoms:** Ollama truncates context

**Solutions:**
1. Set explicit context size:
   ```json
   {"session": {"summarization": {"ollama": {"contextTokens": 131072}}}}
   ```
2. Use model with larger context
3. Check model's actual context limit:
   ```bash
   ollama show qwen2.5:7b
   ```

### Repeated "hash changed" Logs

**Symptoms:** Prompt cache constantly invalidating

**Solution:** This was a bug, fixed in recent versions. Update GoClaw.

---

## Context/Compaction Issues

### "prompt is too long"

**Symptoms:** LLM rejects request due to token limit

**Solutions:**
1. Lower reserve tokens (triggers compaction earlier):
   ```json
   {"session": {"summarization": {"compaction": {"reserveTokens": 40000}}}}
   ```
2. Force compaction: `/compact` in Telegram
3. Clear session: `/clear` in Telegram

### "compaction failed: session too short"

**Symptoms:** Manual compaction fails

**Cause:** Not enough messages to compact

**Solution:** This is normal for short sessions. Wait for more conversation.

### Emergency Truncation Happening

**Symptoms:** "compaction failed, truncating session memory" messages

**Causes:**
1. Ollama unavailable
2. Anthropic fallback also failed
3. No checkpoint available

**Solutions:**
1. Check Ollama is running
2. Check Anthropic API key valid
3. Enable checkpoints for better recovery
4. Background retry will attempt to regenerate summary

---

## Memory Search Issues

### "no results found"

**Symptoms:** Memory search returns empty

**Solutions:**
1. Check files exist:
   ```bash
   ls memory/
   cat MEMORY.md
   ```
2. Lower minimum score:
   ```json
   {"memorySearch": {"query": {"minScore": 0.2}}}
   ```
3. Check embedding model is loaded:
   ```bash
   ollama list
   ```

### Slow Search

**Symptoms:** Memory search takes >1 second

**Solutions:**
1. Reduce indexed paths
2. Use faster embedding model (`all-minilm`)
3. Increase `minScore` to reduce results

---

## Tool Errors

### "path traversal detected"

**Symptoms:** File operations rejected

**Cause:** Path contains `../` attempting to escape workspace

**Solution:** Use absolute paths within workspace or relative paths without `../`

### "old_string is not unique"

**Symptoms:** Edit tool fails

**Cause:** The text to replace appears multiple times

**Solution:** Include more context in `old_string` to make it unique

### "command timed out"

**Symptoms:** Exec tool fails after delay

**Solutions:**
1. Increase timeout:
   ```json
   {"command": "long-running-cmd", "timeout": 120}
   ```
2. Run command in background
3. Check if command is actually hanging

---

## Performance Issues

### High Memory Usage

**Causes:**
- Large session history
- Many indexed memory files
- Large workspace files cached

**Solutions:**
1. Compact session: `/compact`
2. Reduce memory search paths
3. Lower prompt cache poll interval

### Slow Responses

**Causes:**
- LLM latency
- Ollama on CPU (slow)
- Large context window

**Solutions:**
1. Use faster model
2. Enable GPU for Ollama
3. Compact more aggressively (lower `reserveTokens`)

---

## Database Issues

### Corrupted Database

**Symptoms:** SQLite errors, "database is locked", "malformed"

**Solutions:**
1. Stop GoClaw
2. Try integrity check:
   ```bash
   sqlite3 ~/.goclaw/sessions.db "PRAGMA integrity_check"
   ```
3. If corrupted, backup and recreate:
   ```bash
   mv ~/.goclaw/sessions.db ~/.goclaw/sessions.db.bak
   # GoClaw will create new database on start
   ```

### "database is locked"

**Symptoms:** Database operations fail

**Causes:**
- Multiple GoClaw instances
- Unclosed database connections

**Solutions:**
1. Check for multiple processes:
   ```bash
   pgrep -f goclaw
   ```
2. Kill duplicate processes
3. Restart GoClaw

---

## Logging

### Enable Debug Logging

```bash
# Via make
make debug

# Via flags
./bin/goclaw gateway -d   # Debug logging
./bin/goclaw gateway -t   # Trace logging (very verbose)
```

### Log Levels

| Level | Use |
|-------|-----|
| `trace` | Very verbose (cache hits, token counts) |
| `debug` | Development details |
| `info` | Normal operation |
| `warn` | Potential issues |
| `error` | Errors only |

### Filter Logs

```bash
# Only errors
make debug 2>&1 | grep -E "ERRO|error"

# Specific component
make debug 2>&1 | grep compaction

# Exclude noise
make debug 2>&1 | grep -v "trace\|TRAC"
```

---

## Getting Help

### Information to Include

When reporting issues, include:

1. GoClaw version: `./bin/goclaw --version`
2. Go version: `go version`
3. OS: `uname -a`
4. Relevant config (redact secrets)
5. Error messages (full log output)
6. Steps to reproduce

### Debug Checklist

- [ ] Running latest version?
- [ ] Config file valid JSON?
- [ ] API keys set and valid?
- [ ] Users file has your user ID?
- [ ] Ollama running (if configured)?
- [ ] Disk space available?
- [ ] No duplicate processes?

---

## See Also

- [Configuration](./configuration.md) - Config reference
- [Deployment](./deployment.md) - Production setup
- [Architecture](./architecture.md) - System internals
