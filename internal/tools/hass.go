// Package tools provides agent tools including Home Assistant integration.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
)

// HASSTool implements the Home Assistant tool for the agent.
type HASSTool struct {
	client     *HASSClient
	wsClient   *hass.WSClient  // for sync registry queries
	manager    *hass.Manager   // for subscriptions
	mediaStore *media.MediaStore
}

// hassInput defines the input schema for the hass tool.
type hassInput struct {
	// Common fields
	Action string `json:"action"` // state, states, call, camera, services, history, devices, areas, entities, subscribe, unsubscribe, enable, disable, subscriptions

	// Entity/service fields
	Entity  string          `json:"entity,omitempty"`  // entity_id
	Service string          `json:"service,omitempty"` // domain.service for call action
	Data    json.RawMessage `json:"data,omitempty"`    // service call data
	Filter  string          `json:"filter,omitempty"`  // glob pattern for states/services filtering
	Class   string          `json:"class,omitempty"`   // exact device_class filter for states
	Domain  string          `json:"domain,omitempty"`  // domain filter for services

	// History fields
	Hours   int    `json:"hours,omitempty"`   // history hours (default: 24)
	Start   string `json:"start,omitempty"`   // history start (ISO date/datetime)
	End     string `json:"end,omitempty"`     // history end (ISO date/datetime)
	Minimal bool   `json:"minimal,omitempty"` // history minimal_response

	// Camera fields
	Filename  string `json:"filename,omitempty"`  // camera custom filename
	Timestamp bool   `json:"timestamp,omitempty"` // camera timestamp suffix

	// Subscription fields
	Pattern        string `json:"pattern,omitempty"`         // glob pattern for subscribe (e.g., binary_sensor.*)
	Regex          string `json:"regex,omitempty"`           // regex pattern for subscribe (e.g., ^person\.)
	Debounce       int    `json:"debounce,omitempty"`        // debounce seconds (default: 5), same state suppression
	Interval       int    `json:"interval,omitempty"`        // interval seconds (default: 0 = disabled), per-entity rate limit
	Prefix         string `json:"prefix,omitempty"`          // custom prefix for injected messages
	Prompt         string `json:"prompt,omitempty"`          // instructions for agent when event fires
	Full           bool   `json:"full,omitempty"`            // include full state object (default: false = brief)
	Wake           *bool  `json:"wake,omitempty"`            // trigger immediate agent invocation (default: true)
	SubscriptionID string `json:"subscription_id,omitempty"` // subscription ID for unsubscribe
}

// NewHASSTool creates a new Home Assistant tool.
// The wsClient and manager parameters are optional - if nil, WebSocket features
// (devices, areas, entities, subscriptions) will return errors.
func NewHASSTool(cfg config.HomeAssistantConfig, mediaStore *media.MediaStore, wsClient *hass.WSClient, manager *hass.Manager) (*HASSTool, error) {
	client, err := NewHASSClient(cfg)
	if err != nil {
		return nil, err
	}

	L_info("hass: tool created", "url", cfg.URL, "hasWSClient", wsClient != nil, "hasManager", manager != nil)
	return &HASSTool{
		client:     client,
		wsClient:   wsClient,
		manager:    manager,
		mediaStore: mediaStore,
	}, nil
}

// Name returns the tool name.
func (t *HASSTool) Name() string {
	return "hass"
}

// Description returns the tool description for the LLM.
func (t *HASSTool) Description() string {
	return `Home Assistant integration. Query entity states, call services, capture camera snapshots, retrieve history, and manage event subscriptions.

REST Actions:
- state: Get single entity state (requires entity)
- states: List all entity states (optional filter glob, optional class for exact device_class match)
- call: Call a service (requires service like "light.turn_on", optional entity and data)
- camera: Get camera snapshot (requires entity, optional filename/timestamp)
- services: List available services (optional domain filter)
- history: Get state history (requires entity, optional hours/start/end/minimal)

Registry Actions (WebSocket):
- devices: List devices (optional pattern/regex filter on name/id/manufacturer/model/area_id)
- areas: List areas (optional pattern/regex filter on name/area_id)
- entities: List entities with metadata (optional pattern/regex filter on entity_id/name/device_class/area_id)

Subscription Actions:
- subscribe: Subscribe to state_changed events (pattern OR regex, optional debounce/interval/prompt/prefix/full/wake)
- unsubscribe: Cancel a subscription (requires subscription_id)
- enable: Enable a disabled subscription (requires subscription_id)
- disable: Disable a subscription without removing it (requires subscription_id)
- subscriptions: List all subscriptions with enabled/disabled status

Rate limiting:
- debounce: Suppress same entity:state events within window (default 5s)
- interval: Per-entity rate limit regardless of state (default 0 = disabled)

Examples:
- hass(action="state", entity="light.kitchen")
- hass(action="states", filter="*kitchen*")
- hass(action="states", class="motion")
- hass(action="states", filter="*driveway*", class="motion")
- hass(action="call", service="light.turn_on", entity="light.kitchen", data={"brightness": 255})
- hass(action="camera", entity="camera.driveway")
- hass(action="devices", pattern="*motion*")
- hass(action="entities", pattern="binary_sensor.*")
- hass(action="entities", regex="^light\\.")
- hass(action="subscribe", pattern="binary_sensor.driveway*", prompt="Notify me someone is at the driveway")
- hass(action="subscribe", pattern="sensor.load*", interval=60, prompt="Alert if load exceeds 1500W")
- hass(action="unsubscribe", subscription_id="550e8400-e29b-41d4-a716-446655440000")`
}

// Schema returns the JSON Schema for the tool input.
func (t *HASSTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"state", "states", "call", "camera", "services", "history", "devices", "areas", "entities", "subscribe", "unsubscribe", "enable", "disable", "subscriptions"},
				"description": "The action to perform",
			},
			"entity": map[string]any{
				"type":        "string",
				"description": "Entity ID (required for state, camera, history; optional shorthand for call)",
			},
			"service": map[string]any{
				"type":        "string",
				"description": "Service to call in format domain.service (e.g., light.turn_on)",
			},
			"data": map[string]any{
				"type":        "object",
				"description": "Additional data for service calls",
			},
			"filter": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter states (case-insensitive, matches entity_id, friendly_name, or device_class)",
			},
			"class": map[string]any{
				"type":        "string",
				"description": "Exact device_class filter for states (e.g., motion, temperature, door, light)",
			},
			"domain": map[string]any{
				"type":        "string",
				"description": "Domain filter for services action",
			},
			"hours": map[string]any{
				"type":        "integer",
				"description": "Hours of history to retrieve (default: 24)",
			},
			"start": map[string]any{
				"type":        "string",
				"description": "Start date/time for history (ISO format)",
			},
			"end": map[string]any{
				"type":        "string",
				"description": "End date/time for history (ISO format)",
			},
			"minimal": map[string]any{
				"type":        "boolean",
				"description": "Return minimal history response (faster)",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Custom filename for camera snapshot",
			},
			"timestamp": map[string]any{
				"type":        "boolean",
				"description": "Append timestamp to camera filename",
			},
		"pattern": map[string]any{
			"type":        "string",
			"description": "Glob pattern for filtering (devices/areas/entities/subscribe). E.g., binary_sensor.* - mutually exclusive with regex",
		},
		"regex": map[string]any{
			"type":        "string",
			"description": "Regex pattern for filtering (devices/areas/entities/subscribe). E.g., ^person\\. - mutually exclusive with pattern",
		},
			"debounce": map[string]any{
				"type":        "integer",
				"description": "Debounce seconds between events for same entity:state (default: 5)",
			},
			"interval": map[string]any{
				"type":        "integer",
				"description": "Interval seconds for per-entity rate limiting, regardless of state (default: 0 = disabled)",
			},
			"prefix": map[string]any{
				"type":        "string",
				"description": "Custom prefix for injected event messages",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Instructions for agent when event fires (e.g., 'Alert if load exceeds 1500W')",
			},
			"full": map[string]any{
				"type":        "boolean",
				"description": "Include full state object in events (default: false = brief)",
			},
			"wake": map[string]any{
				"type":        "boolean",
				"description": "Trigger immediate heartbeat on event (default: true)",
			},
			"subscription_id": map[string]any{
				"type":        "string",
				"description": "Subscription ID for unsubscribe action",
			},
		},
		"required": []string{"action"},
	}
}

// Execute runs the tool with the given input.
func (t *HASSTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in hassInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	L_debug("hass: executing", "action", in.Action, "entity", in.Entity)

	var result json.RawMessage
	var err error

	switch in.Action {
	case "state":
		result, err = t.getState(ctx, in)
	case "states":
		result, err = t.getStates(ctx, in)
	case "call":
		result, err = t.callService(ctx, in)
	case "camera":
		return t.getCamera(ctx, in)
	case "services":
		result, err = t.getServices(ctx, in)
	case "history":
		result, err = t.getHistory(ctx, in)
	case "devices":
		result, err = t.listDevices(ctx, in)
	case "areas":
		result, err = t.listAreas(ctx, in)
	case "entities":
		result, err = t.listEntities(ctx, in)
	case "subscribe":
		return t.subscribe(ctx, in)
	case "unsubscribe":
		return t.unsubscribe(ctx, in)
	case "enable":
		return t.enableSubscription(ctx, in)
	case "disable":
		return t.disableSubscription(ctx, in)
	case "subscriptions":
		return t.listSubscriptions(ctx)
	default:
		return t.errorResult("invalid action", fmt.Sprintf("unknown action: %s", in.Action))
	}

	if err != nil {
		// Check if it's a HASS error
		if hassErr, ok := err.(*HASSError); ok {
			return t.errorResult(hassErr.Status, hassErr.Message)
		}
		return t.errorResult("error", err.Error())
	}

	return string(result), nil
}

// getState retrieves a single entity state.
func (t *HASSTool) getState(ctx context.Context, in hassInput) (json.RawMessage, error) {
	if in.Entity == "" {
		return nil, fmt.Errorf("entity is required for state action")
	}

	path := fmt.Sprintf("/api/states/%s", in.Entity)
	return t.client.Get(ctx, path)
}

// getStates retrieves all entity states with optional filtering.
func (t *HASSTool) getStates(ctx context.Context, in hassInput) (json.RawMessage, error) {
	result, err := t.client.Get(ctx, "/api/states")
	if err != nil {
		return nil, err
	}

	// If no filters, return as-is
	if in.Filter == "" && in.Class == "" {
		return result, nil
	}

	// Parse and filter
	var states []map[string]any
	if err := json.Unmarshal(result, &states); err != nil {
		return nil, fmt.Errorf("failed to parse states: %w", err)
	}

	var filtered []map[string]any
	for _, s := range states {
		// Check class filter (exact match, case-insensitive)
		if in.Class != "" {
			deviceClass := t.getStateDeviceClass(s)
			if !strings.EqualFold(deviceClass, in.Class) {
				continue
			}
		}

		// Check glob filter (if specified)
		if in.Filter != "" && !t.matchStateFilter(s, in.Filter) {
			continue
		}

		filtered = append(filtered, s)
	}

	L_debug("hass: states filtered", "filter", in.Filter, "class", in.Class, "total", len(states), "matched", len(filtered))
	return json.Marshal(filtered)
}

// matchStateFilter checks if a state object matches the filter pattern.
// Matches case-insensitively against: entity_id, friendly_name, device_class.
func (t *HASSTool) matchStateFilter(state map[string]any, filter string) bool {
	// Check entity_id
	if entityID, ok := state["entity_id"].(string); ok {
		if hass.MatchGlobInsensitive(filter, entityID) {
			return true
		}
	}

	// Check attributes.friendly_name and attributes.device_class
	if attrs, ok := state["attributes"].(map[string]any); ok {
		if friendlyName, ok := attrs["friendly_name"].(string); ok {
			if hass.MatchGlobInsensitive(filter, friendlyName) {
				return true
			}
		}
		if deviceClass, ok := attrs["device_class"].(string); ok {
			if hass.MatchGlobInsensitive(filter, deviceClass) {
				return true
			}
		}
	}

	return false
}

// getStateDeviceClass extracts the device_class from a state object.
func (t *HASSTool) getStateDeviceClass(state map[string]any) string {
	if attrs, ok := state["attributes"].(map[string]any); ok {
		if deviceClass, ok := attrs["device_class"].(string); ok {
			return deviceClass
		}
	}
	return ""
}

// callService calls a Home Assistant service.
func (t *HASSTool) callService(ctx context.Context, in hassInput) (json.RawMessage, error) {
	if in.Service == "" {
		return nil, fmt.Errorf("service is required for call action")
	}

	// Parse service: domain.service
	parts := strings.SplitN(in.Service, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid service format: expected domain.service (e.g., light.turn_on)")
	}
	domain := parts[0]
	service := parts[1]

	// Build request body
	var data map[string]any
	if in.Data != nil && len(in.Data) > 0 {
		if err := json.Unmarshal(in.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
	}
	if data == nil {
		data = make(map[string]any)
	}

	// Merge entity shorthand
	if in.Entity != "" {
		data["entity_id"] = in.Entity
	}

	path := fmt.Sprintf("/api/services/%s/%s?return_response", domain, service)
	L_info("hass: calling service", "service", in.Service, "entity", in.Entity)

	result, err := t.client.Post(ctx, path, data)
	if err != nil {
		// If 400 error, retry without return_response (service doesn't support it)
		if hassErr, ok := err.(*HASSError); ok && hassErr.StatusCode == 400 {
			L_debug("hass: retrying without return_response", "service", in.Service)
			path = fmt.Sprintf("/api/services/%s/%s", domain, service)
			return t.client.Post(ctx, path, data)
		}
		return nil, err
	}

	return result, nil
}

// getCamera captures a camera snapshot and saves it to media storage.
func (t *HASSTool) getCamera(ctx context.Context, in hassInput) (string, error) {
	if in.Entity == "" {
		return t.errorResult("error", "entity is required for camera action")
	}

	if t.mediaStore == nil {
		return t.errorResult("error", "media store not configured")
	}

	// Get camera image
	path := fmt.Sprintf("/api/camera_proxy/%s", in.Entity)
	imageData, contentType, err := t.client.GetBinary(ctx, path)
	if err != nil {
		if hassErr, ok := err.(*HASSError); ok {
			return t.errorResult(hassErr.Status, hassErr.Message)
		}
		return t.errorResult("error", err.Error())
	}

	// Determine extension from content type
	ext := ".jpg"
	if strings.Contains(contentType, "png") {
		ext = ".png"
	}

	// Build filename
	var filename string
	if in.Filename != "" {
		filename = in.Filename
	} else {
		// Extract entity name from entity_id (e.g., camera.driveway2 -> driveway2)
		entityName := in.Entity
		if idx := strings.Index(entityName, "."); idx >= 0 {
			entityName = entityName[idx+1:]
		}

		if in.Timestamp {
			filename = fmt.Sprintf("%s_%d%s", entityName, time.Now().Unix(), ext)
		} else {
			filename = entityName + ext
		}
	}

	// Ensure camera subdirectory exists
	cameraDir := filepath.Join(t.mediaStore.BaseDir(), "camera")
	if err := os.MkdirAll(cameraDir, 0700); err != nil {
		return t.errorResult("error", fmt.Sprintf("failed to create camera directory: %v", err))
	}

	// Save file
	absPath := filepath.Join(cameraDir, filename)
	if err := os.WriteFile(absPath, imageData, 0600); err != nil {
		return t.errorResult("error", fmt.Sprintf("failed to save image: %v", err))
	}

	// Return relative path (relative to media root)
	relPath := "camera/" + filename

	L_info("hass: camera snapshot saved", "entity", in.Entity, "path", relPath, "size", len(imageData))

	result := map[string]string{"path": relPath}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// getServices retrieves available services.
func (t *HASSTool) getServices(ctx context.Context, in hassInput) (json.RawMessage, error) {
	result, err := t.client.Get(ctx, "/api/services")
	if err != nil {
		return nil, err
	}

	// If no domain filter, return as-is
	if in.Domain == "" {
		return result, nil
	}

	// Parse and filter by domain
	var services []map[string]any
	if err := json.Unmarshal(result, &services); err != nil {
		return nil, fmt.Errorf("failed to parse services: %w", err)
	}

	var filtered []map[string]any
	for _, s := range services {
		domain, ok := s["domain"].(string)
		if ok && domain == in.Domain {
			filtered = append(filtered, s)
		}
	}

	L_debug("hass: services filtered", "domain", in.Domain, "total", len(services), "matched", len(filtered))
	return json.Marshal(filtered)
}

// getHistory retrieves state history for an entity.
func (t *HASSTool) getHistory(ctx context.Context, in hassInput) (json.RawMessage, error) {
	if in.Entity == "" {
		return nil, fmt.Errorf("entity is required for history action")
	}

	// Calculate time range
	var startTime, endTime time.Time
	now := time.Now()

	if in.Start != "" {
		var err error
		startTime, err = parseDateTime(in.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid start time: %w", err)
		}
	} else {
		// Default to hours ago (default 24)
		hours := in.Hours
		if hours <= 0 {
			hours = 24
		}
		startTime = now.Add(-time.Duration(hours) * time.Hour)
	}

	if in.End != "" {
		var err error
		endTime, err = parseDateTime(in.End)
		if err != nil {
			return nil, fmt.Errorf("invalid end time: %w", err)
		}
	} else {
		endTime = now
	}

	// Build API path with query params
	// Format: /api/history/period/<timestamp>?filter_entity_id=<entity>&end_time=<end>
	path := fmt.Sprintf("/api/history/period/%s?filter_entity_id=%s&end_time=%s",
		startTime.Format(time.RFC3339),
		in.Entity,
		endTime.Format(time.RFC3339))

	if in.Minimal {
		path += "&minimal_response"
	}

	L_debug("hass: history request", "entity", in.Entity, "start", startTime, "end", endTime, "minimal", in.Minimal)
	return t.client.Get(ctx, path)
}

// errorResult returns a JSON error response.
func (t *HASSTool) errorResult(errType, message string) (string, error) {
	result := map[string]string{
		"error":   errType,
		"message": message,
	}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// listDevices retrieves all devices from the device registry via WebSocket.
func (t *HASSTool) listDevices(ctx context.Context, in hassInput) (json.RawMessage, error) {
	if t.wsClient == nil {
		return nil, fmt.Errorf("WebSocket client not configured")
	}
	result, err := t.wsClient.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	// Filter by pattern/regex on device name or id
	if in.Pattern != "" || in.Regex != "" {
		return t.filterDevices(result, in)
	}
	return result, nil
}

// listAreas retrieves all areas from the area registry via WebSocket.
func (t *HASSTool) listAreas(ctx context.Context, in hassInput) (json.RawMessage, error) {
	if t.wsClient == nil {
		return nil, fmt.Errorf("WebSocket client not configured")
	}
	result, err := t.wsClient.ListAreas(ctx)
	if err != nil {
		return nil, err
	}
	// Filter by pattern/regex on area name or area_id
	if in.Pattern != "" || in.Regex != "" {
		return t.filterAreas(result, in)
	}
	return result, nil
}

// listEntities retrieves all entities from the entity registry via WebSocket.
func (t *HASSTool) listEntities(ctx context.Context, in hassInput) (json.RawMessage, error) {
	if t.wsClient == nil {
		return nil, fmt.Errorf("WebSocket client not configured")
	}
	result, err := t.wsClient.ListEntities(ctx)
	if err != nil {
		return nil, err
	}
	// Filter by pattern/regex on entity_id
	if in.Pattern != "" || in.Regex != "" {
		return t.filterEntities(result, in)
	}
	return result, nil
}

// filterDevices filters devices by pattern/regex on multiple fields.
// Matches (case-insensitive): name, name_by_user, id, manufacturer, model, area_id
func (t *HASSTool) filterDevices(data json.RawMessage, in hassInput) (json.RawMessage, error) {
	var devices []map[string]any
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, fmt.Errorf("failed to parse devices: %w", err)
	}

	var filtered []map[string]any
	for _, d := range devices {
		if t.matchAny(in, d["name"], d["name_by_user"], d["id"], d["manufacturer"], d["model"], d["area_id"]) {
			filtered = append(filtered, d)
		}
	}

	L_debug("hass: devices filtered", "pattern", in.Pattern, "regex", in.Regex, "total", len(devices), "matched", len(filtered))
	return json.Marshal(filtered)
}

// filterAreas filters areas by pattern/regex on name or area_id fields.
func (t *HASSTool) filterAreas(data json.RawMessage, in hassInput) (json.RawMessage, error) {
	var areas []map[string]any
	if err := json.Unmarshal(data, &areas); err != nil {
		return nil, fmt.Errorf("failed to parse areas: %w", err)
	}

	var filtered []map[string]any
	for _, a := range areas {
		// Match against name or area_id
		if t.matchAny(in, a["name"], a["area_id"]) {
			filtered = append(filtered, a)
		}
	}

	L_debug("hass: areas filtered", "pattern", in.Pattern, "regex", in.Regex, "total", len(areas), "matched", len(filtered))
	return json.Marshal(filtered)
}

// filterEntities filters entities by pattern/regex on multiple fields.
// Matches (case-insensitive): entity_id, original_name, name, device_class, area_id
func (t *HASSTool) filterEntities(data json.RawMessage, in hassInput) (json.RawMessage, error) {
	var entities []map[string]any
	if err := json.Unmarshal(data, &entities); err != nil {
		return nil, fmt.Errorf("failed to parse entities: %w", err)
	}

	var filtered []map[string]any
	for _, e := range entities {
		if t.matchAny(in, e["entity_id"], e["original_name"], e["name"], e["device_class"], e["area_id"]) {
			filtered = append(filtered, e)
		}
	}

	L_debug("hass: entities filtered", "pattern", in.Pattern, "regex", in.Regex, "total", len(entities), "matched", len(filtered))
	return json.Marshal(filtered)
}

// matchAny checks if any of the values match the pattern or regex (case-insensitive for glob).
func (t *HASSTool) matchAny(in hassInput, values ...any) bool {
	for _, v := range values {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if in.Pattern != "" && hass.MatchGlobInsensitive(in.Pattern, s) {
			return true
		}
		if in.Regex != "" {
			// Regex is case-sensitive by default; user can use (?i) if needed
			if matched, _ := hass.MatchRegex(in.Regex, s); matched {
				return true
			}
		}
	}
	return false
}

// subscribe creates a new event subscription.
func (t *HASSTool) subscribe(ctx context.Context, in hassInput) (string, error) {
	if t.manager == nil {
		return t.errorResult("error", "subscription manager not configured")
	}

	// Validate: must have pattern OR regex, not both
	if in.Pattern == "" && in.Regex == "" {
		return t.errorResult("error", "either pattern or regex is required for subscribe")
	}
	if in.Pattern != "" && in.Regex != "" {
		return t.errorResult("error", "pattern and regex are mutually exclusive")
	}

	// Validate regex if provided
	if in.Regex != "" {
		if _, err := hass.MatchRegex(in.Regex, "test"); err != nil {
			return t.errorResult("error", fmt.Sprintf("invalid regex: %v", err))
		}
	}

	// Create subscription
	sub := hass.NewSubscription(uuid.New().String())
	sub.Pattern = in.Pattern
	sub.Regex = in.Regex
	sub.Prefix = in.Prefix
	sub.Prompt = in.Prompt
	sub.Full = in.Full

	// Set debounce (default 5)
	if in.Debounce > 0 {
		sub.DebounceSeconds = in.Debounce
	}

	// Set interval (default 0 = disabled)
	if in.Interval > 0 {
		sub.IntervalSeconds = in.Interval
	}

	// Set wake (default true)
	if in.Wake != nil {
		sub.Wake = *in.Wake
	}

	if err := t.manager.Subscribe(sub); err != nil {
		return t.errorResult("error", err.Error())
	}

	L_info("hass: subscription created", "id", sub.ID, "pattern", sub.Pattern, "regex", sub.Regex, "hasPrompt", sub.Prompt != "")

	result := map[string]any{
		"status":     "subscribed",
		"id":         sub.ID,
		"pattern":    sub.Pattern,
		"regex":      sub.Regex,
		"debounce":   sub.DebounceSeconds,
		"interval":   sub.IntervalSeconds,
		"prompt":     sub.Prompt,
		"full":       sub.Full,
		"wake":       sub.Wake,
		"connected":  t.manager.IsConnected(),
		"created_at": sub.CreatedAt.Format(time.RFC3339),
	}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// unsubscribe removes an event subscription.
func (t *HASSTool) unsubscribe(ctx context.Context, in hassInput) (string, error) {
	if t.manager == nil {
		return t.errorResult("error", "subscription manager not configured")
	}

	if in.SubscriptionID == "" {
		return t.errorResult("error", "subscription_id is required for unsubscribe")
	}

	if err := t.manager.Unsubscribe(in.SubscriptionID); err != nil {
		return t.errorResult("error", err.Error())
	}

	L_info("hass: subscription removed", "id", in.SubscriptionID)

	result := map[string]any{
		"status": "unsubscribed",
		"id":     in.SubscriptionID,
	}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// enableSubscription enables a subscription by ID.
func (t *HASSTool) enableSubscription(ctx context.Context, in hassInput) (string, error) {
	if t.manager == nil {
		return t.errorResult("error", "subscription manager not configured")
	}

	if in.SubscriptionID == "" {
		return t.errorResult("error", "subscription_id is required for enable")
	}

	if err := t.manager.EnableSubscription(in.SubscriptionID); err != nil {
		return t.errorResult("error", err.Error())
	}

	L_info("hass: subscription enabled", "id", in.SubscriptionID)

	result := map[string]any{
		"status":  "enabled",
		"id":      in.SubscriptionID,
		"enabled": true,
	}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// disableSubscription disables a subscription by ID.
func (t *HASSTool) disableSubscription(ctx context.Context, in hassInput) (string, error) {
	if t.manager == nil {
		return t.errorResult("error", "subscription manager not configured")
	}

	if in.SubscriptionID == "" {
		return t.errorResult("error", "subscription_id is required for disable")
	}

	if err := t.manager.DisableSubscription(in.SubscriptionID); err != nil {
		return t.errorResult("error", err.Error())
	}

	L_info("hass: subscription disabled", "id", in.SubscriptionID)

	result := map[string]any{
		"status":  "disabled",
		"id":      in.SubscriptionID,
		"enabled": false,
	}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// listSubscriptions returns all active subscriptions.
func (t *HASSTool) listSubscriptions(ctx context.Context) (string, error) {
	if t.manager == nil {
		return t.errorResult("error", "subscription manager not configured")
	}

	subs := t.manager.GetSubscriptions()

	result := map[string]any{
		"count":         len(subs),
		"connected":     t.manager.IsConnected(),
		"subscriptions": subs,
	}
	jsonResult, _ := json.Marshal(result)
	return string(jsonResult), nil
}

// parseDateTime parses a date or datetime string.
func parseDateTime(s string) (time.Time, error) {
	// Try various formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}

	// Try parsing in local timezone
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("could not parse datetime: %s", s)
}
