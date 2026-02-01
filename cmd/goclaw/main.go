package main

import (
	"fmt"
	"os"

	"github.com/rodent/goclaw/internal/config"
)

const version = "0.0.1"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("goclaw %s\n", version)
		return
	}

	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("goclaw %s\n", version)
	fmt.Printf("loaded config: %+v\n", cfg)
	
	// TODO: Start gateway
	// TODO: Start channel adapters
	// TODO: Connect to LLM
}
