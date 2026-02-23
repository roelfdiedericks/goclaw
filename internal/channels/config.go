package channels

import (
	"github.com/roelfdiedericks/goclaw/internal/channels/http"
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	"github.com/roelfdiedericks/goclaw/internal/channels/tui"
)

// Config aggregates all channel configurations
type Config struct {
	Telegram telegram.Config `json:"telegram"`
	HTTP     http.Config     `json:"http"`
	TUI      tui.Config      `json:"tui"`
}
