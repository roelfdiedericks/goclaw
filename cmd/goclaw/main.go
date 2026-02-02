package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/sevlyar/go-daemon"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/telegram"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/tui"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

const version = "0.0.1"

// Default paths
func pidFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "goclaw.pid")
}

func logFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "goclaw.log")
}

// CLI defines the command-line interface
type CLI struct {
	Debug  bool   `help:"Enable debug logging" short:"d"`
	Trace  bool   `help:"Enable trace logging" short:"t"`
	Config string `help:"Config file path" short:"c" type:"path"`

	Gateway GatewayCmd `cmd:"" help:"Run the gateway (foreground by default)"`
	Start   StartCmd   `cmd:"" help:"Start gateway as background daemon"`
	Stop    StopCmd    `cmd:"" help:"Stop the background daemon"`
	Status  StatusCmd  `cmd:"" help:"Show gateway status"`
	Version VersionCmd `cmd:"" help:"Show version"`
}

// GatewayCmd runs gateway in foreground
type GatewayCmd struct {
	TUI bool `help:"Run with interactive TUI" short:"i"`
}

func (g *GatewayCmd) Run(ctx *Context) error {
	return runGateway(ctx, g.TUI)
}

// StartCmd daemonizes the gateway
type StartCmd struct{}

func (s *StartCmd) Run(ctx *Context) error {
	// Check if already running
	if isRunning() {
		L_error("gateway already running")
		return fmt.Errorf("already running")
	}

	cntxt := &daemon.Context{
		PidFileName: pidFile(),
		PidFilePerm: 0644,
		LogFileName: logFile(),
		LogFilePerm: 0640,
		WorkDir:     "./",
		Umask:       027,
	}

	d, err := cntxt.Reborn()
	if err != nil {
		L_fatal("daemonize failed", "error", err)
	}
	if d != nil {
		// Parent process
		L_info("gateway started", "pid", d.Pid)
		return nil
	}
	// Child process continues
	defer cntxt.Release()

	L_info("daemon started", "pid", os.Getpid())
	return runGateway(ctx, false)
}

// StopCmd stops the daemon
type StopCmd struct{}

func (s *StopCmd) Run(ctx *Context) error {
	pid, running := getPid()
	if !running {
		L_info("gateway not running")
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("failed to stop: %w", err)
	}

	L_info("gateway stopped", "pid", pid)
	os.Remove(pidFile())
	return nil
}

// StatusCmd shows gateway status
type StatusCmd struct{}

func (s *StatusCmd) Run(ctx *Context) error {
	pid, running := getPid()
	if running {
		L_info("gateway running", "pid", pid)
	} else {
		L_info("gateway not running")
	}
	return nil
}

// VersionCmd shows version info
type VersionCmd struct{}

func (v *VersionCmd) Run(ctx *Context) error {
	fmt.Printf("goclaw %s\n", version)
	return nil
}

// Context passed to all commands
type Context struct {
	Debug  bool
	Trace  bool
	Config string
}

// runGateway is the actual gateway logic
func runGateway(ctx *Context, useTUI bool) error {
	L_info("starting gateway", "version", version)

	// Load config
	cfg, err := config.Load()
	if err != nil {
		L_error("failed to load config", "error", err)
		return err
	}
	L_debug("config loaded", "users", len(cfg.Users))

	// Create user registry
	users := user.NewRegistry(cfg)
	L_debug("user registry created", "users", users.Count())

	// Create LLM client
	llmClient, err := llm.NewClient(&cfg.LLM)
	if err != nil {
		L_error("failed to create LLM client", "error", err)
		return err
	}
	L_debug("LLM client created", "model", cfg.LLM.Model)

	// Create browser pool if enabled
	var browserPool *tools.BrowserPool
	if cfg.Tools.Browser.Enabled {
		var err error
		browserPool, err = tools.NewBrowserPool(tools.BrowserPoolConfig{
			Headless:  cfg.Tools.Browser.Headless,
			NoSandbox: cfg.Tools.Browser.NoSandbox,
			Profile:   cfg.Tools.Browser.Profile,
		})
		if err != nil {
			L_warn("failed to create browser pool, browser tool disabled", "error", err)
		} else {
			defer browserPool.Close()
		}
	}

	// Create tool registry and register base defaults (no media-dependent tools yet)
	toolsReg := tools.NewRegistry()
	tools.RegisterDefaults(toolsReg, tools.ToolsConfig{
		WorkingDir:     cfg.Gateway.WorkingDir,
		BraveAPIKey:    cfg.Tools.Web.BraveAPIKey,
		BrowserPool:    nil,       // Browser registered after gateway (needs MediaStore)
		BrowserEnabled: false,     // Deferred
	})
	L_debug("base tools registered", "count", toolsReg.Count())

	// Create gateway (creates MediaStore internally)
	gw, err := gateway.New(cfg, users, llmClient, toolsReg)
	if err != nil {
		L_error("failed to create gateway", "error", err)
		os.Exit(1)
	}
	L_info("gateway initialized")

	// Register browser tool (needs gateway's MediaStore)
	if cfg.Tools.Browser.Enabled && browserPool != nil {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			toolsReg.Register(tools.NewBrowserTool(browserPool, mediaStore))
			L_debug("browser tool registered")
		}
	}

	// Register memory tools (needs gateway's memory manager)
	if memMgr := gw.MemoryManager(); memMgr != nil {
		toolsReg.Register(tools.NewMemorySearchTool(memMgr))
		toolsReg.Register(tools.NewMemoryGetTool(memMgr))
		L_debug("memory tools registered")
	}

	// Register skills tool (needs gateway's skills manager)
	if skillsMgr := gw.SkillManager(); skillsMgr != nil {
		toolsReg.Register(tools.NewSkillsTool(skillsMgr))
		L_debug("skills tool registered")
	}

	// Setup context with cancellation for graceful shutdown
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start session file watcher for live OpenClaw sync
	if err := gw.StartSessionWatcher(runCtx); err != nil {
		L_warn("failed to start session watcher", "error", err)
		// Continue anyway - we just won't get live updates
	}

	// Start gateway background tasks (compaction retry, etc.)
	gw.Start(runCtx)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		L_info("received signal", "signal", sig)
		gw.Shutdown()
		cancel()
	}()

	// Start Telegram bot if configured
	var telegramBot *telegram.Bot
	messageChannels := make(map[string]tools.MessageChannel)

	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
		var err error
		telegramBot, err = telegram.New(&cfg.Telegram, gw, users)
		if err != nil {
			L_error("failed to create telegram bot", "error", err)
		} else {
			telegramBot.Start()
			gw.RegisterChannel(telegramBot)
			L_info("telegram bot started")
			defer telegramBot.Stop()

			// Create Telegram message channel adapter for message tool
			if mediaStore := gw.MediaStore(); mediaStore != nil {
				adapter := telegram.NewMessageChannelAdapter(telegramBot, mediaStore.BaseDir())
				messageChannels["telegram"] = adapter
			}
		}
	} else {
		L_debug("telegram not enabled or no token configured")
	}

	// Register message tool with available channels
	if len(messageChannels) > 0 {
		messageTool := tools.NewMessageTool(messageChannels)
		toolsReg.Register(messageTool)
		L_debug("message tool registered", "channels", len(messageChannels))
	}

	if useTUI {
		// Run TUI mode
		L_info("starting TUI mode")
		return runTUI(runCtx, gw, users, cfg.TUI.ShowLogs)
	}

	// Non-TUI mode: just wait for signals
	L_info("gateway ready", "port", cfg.Gateway.Port)
	L_info("press Ctrl+C to stop")

	<-runCtx.Done()
	L_info("gateway shutting down")
	return nil
}

// runTUI runs the interactive TUI mode
func runTUI(ctx context.Context, gw *gateway.Gateway, users *user.Registry, showLogs bool) error {
	owner := users.Owner()
	if owner == nil {
		return fmt.Errorf("no owner user configured")
	}
	return tui.Run(ctx, gw, owner, showLogs)
}

// getPid returns the pid and whether the process is running
func getPid() (int, bool) {
	pidBytes, err := os.ReadFile(pidFile())
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		return 0, false
	}

	// Check if process is alive
	process, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		os.Remove(pidFile())
		return pid, false
	}

	return pid, true
}

// isRunning checks if gateway is already running
func isRunning() bool {
	_, running := getPid()
	return running
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("goclaw"),
		kong.Description("A Go rewrite of OpenClaw"),
		kong.UsageOnError(),
	)

	// Initialize logging based on flags
	level := LevelInfo
	if cli.Trace {
		level = LevelTrace
	} else if cli.Debug {
		level = LevelDebug
	}

	Init(&Config{
		Level:      level,
		ShowCaller: true,
	})

	// Run the selected command
	err := ctx.Run(&Context{
		Debug:  cli.Debug,
		Trace:  cli.Trace,
		Config: cli.Config,
	})
	if err != nil {
		L_fatal("command failed", "error", err)
	}
}
