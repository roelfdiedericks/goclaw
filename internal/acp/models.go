package acp

import (
	"fmt"
	"sort"
	"strings"
)

const DefaultCursorModel = "claude-4.6-opus-high-thinking"

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
