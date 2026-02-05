package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/sevlyar/go-daemon"
	"golang.org/x/term"

	"github.com/roelfdiedericks/goclaw/internal/browser"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/setup"
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
	Browser BrowserCmd `cmd:"" help:"Manage browser (download, profiles, setup)"`
	Setup   SetupCmd   `cmd:"" help:"Interactive setup wizard"`
	Cfg     ConfigCmd  `cmd:"config" help:"View configuration"`
	TUI     TUICmd     `cmd:"tui" help:"Run gateway with interactive TUI"`
}

// GatewayCmd runs gateway in foreground
type GatewayCmd struct {
	Run GatewayRunCmd `cmd:"" default:"withargs" help:"Run gateway in foreground"`
	TUI GatewayTUICmd `cmd:"tui" help:"Run gateway with interactive TUI"`
}

// GatewayRunCmd runs gateway in foreground (default)
type GatewayRunCmd struct {
	TUI bool `help:"Run with interactive TUI" short:"i" name:"interactive"`
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (g *GatewayRunCmd) Run(ctx *Context) error {
	return runGateway(ctx, g.TUI, g.Dev)
}

// GatewayTUICmd runs gateway with TUI (goclaw gateway tui)
type GatewayTUICmd struct {
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (g *GatewayTUICmd) Run(ctx *Context) error {
	return runGateway(ctx, true, g.Dev)
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
	Kill   CronKillCmd   `cmd:"" help:"Clear stuck running state for a job"`
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
		if job.IsRunning() {
			runningFor := time.Since(time.UnixMilli(*job.State.RunningAtMs))
			fmt.Printf("  RUNNING: for %s (use 'goclaw cron kill %s' to clear)\n", runningFor.Round(time.Second), job.ID)
		}
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

// CronKillCmd clears the stuck running state for a job
type CronKillCmd struct {
	ID string `arg:"" help:"Job ID to kill (clear running state)"`
}

func (c *CronKillCmd) Run(ctx *Context) error {
	store := cron.NewStore("", "")
	if err := store.Load(); err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	job := store.GetJob(c.ID)
	if job == nil {
		return fmt.Errorf("job not found: %s", c.ID)
	}

	if !job.IsRunning() {
		fmt.Printf("Job '%s' is not currently marked as running.\n", job.Name)
		return nil
	}

	// Get running duration for info
	runningFor := time.Since(time.UnixMilli(*job.State.RunningAtMs))

	// Clear the running state
	job.ClearRunning()
	if err := store.UpdateJob(job); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	fmt.Printf("Cleared running state for job '%s' (was running for %s).\n", job.Name, runningFor.Round(time.Second))
	fmt.Printf("Note: If the job is actually still executing, it will continue until completion or timeout.\n")
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
	Edit        UserEditCmd        `cmd:"" help:"Interactive user management (TUI)"`
	Delete      UserDeleteCmd      `cmd:"" help:"Delete a user"`
	SetTelegram UserTelegramCmd    `cmd:"set-telegram" help:"Set Telegram ID"`
	SetPassword UserPasswordCmd    `cmd:"set-password" help:"Set HTTP password"`
}

// UserEditCmd launches the TUI user editor
type UserEditCmd struct{}

func (u *UserEditCmd) Run(ctx *Context) error {
	return setup.RunUserEditor()
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

// BrowserCmd manages browser (download, profiles, setup)
type BrowserCmd struct {
	Download BrowserDownloadCmd `cmd:"" help:"Download/update Chromium browser"`
	Setup    BrowserSetupCmd    `cmd:"" help:"Launch browser for profile setup (login, cookies, etc.)"`
	Profiles BrowserProfilesCmd `cmd:"" help:"List browser profiles"`
	Clear    BrowserClearCmd    `cmd:"" help:"Clear profile data (cookies, cache, etc.)"`
	Status   BrowserStatusCmd   `cmd:"" help:"Show browser status (running instances, download state)"`
	Migrate  BrowserMigrateCmd  `cmd:"" help:"Import profiles from OpenClaw"`
}

// BrowserDownloadCmd downloads Chromium
type BrowserDownloadCmd struct {
	Force bool `help:"Force re-download even if already present"`
}

func (b *BrowserDownloadCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config to get browser settings
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config adapter
	browserCfg := browser.ToolsConfigAdapter{
		Dir:            cfg.Tools.Browser.Dir,
		AutoDownload:   true, // Force for this command
		Revision:       cfg.Tools.Browser.Revision,
		Headless:       cfg.Tools.Browser.Headless,
		NoSandbox:      cfg.Tools.Browser.NoSandbox,
		DefaultProfile: cfg.Tools.Browser.DefaultProfile,
		Timeout:        cfg.Tools.Browser.Timeout,
		Stealth:        cfg.Tools.Browser.Stealth,
		Device:         cfg.Tools.Browser.Device,
		ProfileDomains: cfg.Tools.Browser.ProfileDomains,
	}.ToConfig()

	// Create downloader
	binDir := browserCfg.ResolveBinDir(home)
	downloader := browser.NewDownloader(binDir, browserCfg.Revision)

	if b.Force {
		fmt.Println("Force downloading Chromium...")
		path, err := downloader.ForceDownload()
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Printf("Chromium downloaded to: %s\n", path)
	} else {
		// Check if already downloaded
		if path, err := downloader.FindExistingBrowser(); err == nil {
			fmt.Printf("Chromium already downloaded: %s\n", path)
			fmt.Println("Use --force to re-download.")
			return nil
		}

		fmt.Println("Downloading Chromium...")
		path, err := downloader.EnsureBrowser()
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Printf("Chromium downloaded to: %s\n", path)
	}

	return nil
}

// BrowserSetupCmd launches browser for profile setup
type BrowserSetupCmd struct {
	Profile string `arg:"" optional:"" help:"Profile name (default: 'default')"`
	URL     string `arg:"" optional:"" help:"Starting URL (optional)"`
}

func (b *BrowserSetupCmd) Run(ctx *Context) error {
	profile := b.Profile
	if profile == "" {
		profile = "default"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:            cfg.Tools.Browser.Dir,
		AutoDownload:   cfg.Tools.Browser.AutoDownload,
		Revision:       cfg.Tools.Browser.Revision,
		Headless:       false, // Headed for setup
		NoSandbox:      cfg.Tools.Browser.NoSandbox,
		DefaultProfile: cfg.Tools.Browser.DefaultProfile,
		Timeout:        cfg.Tools.Browser.Timeout,
		Stealth:        cfg.Tools.Browser.Stealth,
		Device:         cfg.Tools.Browser.Device,
		ProfileDomains: cfg.Tools.Browser.ProfileDomains,
	}.ToConfig()

	// Initialize manager
	mgr, err := browser.InitManager(browserCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize browser manager: %w", err)
	}

	// Ensure browser is downloaded
	if _, err := mgr.EnsureBrowser(); err != nil {
		return fmt.Errorf("failed to ensure browser: %w", err)
	}

	profileDir := browserCfg.ResolveProfileDir(home, profile)
	fmt.Printf("Launching browser for profile: %s\n", profile)
	fmt.Printf("Profile directory: %s\n", profileDir)
	if b.URL != "" {
		fmt.Printf("Starting URL: %s\n", b.URL)
	}
	fmt.Println("\nLog in, set cookies, etc. Close the browser when done.")
	fmt.Println("Press Ctrl+C to cancel.")

	// Launch headed browser
	browserInstance, _, err := mgr.LaunchHeaded(profile, b.URL)
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	// Wait for browser window to close or Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	doneChan := make(chan struct{})
	go func() {
		// Poll for all pages closed (workaround: go-rod doesn't have window close event)
		// Initial delay to let the first page open
		time.Sleep(2 * time.Second)

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				pages, err := browserInstance.Pages()
				if err != nil {
					L_debug("browser setup: error getting pages", "error", err)
					close(doneChan)
					return
				}
				if len(pages) == 0 {
					L_debug("browser setup: all pages closed")
					close(doneChan)
					return
				}
			}
		}
	}()

	select {
	case <-sigChan:
		fmt.Println("\nCancelled.")
	case <-doneChan:
		fmt.Println("\nBrowser window closed.")
	}

	browserInstance.Close()

	fmt.Printf("\nProfile '%s' is ready to use.\n", profile)
	return nil
}

// BrowserProfilesCmd lists browser profiles
type BrowserProfilesCmd struct{}

func (b *BrowserProfilesCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:            cfg.Tools.Browser.Dir,
		DefaultProfile: cfg.Tools.Browser.DefaultProfile,
	}.ToConfig()

	profilesDir := browserCfg.ResolveProfilesDir(home)
	profileMgr := browser.NewProfileManager(profilesDir)

	profiles, err := profileMgr.ListProfiles()
	if err != nil {
		return fmt.Errorf("failed to list profiles: %w", err)
	}

	if len(profiles) == 0 {
		fmt.Println("No browser profiles found.")
		fmt.Printf("Profiles directory: %s\n", profilesDir)
		fmt.Println("\nUse 'goclaw browser setup [profile]' to create a profile.")
		return nil
	}

	fmt.Printf("Browser profiles (%d):\n\n", len(profiles))
	for _, p := range profiles {
		marker := ""
		if p.Name == cfg.Tools.Browser.DefaultProfile {
			marker = " (default)"
		}
		lastUsed := "never"
		if !p.LastUsed.IsZero() {
			lastUsed = p.LastUsed.Format("2006-01-02 15:04")
		}
		fmt.Printf("  %s%s\n", p.Name, marker)
		fmt.Printf("    Size: %s, Last used: %s\n", browser.FormatSize(p.Size), lastUsed)
		fmt.Printf("    Path: %s\n\n", p.Path)
	}

	// Show domain mappings if any
	if len(cfg.Tools.Browser.ProfileDomains) > 0 {
		fmt.Println("Domain mappings:")
		for domain, profile := range cfg.Tools.Browser.ProfileDomains {
			fmt.Printf("  %s → %s\n", domain, profile)
		}
	}

	return nil
}

// BrowserClearCmd clears profile data
type BrowserClearCmd struct {
	Profile string `arg:"" help:"Profile name to clear"`
	Force   bool   `help:"Skip confirmation"`
}

func (b *BrowserClearCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:            cfg.Tools.Browser.Dir,
		DefaultProfile: cfg.Tools.Browser.DefaultProfile,
	}.ToConfig()

	profilesDir := browserCfg.ResolveProfilesDir(home)
	profileMgr := browser.NewProfileManager(profilesDir)

	if !profileMgr.ProfileExists(b.Profile) {
		return fmt.Errorf("profile '%s' does not exist", b.Profile)
	}

	if !b.Force {
		fmt.Printf("Clear all data for profile '%s'? This will delete cookies, cache, and login sessions.\n", b.Profile)
		fmt.Print("Type 'yes' to confirm: ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := profileMgr.ClearProfile(b.Profile); err != nil {
		return fmt.Errorf("failed to clear profile: %w", err)
	}

	fmt.Printf("Profile '%s' cleared.\n", b.Profile)
	return nil
}

// BrowserStatusCmd shows browser status
type BrowserStatusCmd struct{}

func (b *BrowserStatusCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load config
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	fmt.Println("Browser Status")
	fmt.Println("==============")
	fmt.Println()

	// Check if browser is enabled
	if !cfg.Tools.Browser.Enabled {
		fmt.Println("Browser: DISABLED (set tools.browser.enabled=true to enable)")
		return nil
	}
	fmt.Println("Browser: ENABLED")

	// Create browser config
	browserCfg := browser.ToolsConfigAdapter{
		Dir:            cfg.Tools.Browser.Dir,
		AutoDownload:   cfg.Tools.Browser.AutoDownload,
		Revision:       cfg.Tools.Browser.Revision,
		DefaultProfile: cfg.Tools.Browser.DefaultProfile,
	}.ToConfig()

	// Check download status
	binDir := browserCfg.ResolveBinDir(home)
	downloader := browser.NewDownloader(binDir, browserCfg.Revision)

	if binPath, err := downloader.FindExistingBrowser(); err == nil {
		fmt.Printf("Chromium: DOWNLOADED\n")
		fmt.Printf("  Path: %s\n", binPath)
	} else {
		if cfg.Tools.Browser.AutoDownload {
			fmt.Println("Chromium: NOT DOWNLOADED (will auto-download on first use)")
		} else {
			fmt.Println("Chromium: NOT DOWNLOADED (run 'goclaw browser download')")
		}
	}

	// Check profiles
	profilesDir := browserCfg.ResolveProfilesDir(home)
	profileMgr := browser.NewProfileManager(profilesDir)
	profiles, _ := profileMgr.ListProfiles()

	fmt.Printf("\nProfiles: %d\n", len(profiles))
	if len(profiles) > 0 {
		for _, p := range profiles {
			marker := ""
			if p.Name == cfg.Tools.Browser.DefaultProfile {
				marker = " (default)"
			}
			fmt.Printf("  - %s%s (%s)\n", p.Name, marker, browser.FormatSize(p.Size))
		}
	}

	// Note about running instances
	fmt.Println("\nNote: Running browser instances are managed by the gateway.")
	fmt.Println("Use 'goclaw status' to check if the gateway is running.")

	return nil
}

// BrowserMigrateCmd imports profiles from OpenClaw
type BrowserMigrateCmd struct{}

func (b *BrowserMigrateCmd) Run(ctx *Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// OpenClaw profile location
	openclawProfilesDir := filepath.Join(home, ".openclaw", "browser", "profiles")
	
	// GoClaw profile location
	goclawProfilesDir := filepath.Join(home, ".openclaw", "goclaw", "browser", "profiles")

	// Check if OpenClaw profiles exist
	if _, err := os.Stat(openclawProfilesDir); os.IsNotExist(err) {
		fmt.Println("No OpenClaw profiles found at:", openclawProfilesDir)
		return nil
	}

	// List OpenClaw profiles
	entries, err := os.ReadDir(openclawProfilesDir)
	if err != nil {
		return fmt.Errorf("failed to read OpenClaw profiles: %w", err)
	}

	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			profiles = append(profiles, entry.Name())
		}
	}

	if len(profiles) == 0 {
		fmt.Println("No profiles found in OpenClaw directory")
		return nil
	}

	fmt.Println("Found OpenClaw profiles:")
	for _, p := range profiles {
		srcPath := filepath.Join(openclawProfilesDir, p)
		size := getDirSize(srcPath)
		fmt.Printf("  - %s (%s)\n", p, browser.FormatSize(size))
	}
	fmt.Println()

	// Ensure GoClaw profiles directory exists
	if err := os.MkdirAll(goclawProfilesDir, 0755); err != nil {
		return fmt.Errorf("failed to create GoClaw profiles directory: %w", err)
	}

	// Process each profile
	reader := bufio.NewReader(os.Stdin)
	for _, p := range profiles {
		srcPath := filepath.Join(openclawProfilesDir, p)
		
		// Suggest renaming "openclaw" to "default"
		destName := p
		if p == "openclaw" {
			fmt.Printf("\nProfile '%s' found. Import as:\n", p)
			fmt.Println("  [1] 'default' (recommended - GoClaw's default profile name)")
			fmt.Println("  [2] 'openclaw' (keep original name)")
			fmt.Println("  [3] Skip this profile")
			fmt.Print("Choice [1]: ")
			
			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(choice)
			
			switch choice {
			case "", "1":
				destName = "default"
			case "2":
				destName = "openclaw"
			case "3":
				fmt.Printf("Skipped '%s'\n", p)
				continue
			default:
				fmt.Printf("Invalid choice, skipping '%s'\n", p)
				continue
			}
		} else {
			fmt.Printf("\nImport '%s'? [Y/n]: ", p)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer == "n" || answer == "no" {
				fmt.Printf("Skipped '%s'\n", p)
				continue
			}
		}

		destPath := filepath.Join(goclawProfilesDir, destName)
		
		// Check if destination exists
		if _, err := os.Stat(destPath); err == nil {
			fmt.Printf("  Warning: '%s' already exists in GoClaw. Overwrite? [y/N]: ", destName)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Printf("  Skipped (destination exists)\n")
				continue
			}
			// Remove existing
			os.RemoveAll(destPath)
		}

		// Copy the profile directory
		fmt.Printf("  Copying '%s' -> '%s'...", p, destName)
		if err := copyDir(srcPath, destPath); err != nil {
			fmt.Printf(" FAILED: %v\n", err)
			continue
		}
		fmt.Println(" OK")
	}

	fmt.Println("\nMigration complete!")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Run 'goclaw browser profiles' to verify imported profiles")
	fmt.Println("  2. Update profileDomains in goclaw.json to map domains to profiles")
	fmt.Println("  3. Or set allowAgentProfiles: true to let agent specify profiles directly")
	
	return nil
}

// getDirSize calculates the total size of a directory
func getDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		// Copy file
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			return err
		}

		return os.Chmod(destPath, info.Mode())
	})
}

// SetupCmd is the interactive setup wizard
type SetupCmd struct {
	Auto   SetupAutoCmd   `cmd:"" default:"withargs" help:"Run setup (auto-detect mode)"`
	Wizard SetupWizardCmd `cmd:"wizard" help:"Run full setup wizard (even if config exists)"`
	Edit   SetupEditCmd   `cmd:"edit" help:"Edit existing configuration"`
}

// SetupAutoCmd auto-detects mode: wizard if no config, edit if exists
type SetupAutoCmd struct{}

func (s *SetupAutoCmd) Run(ctx *Context) error {
	return setup.RunAuto()
}

// SetupWizardCmd forces the full wizard
type SetupWizardCmd struct{}

func (s *SetupWizardCmd) Run(ctx *Context) error {
	return setup.RunWizard()
}

// SetupEditCmd edits existing config
type SetupEditCmd struct{}

func (s *SetupEditCmd) Run(ctx *Context) error {
	return setup.RunEdit()
}

// ConfigCmd shows configuration
type ConfigCmd struct {
	Show ConfigShowCmd `cmd:"" default:"withargs" help:"Show current configuration"`
	Path ConfigPathCmd `cmd:"path" help:"Show path to goclaw.json"`
}

// ConfigShowCmd shows the current configuration
type ConfigShowCmd struct{}

func (c *ConfigShowCmd) Run(ctx *Context) error {
	return setup.ShowConfig()
}

// ConfigPathCmd shows the config file path
type ConfigPathCmd struct{}

func (c *ConfigPathCmd) Run(ctx *Context) error {
	return setup.ShowConfigPath()
}

// TUICmd is a top-level shortcut for goclaw tui
type TUICmd struct {
	Dev bool `help:"Development mode: reload HTML templates from disk on each request"`
}

func (t *TUICmd) Run(ctx *Context) error {
	return runGateway(ctx, true, t.Dev)
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
		return err
	}
	cfg := loadResult.Config
	L_debug("config loaded", "path", loadResult.SourcePath)

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

	// Initialize browser manager for web_fetch fallback and browser tool
	if cfg.Tools.Browser.Enabled {
		browserCfg := browser.ToolsConfigAdapter{
			Dir:            cfg.Tools.Browser.Dir,
			AutoDownload:   cfg.Tools.Browser.AutoDownload,
			Revision:       cfg.Tools.Browser.Revision,
			Headless:       cfg.Tools.Browser.Headless,
			NoSandbox:      cfg.Tools.Browser.NoSandbox,
			DefaultProfile: cfg.Tools.Browser.DefaultProfile,
			Timeout:        cfg.Tools.Browser.Timeout,
			Stealth:        cfg.Tools.Browser.Stealth,
			Device:         cfg.Tools.Browser.Device,
			ProfileDomains: cfg.Tools.Browser.ProfileDomains,
		}.ToConfig()

		browserMgr, err := browser.InitManager(browserCfg)
		if err != nil {
			L_warn("browser: failed to initialize manager", "error", err)
		} else {
			defer browserMgr.CloseAll()
			L_info("browser: manager initialized", "headless", cfg.Tools.Browser.Headless)
		}
	} else {
		L_info("browser: disabled by configuration")
	}

	// Create tool registry and register base defaults (browser tool registered after gateway)
	toolsReg := tools.NewRegistry()
	// Determine web_fetch headless mode (defaults to true if not specified)
	webHeadless := true
	if cfg.Tools.Web.Headless != nil {
		webHeadless = *cfg.Tools.Web.Headless
	}
	tools.RegisterDefaults(toolsReg, tools.ToolsConfig{
		WorkingDir:  cfg.Gateway.WorkingDir,
		BraveAPIKey: cfg.Tools.Web.BraveAPIKey,
		UseBrowser:  cfg.Tools.Web.UseBrowser, // "auto", "always", "never" for web_fetch
		WebProfile:  cfg.Tools.Web.Profile,    // browser profile for web_fetch
		WebHeadless: webHeadless,              // headless mode for web_fetch browser
	})
	L_debug("base tools registered", "count", toolsReg.Count())

	// Create gateway (creates MediaStore internally)
	gw, err := gateway.New(cfg, users, llmRegistry, toolsReg)
	if err != nil {
		L_error("failed to create gateway", "error", err)
		os.Exit(1)
	}
	L_info("gateway initialized")

	// Register browser tool (needs gateway's MediaStore and browser.Manager)
	if cfg.Tools.Browser.Enabled {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			if browserMgr := browser.GetManager(); browserMgr != nil {
				toolsReg.Register(browser.NewTool(browserMgr, mediaStore))
				L_debug("browser tool registered", "headless", cfg.Tools.Browser.Headless)
			} else {
				L_warn("browser tool not registered: manager not initialized")
			}
		} else {
			L_warn("browser tool not registered: no media store")
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
	if cfg.Transcript.Enabled {
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
	} else {
		L_info("transcript: disabled by configuration")
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
		L_info("telegram: disabled by configuration")
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
	} else {
		L_info("cron: disabled by configuration")
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
		// Print user-facing errors cleanly without log formatting
		errMsg := err.Error()
		if strings.HasPrefix(errMsg, "no goclaw.json") ||
			strings.HasPrefix(errMsg, "goclaw.json is empty") ||
			strings.HasPrefix(errMsg, "at least one") ||
			strings.HasPrefix(errMsg, "setup:") {
			fmt.Fprintln(os.Stderr, errMsg)
			os.Exit(1)
		}
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
