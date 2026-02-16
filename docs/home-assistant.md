# Home Assistant Integration

GoClaw integrates natively with Home Assistant, allowing your agent to control smart home devices, query sensors, and respond to events.

## Configuration

Add Home Assistant settings to your `goclaw.json`:

```json
{
  "home_assistant": {
    "url": "http://homeassistant.local:8123",
    "token": "your-long-lived-access-token"
  }
}
```

### Getting a Token

1. In Home Assistant, go to your profile (bottom left)
2. Scroll to "Long-Lived Access Tokens"
3. Click "Create Token"
4. Copy the token to your config

## Available Tools

When Home Assistant is configured, your agent gains access to these tools:

### hass_call_service

Call any Home Assistant service:

```
Turn on the living room lights
→ hass_call_service(domain="light", service="turn_on", entity_id="light.living_room")
```

### hass_get_state

Query entity states:

```
What's the temperature in the bedroom?
→ hass_get_state(entity_id="sensor.bedroom_temperature")
→ "21.5°C"
```

### hass_list_entities

List available entities:

```
What lights do I have?
→ hass_list_entities(domain="light")
```

## Example Interactions

```
You: Turn off all the lights
Agent: Done — turned off 12 lights.

You: What's the front door status?
Agent: The front door is locked and closed.

You: Set the thermostat to 22 degrees
Agent: Thermostat set to 22°C.
```

## Event Subscriptions

GoClaw can subscribe to Home Assistant events and trigger agent actions. Configure in `goclaw.json`:

```json
{
  "home_assistant": {
    "url": "...",
    "token": "...",
    "subscriptions": [
      {
        "event_type": "state_changed",
        "entity_id": "binary_sensor.front_door",
        "prompt": "The front door state changed to {{state}}. Note this."
      }
    ]
  }
}
```

## Security Notes

- The Home Assistant token grants full API access
- Keep your `goclaw.json` secure (file permissions, no commits)
- Consider using Home Assistant's built-in user permissions to limit scope

## Troubleshooting

### Connection Failed

- Verify the URL is reachable from the GoClaw host
- Check the token is valid and not expired
- Ensure Home Assistant API is enabled

### Entity Not Found

- Use `hass_list_entities` to see available entities
- Check entity IDs match exactly (case-sensitive)

### Service Call Failed

- Verify the service exists: check Home Assistant Developer Tools
- Check required parameters for the service
