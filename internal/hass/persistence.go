package hass

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// LoadSubscriptions loads subscriptions from a JSON file.
// Returns an empty slice if the file doesn't exist.
func LoadSubscriptions(path string) ([]Subscription, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			L_debug("hass: no subscription file found", "path", path)
			return []Subscription{}, nil
		}
		return nil, err
	}

	var file SubscriptionFile
	if err := json.Unmarshal(data, &file); err != nil {
		L_error("hass: failed to parse subscription file", "path", path, "error", err)
		return nil, err
	}

	L_debug("hass: loaded subscriptions", "count", len(file.Subscriptions), "path", path)
	return file.Subscriptions, nil
}

// SaveSubscriptions saves subscriptions to a JSON file.
// Creates the parent directory if it doesn't exist.
func SaveSubscriptions(path string, subs []Subscription) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	file := SubscriptionFile{
		Subscriptions: subs,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}

	L_debug("hass: saved subscriptions", "count", len(subs), "path", path)
	return nil
}
