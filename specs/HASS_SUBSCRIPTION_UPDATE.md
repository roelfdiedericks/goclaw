# Hass Subscription Update Action

**Status:** Draft  
**Date:** 2026-02-14  
**Author:** Ratpup

## Problem

Currently, to modify a subscription's parameters (prompt, debounce, interval, etc.), you must:
1. `unsubscribe` the existing subscription
2. `subscribe` with new parameters

This is clunky and loses the original subscription ID, which may be referenced elsewhere.

## Solution

Add an `update` action to the hass tool for subscriptions.

## Proposed API

```
hass(action="update", subscription_id="uuid", prompt="new prompt", debounce=60, ...)
```

**Parameters:**
- `subscription_id` (required) — ID of subscription to update
- All other subscription params are optional — only provided params are updated

**Example:**
```
hass(action="update", subscription_id="458eda4a-75b7-49b5-9e29-c96243700c8c", prompt="New instructions here")
```

Updates only the prompt, keeps debounce/interval/pattern unchanged.

## Implementation

### File: `internal/tools/hass.go`

In the action switch:

```go
case "update":
    subID := params.SubscriptionID
    if subID == "" {
        return nil, fmt.Errorf("subscription_id required for update action")
    }
    
    updates := hass.SubscriptionUpdates{
        Prompt:   params.Prompt,   // empty string = no change
        Debounce: params.Debounce, // 0 = no change (or use pointer?)
        Interval: params.Interval,
        Enabled:  params.Enabled,  // needs to be *bool to distinguish unset
    }
    
    sub, err := t.hassClient.UpdateSubscription(ctx, subID, updates)
    if err != nil {
        return nil, err
    }
    return sub, nil
```

### File: `internal/hass/subscriptions.go`

Add method to subscription manager:

```go
type SubscriptionUpdates struct {
    Prompt   *string // nil = no change
    Debounce *int
    Interval *int
    Enabled  *bool
    // Pattern/Regex NOT updatable — would require re-subscribe to WS
}

func (m *SubscriptionManager) UpdateSubscription(ctx context.Context, id string, updates SubscriptionUpdates) (*Subscription, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    sub, exists := m.subscriptions[id]
    if !exists {
        return nil, fmt.Errorf("subscription not found: %s", id)
    }
    
    // Apply updates
    if updates.Prompt != nil {
        sub.Prompt = *updates.Prompt
    }
    if updates.Debounce != nil {
        sub.Debounce = *updates.Debounce
    }
    if updates.Interval != nil {
        sub.Interval = *updates.Interval
    }
    if updates.Enabled != nil {
        sub.Enabled = *updates.Enabled
    }
    
    sub.UpdatedAt = time.Now()
    
    // Persist
    if err := m.saveSubscriptions(); err != nil {
        return nil, err
    }
    
    return sub, nil
}
```

## What's Updatable

| Field | Updatable | Notes |
|-------|-----------|-------|
| `prompt` | ✅ Yes | Primary use case |
| `debounce` | ✅ Yes | |
| `interval` | ✅ Yes | |
| `enabled` | ✅ Yes | Already have enable/disable actions, but could consolidate |
| `pattern` | ❌ No | Would need to unsubscribe/resubscribe to WS |
| `regex` | ❌ No | Same as pattern |
| `wake` | ✅ Yes | |
| `full` | ✅ Yes | |

## Response

Same format as subscribe:

```json
{
  "id": "458eda4a-...",
  "pattern": "device_tracker.ames_newphone",
  "prompt": "Updated prompt here",
  "debounce": 60,
  "status": "updated",
  "updated_at": "2026-02-14T20:30:47+02:00"
}
```

## Edge Cases

1. **Subscription not found** → Error: "subscription not found: {id}"
2. **No updates provided** → Return current subscription unchanged (no-op)
3. **Trying to update pattern/regex** → Error: "pattern cannot be updated, unsubscribe and resubscribe"

## Testing

```
# Update prompt only
hass(action="update", subscription_id="xxx", prompt="New prompt")

# Update multiple fields
hass(action="update", subscription_id="xxx", prompt="New prompt", debounce=120)

# Disable via update (alternative to disable action)
hass(action="update", subscription_id="xxx", enabled=false)
```

## Migration

No breaking changes. New action, existing actions unchanged.
