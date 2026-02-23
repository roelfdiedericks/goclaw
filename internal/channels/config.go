package channels

import (
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
)

// Config aggregates all channel configurations
type Config struct {
	Telegram telegram.Config `json:"telegram"`
	// HTTP     http.Config     `json:"http"`     // Added in Phase 3
	// TUI      tui.Config      `json:"tui"`      // Added in Phase 4
}
