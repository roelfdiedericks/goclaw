# Heartbeat Context Cancellation Fix

## Problem

When heartbeat is triggered manually via `/heartbeat` command (Telegram), the agent request fails immediately with `context canceled`.

```
01:54:22 [INFO] heartbeat: starting
01:54:22 [DEBUG] heartbeat: invoking agent...
01:54:22 [DEBUG] sending request to Anthropic model=claude-opus-4-5
01:54:22 [ERROR] stream error error=context canceled
```

The heartbeat works fine when triggered by the cron timer.

## Root Cause

In `internal/cron/service.go`, `TriggerHeartbeatNow` passes the caller's context to the goroutine:

```go
func (s *Service) TriggerHeartbeatNow(ctx context.Context) error {
    if s.heartbeatConfig == nil || !s.heartbeatConfig.Enabled {
        return fmt.Errorf("heartbeat not enabled")
    }
    go s.runHeartbeat(ctx)  // <-- BUG: uses caller's ctx
    return nil
}
```

When called from Telegram's `/heartbeat` handler:
1. Handler calls `TriggerHeartbeatNow(ctx)`
2. Goroutine starts with that context
3. `TriggerHeartbeatNow` returns immediately
4. Telegram handler finishes → **context is cancelled**
5. Goroutine's API call to Anthropic fails with `context canceled`

When triggered by cron timer (`<-heartbeatC`), it uses the service's long-lived context, so it works.

## Fix

Use `context.Background()` for the heartbeat goroutine since it runs independently of the caller:

```go
func (s *Service) TriggerHeartbeatNow(ctx context.Context) error {
    if s.heartbeatConfig == nil || !s.heartbeatConfig.Enabled {
        return fmt.Errorf("heartbeat not enabled")
    }
    go s.runHeartbeat(context.Background())  // Independent context
    return nil
}
```

Alternatively, if cancellation should be possible (e.g., on shutdown), derive from a service-level context:

```go
func (s *Service) TriggerHeartbeatNow(_ context.Context) error {
    if s.heartbeatConfig == nil || !s.heartbeatConfig.Enabled {
        return fmt.Errorf("heartbeat not enabled")
    }
    // Use service's run context (lives until Stop() is called)
    go s.runHeartbeat(s.runCtx)
    return nil
}
```

## Files to Change

- `internal/cron/service.go` — `TriggerHeartbeatNow` function

## Test

After fix:
1. Run `goclaw gateway`
2. Send `/heartbeat` via Telegram
3. Should see heartbeat complete successfully, not `context canceled`
