package web

import (
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/auth"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	whatsappconfig "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp/config"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/localllm"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memorygraph"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/stt"
	toolsconfig "github.com/roelfdiedericks/goclaw/internal/tools/config"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/voicellm"
)

var registerWebActionCommandsOnce sync.Once

func registerWebActionCommands() {
	registerWebActionCommandsOnce.Do(func() {
		telegramconfig.RegisterCommands()
		whatsappconfig.RegisterCommands()
		httpconfig.RegisterCommands()
		tuiconfig.RegisterCommands()
		llm.RegisterCommands()
		localllm.RegisterCommands()
		acp.RegisterCommands()
		a2a.RegisterCommands()
		gateway.RegisterCommands()
		session.RegisterCommands()
		media.RegisterCommands()
		skills.RegisterCommands()
		sandbox.RegisterCommands()
		cron.RegisterCommands()
		auth.RegisterCommands()
		transcript.RegisterCommands()
		memorygraph.RegisterCommands()
		stt.RegisterCommands()
		voicellm.RegisterCommands()
		toolsconfig.RegisterCommands()
	})
}
