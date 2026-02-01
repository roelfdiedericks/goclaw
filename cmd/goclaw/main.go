package main

import (
	"fmt"
	"os"

	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const version = "0.0.1"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("goclaw %s\n", version)
		return
	}

	// Initialize logging
	Init(&Config{
		Level:      LevelDebug,
		ShowCaller: true,
	})

	L_info("goclaw %s starting", version)

	// Load config
	cfg, err := config.Load()
	if err != nil {
		L_fatal("failed to load config: %v", err)
	}

	L_debug("config loaded")
	L_object("config", cfg)

	// TODO: Start gateway
	// TODO: Start channel adapters
	// TODO: Connect to LLM

	L_info("goclaw ready")
}
