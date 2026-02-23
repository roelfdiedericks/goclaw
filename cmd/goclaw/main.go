package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/go-rod/rod/lib/proto"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sevlyar/go-daemon"
	"golang.org/x/term"

	"github.com/roelfdiedericks/goclaw/internal/auth"
	"github.com/roelfdiedericks/goclaw/internal/browser"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/bwrap"
	"github.com/roelfdiedericks/goclaw/internal/channels"
	goclawhttp "github.com/roelfdiedericks/goclaw/internal/channels/http"
	httpconfig "github.com/roelfdiedericks/goclaw/internal/channels/http/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram"
	telegramconfig "github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	"github.com/roelfdiedericks/goclaw/internal/channels/tui"
	tuiconfig "github.com/roelfdiedericks/goclaw/internal/channels/tui/config"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/embeddings"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/hass"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/memory"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/setup"
	"github.com/roelfdiedericks/goclaw/internal/skills"
	"github.com/roelfdiedericks/goclaw/internal/supervisor"
	"github.com/roelfdiedericks/goclaw/internal/tools"
	"github.com/roelfdiedericks/goclaw/internal/tools/exec"
	toolhass "github.com/roelfdiedericks/goclaw/internal/tools/hass"
	"github.com/roelfdiedericks/goclaw/internal/tools/memoryget"
	"github.com/roelfdiedericks/goclaw/internal/tools/memorysearch"
	toolmessage "github.com/roelfdiedericks/goclaw/internal/tools/message"
	toolskills "github.com/roelfdiedericks/goclaw/internal/tools/skills"
	tooltranscript "github.com/roelfdiedericks/goclaw/internal/tools/transcript"
	toolupdate "github.com/roelfdiedericks/goclaw/internal/tools/update"
	"github.com/roelfdiedericks/goclaw/internal/tools/userauth"
	"github.com/roelfdiedericks/goclaw/internal/tools/xaiimagine"
	"github.com/roelfdiedericks/goclaw/internal/transcript"
	"github.com/roelfdiedericks/goclaw/internal/update"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// version is set by goreleaser via ldflags: -X main.version=...
// Default "dev" indicates a local/non-release build
var version = "dev"

// RuntimePaths holds derived paths for daemon operation
type RuntimePaths struct {
	DataDir string // Directory for all runtime files
	PidFile string
	LogFile string
}

// loadRuntimePaths loads config and derives all runtime paths from session.storePath
func loadRuntimePaths() (*RuntimePaths, error) {
	loadResult, err := config.Load()
	if err != nil {
		// Don't wrap - config.Load() error already includes "Run 'goclaw setup'" hint
		return nil, err
	}

	// Derive data directory from session store path
	storePath := loadResult.Config.Session.GetStorePath()
	dataDir := filepath.Dir(storePath)

	return &RuntimePaths{
		DataDir: dataDir,
		PidFile: filepath.Join(dataDir, "goclaw.pid"),
		LogFile: filepath.Join(dataDir, "goclaw.log"),
	}, nil
}

// CLI defines the command-line interface
type CLI struct {
	Debug  bool   `help:"Enable debug logging" short:"d"`
	Trace  bool   `help:"Enable trace logging" short:"t"`
	Config string `help:"Config file path" short:"c" type:"path"`

	Gateway    GatewayCmd    `cmd:"" help:"Run the gateway (foreground by default)"`
	Start      StartCmd      `cmd:"" help:"Start gateway as background daemon"`
	Stop       StopCmd       `cmd:"" help:"Stop the background daemon"`
	Status     StatusCmd     `cmd:"" help:"Show gateway status"`
	Version    VersionCmd    `cmd:"" help:"Show version"`
	Update     UpdateCmd     `cmd:"" help:"Check for and install updates"`
	Cron       CronCmd       `cmd:"" help:"Manage cron jobs"`
	User       UserCmd       `cmd:"" help:"Manage users"`
	Browser    BrowserCmd    `cmd:"" help:"Manage browser (download, profiles, setup)"`
	Embeddings EmbeddingsCmd `cmd:"" help:"Manage embeddings (status, rebuild)"`
	Setup      SetupCmd      `cmd:"" help:"Interactive setup wizard"`
	Onboard    OnboardCmd    `cmd:"" help:"Run onboarding wizard"`
	Cfg        ConfigCmd     `cmd:"config" help:"View configuration"`
	TUI        TUICmd        `cmd:"tui" help:"Run gateway with interactive TUI"`
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

// StartCmd daemonizes the gateway with supervision
type StartCmd struct{}

func (s *StartCmd) Run(ctx *Context) error {
	// Load config to get runtime paths
	paths, err := loadRuntimePaths()
	if err != nil {
		// Print user-friendly message (config.Load already includes setup hint)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	// Ensure data directory exists
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		L_error("failed to create data directory", "error", err)
		return err
	}

	// Check if already running
	if isRunningAt(paths.PidFile) {
		L_error("gateway already running")
		return fmt.Errorf("already running")
	}

	cntxt := &daemon.Context{
		PidFileName: paths.PidFile,
		PidFilePerm: 0644,
		LogFileName: paths.LogFile,
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
		L_info("gateway started", "pid", d.Pid, "dataDir", paths.DataDir)
		return nil
	}
	// Child process continues as supervisor
	defer cntxt.Release() //nolint:errcheck // daemon cleanup

	L_info("supervisor: started", "pid", os.Getpid(), "dataDir", paths.DataDir)

	// Run supervisor loop (spawns gateway subprocesses)
	sup := supervisor.New(paths.DataDir)
	return sup.Run()
}

// StopCmd stops the daemon
type StopCmd struct{}

func (s *StopCmd) Run(ctx *Context) error {
	// Load config to get runtime paths
	paths, err := loadRuntimePaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	pid, running := getPidFromFile(paths.PidFile)
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
	os.Remove(paths.PidFile)
	return nil
}

// StatusCmd shows gateway status
type StatusCmd struct{}

func (s *StatusCmd) Run(ctx *Context) error {
	// Load config to get runtime paths
	paths, err := loadRuntimePaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	pid, running := getPidFromFile(paths.PidFile)

	if !running {
		L_info("gateway not running")
		return nil
	}

	// Load supervisor state
	state, err := supervisor.LoadState(paths.DataDir)

	if err != nil {
		// Fall back to basic status if supervisor.json not available
		L_info("gateway running", "pid", pid)
		return nil
	}

	// Calculate uptime
	uptime := time.Since(state.StartedAt).Round(time.Second)

	// Format status output
	fmt.Println("Gateway:  running")
	if state.GatewayPID > 0 {
		fmt.Printf("PID:      %d (supervisor), %d (gateway)\n", state.PID, state.GatewayPID)
	} else {
		fmt.Printf("PID:      %d (supervisor)\n", state.PID)
	}
	fmt.Printf("Uptime:   %s\n", formatDuration(uptime))

	if state.CrashCount > 0 {
		lastCrash := "unknown"
		if state.LastCrashAt != nil {
			lastCrash = formatTimeAgo(*state.LastCrashAt)
		}
		fmt.Printf("Crashes:  %d this session (last: %s)\n", state.CrashCount, lastCrash)
	} else {
		fmt.Println("Crashes:  0 this session")
	}

	return nil
}

// formatDuration formats a duration in human-readable form
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours >= 24 {
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd%dh%dm", days, hours, mins)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// formatTimeAgo formats a time as "X ago"
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// VersionCmd shows version info
type VersionCmd struct{}

func (v *VersionCmd) Run(ctx *Context) error {
	fmt.Printf("goclaw %s\n", version)
	return nil
}

// UpdateCmd checks for and installs updates
type UpdateCmd struct {
	Check     bool   `help:"Check for updates without installing"`
	Channel   string `help:"Update channel (stable, beta)" default:"stable"`
	NoRestart bool   `help:"Update but don't restart" name:"no-restart"`
	Force     bool   `help:"Update even if already on latest version"`
}

func (u *UpdateCmd) Run(ctx *Context) error {
	return runUpdate(u.Check, u.Channel, u.NoRestart, u.Force)
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
	Add         UserAddCmd      `cmd:"" help:"Add a new user"`
	List        UserListCmd     `cmd:"" help:"List all users"`
	Edit        UserEditCmd     `cmd:"" help:"Interactive user management (TUI)"`
	Delete      UserDeleteCmd   `cmd:"" help:"Delete a user"`
	SetTelegram UserTelegramCmd `cmd:"set-telegram" help:"Set Telegram ID"`
	SetPassword UserPasswordCmd `cmd:"set-password" help:"Set HTTP password"`
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
	if err := user.ValidateUsername(u.Username); err != nil {
		return err
	}

	// Load existing users
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	// Check if user already exists
	if _, exists := users[u.Username]; exists {
		return fmt.Errorf("user %q already exists", u.Username)
	}

	// Add new user
	users[u.Username] = &user.UserEntry{
		Name: u.Name,
		Role: u.Role,
	}

	// Save
	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
		return err
	}

	fmt.Printf("User %q added. Use 'goclaw user set-telegram' or 'goclaw user set-http' to add credentials.\n", u.Username)
	return nil
}

// UserListCmd lists all users
type UserListCmd struct{}

func (u *UserListCmd) Run(ctx *Context) error {
	users, err := user.LoadUsers()
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
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}

	entry, exists := users[u.Username]
	if !exists {
		return fmt.Errorf("user %q not found", u.Username)
	}

	entry.TelegramID = u.TelegramID

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
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
	users, err := user.LoadUsers()
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

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
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
	users, err := user.LoadUsers()
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
	if _, err := fmt.Scanln(&confirm); err != nil {
		fmt.Fprintf(os.Stderr, "Input error: %v\n", err)
		os.Exit(1)
	}
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}

	// Delete user
	delete(users, u.Username)

	path := user.GetUsersFilePath()
	if err := user.SaveUsers(users, path); err != nil {
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
	fmt.Println()
	fmt.Println("Starting browser, please wait...")

	// Launch headed browser via the manager's unified API
	browserInstance, err := mgr.GetBrowser(profile, true)
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	// Navigate to start URL if provided
	if b.URL != "" {
		_, err := browserInstance.Page(proto.TargetCreateTarget{URL: b.URL})
		if err != nil {
			L_warn("browser setup: failed to open start URL", "url", b.URL, "error", err)
		}
	}

	// Wait for browser to close (user closes window) or Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Use the browser's context to detect when it dies (user closes window)
	browserCtx := browserInstance.GetContext()
	doneChan := browserCtx.Done()

	select {
	case <-sigChan:
		fmt.Println("\nCancelled.")
	case <-doneChan:
		fmt.Println("\nBrowser window closed.")
	}

	browserInstance.Close() //nolint:errcheck // cleanup

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
		if _, err := fmt.Scanln(&confirm); err != nil {
			fmt.Fprintf(os.Stderr, "Input error: %v\n", err)
			os.Exit(1)
		}
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
	if err := os.MkdirAll(goclawProfilesDir, 0750); err != nil {
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
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error { //nolint:errcheck // errors handled in callback
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

// EmbeddingsCmd manages embeddings (status, rebuild)
type EmbeddingsCmd struct {
	Status  EmbeddingsStatusCmd  `cmd:"" help:"Show embeddings status"`
	Rebuild EmbeddingsRebuildCmd `cmd:"" help:"Rebuild embeddings to primary model"`
}

// EmbeddingsStatusCmd shows embedding status
type EmbeddingsStatusCmd struct{}

func (e *EmbeddingsStatusCmd) Run(ctx *Context) error {
	return runEmbeddingsStatus()
}

// EmbeddingsRebuildCmd rebuilds embeddings
type EmbeddingsRebuildCmd struct {
	BatchSize int `help:"Batch size for processing" default:"50"`
}

func (e *EmbeddingsRebuildCmd) Run(ctx *Context) error {
	return runEmbeddingsRebuild(e.BatchSize)
}

// runEmbeddingsStatus shows detailed embedding status
func runEmbeddingsStatus() error {
	// Load config
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Check if embeddings are configured
	if len(cfg.LLM.Embeddings.Models) == 0 {
		fmt.Println("Embeddings not configured (no models in llm.embeddings.models)")
		return nil
	}

	// Open sessions DB (transcript_chunks)
	sessionsDB, err := openSessionsDB(cfg)
	if err != nil {
		return fmt.Errorf("open sessions DB: %w", err)
	}
	defer sessionsDB.Close()

	// Open memory DB if enabled
	var memoryDB *sql.DB
	if cfg.MemorySearch.Enabled {
		memoryDB, err = openMemoryDB(cfg)
		if err != nil {
			L_warn("embeddings: failed to open memory DB", "error", err)
			// Continue without memory DB
		} else {
			defer memoryDB.Close()
		}
	}

	// Get status
	status, err := embeddings.GetStatus(sessionsDB, memoryDB, cfg.LLM.Embeddings)
	if err != nil {
		return fmt.Errorf("get status: %w", err)
	}

	// Print status
	fmt.Println("Embeddings Status")
	fmt.Println("=================")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Printf("  Primary model: %s\n", status.PrimaryModel)
	fmt.Printf("  Auto-rebuild:  %v\n", status.AutoRebuild)
	fmt.Println()

	// Models in database
	fmt.Println("Models in Database:")
	allModels := make(map[string]int)
	for _, m := range status.Transcript.Models {
		allModels[m.Model] += m.Count
	}
	for _, m := range status.Memory.Models {
		allModels[m.Model] += m.Count
	}
	for model, count := range allModels {
		if model == status.PrimaryModel {
			fmt.Printf("  ✓ %s: %d chunks (primary)\n", model, count)
		} else {
			fmt.Printf("  ⚠ %s: %d chunks (needs rebuild)\n", model, count)
		}
	}
	fmt.Println()

	// Transcript status
	fmt.Println("Transcript Embeddings:")
	fmt.Printf("  Total chunks:     %d\n", status.Transcript.TotalChunks)
	if status.Transcript.TotalChunks > 0 {
		primaryPct := float64(status.Transcript.PrimaryModelCount) / float64(status.Transcript.TotalChunks) * 100
		fmt.Printf("  Primary model:    %d (%.1f%%)\n", status.Transcript.PrimaryModelCount, primaryPct)
		fmt.Printf("  Needs rebuild:    %d (%.1f%%)\n", status.Transcript.NeedsRebuildCount, 100-primaryPct)
	}
	fmt.Println()

	// Memory status
	if memoryDB != nil {
		fmt.Println("Memory Embeddings:")
		fmt.Printf("  Total chunks:     %d\n", status.Memory.TotalChunks)
		if status.Memory.TotalChunks > 0 {
			primaryPct := float64(status.Memory.PrimaryModelCount) / float64(status.Memory.TotalChunks) * 100
			fmt.Printf("  Primary model:    %d (%.1f%%)\n", status.Memory.PrimaryModelCount, primaryPct)
			fmt.Printf("  Needs rebuild:    %d (%.1f%%)\n", status.Memory.NeedsRebuildCount, 100-primaryPct)
		}
	} else {
		fmt.Println("Memory Embeddings: disabled")
	}

	return nil
}

// buildLLMRegistry creates an LLM registry from config
func buildLLMRegistry(cfg *config.Config) (*llm.Registry, error) {
	regCfg := llm.RegistryConfig{
		Providers:     cfg.LLM.Providers,
		Agent:         cfg.LLM.Agent,
		Summarization: cfg.LLM.Summarization,
		Embeddings:    cfg.LLM.Embeddings,
	}
	return llm.NewRegistry(regCfg)
}

// runEmbeddingsRebuild rebuilds all non-primary embeddings
func runEmbeddingsRebuild(batchSize int) error {
	// Load config
	loadResult, err := config.Load()
	if err != nil {
		return err
	}
	cfg := loadResult.Config

	// Check if embeddings are configured
	if len(cfg.LLM.Embeddings.Models) == 0 {
		fmt.Println("Embeddings not configured (no models in llm.embeddings.models)")
		return nil
	}

	primaryModel := cfg.LLM.Embeddings.Models[0]
	fmt.Printf("Rebuilding embeddings to primary model: %s\n", primaryModel)

	// Initialize LLM registry
	registry, err := buildLLMRegistry(cfg)
	if err != nil {
		return fmt.Errorf("create LLM registry: %w", err)
	}
	llm.SetGlobalRegistry(registry)

	// Open sessions DB (transcript_chunks)
	sessionsDB, err := openSessionsDB(cfg)
	if err != nil {
		return fmt.Errorf("open sessions DB: %w", err)
	}
	defer sessionsDB.Close()

	// Open memory DB if enabled
	var memoryDB *sql.DB
	if cfg.MemorySearch.Enabled {
		memoryDB, err = openMemoryDB(cfg)
		if err != nil {
			L_warn("embeddings: failed to open memory DB", "error", err)
			// Continue without memory DB
		} else {
			defer memoryDB.Close()
		}
	}

	// Progress callback
	onProgress := func(processed, total int, err error, done bool) {
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			return
		}
		if done {
			fmt.Printf("\nRebuild complete. %d chunks processed.\n", processed)
			return
		}
		// Periodic progress update
		fmt.Printf("  %d/%d (%.1f%%)\n", processed, total, float64(processed)/float64(total)*100)
	}

	// Run rebuild (CLI always forces full rebuild)
	ctx := context.Background()
	fmt.Printf("Processing chunks in batches of %d...\n\n", batchSize)

	err = embeddings.Rebuild(ctx, sessionsDB, memoryDB, cfg.LLM.Embeddings, registry, batchSize, true, onProgress)
	if err != nil {
		return fmt.Errorf("rebuild failed: %w", err)
	}

	return nil
}

// openSessionsDB opens the sessions database
func openSessionsDB(cfg *config.Config) (*sql.DB, error) {
	storePath := cfg.Session.GetStorePath()
	return sql.Open("sqlite3", storePath+"?_journal_mode=WAL&_busy_timeout=5000")
}

// openMemoryDB opens the memory database
func openMemoryDB(cfg *config.Config) (*sql.DB, error) {
	dbPath := cfg.MemorySearch.DbPath
	if dbPath == "" {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".goclaw", "memory.db")
	} else if strings.HasPrefix(dbPath, "~") {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, dbPath[1:])
	}
	return sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
}

// SetupCmd is the interactive setup wizard
type SetupCmd struct {
	Auto       SetupAutoCmd       `cmd:"" default:"withargs" help:"Run setup (auto-detect mode)"`
	Wizard     SetupWizardCmd     `cmd:"wizard" help:"Run full setup wizard (even if config exists)"`
	Edit       SetupEditCmd       `cmd:"edit" help:"Edit existing configuration"`
	Editor     SetupEditorCmd     `cmd:"editor" help:"Edit config (new tview UI)"`
	Generate   SetupGenerateCmd   `cmd:"generate" help:"Output default config template to stdout"`
	Transcript SetupTranscriptCmd `cmd:"transcript" help:"Configure transcript indexing"`
	Telegram   SetupTelegramCmd   `cmd:"telegram" help:"Configure Telegram bot"`
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

// SetupEditorCmd runs the new tview-based editor
type SetupEditorCmd struct{}

func (s *SetupEditorCmd) Run(ctx *Context) error {
	return setup.RunEditorTview()
}

// SetupGenerateCmd outputs default config template
type SetupGenerateCmd struct {
	Users        bool `help:"Generate users.json instead of goclaw.json"`
	WithPassword bool `help:"Generate a random password for the owner (users.json only)"`
}

func (s *SetupGenerateCmd) Run(ctx *Context) error {
	if s.Users {
		return setup.GenerateDefaultUsers(s.WithPassword)
	}
	return setup.GenerateDefault()
}

// SetupTranscriptCmd configures transcript indexing (tview version)
type SetupTranscriptCmd struct {
	Huh bool `help:"Use old huh-based form (for comparison)"`
}

func (s *SetupTranscriptCmd) Run(ctx *Context) error {
	if s.Huh {
		return setup.RunTranscriptSetup()
	}
	return setup.RunTranscriptSetupTview()
}

// SetupTelegramCmd configures Telegram bot
type SetupTelegramCmd struct{}

func (s *SetupTelegramCmd) Run(ctx *Context) error {
	return setup.RunTelegramSetupTview()
}

// OnboardCmd runs the onboarding wizard
type OnboardCmd struct{}

func (o *OnboardCmd) Run(ctx *Context) error {
	return setup.RunOnboardWizard()
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
	usersConfig, err := user.LoadUsers()
	if err != nil {
		L_error("failed to load users", "error", err)
		return err
	}

	// Create user registry from users.json with roles from config
	users := user.NewRegistryFromUsers(usersConfig, cfg.Roles)
	L_debug("user registry created", "users", users.Count())

	// Create LLM registry from config
	if len(cfg.LLM.Providers) == 0 {
		L_error("no LLM providers configured")
		return fmt.Errorf("llm.providers must be configured in goclaw.json")
	}

	llmRegistry, err := buildLLMRegistry(cfg)
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
			// Bubblewrap sandboxing
			Workspace:         cfg.Gateway.WorkingDir,
			BubblewrapEnabled: cfg.Tools.Browser.Bubblewrap.Enabled,
			BubblewrapPath:    cfg.Tools.Bubblewrap.Path,
			BubblewrapGPU:     cfg.Tools.Browser.Bubblewrap.GPU,
			ExtraRoBind:       cfg.Tools.Browser.Bubblewrap.ExtraRoBind,
			ExtraBind:         cfg.Tools.Browser.Bubblewrap.ExtraBind,
		}.ToConfig()

		browserMgr, err := browser.InitManager(browserCfg)
		if err != nil {
			L_warn("browser: failed to initialize manager", "error", err)
		} else {
			defer browserMgr.CloseAll()
			L_info("browser: manager initialized",
				"headless", cfg.Tools.Browser.Headless,
				"sandbox", cfg.Tools.Browser.Bubblewrap.Enabled)
		}
	} else {
		L_info("browser: disabled by configuration")
	}

	// Check bubblewrap availability for sandboxing
	execBwrapEnabled := cfg.Tools.Exec.Bubblewrap.Enabled
	browserBwrapEnabled := cfg.Tools.Browser.Bubblewrap.Enabled
	sandboxDisabledReason := "" // Track if sandbox was disabled for later warning
	if execBwrapEnabled || browserBwrapEnabled {
		if !bwrap.IsLinux() {
			L_warn("sandbox: bubblewrap only available on Linux, disabling")
			cfg.Tools.Exec.Bubblewrap.Enabled = false
			cfg.Tools.Browser.Bubblewrap.Enabled = false
			sandboxDisabledReason = "not Linux"
		} else if !bwrap.IsAvailable(cfg.Tools.Bubblewrap.Path) {
			L_warn("sandbox: bwrap not found, disabling sandboxing",
				"execEnabled", execBwrapEnabled,
				"browserEnabled", browserBwrapEnabled)
			cfg.Tools.Exec.Bubblewrap.Enabled = false
			cfg.Tools.Browser.Bubblewrap.Enabled = false
			sandboxDisabledReason = "bwrap not installed"
		} else {
			L_info("sandbox: bubblewrap available",
				"execEnabled", cfg.Tools.Exec.Bubblewrap.Enabled,
				"browserEnabled", cfg.Tools.Browser.Bubblewrap.Enabled)
		}
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

		// Exec tool config
		ExecTimeout:    cfg.Tools.Exec.Timeout,
		BubblewrapPath: cfg.Tools.Bubblewrap.Path,
		ExecBubblewrap: exec.BubblewrapConfig{
			Enabled:      cfg.Tools.Exec.Bubblewrap.Enabled,
			ExtraRoBind:  cfg.Tools.Exec.Bubblewrap.ExtraRoBind,
			ExtraBind:    cfg.Tools.Exec.Bubblewrap.ExtraBind,
			ExtraEnv:     cfg.Tools.Exec.Bubblewrap.ExtraEnv,
			AllowNetwork: cfg.Tools.Exec.Bubblewrap.AllowNetwork,
			ClearEnv:     cfg.Tools.Exec.Bubblewrap.ClearEnv,
		},
	})
	L_debug("base tools registered", "count", toolsReg.Count())

	// Create gateway (creates MediaStore internally)
	gw, err := gateway.New(cfg, users, llmRegistry, toolsReg)
	if err != nil {
		L_error("failed to create gateway", "error", err)
		return fmt.Errorf("failed to create gateway: %w", err)
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
		toolsReg.Register(memorysearch.NewTool(memMgr))
		toolsReg.Register(memoryget.NewTool(memMgr))
		L_debug("memory tools registered")
	}

	// Register skills tool (needs gateway's skills manager)
	if skillsMgr := gw.SkillManager(); skillsMgr != nil {
		toolsReg.Register(toolskills.NewTool(skillsMgr))
		skillsMgr.RegisterOperationalCommands()
		L_debug("skills tool registered")
	}

	// Register user_auth tool (for role elevation)
	if cfg.Auth.Enabled && cfg.Auth.Script != "" {
		toolsReg.Register(userauth.NewTool(cfg.Auth, cfg.Roles))
		L_info("user_auth: tool registered", "allowedRoles", cfg.Auth.AllowedRoles)
	}

	// Register goclaw_update tool (allows agent to update GoClaw)
	toolsReg.Register(toolupdate.NewTool(version))
	L_debug("goclaw_update tool registered")

	// Register Home Assistant tool
	if cfg.HomeAssistant.Enabled && cfg.HomeAssistant.Token != "" {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			// Create WebSocket client for sync registry queries
			wsClient := hass.NewWSClient(cfg.HomeAssistant)

			// Create event subscription manager
			home, _ := os.UserHomeDir()
			dataDir := filepath.Join(home, ".goclaw")
			hassManager := hass.NewManager(cfg.HomeAssistant, gw, dataDir)
			gw.SetHassManager(hassManager)

			// Start the manager (loads persisted subscriptions, connects if needed)
			if err := gw.StartHassManager(context.Background()); err != nil {
				L_warn("hass: failed to start manager", "error", err)
			}

			// Create and register the tool
			hassTool, err := toolhass.NewTool(cfg.HomeAssistant, mediaStore, wsClient, hassManager)
			if err != nil {
				L_warn("hass tool not registered", "error", err)
			} else {
				toolsReg.Register(hassTool)
				L_info("hass: tool registered", "url", cfg.HomeAssistant.URL)
			}
		} else {
			L_warn("hass tool not registered: no media store")
		}
	}

	// Register xAI Imagine tool
	if cfg.Tools.XAIImagine.Enabled {
		if mediaStore := gw.MediaStore(); mediaStore != nil {
			xaiImagineTool, err := xaiimagine.NewTool(cfg.Tools.XAIImagine, mediaStore)
			if err != nil {
				L_warn("xai_imagine tool not registered", "error", err)
			} else {
				toolsReg.Register(xaiImagineTool)
				L_info("xai_imagine: tool registered", "model", cfg.Tools.XAIImagine.Model)
			}
		} else {
			L_warn("xai_imagine tool not registered: no media store")
		}
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
				transcriptMgr.RegisterOperationalCommands()
				// Note: transcriptMgr.Stop() is called in signal handler before gw.Shutdown()
				// to ensure it stops before the SQLite database is closed
				toolsReg.Register(tooltranscript.NewTool(transcriptMgr))
				L_info("transcript: manager started and tool registered")
			}
		} else {
			L_debug("transcript: skipped (no SQLite store)")
		}
	} else {
		L_info("transcript: disabled by configuration")
	}

	// Register component config commands
	// These allow config forms to trigger test/apply actions via the bus
	media.RegisterCommands()
	httpconfig.RegisterCommands()
	tuiconfig.RegisterCommands()
	telegramconfig.RegisterCommands()
	session.RegisterCommands()
	skills.RegisterCommands()
	cron.RegisterCommands()
	auth.RegisterCommands()
	gateway.RegisterCommands()
	transcript.RegisterCommands()
	llm.RegisterCommands()
	L_debug("config commands registered")

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
		signal.Stop(sigCh) // Prevent handling the same signal twice
		// Cancel runCtx FIRST so goroutines (compaction retry, etc.) can exit.
		// Otherwise Shutdown blocks waiting for them while they wait on I/O.
		cancel()
		// Stop transcript manager BEFORE gateway shutdown (uses gateway's SQLite DB)
		if transcriptMgr != nil {
			transcriptMgr.Stop()
		}
		gw.Shutdown()
	}()

	// Create channel manager
	chanMgr := channels.NewManager(gw, users)

	// Create message tool early (channels will be added dynamically via bus events)
	messageTool := toolmessage.NewTool(nil)
	if mediaStore := gw.MediaStore(); mediaStore != nil {
		messageTool.SetMediaRoot(mediaStore.BaseDir())
	}
	toolsReg.Register(messageTool)

	// Subscribe to channel events to update message tool adapters
	bus.SubscribeEvent("channels.telegram.started", func(event bus.Event) {
		if bot := chanMgr.GetTelegram(); bot != nil {
			if mediaStore := gw.MediaStore(); mediaStore != nil {
				adapter := telegram.NewMessageChannelAdapter(bot, mediaStore.BaseDir())
				messageTool.SetChannel("telegram", adapter)
			}
		}
	})
	bus.SubscribeEvent("channels.telegram.stopped", func(event bus.Event) {
		messageTool.RemoveChannel("telegram")
	})
	bus.SubscribeEvent("channels.http.started", func(event bus.Event) {
		if srv := chanMgr.GetHTTP(); srv != nil {
			adapter := goclawhttp.NewMessageChannelAdapter(srv.Channel(), "/api/media")
			messageTool.SetChannel("http", adapter)
		}
	})
	bus.SubscribeEvent("channels.http.stopped", func(event bus.Event) {
		messageTool.RemoveChannel("http")
	})
	L_debug("message tool registered (channels will be added dynamically)")

	// Start all enabled channels via manager
	if err := chanMgr.StartAll(runCtx, cfg.Channels, channels.RuntimeOptions{DevMode: devMode}); err != nil {
		L_error("channels: failed to start", "error", err)
	}

	// Start cron service AFTER channels are registered
	if cfg.Cron.Enabled {
		if err := gw.StartCron(runCtx); err != nil {
			L_error("cron: failed to start service", "error", err)
		} else if cronSvc := gw.CronService(); cronSvc != nil {
			cronSvc.RegisterOperationalCommands()
		}
	} else {
		L_info("cron: disabled by configuration")
	}

	if useTUI {
		// Run TUI mode
		L_info("starting TUI mode")
		return runTUI(runCtx, gw, users, cfg.Channels.TUI.ShowLogs, sandboxDisabledReason)
	}

	// Non-TUI mode: just wait for signals
	if sandboxDisabledReason != "" {
		L_warn("sandbox: sandboxing is disabled", "reason", sandboxDisabledReason,
			"hint", "install bubblewrap and restart to enable")
	}
	L_info("gateway ready")
	L_info("press Ctrl+C to stop")

	<-runCtx.Done()
	L_info("gateway shutting down")

	// Stop all channels via manager
	chanMgr.StopAll()

	return nil
}

// runTUI runs the interactive TUI mode
func runTUI(ctx context.Context, gw *gateway.Gateway, users *user.Registry, showLogs bool, sandboxDisabledReason string) error {
	owner := users.Owner()
	if owner == nil {
		return fmt.Errorf("no owner user configured")
	}

	// Log sandbox warning after TUI starts (in a goroutine so TUI can initialize first)
	if sandboxDisabledReason != "" {
		go func() {
			time.Sleep(100 * time.Millisecond) // Brief delay for TUI to initialize
			L_warn("sandbox: sandboxing is disabled", "reason", sandboxDisabledReason,
				"hint", "install bubblewrap and restart to enable")
		}()
	}

	return tui.Run(ctx, gw, owner, showLogs)
}

// getPidFromFile returns the pid and whether the process is running
func getPidFromFile(pidFile string) (int, bool) {
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
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
		os.Remove(pidFile)
		return pid, false
	}

	return pid, true
}

// isRunningAt checks if gateway is already running using the given pid file
func isRunningAt(pidFile string) bool {
	_, running := getPidFromFile(pidFile)
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
			strings.HasPrefix(errMsg, "setup:") ||
			strings.Contains(errMsg, "user aborted") {
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
	if _, err := fmt.Scanln(&password); err != nil {
		return nil, fmt.Errorf("failed to read password: %w", err)
	}
	return []byte(password), nil
}

// runUpdate handles the update command
func runUpdate(checkOnly bool, channel string, noRestart, force bool) error {
	// Check if system-managed
	if update.IsSystemManaged() {
		exePath, _ := update.GetExecutablePath()
		fmt.Println("GoClaw is installed at a system-managed location:")
		fmt.Printf("  %s\n\n", exePath)
		fmt.Println("Please update using your package manager:")
		fmt.Println()
		fmt.Println("  # For Debian/Ubuntu:")
		fmt.Println("  sudo apt update && sudo apt upgrade goclaw")
		fmt.Println()
		fmt.Println("  # Or download the latest .deb from:")
		fmt.Println("  https://github.com/roelfdiedericks/goclaw/releases/latest")
		return nil
	}

	updater := update.NewUpdater(version)

	fmt.Printf("Checking for updates (channel: %s)...\n", channel)

	info, err := updater.CheckForUpdate(channel)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("Current version: %s\n", info.CurrentVersion)
	fmt.Printf("Latest version:  %s (%s)\n", info.NewVersion, info.Channel)

	if !info.IsNewer && !force {
		fmt.Println("\nYou are already running the latest version.")
		return nil
	}

	if !info.IsNewer && force {
		fmt.Println("\nForcing reinstall of current version.")
	} else {
		fmt.Println("\nA new version is available!")
	}

	// Show changelog preview
	if info.Changelog != "" {
		fmt.Println("\nChangelog:")
		fmt.Println("----------")
		// Truncate changelog if too long
		changelog := info.Changelog
		if len(changelog) > 1000 {
			changelog = changelog[:1000] + "\n..."
		}
		fmt.Println(changelog)
		fmt.Println()
	}

	if checkOnly {
		fmt.Println("Run 'goclaw update' to install the update.")
		return nil
	}

	// Download with progress
	fmt.Println("Downloading...")
	binaryPath, err := updater.Download(info, func(downloaded, total int64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("\r  %.1f%% (%d / %d bytes)", pct, downloaded, total)
		}
	})
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Println("\n  Download complete!")

	// Apply update
	fmt.Println("Installing...")
	if err := updater.Apply(binaryPath, noRestart); err != nil {
		return fmt.Errorf("failed to apply update: %w", err)
	}

	if noRestart {
		fmt.Println("\nUpdate installed successfully!")
		fmt.Println("Restart GoClaw to use the new version.")
	}
	// If not noRestart, the process will be replaced via exec and this line won't be reached

	return nil
}
