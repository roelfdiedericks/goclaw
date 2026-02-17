# HASS History URL Encoding Fix

## Problem

The `hass` tool's `history` action returns `400 Bad Request: Invalid end_time` because timestamps aren't URL-encoded.

## Root Cause

In `internal/tools/hass.go`, the `getHistory` function builds the URL like this:

```go
path := fmt.Sprintf("/api/history/period/%s?filter_entity_id=%s&end_time=%s",
    startTime.Format(time.RFC3339),
    in.Entity,
    endTime.Format(time.RFC3339))
```

RFC3339 timestamps contain `+` for timezone offsets (e.g., `2026-02-13T21:00:00+02:00`).

In URL query strings, `+` is interpreted as a **space**. So:
- Sent: `end_time=2026-02-13T21:00:00+02:00`
- Received: `end_time=2026-02-13T21:00:00 02:00`

This is an invalid timestamp → 400 Bad Request.

## Fix

Use `url.QueryEscape()` on all query parameter values:

```go
import "net/url"

// In getHistory function:
path := fmt.Sprintf("/api/history/period/%s?filter_entity_id=%s&end_time=%s",
    url.QueryEscape(startTime.Format(time.RFC3339)),
    url.QueryEscape(in.Entity),
    url.QueryEscape(endTime.Format(time.RFC3339)))
```

Or use `url.Values{}` for cleaner query building:

```go
params := url.Values{}
params.Set("filter_entity_id", in.Entity)
params.Set("end_time", endTime.Format(time.RFC3339))
if in.Minimal {
    params.Set("minimal_response", "")
}

path := fmt.Sprintf("/api/history/period/%s?%s",
    url.QueryEscape(startTime.Format(time.RFC3339)),
    params.Encode())
```

## Location

File: `internal/tools/hass.go`
Function: `getHistory` (around line 550)

## Test

```
hass(action="history", entity="sensor.acurite_5n1_a_3969_rain_total", hours=24)
```

Should return history data instead of `400 Bad Request`.
