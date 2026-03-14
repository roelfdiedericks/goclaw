package forms

import (
	"fmt"
	"strings"
)

// ValidateJSONPointer validates strict JSON Pointer syntax used in setup sections.
// Allowed: "/" root, or "/a/b/c". Empty path is invalid.
func ValidateJSONPointer(pointer string) error {
	if pointer == "" {
		return fmt.Errorf("empty config path is not allowed; use '/' for root")
	}
	if pointer[0] != '/' {
		return fmt.Errorf("config path must be a JSON Pointer starting with '/'")
	}
	// "/" root is valid.
	if pointer == "/" {
		return nil
	}
	parts := strings.Split(pointer, "/")[1:]
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid JSON Pointer: empty segment")
		}
		// RFC6901 escape validation
		if strings.Contains(p, "~") {
			for i := 0; i < len(p); i++ {
				if p[i] == '~' {
					if i+1 >= len(p) || (p[i+1] != '0' && p[i+1] != '1') {
						return fmt.Errorf("invalid JSON Pointer escape in segment %q", p)
					}
				}
			}
		}
	}
	return nil
}

func decodePointerToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}

// JSONPointerGet traverses map-backed JSON data using a strict JSON Pointer.
func JSONPointerGet(root map[string]interface{}, pointer string) (interface{}, error) {
	if err := ValidateJSONPointer(pointer); err != nil {
		return nil, err
	}
	if pointer == "/" {
		return root, nil
	}

	var current interface{} = root
	for _, raw := range strings.Split(pointer, "/")[1:] {
		key := decodePointerToken(raw)
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("pointer segment %q is not traversable", key)
		}
		next, ok := m[key]
		if !ok {
			return nil, nil
		}
		current = next
	}
	return current, nil
}

// JSONPointerSet writes value at pointer in map-backed JSON data.
// For "/", value must be a map; the root map is replaced in-place.
func JSONPointerSet(root map[string]interface{}, pointer string, value interface{}) error {
	if err := ValidateJSONPointer(pointer); err != nil {
		return err
	}
	if pointer == "/" {
		mv, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("root update expects object payload")
		}
		for k := range root {
			delete(root, k)
		}
		for k, v := range mv {
			root[k] = v
		}
		return nil
	}

	parts := strings.Split(pointer, "/")[1:]
	current := root
	for i := 0; i < len(parts)-1; i++ {
		key := decodePointerToken(parts[i])
		next, ok := current[key]
		if !ok {
			newMap := map[string]interface{}{}
			current[key] = newMap
			current = newMap
			continue
		}
		nextMap, ok := next.(map[string]interface{})
		if !ok {
			return fmt.Errorf("pointer segment %q is not an object", key)
		}
		current = nextMap
	}
	lastKey := decodePointerToken(parts[len(parts)-1])
	current[lastKey] = value
	return nil
}
