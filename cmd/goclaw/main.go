package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/sevlyar/go-daemon"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
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
type GatewayCmd struct{}

func (g *GatewayCmd) Run(ctx *Context) error {
	return runGateway(ctx)
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
	return runGateway(ctx)
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
func runGateway(ctx *Context) error {
	L_info("starting gateway", "version", version)

	// TODO: Load config
	// TODO: Initialize LLM client
	// TODO: Initialize Telegram adapter
	// TODO: Start HTTP server

	L_info("gateway ready", "port", 3378)

	// Block forever for now
	select {}
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
