package main

import (
	"github.com/alecthomas/kong"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const version = "0.0.1"

// CLI defines the command-line interface
type CLI struct {
	Debug  bool   `help:"Enable debug logging" short:"d"`
	Trace  bool   `help:"Enable trace logging" short:"t"`
	Config string `help:"Config file path" short:"c" type:"path"`

	Gateway GatewayCmd `cmd:"" help:"Start the gateway daemon"`
	Status  StatusCmd  `cmd:"" help:"Show gateway status"`
	Version VersionCmd `cmd:"" help:"Show version"`
}

// GatewayCmd starts the gateway daemon
type GatewayCmd struct{}

func (g *GatewayCmd) Run(ctx *Context) error {
	L_info("starting gateway daemon")

	// TODO: Load config
	// TODO: Initialize LLM client
	// TODO: Initialize Telegram adapter
	// TODO: Start HTTP server
	// TODO: Main loop

	L_info("gateway ready", "port", 3378)

	// Block forever for now
	select {}
}

// StatusCmd shows gateway status
type StatusCmd struct{}

func (s *StatusCmd) Run(ctx *Context) error {
	L_info("checking gateway status...")
	// TODO: Connect to running gateway and get status
	L_info("status", "running", false)
	return nil
}

// VersionCmd shows version info
type VersionCmd struct{}

func (v *VersionCmd) Run(ctx *Context) error {
	L_info("goclaw", "version", version)
	return nil
}

// Context passed to all commands
type Context struct {
	Debug  bool
	Trace  bool
	Config string
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
