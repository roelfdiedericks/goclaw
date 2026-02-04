package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/sevlyar/go-daemon"
	"golang.org/x/term"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	goclawhttp "github.com/roelfdiedericks/goclaw/internal/http"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/telegram"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
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
	Cron    CronCmd    `cmd:"" help:"Manage cron jobs"`
	User    UserCmd    `cmd:"" help:"Manage users"`
}

// GatewayCmd runs gateway in foreground
type GatewayCmd struct {
	TUI bool `help:"Run with interactive TUI" short:"i"`
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (g *GatewayCmd) Run(ctx *Context) error {
	return runGateway(ctx, g.TUI, g.Dev)
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
	return runGateway(ctx, false, false) // daemon never uses TUI or dev mode
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

// CronCmd manages cron jobs
type CronCmd struct {
	List   CronListCmd   `cmd:"" help:"List all cron jobs"`
	Add    CronAddCmd    `cmd:"" help:"Add a new cron job"`
	Edit   CronEditCmd   `cmd:"" help:"Edit an existing cron job"`
	Remove CronRemoveCmd `cmd:"" help:"Remove a cron job"`
	Run    CronRunCmd    `cmd:"" help:"Run a job immediately"`
	Runs   CronRunsCmd   `cmd:"" help:"View job execution history"`
}

// CronListCmd lists all cron jobs
type CronListCmd struct{}

func (c *CronListCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	jobs := store.GetAllJobs()
	if len(jobs) == 0 {
		fmt.Println("No cron jobs configured.")
		return nil
	}

	fmt.Printf("Found %d job(s):\n\n", len(jobs))
	for _, job := range jobs {
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}
		fmt.Printf("%s (%s)\n", job.Name, status)
		fmt.Printf("  ID: %s\n", job.ID)
		fmt.Printf("  Session: %s\n", job.SessionTarget)
		fmt.Printf("  Schedule: %s\n", formatCronSchedule(&job.Schedule))
		if job.State.NextRunAtMs != nil {
			fmt.Printf("  Next run: %s\n", time.UnixMilli(*job.State.NextRunAtMs).Format(time.RFC3339))
		}
		if job.State.LastRunAtMs != nil {
			fmt.Printf("  Last run: %s (%s)\n", time.UnixMilli(*job.State.LastRunAtMs).Format(time.RFC3339), job.State.LastStatus)
		}
		fmt.Println()
	}
	return nil
}

// CronAddCmd adds a new cron job
type CronAddCmd struct {
	Name    string `arg:"" help:"Job name"`
	Message string `arg:"" help:"Prompt message to execute"`
	Every   string `help:"Run every interval (e.g., 5m, 2h, 1d)" xor:"schedule"`
	At      string `help:"Run once at time (+5m, 2024-01-01T12:00:00Z)" xor:"schedule"`
	Cron    string `help:"Run on cron schedule (e.g., '0 9 * * 1-5')" xor:"schedule"`
	Tz      string `help:"Timezone for cron schedule"`
	Session string `help:"Session target: main or isolated" default:"main"`
	Deliver bool   `help:"Deliver output to channels"`
}

func (c *CronAddCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	schedule, err := buildScheduleFromFlags(c.Every, c.At, c.Cron, c.Tz)
	if err != nil {
		return err
	}

	sessionTarget := cron.SessionTargetMain
	if c.Session == "isolated" {
		sessionTarget = cron.SessionTargetIsolated
	}

	job := &cron.CronJob{
		Name:          c.Name,
		Enabled:       true,
		Schedule:      schedule,
		SessionTarget: sessionTarget,
		Payload: cron.Payload{
			Kind:    cron.PayloadKindAgentTurn,
			Message: c.Message,
			Deliver: c.Deliver,
		},
	}

	// Calculate initial next run
	next, err := cron.NextRunTime(job, time.Now())
	if err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}
	job.SetNextRun(next)

	if err := store.AddJob(job); err != nil {
		return fmt.Errorf("failed to add job: %w", err)
	}

	fmt.Printf("Job created successfully.\n")
	fmt.Printf("ID: %s\n", job.ID)
	fmt.Printf("Name: %s\n", job.Name)
	fmt.Printf("Schedule: %s\n", formatCronSchedule(&job.Schedule))
	if next != nil {
		fmt.Printf("Next run: %s\n", next.Format(time.RFC3339))
	}
	return nil
}

// CronEditCmd edits an existing cron job
type CronEditCmd struct {
	ID      string  `arg:"" help:"Job ID to edit"`
	Name    *string `help:"New job name"`
	Message *string `help:"New prompt message"`
	Enabled *bool   `help:"Enable or disable job"`
}

func (c *CronEditCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	if c.Name != nil {
		job.Name = *c.Name
	}
	if c.Message != nil {
		job.Payload.Message = *c.Message
	}
	if c.Enabled != nil {
		job.Enabled = *c.Enabled
	}

	if err := store.UpdateJob(job); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	fmt.Printf("Job updated: %s\n", job.Name)
	return nil
}

// CronRemoveCmd removes a cron job
type CronRemoveCmd struct {
	ID string `arg:"" help:"Job ID to remove"`
}

func (c *CronRemoveCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	name := job.Name
	if err := store.DeleteJob(c.ID); err != nil {
		return fmt.Errorf("failed to remove job: %w", err)
	}

	fmt.Printf("Job '%s' removed.\n", name)
	return nil
}

// CronRunCmd runs a job immediately
type CronRunCmd struct {
	ID string `arg:"" help:"Job ID to run"`
}

func (c *CronRunCmd) Run(ctx *Context) error {
	// Note: This requires the gateway to be running
	// For now, just print a message
	fmt.Printf("To run a job immediately, use the cron tool via the agent.\n")
	fmt.Printf("The gateway must be running for job execution.\n")
	return nil
}

// CronRunsCmd shows job execution history
type CronRunsCmd struct {
	ID string `arg:"" help:"Job ID to show history for"`
}

func (c *CronRunsCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	fmt.Printf("Run history for '%s' (ID: %s)\n\n", job.Name, job.ID)
	if job.State.LastRunAtMs != nil {
		fmt.Printf("Last run: %s\n", time.UnixMilli(*job.State.LastRunAtMs).Format(time.RFC3339))
		fmt.Printf("Status: %s\n", job.State.LastStatus)
		fmt.Printf("Duration: %dms\n", job.State.LastDurationMs)
		if job.State.LastError != "" {
			fmt.Printf("Error: %s\n", job.State.LastError)
		}
	} else {
		fmt.Println("No runs recorded yet.")
	}
	return nil
}

func buildScheduleFromFlags(every, at, cronExpr, tz string) (cron.Schedule, error) {
	if every != "" {
		dur, err := cron.ParseDuration(every)
		if err != nil {
			return cron.Schedule{}, fmt.Errorf("invalid interval: %w", err)
		}
		return cron.Schedule{
			Kind:    cron.ScheduleKindEvery,
			EveryMs: dur.Milliseconds(),
		}, nil
	}

	if at != "" {
		atTime, err := cron.ParseAt(at, time.Now())
		if err != nil {
			return cron.Schedule{}, fmt.Errorf("invalid time: %w", err)
		}
		return cron.Schedule{
			Kind: cron.ScheduleKindAt,
			AtMs: atTime.UnixMilli(),
		}, nil
	}

	if cronExpr != "" {
		return cron.Schedule{
			Kind: cron.ScheduleKindCron,
			Expr: cronExpr,
			Tz:   tz,
		}, nil
	}

	return cron.Schedule{}, fmt.Errorf("must specify --every, --at, or --cron")
}

func formatCronSchedule(s *cron.Schedule) string {
	switch s.Kind {
	case cron.ScheduleKindAt:
		return fmt.Sprintf("at %s", time.UnixMilli(s.AtMs).Format(time.RFC3339))
	case cron.ScheduleKindEvery:
		return fmt.Sprintf("every %s", time.Duration(s.EveryMs)*time.Millisecond)
	case cron.ScheduleKindCron:
		if s.Tz != "" {
			return fmt.Sprintf("cron '%s' (%s)", s.Expr, s.Tz)
		}
		return fmt.Sprintf("cron '%s'", s.Expr)
	default:
		return "unknown"
	}
}

// UserCmd manages users
type UserCmd struct {
	Add         UserAddCmd         `cmd:"" help:"Add a new user"`
	List        UserListCmd        `cmd:"" help:"List all users"`
	Delete      UserDeleteCmd      `cmd:"" help:"Delete a user"`
	SetTelegram UserTelegramCmd    `cmd:"set-telegram" help:"Set Telegram ID"`
	SetPassword UserPasswordCmd    `cmd:"set-password" help:"Set HTTP password"`
}

// UserAddCmd adds a new user
type UserAddCmd struct {
	Username string `arg:"" help:"Username (lowercase, alphanumeric + underscore, starts with letter)"`
	Name     string `help:"Display name" required:""`
	Role     string `help:"Role: 'owner' (full access) or 'user' (limited)" default:"user" enum:"owner,user"`
}

func (u *UserAddCmd) Run(ctx *Context) error {
	// Validate username
	if err := config.ValidateUsername(u.Username); err != nil {
		return err
	}

	// Load existing users
	users, err := config.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	// Check if user already exists
	if _, exists := users[u.Username]; exists {
		return fmt.Errorf("user %q already exists", u.Username)
	}

	// Add new user
	users[u.Username] = &config.UserEntry{
		Name: u.Name,
		Role: u.Role,
	}

	// Save
	path := config.GetUsersFilePath()
	if err := config.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("User %q added. Use 'goclaw user set-telegram' or 'goclaw user set-http' to add credentials.\n", u.Username)
	return nil
}

// UserListCmd lists all users
type UserListCmd struct{}

func (u *UserListCmd) Run(ctx *Context) error {
	users, err := config.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	if len(users) == 0 {
		fmt.Println("No users configured.")
		return nil
	}

	fmt.Printf("Found %d user(s):\n\n", len(users))
	for username, entry := range users {
		fmt.Printf("%s (%s)\n", username, entry.Role)
		fmt.Printf("  Name: %s\n", entry.Name)
		if entry.TelegramID != "" {
			fmt.Printf("  Telegram: %s\n", entry.TelegramID)
		}
		if entry.HTTPPasswordHash != "" {
			fmt.Printf("  HTTP: configured\n")
		}
		fmt.Println()
	}
	return nil
}

// UserTelegramCmd sets a user's Telegram ID
type UserTelegramCmd struct {
	Username   string `arg:"" help:"Username"`
	TelegramID string `arg:"" help:"Telegram user ID (numeric)"`
}

func (u *UserTelegramCmd) Run(ctx *Context) error {
	users, err := config.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	entry.TelegramID = u.TelegramID

	path := config.GetUsersFilePath()
	if err := config.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("Telegram ID set for user %q.\n", u.Username)
	return nil
}

// UserPasswordCmd sets a user's HTTP password
type UserPasswordCmd struct {
	Username string `arg:"" help:"Username"`
	Password string `arg:"" optional:"" help:"Password (omit to prompt interactively)"`
}

func (u *UserPasswordCmd) Run(ctx *Context) error {
	users, err := config.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	password := u.Password
	if password == "" {
		// Prompt for password interactively (hidden input)
		fmt.Print("Enter HTTP password: ")
		pwBytes, err := readPassword()
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
		fmt.Println() // newline after hidden input
		password = string(pwBytes)
		if password == "" {
			return fmt.Errorf("password cannot be empty")
		}

		fmt.Print("Confirm password: ")
		confirmBytes, err := readPassword()
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
		fmt.Println() // newline after hidden input
		if password != string(confirmBytes) {
			return fmt.Errorf("passwords do not match")
		}
	}

	// Hash password
	hash, err := user.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	entry.HTTPPasswordHash = hash

	path := config.GetUsersFilePath()
	if err := config.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("HTTP password set for user %q.\n", u.Username)
	return nil
}

// UserDeleteCmd deletes a user
type UserDeleteCmd struct {
	Username string `arg:"" help:"Username to delete"`
	Force    bool   `help:"Force deletion even if user is owner"`
	Purge    bool   `help:"Also delete user's session data (irreversible)"`
}

func (u *UserDeleteCmd) Run(ctx *Context) error {
	users, err := config.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	// Check if owner
	if entry.Role == "owner" && !u.Force {
		return fmt.Errorf("cannot delete owner without --force flag")
	}

	// Confirm deletion
	fmt.Printf("Delete user %q? [y/N]: ", u.Username)
	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}

	// Delete user
	delete(users, u.Username)

	path := config.GetUsersFilePath()
	if err := config.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("User %q deleted.\n", u.Username)

	if u.Purge {
		// TODO: Delete session data from SQLite
		fmt.Println("Note: Session data purging not yet implemented.")
	} else {
		fmt.Println("Session data preserved. Use --purge to delete (irreversible).")
	}

	return nil
}

// Context passed to all commands
type Context struct {
	Debug  bool
	Trace  bool
	Config string
}

// runGateway is the actual gateway logic
func runGateway(ctx *Context, useTUI bool, devMode bool) error {
	L_info("starting gateway", "version", version)

	// Load config (handles bootstrap from openclaw.json if needed)
	loadResult, err := config.Load()
	if err != nil {
		L_error("failed to load config", "error", err)
		return err
	}
	cfg := loadResult.Config
	if loadResult.Bootstrapped {
		L_info("config bootstrapped from openclaw.json", "path", loadResult.SourcePath)
	} else {
		L_debug("config loaded", "path", loadResult.SourcePath)
	}

	// Load users from users.json (new format)
	usersConfig, err := config.LoadUsers()
	if err != nil {
		L_error("failed to load users", "error", err)
		return err
	}

	// Create user registry from users.json
	users := user.NewRegistryFromUsers(usersConfig)
	L_debug("user registry created", "users", users.Count())

	// Create LLM registry from config
	if len(cfg.LLM.Providers) == 0 {
		L_error("no LLM providers configured")
		return fmt.Errorf("llm.providers must be configured in goclaw.json")
	}

	regCfg := llm.RegistryConfig{
		Providers:     make(map[string]llm.ProviderConfig),
		Agent:         llm.PurposeConfig{Models: cfg.LLM.Agent.Models, MaxTokens: cfg.LLM.Agent.MaxTokens},
		Summarization: llm.PurposeConfig{Models: cfg.LLM.Summarization.Models, MaxTokens: cfg.LLM.Summarization.MaxTokens},
		Embeddings:    llm.PurposeConfig{Models: cfg.LLM.Embeddings.Models, MaxTokens: cfg.LLM.Embeddings.MaxTokens},
	}
	// Convert provider configs
	for name, pCfg := range cfg.LLM.Providers {
		regCfg.Providers[name] = llm.ProviderConfig{
			Type:           pCfg.Type,
			APIKey:         pCfg.APIKey,
			BaseURL:        pCfg.BaseURL,
			URL:            pCfg.URL,
			MaxTokens:      pCfg.MaxTokens,
			TimeoutSeconds: pCfg.TimeoutSeconds,
			PromptCaching:  pCfg.PromptCaching,
		}
	}
	llmRegistry, err := llm.NewRegistry(regCfg)
	if err != nil {
		L_error("failed to create LLM registry", "error", err)
		return err
	}
	llm.SetGlobalRegistry(llmRegistry)
	L_info("LLM registry created", "providers", len(cfg.LLM.Providers))

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
	gw, err := gateway.New(cfg, users, llmRegistry, toolsReg)
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

	// Initialize transcript manager (needs SQLite DB from session store)
	var transcriptMgr *transcript.Manager
	if db := gw.SessionDB(); db != nil {
		// Get embedding provider from memory manager if available
		var embeddingProvider memory.EmbeddingProvider
		if memMgr := gw.MemoryManager(); memMgr != nil {
			embeddingProvider = memMgr.Provider()
		}

		var err error
		transcriptMgr, err = transcript.NewManager(db, embeddingProvider, cfg.Transcript)
		if err != nil {
			L_warn("transcript: failed to initialize manager", "error", err)
		} else {
			// Set agent name for transcript labels
			if cfg.Agent.Name != "" {
				transcriptMgr.SetAgentName(cfg.Agent.Name)
			}
			transcriptMgr.Start()
			defer transcriptMgr.Stop()
			toolsReg.Register(tools.NewTranscriptTool(transcriptMgr))
			L_info("transcript: manager started and tool registered")
		}
	} else {
		L_debug("transcript: skipped (no SQLite store)")
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

	// Start Telegram bot if configured (with persistent retry on connection failures)
	var telegramBot *telegram.Bot
	var telegramBotMu sync.Mutex
	messageChannels := make(map[string]tools.MessageChannel)

	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
		// Try initial connection
		var err error
		telegramBot, err = telegram.New(&cfg.Telegram, gw, users)
		if err == nil {
			telegramBot.Start()
			gw.RegisterChannel(telegramBot)
			L_info("telegram: bot ready and listening")

			// Create Telegram message channel adapter for message tool
			if mediaStore := gw.MediaStore(); mediaStore != nil {
				adapter := telegram.NewMessageChannelAdapter(telegramBot, mediaStore.BaseDir())
				messageChannels["telegram"] = adapter
			}
		} else {
			// Initial connection failed - start background retry goroutine
			L_warn("telegram initial connection failed, will retry in background", "error", err)

			go func() {
				backoff := 5 * time.Second
				maxBackoff := 5 * time.Minute
				attempt := 1

				for {
					select {
					case <-runCtx.Done():
						L_info("telegram: shutdown requested, stopping retry")
						return
					case <-time.After(backoff):
					}

					L_info("telegram: retrying connection", "attempt", attempt, "backoff", backoff)
					bot, err := telegram.New(&cfg.Telegram, gw, users)
					if err != nil {
						L_warn("telegram: connection failed", "error", err, "nextRetry", backoff)
						attempt++
						// Exponential backoff with cap
						backoff *= 2
						if backoff > maxBackoff {
							backoff = maxBackoff
						}
						continue
					}

					// Success!
					bot.Start()
					gw.RegisterChannel(bot)
					L_info("telegram: bot ready after retry", "attempts", attempt)

					telegramBotMu.Lock()
					telegramBot = bot
					// Note: message channel adapter not set up here since messageChannels
					// was already passed to tools. Would need refactoring for hot-add.
					telegramBotMu.Unlock()
					return
				}
			}()
		}
	} else {
		L_debug("telegram not enabled or no token configured")
	}

	// Start HTTP server if configured (enabled by default if users have HTTP credentials)
	var httpServer *goclawhttp.Server
	httpEnabled := cfg.HTTP.Enabled == nil || *cfg.HTTP.Enabled // default true
	if httpEnabled {
		listen := cfg.HTTP.Listen
		if listen == "" {
			listen = ":1337"
		}
		// Get media root from gateway's media store
		var mediaRoot string
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			mediaRoot = mediaStore.BaseDir()
			L_debug("http: media root configured", "mediaRoot", mediaRoot)
		} else {
			L_warn("http: no media store available")
		}
		httpCfg := &goclawhttp.ServerConfig{
			Listen:    listen,
			DevMode:   devMode,
			MediaRoot: mediaRoot,
		}
		L_debug("http: creating server", "listen", listen, "devMode", devMode, "mediaRoot", mediaRoot)
		var err error
		httpServer, err = goclawhttp.NewServer(httpCfg, users)
		if err != nil {
			// Log the actual error so we can debug
			L_error("http: server creation failed", "error", err, "listen", listen, "devMode", devMode, "users", users.Count())
			L_warn("http: not starting - check error above for details")
		} else {
			httpServer.SetGateway(gw)
			gw.RegisterChannel(httpServer.Channel())
			if err := httpServer.Start(); err != nil {
				L_error("http: server start failed", "error", err)
				httpServer = nil
			} else {
				if devMode {
					L_info("http: server started (dev mode)", "listen", listen)
				} else {
					L_info("http: server started", "listen", listen)
				}
				// Register HTTP message channel adapter for message tool
				httpAdapter := goclawhttp.NewMessageChannelAdapter(httpServer.Channel(), "/api/media")
				messageChannels["http"] = httpAdapter
			}
		}
	} else {
		L_info("http: disabled in config")
	}

	// Register message tool with available channels (after all channels are set up)
	if len(messageChannels) > 0 {
		messageTool := tools.NewMessageTool(messageChannels)
		// Set media root for content array path resolution
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			messageTool.SetMediaRoot(mediaStore.BaseDir())
		}
		toolsReg.Register(messageTool)
		L_debug("message tool registered", "channels", len(messageChannels))
	}

	// Start cron service AFTER channels are registered
	if cfg.Cron.Enabled {
		if err := gw.StartCron(runCtx); err != nil {
			L_error("cron: failed to start service", "error", err)
		}
	}

	if useTUI {
		// Run TUI mode
		L_info("starting TUI mode")
		return runTUI(runCtx, gw, users, cfg.TUI.ShowLogs)
	}

	// Non-TUI mode: just wait for signals
	L_info("gateway ready")
	L_info("press Ctrl+C to stop")

	<-runCtx.Done()
	L_info("gateway shutting down")

	// Stop telegram bot if running
	telegramBotMu.Lock()
	if telegramBot != nil {
		telegramBot.Stop()
	}
	telegramBotMu.Unlock()

	// Stop HTTP server if running
	if httpServer != nil {
		if err := httpServer.Stop(); err != nil {
			L_error("http: shutdown error", "error", err)
		}
	}

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

// readPassword reads a password from stdin without echoing
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		return term.ReadPassword(fd)
	}
	// Fallback for non-terminal (piped input)
	var password string
	fmt.Scanln(&password)
	return []byte(password), nil
}
