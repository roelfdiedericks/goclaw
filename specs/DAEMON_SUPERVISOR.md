# Daemon Supervisor Spec

## Problem

When `goclaw start` daemonizes the gateway, if the child process crashes, it stays dead. Users only discover this when they notice the bot isn't responding — often hours later.

As one user noted: "The healthcheck cron I had OpenClaw write itself was a joke — can't notify you if it's dead!"

## Current Behavior

```
goclaw start
    └── daemon.Reborn() forks child
    └── parent exits (reports PID)
    └── child runs runGateway() directly
    └── if child crashes → dead forever
```

## Proposed Behavior

The daemon child becomes a supervisor that spawns gateway subprocesses. Each gateway run is a fresh OS process — crashes get clean OS-level teardown with no resource leak accumulation.

```
goclaw start
    └── daemon.Reborn() forks supervisor
    └── parent exits (reports supervisor PID)
    └── supervisor loop:
        └── exec: goclaw gateway
        └── wait for subprocess exit
        └── if crash → log to crash.log → backoff → respawn
        └── if clean exit (exit 0) → supervisor exits
```

**Why subprocess:** Each crash = OS reclaims all memory, closes file handles, kills goroutines. No leak accumulation across restarts. Clean and Unix-y.

## Supervisor Logic

```go
func supervisorLoop() error {
    const (
        initialBackoff = 1 * time.Second
        maxBackoff     = 5 * time.Minute
        resetThreshold = 5 * time.Minute  // healthy run resets backoff
    )
    
    backoff := initialBackoff
    crashCount := 0
    
    for {
        startTime := time.Now()
        
        // Spawn gateway subprocess
        cmd := exec.Command(os.Args[0], "gateway")
        cmd.Stdout = os.Stdout
        cmd.Stderr = os.Stderr
        
        err := cmd.Run()
        runDuration := time.Since(startTime)
        exitCode := cmd.ProcessState.ExitCode()
        
        // Clean exit (exit code 0) → stop supervisor
        if exitCode == 0 {
            L_info("gateway stopped cleanly")
            return nil
        }
        
        // Crash → log and restart with backoff
        crashCount++
        logCrash(startTime, runDuration, exitCode, err, crashCount)
        
        // Reset backoff if it ran long enough (was healthy)
        if runDuration > resetThreshold {
            backoff = initialBackoff
        }
        
        L_info("restarting gateway", "backoff", backoff, "crash_count", crashCount)
        time.Sleep(backoff)
        
        // Exponential backoff with cap
        backoff *= 2
        if backoff > maxBackoff {
            backoff = maxBackoff
        }
    }
}
```

## Backoff Schedule

| Crash # | Backoff | Cumulative Wait |
|---------|---------|-----------------|
| 1       | 1s      | 1s              |
| 2       | 2s      | 3s              |
| 3       | 4s      | 7s              |
| 4       | 8s      | 15s             |
| 5       | 16s     | 31s             |
| 6       | 32s     | 63s             |
| 7       | 64s     | ~2min           |
| 8       | 128s    | ~4min           |
| 9       | 256s    | ~8min           |
| 10+     | 300s    | +5min each      |

After 10 rapid crashes, it settles into 5-minute retry intervals. If the gateway runs for >5 minutes, backoff resets — this prevents slow-burn issues from accumulating backoff.

**No max crash limit** — keep trying forever. User can `goclaw stop` if they want it dead.

## Clean Shutdown

`goclaw stop` sends SIGTERM to the supervisor. Supervisor should:
1. Forward SIGTERM to gateway subprocess
2. Wait for subprocess to exit
3. Exit cleanly (don't respawn)

Gateway subprocess receives SIGTERM → cleans up → exits with code 0 → supervisor sees clean exit → stops.

## Storage

State files live alongside `sessions.db`:

```
~/.goclaw/data/
├── sessions.db
├── supervisor.json    # Current run state
└── crash.log          # Append-only crash history
```

### supervisor.json

```json
{
  "pid": 12345,
  "gateway_pid": 12346,
  "started_at": "2026-02-08T14:30:00Z",
  "crash_count": 2,
  "last_crash_at": "2026-02-08T16:45:00Z"
}
```

Updated on:
- Supervisor start (fresh state)
- Each gateway spawn (update gateway_pid)
- Each crash (increment crash_count, update last_crash_at)

**On `goclaw stop && goclaw start`:** Fresh `supervisor.json` with reset counters.

### crash.log

Append-only log of all crashes (survives stop/start cycles):

```
=== CRASH 2026-02-08 14:32:01 ===
Ran for: 3m42s
Exit code: 1
Error: signal: segmentation fault
Last 50 lines of output:
... stderr captured here ...

=== CRASH 2026-02-08 14:32:05 ===
Ran for: 2s
Exit code: 1
Error: exit status 1
Last 50 lines of output:
panic: runtime error: invalid memory address
goroutine 1 [running]:
main.foo()
    /path/to/file.go:123
...
```

## Status Command Enhancement

`goclaw status` reads `supervisor.json` and shows:

```
$ goclaw status
Gateway: running
PID:     12345 (supervisor), 12346 (gateway)
Uptime:  2h34m
Crashes: 2 this session (last: 2h ago)
```

If stopped:
```
$ goclaw status
Gateway: stopped
Last run: 2026-02-08 14:30:00
```

## Subprocess Output Capture

Supervisor needs to capture gateway stdout/stderr for crash.log. Options:

1. **Pipe to file** — `cmd.Stdout` and `cmd.Stderr` to a ring buffer, dump last N lines on crash
2. **Tee to daemon log** — Output goes to daemon log normally, supervisor greps last N lines on crash

Option 1 is cleaner — supervisor maintains a circular buffer of recent output, dumps to crash.log on crash.

## Alternatives Considered

### External Supervisor (systemd, supervisord)

Pros:
- Battle-tested
- OS-level restart
- Resource limits, dependencies

Cons:
- Extra configuration
- Platform-specific (systemd = Linux only)
- Users have to set it up

**Verdict:** Good for production deployments, but built-in supervisor is better for casual users who just want `goclaw start` to work.

### Same-Process Restart

Call `runGateway()` in a loop instead of spawning subprocess.

Pros:
- Simpler (no subprocess management)

Cons:
- Resource leaks accumulate across restarts
- Goroutines, connections, memory not cleaned up by OS

**Verdict:** Subprocess is cleaner. Let the OS do what it's good at.

## Implementation Notes

1. **PID file** — Tracks supervisor PID (unchanged from current behavior)
2. **Daemon log** — Normal operation logs go here
3. **crash.log** — Crash-specific details with captured output
4. **Signal forwarding** — Supervisor catches SIGTERM, forwards to gateway subprocess, waits, exits
5. **Circular buffer** — For capturing last N lines of subprocess output

## Summary of Decisions

| Question | Decision |
|----------|----------|
| Max crash limit? | No limit, keep trying forever |
| Notifications on crash? | No, just crash.log |
| Subprocess vs same-process? | Subprocess (OS cleanup) |
| Storage location? | Same as sessions.db |
| Crash history on restart? | Preserved in crash.log |
| Backoff counters on restart? | Reset to initial |

---

*Status: Approved*
*Author: Ratpup*
*Date: 2026-02-08*
