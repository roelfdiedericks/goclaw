// Package commands provides unified command handling across all channels.
package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Command represents a slash command
type Command struct {
	Name        string   // e.g., "/status"
	Description string   // e.g., "Show session info"
	Usage       string   // Subcommand usage, e.g. "[debug|info|subs]" (optional)
	Aliases     []string // e.g., ["/stat"]
	Handler     CommandHandler
}

// CommandHandler is the function signature for command handlers
type CommandHandler func(ctx context.Context, args *CommandArgs) *CommandResult

// CommandArgs contains the arguments passed to a command handler
type CommandArgs struct {
	SessionKey string          // Session identifier
	Provider   SessionProvider // Access to session/gateway functionality
	RawArgs    string          // Everything after the command name
	Usage      string          // Copy of Command.Usage for error messages
}

// Manager is the global command registry
type Manager struct {
	mu       sync.RWMutex
	commands map[string]*Command // keyed by name (lowercase)
	provider SessionProvider
}

var (
	globalManager *Manager
	managerOnce   sync.Once
)

// InitManager initializes the global command manager with a provider
// Must be called once at startup before using commands
func InitManager(provider SessionProvider) *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{
			commands: make(map[string]*Command),
			provider: provider,
		}
		registerBuiltins(globalManager)
	})
	return globalManager
}

// GetManager returns the global command manager
// Panics if InitManager hasn't been called
func GetManager() *Manager {
	if globalManager == nil {
		panic("commands.InitManager must be called before GetManager")
	}
	return globalManager
}

// Register adds a command to the manager
func (m *Manager) Register(cmd *Command) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := strings.ToLower(cmd.Name)
	m.commands[name] = cmd

	// Register aliases
	for _, alias := range cmd.Aliases {
		m.commands[strings.ToLower(alias)] = cmd
	}
}

// Get returns a command by name (or alias)
func (m *Manager) Get(name string) *Command {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.commands[strings.ToLower(name)]
}

// List returns all unique commands (no aliases), sorted by name
func (m *Manager) List() []*Command {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Deduplicate (aliases point to same command)
	seen := make(map[*Command]bool)
	var list []*Command
	for _, cmd := range m.commands {
		if !seen[cmd] {
			seen[cmd] = true
			list = append(list, cmd)
		}
	}

	// Sort by name
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list
}

// Execute runs a command by name
func (m *Manager) Execute(ctx context.Context, cmdStr string, sessionKey string) *CommandResult {
	// Parse command name and args
	cmdStr = strings.TrimSpace(cmdStr)
	parts := strings.SplitN(cmdStr, " ", 2)
	name := strings.ToLower(parts[0])
	rawArgs := ""
	if len(parts) > 1 {
		rawArgs = parts[1]
	}

	cmd := m.Get(name)
	if cmd == nil {
		return &CommandResult{
			Text:     fmt.Sprintf("Unknown command: %s\nType /help for available commands.", name),
			Markdown: fmt.Sprintf("Unknown command: `%s`\nType /help for available commands.", name),
		}
	}

	args := &CommandArgs{
		SessionKey: sessionKey,
		Provider:   m.provider,
		RawArgs:    rawArgs,
		Usage:      cmd.Usage,
	}

	return cmd.Handler(ctx, args)
}

// IsCommand checks if text is a command
func IsCommand(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "/")
}
