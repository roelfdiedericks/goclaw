package channels

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/channels/http"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/tui"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/types"
	"github.com/roelfdiedericks/goclaw/internal/channels/whatsapp"
	whatsappconfig "github.com/roelfdiedericks/goclaw/internal/channels/whatsapp/config"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ManagedChannel is re-exported from types for convenience
type ManagedChannel = types.ManagedChannel

// ChannelStatus is re-exported from types for convenience
type ChannelStatus = types.ChannelStatus

// RuntimeOptions contains runtime parameters that aren't persisted in config
type RuntimeOptions struct {
	DevMode bool // Enable development mode for HTTP server
}

// Manager owns the lifecycle of all communication channels
type Manager struct {
	gw    *gateway.Gateway
	users *user.Registry

	channels map[string]ManagedChannel
	mu       sync.RWMutex

	// Runtime options (not persisted)
	opts RuntimeOptions

	// Telegram-specific: bot instance and retry state
	telegramBot      *telegram.Bot
	telegramRetrying bool
	telegramCancel   context.CancelFunc

	// WhatsApp-specific: bot instance and retry state
	whatsappBot      *whatsapp.Bot
	whatsappRetrying bool
	whatsappCancel   context.CancelFunc

	// HTTP server instance
	httpServer *http.Server

	// TUI instance (only during RunTUI)
	tuiInstance *tui.TUI

	// Context for channel operations
	ctx context.Context
}

// NewManager creates a new channel manager
func NewManager(gw *gateway.Gateway, users *user.Registry) *Manager {
	return &Manager{
		gw:       gw,
		users:    users,
		channels: make(map[string]ManagedChannel),
	}
}

// StartAll starts all enabled channels from config (except TUI - use RunTUI for that)
func (m *Manager) StartAll(ctx context.Context, cfg config.ChannelsConfig, opts RuntimeOptions) error {
	m.ctx = ctx
	m.opts = opts

	// Start Telegram if enabled
	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
		if err := m.startTelegram(ctx, &cfg.Telegram); err != nil {
			logging.L_warn("telegram: initial start failed, will retry in background", "error", err)
			m.startTelegramRetry(ctx, &cfg.Telegram)
		}
	} else {
		logging.L_info("telegram: disabled by configuration")
	}

	// Start WhatsApp if enabled
	if cfg.WhatsApp.Enabled {
		if err := m.startWhatsApp(ctx, &cfg.WhatsApp); err != nil {
			logging.L_warn("whatsapp: initial start failed, will retry in background", "error", err)
			m.startWhatsAppRetry(ctx, &cfg.WhatsApp)
		}
	} else {
		logging.L_info("whatsapp: disabled by configuration")
	}

	// Start HTTP if enabled (default: true)
	httpEnabled := cfg.HTTP.Enabled == nil || *cfg.HTTP.Enabled
	if httpEnabled {
		if err := m.startHTTP(ctx, &cfg.HTTP); err != nil {
			logging.L_error("http: start failed", "error", err)
			// HTTP failure is not fatal
		}
	} else {
		logging.L_info("http: disabled by configuration")
	}

	// Subscribe to config reload events
	m.subscribeConfigEvents()

	return nil
}

// startTelegram creates and starts the Telegram bot
func (m *Manager) startTelegram(ctx context.Context, cfg *telegramconfig.Config) error {
	bot, err := telegram.New(cfg, m.gw, m.users)
	if err != nil {
		return err
	}

	if err := bot.Start(ctx); err != nil {
		return err
	}

	bot.RegisterOperationalCommands()
	m.gw.RegisterChannel(bot)

	m.mu.Lock()
	m.telegramBot = bot
	m.channels["telegram"] = bot
	m.mu.Unlock()

	// Publish event for message tool hot-reload
	bus.PublishEvent("channels.telegram.started", nil)

	logging.L_info("telegram: bot ready and listening")
	return nil
}

// startTelegramRetry starts background retry for telegram connection
func (m *Manager) startTelegramRetry(ctx context.Context, cfg *telegramconfig.Config) {
	m.mu.Lock()
	if m.telegramRetrying {
		m.mu.Unlock()
		return
	}
	m.telegramRetrying = true
	retryCtx, cancel := context.WithCancel(ctx)
	m.telegramCancel = cancel
	m.mu.Unlock()

	go func() {
		backoff := 5 * time.Second
		maxBackoff := 5 * time.Minute
		attempt := 1

		for {
			select {
			case <-retryCtx.Done():
				logging.L_info("telegram: shutdown requested, stopping retry")
				return
			case <-time.After(backoff):
			}

			logging.L_info("telegram: retrying connection", "attempt", attempt, "backoff", backoff)

			if err := m.startTelegram(retryCtx, cfg); err != nil {
				logging.L_warn("telegram: connection failed", "error", err, "nextRetry", backoff)
				attempt++
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			// Success
			m.mu.Lock()
			m.telegramRetrying = false
			m.mu.Unlock()
			logging.L_info("telegram: bot ready after retry", "attempts", attempt)
			return
		}
	}()
}

// startWhatsApp creates and starts the WhatsApp bot
func (m *Manager) startWhatsApp(ctx context.Context, cfg *whatsappconfig.Config) error {
	bot, err := whatsapp.New(cfg, m.gw, m.users)
	if err != nil {
		return err
	}

	if err := bot.Start(ctx); err != nil {
		return err
	}

	bot.RegisterOperationalCommands()
	m.gw.RegisterChannel(bot)

	m.mu.Lock()
	m.whatsappBot = bot
	m.channels["whatsapp"] = bot
	m.mu.Unlock()

	bus.PublishEvent("channels.whatsapp.started", nil)

	logging.L_info("whatsapp: channel ready and listening")
	return nil
}

// startWhatsAppRetry starts background retry for whatsapp connection
func (m *Manager) startWhatsAppRetry(ctx context.Context, cfg *whatsappconfig.Config) {
	m.mu.Lock()
	if m.whatsappRetrying {
		m.mu.Unlock()
		return
	}
	m.whatsappRetrying = true
	retryCtx, cancel := context.WithCancel(ctx)
	m.whatsappCancel = cancel
	m.mu.Unlock()

	go func() {
		backoff := 5 * time.Second
		maxBackoff := 5 * time.Minute
		attempt := 1

		for {
			select {
			case <-retryCtx.Done():
				logging.L_info("whatsapp: shutdown requested, stopping retry")
				return
			case <-time.After(backoff):
			}

			logging.L_info("whatsapp: retrying connection", "attempt", attempt, "backoff", backoff)

			if err := m.startWhatsApp(retryCtx, cfg); err != nil {
				logging.L_warn("whatsapp: connection failed", "error", err, "nextRetry", backoff)
				attempt++
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			m.mu.Lock()
			m.whatsappRetrying = false
			m.mu.Unlock()
			logging.L_info("whatsapp: channel ready after retry", "attempts", attempt)
			return
		}
	}()
}

// reloadWhatsApp handles whatsapp config changes
func (m *Manager) reloadWhatsApp(cfg *whatsappconfig.Config) {
	m.mu.Lock()
	bot := m.whatsappBot
	m.mu.Unlock()

	if m.whatsappCancel != nil {
		m.whatsappCancel()
	}

	if bot != nil {
		logging.L_info("whatsapp: stopping for config reload")
		_ = bot.Stop()
		m.gw.UnregisterChannel("whatsapp")
		m.mu.Lock()
		m.whatsappBot = nil
		delete(m.channels, "whatsapp")
		m.mu.Unlock()
		bus.PublishEvent("channels.whatsapp.stopped", nil)
	}

	if !cfg.Enabled {
		logging.L_info("whatsapp: disabled by new config")
		return
	}

	if err := m.startWhatsApp(m.ctx, cfg); err != nil {
		logging.L_error("whatsapp: failed to start with new config", "error", err)
		m.startWhatsAppRetry(m.ctx, cfg)
	} else {
		logging.L_info("whatsapp: reloaded with new config")
	}
}

// startHTTP creates and starts the HTTP server
func (m *Manager) startHTTP(ctx context.Context, cfg *httpconfig.Config) error {
	listen := cfg.Listen
	if listen == "" {
		listen = ":1337"
	}

	serverCfg := &http.ServerConfig{
		Listen:    listen,
		DevMode:   m.opts.DevMode,
		MediaRoot: "",
	}

	if m.gw.MediaStore() != nil {
		serverCfg.MediaRoot = m.gw.MediaStore().BaseDir()
	}

	srv, err := http.NewServer(serverCfg, m.users)
	if err != nil {
		return err
	}

	srv.SetGateway(m.gw)
	m.gw.RegisterChannel(srv.Channel())

	if err := srv.Start(ctx); err != nil {
		return err
	}

	srv.RegisterOperationalCommands()

	m.mu.Lock()
	m.httpServer = srv
	m.channels["http"] = srv
	m.mu.Unlock()

	// Publish event for message tool hot-reload
	bus.PublishEvent("channels.http.started", nil)

	if m.opts.DevMode {
		logging.L_info("http: server started (dev mode)", "listen", listen)
	} else {
		logging.L_info("http: server started", "listen", listen)
	}
	return nil
}

// subscribeConfigEvents sets up handlers for config.applied events
func (m *Manager) subscribeConfigEvents() {
	// Telegram config reload
	bus.SubscribeEvent("channels.telegram.config.applied", func(event bus.Event) {
		cfg, ok := event.Data.(*telegramconfig.Config)
		if !ok {
			logging.L_error("telegram: invalid config event data")
			return
		}
		m.reloadTelegram(cfg)
	})

	// WhatsApp config reload
	bus.SubscribeEvent("channels.whatsapp.config.applied", func(event bus.Event) {
		cfg, ok := event.Data.(*whatsappconfig.Config)
		if !ok {
			logging.L_error("whatsapp: invalid config event data")
			return
		}
		m.reloadWhatsApp(cfg)
	})

	// HTTP config reload
	bus.SubscribeEvent("channels.http.config.applied", func(event bus.Event) {
		cfg, ok := event.Data.(*httpconfig.Config)
		if !ok {
			logging.L_error("http: invalid config event data")
			return
		}
		m.reloadHTTP(cfg)
	})
}

// reloadTelegram handles telegram config changes
func (m *Manager) reloadTelegram(cfg *telegramconfig.Config) {
	m.mu.Lock()
	bot := m.telegramBot
	m.mu.Unlock()

	// Stop retry if running
	if m.telegramCancel != nil {
		m.telegramCancel()
	}

	// Stop existing bot
	if bot != nil {
		logging.L_info("telegram: stopping for config reload")
		_ = bot.Stop()
		m.gw.UnregisterChannel("telegram")
		m.mu.Lock()
		m.telegramBot = nil
		delete(m.channels, "telegram")
		m.mu.Unlock()

		// Publish stopped event for message tool
		bus.PublishEvent("channels.telegram.stopped", nil)
	}

	// If disabled, just stop
	if !cfg.Enabled || cfg.BotToken == "" {
		logging.L_info("telegram: disabled by new config")
		return
	}

	// Start with new config (startTelegram publishes started event)
	if err := m.startTelegram(m.ctx, cfg); err != nil {
		logging.L_error("telegram: failed to start with new config", "error", err)
		m.startTelegramRetry(m.ctx, cfg)
	} else {
		logging.L_info("telegram: reloaded with new config")
	}
}

// reloadHTTP handles HTTP config changes
func (m *Manager) reloadHTTP(cfg *httpconfig.Config) {
	m.mu.Lock()
	srv := m.httpServer
	m.mu.Unlock()

	if srv != nil {
		_ = srv.Reload(cfg)
	}
}

// StopAll gracefully shuts down all running channels
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop retries if running
	if m.telegramCancel != nil {
		m.telegramCancel()
	}
	if m.whatsappCancel != nil {
		m.whatsappCancel()
	}

	for name, ch := range m.channels {
		logging.L_debug("channels: stopping", "channel", name)
		if err := ch.Stop(); err != nil {
			logging.L_error("channels: stop failed", "channel", name, "error", err)
		}
		m.gw.UnregisterChannel(name)

		// Publish stopped event for message tool
		bus.PublishEvent("channels."+name+".stopped", nil)
	}
	m.channels = make(map[string]ManagedChannel)
	m.telegramBot = nil
	m.whatsappBot = nil
	m.httpServer = nil
}

// RunTUI starts the TUI channel and blocks until it exits
func (m *Manager) RunTUI(ctx context.Context, cfg *tuiconfig.Config) error {
	t := tui.NewTUI(m.gw, m.users, cfg)

	m.mu.Lock()
	m.tuiInstance = t
	m.channels["tui"] = t
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.tuiInstance = nil
		delete(m.channels, "tui")
		m.mu.Unlock()
	}()

	return t.RunBlocking(ctx)
}

// Reload applies new configuration to a running channel
func (m *Manager) Reload(name string, cfg any) error {
	m.mu.RLock()
	ch, exists := m.channels[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %q not running", name)
	}

	return ch.Reload(cfg)
}

// Get returns a channel by name, or nil if not found
func (m *Manager) Get(name string) ManagedChannel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.channels[name]
}

// GetTelegram returns the Telegram bot (for message tool adapter)
func (m *Manager) GetTelegram() *telegram.Bot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.telegramBot
}

// GetWhatsApp returns the WhatsApp bot (for message tool adapter)
func (m *Manager) GetWhatsApp() *whatsapp.Bot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.whatsappBot
}

// GetHTTP returns the HTTP server
func (m *Manager) GetHTTP() *http.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.httpServer
}

// Status returns the status of all channels
func (m *Manager) Status() map[string]ChannelStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]ChannelStatus, len(m.channels))
	for name, ch := range m.channels {
		result[name] = ch.Status()
	}
	return result
}

// RegisterCommands registers bus commands for all channel types
func (m *Manager) RegisterCommands() {
	telegramconfig.RegisterCommands()
	whatsappconfig.RegisterCommands()
	httpconfig.RegisterCommands()
	tuiconfig.RegisterCommands()
}

// UnregisterCommands unregisters all bus commands
func (m *Manager) UnregisterCommands() {
	telegramconfig.UnregisterCommands()
	whatsappconfig.UnregisterCommands()
	httpconfig.UnregisterCommands()
	tuiconfig.UnregisterCommands()
}
