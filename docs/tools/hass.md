# Home Assistant Tool

GoClaw integrates with Home Assistant for smart home control, automation, and monitoring.

## Configuration

```json
{
  "homeAssistant": {
    "enabled": true,
    "url": "http://homeassistant.local:8123",
    "token": "YOUR_LONG_LIVED_ACCESS_TOKEN"
  }
}
```

| Option | Required | Description |
|--------|----------|-------------|
| `enabled` | Yes | Enable Home Assistant integration |
| `url` | Yes | Home Assistant URL |
| `token` | Yes | Long-lived access token |

Get a token: Home Assistant → Profile → Long-Lived Access Tokens → Create Token

## Actions

### state

Get a single entity state.

```json
{
  "action": "state",
  "entity": "light.kitchen"
}
```

### states

List all entity states with optional filtering.

```json
{
  "action": "states",
  "filter": "*kitchen*",
  "class": "motion"
}
```

| Parameter | Description |
|-----------|-------------|
| `filter` | Glob pattern for entity IDs |
| `class` | Exact device_class filter |

### call

Call a Home Assistant service.

```json
{
  "action": "call",
  "service": "light.turn_on",
  "entity": "light.kitchen",
  "data": {"brightness": 255}
}
```

| Parameter | Required | Description |
|-----------|----------|-------------|
| `service` | Yes | Service in domain.service format |
| `entity` | No | Target entity ID |
| `data` | No | Additional service data |

### camera

Capture a camera snapshot.

```json
{
  "action": "camera",
  "entity": "camera.driveway",
  "timestamp": true
}
```

| Parameter | Description |
|-----------|-------------|
| `filename` | Custom filename |
| `timestamp` | Add timestamp suffix |

Returns the captured image.

### services

List available services.

```json
{
  "action": "services",
  "domain": "light"
}
```

### history

Get entity state history.

```json
{
  "action": "history",
  "entity": "sensor.temperature",
  "hours": 24
}
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `hours` | 24 | Hours of history |
| `start` | - | Start time (ISO 8601) |
| `end` | - | End time (ISO 8601) |
| `minimal` | false | Minimal response format |

### devices

List devices (requires WebSocket connection).

```json
{
  "action": "devices",
  "pattern": "*motion*"
}
```

### areas

List areas (requires WebSocket connection).

```json
{
  "action": "areas",
  "pattern": "*living*"
}
```

### entities

List entities with metadata (requires WebSocket connection).

```json
{
  "action": "entities",
  "pattern": "binary_sensor.*"
}
```

## Subscriptions

Subscribe to real-time state changes.

### subscribe

```json
{
  "action": "subscribe",
  "pattern": "binary_sensor.driveway*",
  "prompt": "Notify me when someone is at the driveway",
  "debounce": 10
}
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `pattern` | - | Glob pattern for entity IDs |
| `regex` | - | Regex pattern (alternative to pattern) |
| `debounce` | 5 | Suppress same state within seconds |
| `interval` | 0 | Per-entity rate limit seconds |
| `prompt` | - | Instructions when event fires |
| `prefix` | - | Custom message prefix |
| `full` | false | Include full state object |
| `wake` | true | Trigger immediate agent invocation |

### subscriptions

List active subscriptions.

```json
{
  "action": "subscriptions"
}
```

### unsubscribe

Cancel a subscription.

```json
{
  "action": "unsubscribe",
  "subscription_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### enable / disable

Enable or disable a subscription without removing it.

```json
{
  "action": "disable",
  "subscription_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

## Examples

**Turn on a light with brightness:**
```json
{
  "action": "call",
  "service": "light.turn_on",
  "entity": "light.bedroom",
  "data": {"brightness_pct": 75}
}
```

**Monitor motion sensors:**
```json
{
  "action": "subscribe",
  "pattern": "binary_sensor.*motion*",
  "prompt": "Tell me when motion is detected",
  "debounce": 30
}
```

**Get power usage history:**
```json
{
  "action": "history",
  "entity": "sensor.house_power",
  "hours": 48
}
```

## Troubleshooting

### "Connection refused"

1. Verify Home Assistant is running
2. Check URL is accessible from GoClaw host
3. Ensure port (usually 8123) is correct

### "Unauthorized"

1. Verify token is correct
2. Check token hasn't expired
3. Try generating a new token

### WebSocket features unavailable

Some features (devices, areas, entities, subscriptions) require a WebSocket connection. Check logs for WebSocket connection status.

---

## See Also

- [Tools](../tools.md) — Tool overview
- [Configuration](../configuration.md) — Full config reference
