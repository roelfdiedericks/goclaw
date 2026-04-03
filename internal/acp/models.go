package acp

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const DefaultCursorModel = "claude-4.6-opus-high-thinking"

var modelCatalogCache = struct {
	mu     sync.RWMutex
	states map[string]*ACPModelState
}{
	states: map[string]*ACPModelState{},
}

var cursorKnownModelCatalog = &ACPModelState{
	CurrentValue: DefaultCursorModel,
	Options: []ACPModelChoice{
		{Value: "auto", Name: "Auto"},
		{Value: "composer-2-fast", Name: "Composer 2 Fast"},
		{Value: "composer-2", Name: "Composer 2"},
		{Value: "composer-1.5", Name: "Composer 1.5"},
		{Value: "gpt-5.3-codex-low", Name: "GPT-5.3 Codex Low"},
		{Value: "gpt-5.3-codex-low-fast", Name: "GPT-5.3 Codex Low Fast"},
		{Value: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
		{Value: "gpt-5.3-codex-fast", Name: "GPT-5.3 Codex Fast"},
		{Value: "gpt-5.3-codex-high", Name: "GPT-5.3 Codex High"},
		{Value: "gpt-5.3-codex-high-fast", Name: "GPT-5.3 Codex High Fast"},
		{Value: "gpt-5.3-codex-xhigh", Name: "GPT-5.3 Codex Extra High"},
		{Value: "gpt-5.3-codex-xhigh-fast", Name: "GPT-5.3 Codex Extra High Fast"},
		{Value: "gpt-5.2", Name: "GPT-5.2"},
		{Value: "gpt-5.3-codex-spark-preview-low", Name: "GPT-5.3 Codex Spark Low"},
		{Value: "gpt-5.3-codex-spark-preview", Name: "GPT-5.3 Codex Spark"},
		{Value: "gpt-5.3-codex-spark-preview-high", Name: "GPT-5.3 Codex Spark High"},
		{Value: "gpt-5.3-codex-spark-preview-xhigh", Name: "GPT-5.3 Codex Spark Extra High"},
		{Value: "gpt-5.2-codex-low", Name: "GPT-5.2 Codex Low"},
		{Value: "gpt-5.2-codex-low-fast", Name: "GPT-5.2 Codex Low Fast"},
		{Value: "gpt-5.2-codex", Name: "GPT-5.2 Codex"},
		{Value: "gpt-5.2-codex-fast", Name: "GPT-5.2 Codex Fast"},
		{Value: "gpt-5.2-codex-high", Name: "GPT-5.2 Codex High"},
		{Value: "gpt-5.2-codex-high-fast", Name: "GPT-5.2 Codex High Fast"},
		{Value: "gpt-5.2-codex-xhigh", Name: "GPT-5.2 Codex Extra High"},
		{Value: "gpt-5.2-codex-xhigh-fast", Name: "GPT-5.2 Codex Extra High Fast"},
		{Value: "gpt-5.1-codex-max-low", Name: "GPT-5.1 Codex Max Low"},
		{Value: "gpt-5.1-codex-max-low-fast", Name: "GPT-5.1 Codex Max Low Fast"},
		{Value: "gpt-5.1-codex-max-medium", Name: "GPT-5.1 Codex Max"},
		{Value: "gpt-5.1-codex-max-medium-fast", Name: "GPT-5.1 Codex Max Medium Fast"},
		{Value: "gpt-5.1-codex-max-high", Name: "GPT-5.1 Codex Max High"},
		{Value: "gpt-5.1-codex-max-high-fast", Name: "GPT-5.1 Codex Max High Fast"},
		{Value: "gpt-5.1-codex-max-xhigh", Name: "GPT-5.1 Codex Max Extra High"},
		{Value: "gpt-5.1-codex-max-xhigh-fast", Name: "GPT-5.1 Codex Max Extra High Fast"},
		{Value: "gpt-5.4-high", Name: "GPT-5.4 1M High"},
		{Value: "gpt-5.4-high-fast", Name: "GPT-5.4 High Fast"},
		{Value: "gpt-5.4-xhigh-fast", Name: "GPT-5.4 Extra High Fast"},
		{Value: "claude-4.6-opus-high-thinking", Name: "Opus 4.6 1M Thinking"},
		{Value: "gpt-5.4-low", Name: "GPT-5.4 1M Low"},
		{Value: "gpt-5.4-medium", Name: "GPT-5.4 1M"},
		{Value: "gpt-5.4-medium-fast", Name: "GPT-5.4 Fast"},
		{Value: "gpt-5.4-xhigh", Name: "GPT-5.4 1M Extra High"},
		{Value: "claude-4.6-sonnet-medium", Name: "Sonnet 4.6 1M"},
		{Value: "claude-4.6-sonnet-medium-thinking", Name: "Sonnet 4.6 1M Thinking"},
		{Value: "claude-4.6-opus-high", Name: "Opus 4.6 1M"},
		{Value: "claude-4.6-opus-max", Name: "Opus 4.6 1M Max"},
		{Value: "claude-4.6-opus-max-thinking", Name: "Opus 4.6 1M Max Thinking"},
		{Value: "claude-4.5-opus-high", Name: "Opus 4.5"},
		{Value: "claude-4.5-opus-high-thinking", Name: "Opus 4.5 Thinking"},
		{Value: "gpt-5.2-low", Name: "GPT-5.2 Low"},
		{Value: "gpt-5.2-low-fast", Name: "GPT-5.2 Low Fast"},
		{Value: "gpt-5.2-fast", Name: "GPT-5.2 Fast"},
		{Value: "gpt-5.2-high", Name: "GPT-5.2 High"},
		{Value: "gpt-5.2-high-fast", Name: "GPT-5.2 High Fast"},
		{Value: "gpt-5.2-xhigh", Name: "GPT-5.2 Extra High"},
		{Value: "gpt-5.2-xhigh-fast", Name: "GPT-5.2 Extra High Fast"},
		{Value: "gemini-3.1-pro", Name: "Gemini 3.1 Pro"},
		{Value: "gpt-5.4-mini-none", Name: "GPT-5.4 Mini None"},
		{Value: "gpt-5.4-mini-low", Name: "GPT-5.4 Mini Low"},
		{Value: "gpt-5.4-mini-medium", Name: "GPT-5.4 Mini"},
		{Value: "gpt-5.4-mini-high", Name: "GPT-5.4 Mini High"},
		{Value: "gpt-5.4-mini-xhigh", Name: "GPT-5.4 Mini Extra High"},
		{Value: "gpt-5.4-nano-none", Name: "GPT-5.4 Nano None"},
		{Value: "gpt-5.4-nano-low", Name: "GPT-5.4 Nano Low"},
		{Value: "gpt-5.4-nano-medium", Name: "GPT-5.4 Nano"},
		{Value: "gpt-5.4-nano-high", Name: "GPT-5.4 Nano High"},
		{Value: "gpt-5.4-nano-xhigh", Name: "GPT-5.4 Nano Extra High"},
		{Value: "grok-4-20", Name: "Grok 4.20"},
		{Value: "grok-4-20-thinking", Name: "Grok 4.20 Thinking"},
		{Value: "claude-4.5-sonnet", Name: "Sonnet 4.5 1M"},
		{Value: "claude-4.5-sonnet-thinking", Name: "Sonnet 4.5 1M Thinking"},
		{Value: "gpt-5.1-low", Name: "GPT-5.1 Low"},
		{Value: "gpt-5.1", Name: "GPT-5.1"},
		{Value: "gpt-5.1-high", Name: "GPT-5.1 High"},
		{Value: "gemini-3-flash", Name: "Gemini 3 Flash"},
		{Value: "gpt-5.1-codex-mini-low", Name: "GPT-5.1 Codex Mini Low"},
		{Value: "gpt-5.1-codex-mini", Name: "GPT-5.1 Codex Mini"},
		{Value: "gpt-5.1-codex-mini-high", Name: "GPT-5.1 Codex Mini High"},
		{Value: "claude-4-sonnet", Name: "Sonnet 4"},
		{Value: "claude-4-sonnet-1m", Name: "Sonnet 4 1M"},
		{Value: "claude-4-sonnet-thinking", Name: "Sonnet 4 Thinking"},
		{Value: "claude-4-sonnet-1m-thinking", Name: "Sonnet 4 1M Thinking"},
		{Value: "gpt-5-mini", Name: "GPT-5 Mini"},
		{Value: "kimi-k2.5", Name: "Kimi K2.5"},
	},
}

func cloneModelState(state *ACPModelState) *ACPModelState {
	if state == nil {
		return nil
	}
	cloned := &ACPModelState{
		CurrentValue: state.CurrentValue,
	}
	if len(state.Options) > 0 {
		cloned.Options = append([]ACPModelChoice(nil), state.Options...)
	}
	return cloned
}

func buildFriendlyModelOptions(state *ACPModelState) []ACPModelOption {
	if state == nil || len(state.Options) == 0 {
		return nil
	}
	out := make([]ACPModelOption, 0, len(state.Options))
	for _, option := range state.Options {
		friendlyID := friendlyModelID(option.Value)
		if friendlyID == "" {
			friendlyID = strings.TrimSpace(option.Value)
		}
		out = append(out, ACPModelOption{
			FriendlyID:  friendlyID,
			Name:        option.Name,
			ACPValue:    option.Value,
			Description: option.Description,
			Current:     option.Value == state.CurrentValue,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].FriendlyID < out[j].FriendlyID
	})
	return out
}

func cachedModelCatalog(driverID string) *ACPModelState {
	modelCatalogCache.mu.RLock()
	defer modelCatalogCache.mu.RUnlock()
	return cloneModelState(modelCatalogCache.states[normalizeModelToken(driverID)])
}

func setCachedModelCatalog(driverID string, state *ACPModelState) {
	modelCatalogCache.mu.Lock()
	defer modelCatalogCache.mu.Unlock()
	key := normalizeModelToken(driverID)
	if key == "" {
		return
	}
	if state == nil {
		delete(modelCatalogCache.states, key)
		return
	}
	modelCatalogCache.states[key] = cloneModelState(state)
}

func knownModelCatalog(driverID string) *ACPModelState {
	switch normalizeModelToken(driverID) {
	case DriverCursor:
		return cloneModelState(cursorKnownModelCatalog)
	default:
		return nil
	}
}

func resolveFriendlyModelValue(input string, state *ACPModelState) (ACPModelChoice, error) {
	if state == nil || len(state.Options) == 0 {
		return ACPModelChoice{}, fmt.Errorf("ACP session does not advertise any model options")
	}
	needle := normalizeModelToken(input)
	if needle == "" {
		return ACPModelChoice{}, fmt.Errorf("model is required")
	}
	for _, option := range state.Options {
		if normalizeModelToken(option.Value) == needle || normalizeModelToken(friendlyModelID(option.Value)) == needle {
			return option, nil
		}
	}
	return ACPModelChoice{}, fmt.Errorf("unknown ACP model: %s", input)
}

func friendlyModelID(acpValue string) string {
	acpValue = strings.TrimSpace(acpValue)
	if acpValue == "" {
		return ""
	}
	if acpValue == "default[]" {
		return "auto"
	}

	base, params := splitACPModelValue(acpValue)
	switch {
	case strings.HasPrefix(base, "claude-opus-"):
		version := strings.TrimPrefix(base, "claude-opus-")
		base = "claude-" + strings.Replace(version, "-", ".", 1) + "-opus"
	case strings.HasPrefix(base, "claude-sonnet-"):
		version := strings.TrimPrefix(base, "claude-sonnet-")
		base = "claude-" + strings.Replace(version, "-", ".", 1) + "-sonnet"
	case strings.HasPrefix(base, "claude-haiku-"):
		version := strings.TrimPrefix(base, "claude-haiku-")
		base = "claude-" + strings.Replace(version, "-", ".", 1) + "-haiku"
	}

	suffixes := make([]string, 0, 4)
	if effort := strings.TrimSpace(params["effort"]); effort != "" {
		suffixes = append(suffixes, effort)
	}
	if reasoning := strings.TrimSpace(params["reasoning"]); reasoning != "" && reasoning != "none" {
		suffixes = append(suffixes, reasoning)
	}
	if strings.EqualFold(strings.TrimSpace(params["thinking"]), "true") {
		suffixes = append(suffixes, "thinking")
	}
	if strings.EqualFold(strings.TrimSpace(params["fast"]), "true") {
		suffixes = append(suffixes, "fast")
	}
	if len(suffixes) == 0 {
		return base
	}
	return base + "-" + strings.Join(suffixes, "-")
}

func splitACPModelValue(acpValue string) (string, map[string]string) {
	acpValue = strings.TrimSpace(acpValue)
	if acpValue == "" {
		return "", map[string]string{}
	}
	open := strings.Index(acpValue, "[")
	close := strings.LastIndex(acpValue, "]")
	if open < 0 || close <= open {
		return acpValue, map[string]string{}
	}
	base := strings.TrimSpace(acpValue[:open])
	rawParams := strings.TrimSpace(acpValue[open+1 : close])
	params := map[string]string{}
	if rawParams == "" {
		return base, params
	}
	for _, part := range strings.Split(rawParams, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		params[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return base, params
}

func normalizeModelToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
