package exec

import (
	"fmt"
	"time"
)

// BubblewrapConfig holds bubblewrap configuration for exec tool
type BubblewrapConfig struct {
	Enabled      bool
	ExtraRoBind  []string
	ExtraBind    []string
	ExtraEnv     map[string]string
	AllowNetwork bool
	ClearEnv     bool
}

// RunnerConfig holds configuration for command execution
type RunnerConfig struct {
	WorkingDir     string
	Timeout        time.Duration
	BubblewrapPath string
	Bubblewrap     BubblewrapConfig
}

// Error represents a command execution error with exit code
type Error struct {
	ExitCode int
	Err      error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
	}
	return fmt.Sprintf("command exited with code %d", e.ExitCode)
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Result holds the result of command execution
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}
