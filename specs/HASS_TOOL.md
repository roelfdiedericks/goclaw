# Home Assistant Tool Spec

## Overview

Native GoClaw tool for Home Assistant integration. Provides both REST API access for standard queries and WebSocket API access for registry queries and real-time event subscriptions.

## Design Principle

**Pass-through API responses** — Return raw JSON from the HA REST/WebSocket API without transformation. As the API evolves, the agent automatically gets access to new fields. No mapping, no filtering.

## Configuration

Configuration is at the top level of `goclaw.json` (not under `tools`):

```json
// goclaw.json
{
  "homeassistant": {
    "enabled": true,
    "url": "https://home.rodent.za.net:8123",
    "token": "your-long-lived-access-token",
    "insecure": false,
    "timeout": "10s",
    "eventPrefix": "[HomeAssistant Event]",
    "subscriptionFile": "hass-subscriptions.json",
    "reconnectDelay": "5s"
  }
}
```

| Field | Description | Default |
|-------|-------------|---------|
| `enabled` | Enable Home Assistant integration | `false` |
| `url` | HA base URL | - |
| `token` | Long-lived access token | - |
| `insecure` | Skip TLS verification | `false` |
| `timeout` | Request timeout | `"10s"` |
| `eventPrefix` | Prefix for injected event messages | `"[HomeAssistant Event]"` |
| `subscriptionFile` | Filename for subscription persistence | `"hass-subscriptions.json"` |
| `reconnectDelay` | WebSocket reconnect delay | `"5s"` |

## Actions

### state — Get Single Entity State

```
hass(action="state", entity="light.kitchen")
```

**API:** `GET /api/states/<entity_id>`

**Returns:** Raw API response (example):
```json
{
  "entity_id": "light.kitchen",
  "state": "on",
  "attributes": {
    "brightness": 255,
    "friendly_name": "Kitchen Light"
  },
  "last_changed": "2026-02-08T12:34:56+00:00",
  "last_updated": "2026-02-08T12:34:56+00:00"
}
```

---

### states — List Entity States

```
hass(action="states")
hass(action="states", filter="*kitchen*")
hass(action="states", class="motion")
hass(action="states", filter="*driveway*", class="motion")
```

**API:** `GET /api/states`

**Params:**
- `filter` — optional glob pattern for client-side filtering (case-insensitive)
- `class` — optional exact device_class match (case-insensitive)

**Filter matching:**
The `filter` param matches against multiple fields (case-insensitive):
- `entity_id` — e.g., `binary_sensor.sonoff_a4800c1f71`
- `attributes.friendly_name` — e.g., `Master Passage Motion Sensor`
- `attributes.device_class` — e.g., `motion`, `temperature`, `door`

The `class` param is an exact match on `device_class` only:
- `class="motion"` — all motion sensors
- `class="temperature"` — all temperature sensors
- `class="door"` — all door sensors

Both can be combined: `filter="*driveway*", class="motion"` finds driveway motion sensors.

**Returns:** Raw API response — array of state objects (filtered client-side if filters provided).

---

### call — Call a Service

```
hass(action="call", service="light.turn_on", entity="light.kitchen")
hass(action="call", service="light.turn_on", entity="light.kitchen", data={"brightness": 255})
hass(action="call", service="notify.mobile_app_roelf_note14", data={"message": "TTS", "data": {"tts_text": "Hello"}})
hass(action="call", service="weather.get_forecasts", entity="weather.home", data={"type": "daily"})
```

**API:** `POST /api/services/<domain>/<service>?return_response`

**Params:**
- `service` — required, format `domain.service` (e.g., `light.turn_on`)
- `entity` — optional shorthand, merged as `entity_id` into data
- `data` — optional object, passed as request body

**Entity handling:**
- If `entity` param provided, merge `{"entity_id": entity}` into `data`
- If both `entity` param and `entity_id` in data, param wins
- Pure API style also works: `data={"entity_id": "...", ...}`

**Returns:** Raw API response with `return_response` enabled by default:
```json
{
  "changed_states": [...],
  "service_response": {...}
}
```

For services that don't support response data, falls back to standard response (changed states array). Handle 400 gracefully by retrying without `return_response`.

---

### camera — Get Camera Snapshot

```
hass(action="camera", entity="camera.driveway2")
hass(action="camera", entity="camera.driveway2", filename="front.jpg")
hass(action="camera", entity="camera.driveway2", timestamp=true)
```

**API:** `GET /api/camera_proxy/<entity_id>`

**Params:**
- `entity` — required, camera entity ID
- `filename` — optional, custom filename (default: entity name + `.jpg`)
- `timestamp` — optional boolean, append unix timestamp to filename

**File naming:**
- Default: `camera/driveway2.jpg` (overwrites)
- With `timestamp=true`: `camera/driveway2_1707412800.jpg`
- With `filename="custom.jpg"`: `camera/custom.jpg`

**Returns:** (camera is special — saves image to disk, returns path relative to media root)
```json
{
  "path": "camera/driveway2.jpg"
}
```

The path is relative to the media root. Agent can use `{{media:camera/driveway2.jpg}}` inline or send via message tool.

---

### services — List Available Services

```
hass(action="services")
hass(action="services", domain="notify")
hass(action="services", domain="light")
```

**API:** `GET /api/services`

**Params:**
- `domain` — optional, client-side filter by domain

**Returns:** Raw API response:
```json
[
  {
    "domain": "browser",
    "services": ["browse_url"]
  },
  {
    "domain": "light",
    "services": ["turn_on", "turn_off", "toggle"]
  }
]
```

---

### history — Get State History

```
hass(action="history", entity="sensor.temperature", hours=24)
hass(action="history", entity="sensor.temperature", start="2026-02-01", end="2026-02-07")
hass(action="history", entity="sensor.temperature", hours=24, minimal=true)
```

**API:** `GET /api/history/period/<timestamp>?filter_entity_id=<entity>&end_time=<end>`

**Params:**
- `entity` — required (API requires `filter_entity_id`)
- `hours` — optional, hours of history from now (default: 24)
- `start` — optional, ISO date/datetime for period start
- `end` — optional, ISO date/datetime for period end
- `minimal` — optional boolean, adds `minimal_response` param (faster)

**Returns:** Raw API response — array of state change objects.

---

## Registry Actions (WebSocket)

These actions use the WebSocket API to query Home Assistant registries. Each query opens a fresh WebSocket connection. All registry actions support optional `pattern` or `regex` filtering to reduce token usage.

### devices — List Devices

```
hass(action="devices")
hass(action="devices", pattern="*sonoff*")
hass(action="devices", pattern="*living_room*")
hass(action="devices", regex="(?i)camera")
```

**API:** WebSocket `config/device_registry/list`

**Params:**
- `pattern` — optional glob pattern (case-insensitive)
- `regex` — optional regex pattern (mutually exclusive with pattern)

**Pattern matching (case-insensitive):**
- `name` — device name
- `name_by_user` — user-customized name
- `id` — device ID
- `manufacturer` — e.g., "Sonoff", "Zigbee2MQTT", "Tasmota"
- `model` — device model name
- `area_id` — area the device is in

**Returns:** Raw API response — array of device objects (filtered if pattern/regex provided)

---

### areas — List Areas

```
hass(action="areas")
hass(action="areas", pattern="*floor*")
```

**API:** WebSocket `config/area_registry/list`

**Params:**
- `pattern` — optional glob pattern, matches against area name or area_id
- `regex` — optional regex pattern (mutually exclusive with pattern)

**Returns:** Raw API response — array of area objects (filtered if pattern/regex provided)

---

### entities — List Entities

```
hass(action="entities")
hass(action="entities", pattern="*kitchen*")
hass(action="entities", pattern="*motion*")
hass(action="entities", regex="^light\\.kitchen")
```

**API:** WebSocket `config/entity_registry/list`

**Params:**
- `pattern` — optional glob pattern (case-insensitive)
- `regex` — optional regex pattern (mutually exclusive with pattern)

**Pattern matching (case-insensitive):**
- `entity_id` — e.g., `binary_sensor.sonoff_a4800c1f71`
- `original_name` — original entity name
- `name` — customized entity name
- `device_class` — e.g., "motion", "temperature", "door"
- `area_id` — area the entity is in

**Returns:** Raw API response — array of entity registry entries (filtered if pattern/regex provided)

---

## Subscription Actions (WebSocket)

Event subscriptions allow the agent to receive real-time notifications when entity states change. A persistent WebSocket connection is maintained while subscriptions exist.

### subscribe — Subscribe to State Changes

```
hass(action="subscribe", pattern="binary_sensor.driveway*", prompt="Notify me someone is at the driveway")
hass(action="subscribe", pattern="sensor.load*", interval=60, prompt="Alert if load exceeds 1500W")
hass(action="subscribe", regex="^person\\.", prompt="Log location changes silently", wake=false)
hass(action="subscribe", pattern="sensor.temperature*", interval=60, prefix="Temperature", wake=false)
```

**Params:**
- `pattern` — Glob pattern for entity matching (e.g., `binary_sensor.*`, `light.kitchen*`)
- `regex` — Regex pattern for entity matching (e.g., `^person\\.`) — mutually exclusive with `pattern`
- `debounce` — Seconds to wait before allowing same entity:state event (default: 5) — suppresses duplicate states
- `interval` — Per-entity rate limit in seconds (default: 0 = disabled) — limits events regardless of state change
- `prompt` — Instructions for agent when event fires (e.g., "Alert if load exceeds 1500W")
- `prefix` — Custom prefix for injected messages (default: uses `eventPrefix` from config)
- `full` — Include full state object (default: false = brief)
- `wake` — Trigger immediate agent invocation when event fires (default: true)

**Pattern matching:**
- One of `pattern` or `regex` must be specified, not both
- `pattern` uses glob matching: `*` matches any characters
- `regex` uses Go regex matching

**Returns:**
```json
{
  "status": "subscribed",
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "pattern": "binary_sensor.driveway*",
  "regex": "",
  "debounce": 5,
  "interval": 0,
  "prompt": "Notify me someone is at the driveway",
  "full": false,
  "wake": true,
  "connected": true,
  "created_at": "2026-02-04T12:00:00Z"
}
```

**Injected message format (wake=true with prompt):**
```
[HomeAssistant Event] {"entity_id":"binary_sensor.driveway_motion","state":"on",...}

Instructions: Notify me someone is at the driveway

Reply EVENT_OK if no action needed.
```

**Rate limiting behavior:**
- **Debounce**: Suppresses same `entity:state` combination within window. Use for sensors that repeatedly report the same value.
- **Interval**: Per-entity rate limit regardless of state. Use for sensors with frequent state changes where you only want periodic updates.
- Both can be combined: interval is checked first, then debounce.

---

### unsubscribe — Cancel a Subscription

```
hass(action="unsubscribe", subscription_id="550e8400-e29b-41d4-a716-446655440000")
```

**Params:**
- `subscription_id` — UUID of the subscription to cancel

**Returns:**
```json
{
  "status": "unsubscribed",
  "id": "550e8400-e29b-41d4-a716-446655440000"
}
```

---

### subscriptions — List Active Subscriptions

```
hass(action="subscriptions")
```

**Returns:**
```json
{
  "count": 2,
  "connected": true,
  "subscriptions": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "pattern": "binary_sensor.driveway*",
      "regex": "",
      "debounce_seconds": 30,
      "prefix": "",
      "full": false,
      "wake": true,
      "created_at": "2026-02-04T12:00:00Z"
    }
  ]
}
```

---

## Event Injection

When a subscribed entity changes state, an event message is injected into the agent's session:

**Brief format (default):**
```
[HomeAssistant Event] {"entity_id":"binary_sensor.driveway_motion","state":"on","old_state":"off","friendly_name":"Driveway Motion","time_fired":"2026-02-04T12:00:00Z"}
```

**Full format (`full=true`):**
```
[HomeAssistant Event] {"entity_id":"binary_sensor.driveway_motion","new_state":{"entity_id":"binary_sensor.driveway_motion","state":"on","attributes":{"device_class":"motion","friendly_name":"Driveway Motion"},...},"old_state":{...},"time_fired":"2026-02-04T12:00:00Z"}
```

**Wake behavior:**
- If `wake=true` (default): The event is passed to `InvokeAgent()` which runs the agent immediately with the event as a prompt, wrapped with instructions (`Process this event. Reply EVENT_OK if no action needed.`). If the agent responds with `EVENT_OK`, the response is suppressed; otherwise it is delivered to channels. The invocation is ephemeral (not persisted to session history).
- If `wake=false`: The event is injected via `InjectSystemEvent()` as a system message. The agent will see it on the next user interaction but is not immediately invoked.

**Debouncing:**
- Events are debounced per `entity_id:new_state` combination
- If the same entity changes to the same state within the debounce window, the duplicate is suppressed
- This prevents flooding from sensors with high update rates

---

## Error Handling

Errors returned in result, agent handles them:

```json
{
  "error": "401 Unauthorized",
  "message": "Invalid or expired token"
}
```

```json
{
  "error": "404 Not Found", 
  "message": "Entity light.nonexistent not found"
}
```

```json
{
  "error": "connection refused",
  "message": "Could not connect to Home Assistant at https://..."
}
```

---

## Future Expansion

These features may be added later:

| Feature | Notes |
|---------|-------|
| Unavailable tracking | Alert when device stays offline for N minutes |
| Other event types | Subscribe to events beyond `state_changed` |
| Per-user subscriptions | Currently subscriptions are global |
| config action | GET /api/config — HA version, timezone, location |
| logbook action | GET /api/logbook — Human-readable event log |
| fire_event action | POST /api/events/<type> — Fire an event |

---

## Implementation Notes

### HTTP Client (REST Actions)

- Use standard Go `net/http` client
- Set `Authorization: Bearer <token>` header
- Set `Content-Type: application/json` for POST requests
- Respect `timeout` config
- Handle `insecure` flag for TLS verification

### WebSocket Client (Registry/Subscription Actions)

- Use `gorilla/websocket` library
- Convert REST URL to WebSocket URL: `https://...` → `wss://.../api/websocket`
- Authentication: Send `{"type": "auth", "access_token": "..."}` after `auth_required` message
- Two separate WebSocket connections:
  - **WSClient**: Fresh connection per sync query (devices/areas/entities)
  - **Manager**: Persistent connection for subscriptions (reconnects on failure)

### Service Parsing

For `call` action, parse `service` param:
```go
parts := strings.SplitN(service, ".", 2)
domain := parts[0]  // "light"
service := parts[1] // "turn_on"
// POST to /api/services/{domain}/{service}
```

### Pattern Matching

For `states` filter and subscriptions:
- `*` matches any characters (glob)
- Regex patterns use Go `regexp` package
- Match against `entity_id`

### Camera File Handling

1. Make GET request to `/api/camera_proxy/<entity_id>`
2. Response is raw image bytes (JPEG)
3. Extract entity name from entity_id (e.g., `camera.driveway2` → `driveway2`)
4. Build filename based on params
5. Save to `media/camera/` directory
6. Return path in result

### Subscription Persistence

- Subscriptions are saved to `~/.goclaw/hass-subscriptions.json`
- On gateway startup, persisted subscriptions are loaded
- If subscriptions exist, WebSocket connection is established
- If no subscriptions, WebSocket connects lazily on first `subscribe` call
- Reconnection uses exponential backoff (starting at `reconnectDelay`, max 5 minutes)

---

## Example Usage

### REST API Actions

**Check if light is on:**
```
hass(action="state", entity="light.kitchen")
```

**Turn on light with brightness:**
```
hass(action="call", service="light.turn_on", entity="light.kitchen", data={"brightness": 200})
```

**Send TTS notification:**
```
hass(action="call", service="notify.mobile_app_roelf_note14", data={"message": "TTS", "data": {"tts_text": "XRP below 18", "media_stream": "alarm_stream_max"}})
```

**Get driveway camera snapshot:**
```
hass(action="camera", entity="camera.driveway2")
```

**List all notify services:**
```
hass(action="services", domain="notify")
```

**Get temperature history:**
```
hass(action="history", entity="sensor.temperature", hours=48, minimal=true)
```

**Get weather forecast:**
```
hass(action="call", service="weather.get_forecasts", entity="weather.home", data={"type": "daily"})
```

### WebSocket Registry Actions

**List all devices:**
```
hass(action="devices")
```

**List devices matching a pattern:**
```
hass(action="devices", pattern="*motion*")
```

**List all areas:**
```
hass(action="areas")
```

**List entities by domain:**
```
hass(action="entities", pattern="binary_sensor.*")
```

**List entities with regex:**
```
hass(action="entities", regex="^light\\.kitchen")
```

### Event Subscriptions

**Subscribe to driveway motion (wake agent immediately):**
```
hass(action="subscribe", pattern="binary_sensor.driveway*")
```

**Subscribe to all door sensors with 30s debounce:**
```
hass(action="subscribe", pattern="binary_sensor.*door*", debounce=30)
```

**Subscribe to person tracking (passive, full state):**
```
hass(action="subscribe", regex="^person\\.", full=true, wake=false)
```

**List active subscriptions:**
```
hass(action="subscriptions")
```

**Cancel a subscription:**
```
hass(action="unsubscribe", subscription_id="550e8400-e29b-41d4-a716-446655440000")
```

---

*Status: Implemented*
*Author: Ratpup*
*Date: 2026-02-04*
*Updated: 2026-02-08 — Added WebSocket registry and subscription actions*
