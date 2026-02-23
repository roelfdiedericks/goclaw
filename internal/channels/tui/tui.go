// Package tui provides the terminal user interface for GoClaw.
package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/roelfdiedericks/goclaw/internal/channels/types"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Focus represents which panel has focus
type Focus int

const (
	FocusChat Focus = iota
	FocusLogs
)

// LayoutMode represents the current layout configuration
type LayoutMode int

const (
	LayoutNormal     LayoutMode = iota // 55/45 split
	LayoutLogsHidden                   // Chat fullscreen
	LayoutLogsFull                     // Logs fullscreen
)

// Model is the main TUI model
type Model struct {
	// Components
	chatViewport viewport.Model
	logsViewport viewport.Model
	input        textarea.Model

	// State
	focus       Focus
	width       int
	height      int
	chatLines   []string // Chat history as lines
	logsLines   []string // Logs as lines
	currentLine string   // Current streaming line (not yet complete)
	streaming   bool
	ready       bool
	layout      LayoutMode // Current layout mode

	// Event channel for current agent run
	eventsChan <-chan gateway.AgentEvent

	// Log channel for receiving log messages
	logChan chan string

	// Mirror channel for receiving mirrored messages from other channels
	mirrorChan chan mirrorMsg

	// System channel for receiving direct messages (HASS events, etc.)
	systemChan chan string

	// Dependencies
	gateway *gateway.Gateway
	user    *user.User
	ctx     context.Context
	cancel  context.CancelFunc
}

// Message types
type agentEventMsg gateway.AgentEvent
type agentDoneMsg struct{ err error }
type logMsg string
type mirrorMsg struct {
	source   string
	userMsg  string
	response string
}
type systemMsg string

// New creates a new TUI model
// showLogs controls whether the log panel is visible by default (true = normal layout, false = logs hidden)
func New(gw *gateway.Gateway, u *user.User, showLogs bool) Model {
	// Convert bool to layout mode
	initialLayout := LayoutNormal
	if !showLogs {
		initialLayout = LayoutLogsHidden
	}
	// Create input textarea
	ti := textarea.New()
	ti.Placeholder = "Type a message..."
	ti.Focus()
	ti.CharLimit = 10000
	ti.SetHeight(3)
	ti.ShowLineNumbers = false

	// Create viewports (will be resized on WindowSizeMsg)
	chatVP := viewport.New(80, 20)
	logsVP := viewport.New(40, 20)

	ctx, cancel := context.WithCancel(context.Background())

	// Create channels
	logChan := make(chan string, 100)
	mirrorChan := make(chan mirrorMsg, 10)
	systemChan := make(chan string, 10)

	m := Model{
		chatViewport: chatVP,
		logsViewport: logsVP,
		input:        ti,
		focus:        FocusChat,
		chatLines:    []string{},
		logsLines:    []string{},
		logChan:      logChan,
		mirrorChan:   mirrorChan,
		systemChan:   systemChan,
		gateway:      gw,
		user:         u,
		ctx:          ctx,
		cancel:       cancel,
		layout:       initialLayout,
	}

	// Add welcome message
	m.chatLines = append(m.chatLines,
		assistantStyle.Render("Welcome to GoClaw! Type a message to chat with your AI assistant."),
		helpStyle.Render("Press Tab to switch panels, Ctrl+C to quit, Enter to send"),
		"",
	)

	return m
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.EnterAltScreen,
		m.waitForLog(),
		m.waitForMirror(),
		m.waitForSystem(),
	)
}

// waitForLog returns a command that waits for the next log message
func (m *Model) waitForLog() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-m.logChan:
			if !ok {
				return nil
			}
			return logMsg(msg)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// waitForMirror returns a command that waits for the next mirror message
func (m *Model) waitForMirror() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-m.mirrorChan:
			if !ok {
				return nil
			}
			return msg
		case <-m.ctx.Done():
			return nil
		}
	}
}

// waitForSystem returns a command that waits for the next system message
func (m *Model) waitForSystem() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-m.systemChan:
			if !ok {
				return nil
			}
			return systemMsg(msg)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "tab":
			// Toggle focus between panels (only in normal layout)
			if m.layout == LayoutNormal {
				if m.focus == FocusChat {
					m.focus = FocusLogs
				} else {
					m.focus = FocusChat
				}
			}
			return m, nil

		case "ctrl+l":
			// Cycle layout: Normal -> LogsHidden -> LogsFull -> Normal
			switch m.layout {
			case LayoutNormal:
				m.layout = LayoutLogsHidden
				m.focus = FocusChat
			case LayoutLogsHidden:
				m.layout = LayoutLogsFull
				m.focus = FocusLogs
			case LayoutLogsFull:
				m.layout = LayoutNormal
				m.focus = FocusChat
			}
			// Trigger resize to recalculate layout
			return m, func() tea.Msg {
				return tea.WindowSizeMsg{Width: m.width, Height: m.height}
			}

		case "enter":
			if m.focus == FocusChat && !m.streaming {
				// Send message
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					// Handle commands
					if strings.HasPrefix(text, "/") {
						return m.handleCommand(text)
					}

					// Clear input
					m.input.Reset()

					// Show user message
					m.chatLines = append(m.chatLines,
						userStyle.Render("You: ")+text,
						"",
					)
					m.currentLine = assistantStyle.Render(m.gateway.AgentIdentity().DisplayName() + ": ")
					m.chatViewport.SetContent(m.getChatContent())
					m.chatViewport.GotoBottom()

					// Start streaming
					m.streaming = true
					cmd := m.startAgent(text)
					return m, cmd
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

		// Calculate panel sizes based on layout mode
		var chatWidth, logsWidth int
		switch m.layout {
		case LayoutNormal:
			// Chat panel: 55% width, logs panel: 45% width
			chatWidth = m.width * 55 / 100
			logsWidth = m.width - chatWidth - 1
		case LayoutLogsHidden:
			// Full width for chat
			chatWidth = m.width
			logsWidth = 0
		case LayoutLogsFull:
			// Full width for logs (minimal chat)
			chatWidth = 0
			logsWidth = m.width
		}

		// Height: total - input height - status bar - borders
		contentHeight := m.height - 8

		m.chatViewport.Width = chatWidth - 4 // -4 for borders and padding
		m.chatViewport.Height = contentHeight
		m.logsViewport.Width = logsWidth - 4
		m.logsViewport.Height = contentHeight

		m.input.SetWidth(chatWidth - 6)

		// Update viewport content
		m.chatViewport.SetContent(m.getChatContent())
		m.logsViewport.SetContent(m.getLogsContent())

	case agentEventMsg:
		event := gateway.AgentEvent(msg)
		handleAgentEvent(&m, event)

		// Continue listening for more events if streaming
		if m.streaming && m.eventsChan != nil {
			cmds = append(cmds, m.waitForEvent())
		}

	case agentDoneMsg:
		m.streaming = false
		m.eventsChan = nil
		if msg.err != nil {
			m.finishCurrentLine()
			m.chatLines = append(m.chatLines, errorStyle.Render(fmt.Sprintf("[Error: %s]", msg.err)))
			m.chatViewport.SetContent(m.getChatContent())
			m.chatViewport.GotoBottom()
		}

	case logMsg:
		m.logsLines = append(m.logsLines, string(msg))
		m.logsViewport.SetContent(m.getLogsContent())
		m.logsViewport.GotoBottom()
		// Continue listening for more logs
		cmds = append(cmds, m.waitForLog())

	case compactResultMsg:
		// Display compaction result
		for _, line := range strings.Split(msg.result, "\n") {
			if line != "" {
				m.chatLines = append(m.chatLines, helpStyle.Render(line))
			}
		}
		m.chatLines = append(m.chatLines, "")
		m.chatViewport.SetContent(m.getChatContent())
		m.chatViewport.GotoBottom()

	case mirrorMsg:
		// Display mirrored conversation from another channel
		m.chatLines = append(m.chatLines,
			mirrorStyle.Render(fmt.Sprintf("┌─ 📱 %s ─────────────────────", msg.source)),
			mirrorStyle.Render("│ ")+userStyle.Render("You: ")+truncateMsg(msg.userMsg, 100),
			mirrorStyle.Render("│"),
			mirrorStyle.Render("│ ")+assistantStyle.Render(m.gateway.AgentIdentity().DisplayName()+": ")+truncateMsg(msg.response, 300),
			mirrorStyle.Render("└────────────────────────────────"),
			"",
		)
		m.chatViewport.SetContent(m.getChatContent())
		m.chatViewport.GotoBottom()
		// Continue listening for more mirrors
		cmds = append(cmds, m.waitForMirror())

	case systemMsg:
		// Display system message (HASS events, etc.) - clean format like agent response
		m.chatLines = append(m.chatLines,
			assistantStyle.Render(m.gateway.AgentIdentity().DisplayName()+": ")+string(msg),
			"",
		)
		m.chatViewport.SetContent(m.getChatContent())
		m.chatViewport.GotoBottom()
		// Continue listening for more system messages
		cmds = append(cmds, m.waitForSystem())
	}

	// Update focused component
	if m.focus == FocusChat && !m.streaming {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Allow scrolling
	if m.focus == FocusChat {
		m.chatViewport, _ = m.chatViewport.Update(msg)
	} else {
		m.logsViewport, _ = m.logsViewport.Update(msg)
	}

	return m, tea.Batch(cmds...)
}

// handleAgentEvent processes an agent event
func handleAgentEvent(m *Model, event gateway.AgentEvent) {
	switch e := event.(type) {
	case gateway.EventThinkingDelta:
		// Show thinking with a prefix indicator if this is the first delta
		if !strings.Contains(m.currentLine, "💭") {
			m.currentLine = thinkingStyle.Render("💭 ")
		}
		m.currentLine += e.Delta
	case gateway.EventTextDelta:
		// If we were showing thinking, finish that line first
		if strings.Contains(m.currentLine, "💭") {
			m.finishCurrentLine()
			m.currentLine = assistantStyle.Render(m.gateway.AgentIdentity().DisplayName() + ": ")
		}
		m.currentLine += e.Delta
	case gateway.EventToolStart:
		m.finishCurrentLine()
		m.chatLines = append(m.chatLines, toolStyle.Render(fmt.Sprintf("[Using tool: %s]", e.ToolName)))
	case gateway.EventToolEnd:
		if e.Error != "" {
			m.chatLines = append(m.chatLines, errorStyle.Render(fmt.Sprintf("[Tool error: %s]", e.Error)))
		} else {
			m.chatLines = append(m.chatLines, toolStyle.Render("[Tool completed]"))
		}
		m.chatLines = append(m.chatLines, "")
		m.currentLine = assistantStyle.Render(m.gateway.AgentIdentity().DisplayName() + ": ")
	case gateway.EventAgentEnd:
		m.finishCurrentLine()
		m.chatLines = append(m.chatLines, "")
		m.streaming = false
		m.eventsChan = nil
	case gateway.EventAgentError:
		m.finishCurrentLine()
		m.chatLines = append(m.chatLines, errorStyle.Render(fmt.Sprintf("[Error: %s]", e.Error)), "")
		m.streaming = false
		m.eventsChan = nil
	}
	m.chatViewport.SetContent(m.getChatContent())
	m.chatViewport.GotoBottom()
}

// View renders the TUI
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Calculate widths based on layout mode
	var chatWidth, logsWidth int
	switch m.layout {
	case LayoutNormal:
		chatWidth = m.width * 55 / 100
		logsWidth = m.width - chatWidth - 1
	case LayoutLogsHidden:
		chatWidth = m.width
		logsWidth = 0
	case LayoutLogsFull:
		chatWidth = 0
		logsWidth = m.width
	}

	var content string

	if m.layout == LayoutLogsFull {
		// Logs fullscreen
		logsBorder := focusedBorder
		logsPanel := logsBorder.
			Width(logsWidth - 2).
			Height(m.height - 3).
			Render(
				lipgloss.JoinVertical(lipgloss.Left,
					titleStyle.Render("📋 Logs (fullscreen)"),
					m.logsViewport.View(),
				),
			)
		content = logsPanel
	} else {
		// Build chat panel
		chatBorder := unfocusedBorder
		if m.focus == FocusChat {
			chatBorder = focusedBorder
		}

		inputView := ""
		if !m.streaming {
			inputView = inputPromptStyle.Render("> ") + m.input.View()
		} else {
			inputView = inputPromptStyle.Render("⏳ Thinking...")
		}

		chatPanel := chatBorder.
			Width(chatWidth - 2).
			Height(m.height - 3).
			Render(
				lipgloss.JoinVertical(lipgloss.Left,
					titleStyle.Render("💬 Chat"),
					m.chatViewport.View(),
					"",
					inputView,
				),
			)

		if m.layout == LayoutNormal {
			// Build logs panel
			logsBorder := unfocusedBorder
			if m.focus == FocusLogs {
				logsBorder = focusedBorder
			}
			logsPanel := logsBorder.
				Width(logsWidth - 2).
				Height(m.height - 3).
				Render(
					lipgloss.JoinVertical(lipgloss.Left,
						titleStyle.Render("📋 Logs"),
						m.logsViewport.View(),
					),
				)

			// Join panels horizontally
			content = lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, logsPanel)
		} else {
			// Logs hidden
			content = chatPanel
		}
	}

	// Status bar
	status := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, content, status)
}

// startAgent starts an agent run and returns the command to wait for events
func (m *Model) startAgent(text string) tea.Cmd {
	req := gateway.AgentRequest{
		User:           m.user,
		Source:         "tui",
		ChatID:         "",
		IsGroup:        false,
		UserMsg:        text,
		EnableThinking: m.user.Thinking, // Extended thinking based on user preference
	}

	events := make(chan gateway.AgentEvent, 100)
	m.eventsChan = events

	// Run agent in background
	go func() {
		m.gateway.RunAgent(m.ctx, req, events) //nolint:errcheck // fire-and-forget goroutine
	}()

	// Return command to wait for first event
	return m.waitForEvent()
}

// waitForEvent returns a command that waits for the next event
func (m *Model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		if m.eventsChan == nil {
			return agentDoneMsg{}
		}

		event, ok := <-m.eventsChan
		if !ok {
			return agentDoneMsg{}
		}
		return agentEventMsg(event)
	}
}

// handleCommand processes slash commands via the global command manager
func (m Model) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	sessionKey := "user:" + m.user.ID
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))

	// TUI-specific commands (not in global registry) - always allowed
	switch cmdLower {
	case "/exit", "/quit":
		m.cancel()
		return m, tea.Quit
	}

	// Check command permission via gateway
	if !m.gateway.CanUserUseCommands(m.user) {
		logging.L_debug("tui: commands disabled for user", "user", m.user.Name, "command", cmdLower)
		// Treat as regular message instead
		m.chatLines = append(m.chatLines, userStyle.Render("You: "+cmd))
		m.chatViewport.SetContent(m.getChatContent())
		return m, nil
	}

	// Check if command exists in registry
	mgr := commands.GetManager()
	if mgr.Get(cmdLower) == nil {
		m.chatLines = append(m.chatLines,
			errorStyle.Render(fmt.Sprintf("Unknown command: %s", cmd)),
			helpStyle.Render("Type /help for available commands."),
			"",
		)
		m.chatViewport.SetContent(m.getChatContent())
		return m, nil
	}

	// Special handling for long-running commands
	if cmdLower == "/compact" {
		m.chatLines = append(m.chatLines,
			helpStyle.Render("Compacting session... (this may take a minute)"),
			"",
		)
		m.chatViewport.SetContent(m.getChatContent())
		m.chatViewport.GotoBottom()

		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			result := mgr.Execute(ctx, cmdLower, sessionKey)
			return compactResultMsg{result: result.Text}
		}
	}

	// Special handling for /clear (resets display)
	if cmdLower == "/clear" || cmdLower == "/reset" {
		result := mgr.Execute(m.ctx, cmdLower, sessionKey)
		m.chatLines = []string{assistantStyle.Render(result.Text), ""}
		m.currentLine = ""
		m.chatViewport.SetContent(m.getChatContent())
		return m, nil
	}

	// Standard command execution
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := mgr.Execute(ctx, cmdLower, sessionKey)

	// Display result
	for _, line := range strings.Split(result.Text, "\n") {
		if line != "" {
			m.chatLines = append(m.chatLines, helpStyle.Render(line))
		}
	}
	m.chatLines = append(m.chatLines, "")
	m.chatViewport.SetContent(m.getChatContent())
	m.chatViewport.GotoBottom()

	return m, nil
}

// compactResultMsg is sent when compaction completes
type compactResultMsg struct {
	result string
}

// getChatContent returns the full chat content as a string
func (m *Model) getChatContent() string {
	content := strings.Join(m.chatLines, "\n")
	if m.currentLine != "" {
		content += "\n" + m.currentLine
	}
	return content
}

// getLogsContent returns the full logs content as a string
func (m *Model) getLogsContent() string {
	return strings.Join(m.logsLines, "\n")
}

// finishCurrentLine moves the current streaming line to chatLines
func (m *Model) finishCurrentLine() {
	if m.currentLine != "" {
		m.chatLines = append(m.chatLines, m.currentLine)
		m.currentLine = ""
	}
}

// renderStatusBar creates the status bar
func (m Model) renderStatusBar() string {
	var status string
	if m.streaming {
		status = "⏳ Thinking..."
	} else {
		status = "✅ Ready"
	}

	userName := fmt.Sprintf("👤 %s", m.user.Name)
	var layoutName string
	switch m.layout {
	case LayoutNormal:
		layoutName = "split"
	case LayoutLogsHidden:
		layoutName = "chat"
	case LayoutLogsFull:
		layoutName = "logs"
	}
	help := fmt.Sprintf("Tab: focus | Ctrl+L: layout (%s) | Enter: send | Ctrl+C: quit", layoutName)

	left := statusBarStyle.Render(status + " │ " + userName)
	right := statusBarStyle.Render(help)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	middle := statusBarStyle.Render(strings.Repeat(" ", gap))

	return left + middle + right
}

// truncateMsg truncates a message for display in mirrors
func truncateMsg(s string, maxLen int) string {
	// Remove newlines for compact display
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TUIChannel wraps the TUI to implement the Channel interface
type TUIChannel struct {
	mirrorChan chan<- mirrorMsg
	systemChan chan<- string
	user       *user.User
	gateway    *gateway.Gateway
	mu         sync.Mutex
}

// NewTUIChannel creates a Channel wrapper for the TUI
func NewTUIChannel(mirrorChan chan<- mirrorMsg, systemChan chan<- string, u *user.User, gw *gateway.Gateway) *TUIChannel {
	return &TUIChannel{
		mirrorChan: mirrorChan,
		systemChan: systemChan,
		user:       u,
		gateway:    gw,
	}
}

// Name returns the channel identifier
func (c *TUIChannel) Name() string {
	return "tui"
}

// Start is a no-op for TUI (it's started by Run)
func (c *TUIChannel) Start(ctx context.Context) error {
	return nil
}

// Stop is a no-op for TUI (it's stopped by Run)
func (c *TUIChannel) Stop() error {
	return nil
}

// Send sends a direct message to the TUI (HASS events, etc.)
func (c *TUIChannel) Send(ctx context.Context, msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case c.systemChan <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil // Channel full, drop
	}
}

// SendMirror sends a mirrored conversation to the TUI
func (c *TUIChannel) SendMirror(ctx context.Context, source, userMsg, response string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case c.mirrorChan <- mirrorMsg{source: source, userMsg: userMsg, response: response}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Channel full, drop the mirror
		logging.L_warn("TUI mirror channel full, dropping message")
		return nil
	}
}

// HasUser returns true if this TUI is for the given user
func (c *TUIChannel) HasUser(u *user.User) bool {
	return c.user != nil && u != nil && c.user.ID == u.ID
}

// InjectMessage handles message injection for supervision (guidance/ghostwriting).
//
// StreamEvent returns false - TUI is batch-only, doesn't support real-time streaming.
func (c *TUIChannel) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	return false // TUI doesn't stream events
}

// DeliverGhostwrite displays a ghostwritten message.
func (c *TUIChannel) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	if c.user == nil || u == nil || c.user.ID != u.ID {
		return nil // Not the TUI user
	}

	logging.L_info("tui: ghostwrite", "user", u.ID, "messageLen", len(message))

	// TUI doesn't need typing delay - it's not a real-time chat interface
	return c.SendMirror(ctx, "ghostwrite", "", message)
}

// Run starts the TUI and returns a Channel that can receive mirrors
// showLogs controls whether the log panel is visible by default
func Run(ctx context.Context, gw *gateway.Gateway, u *user.User, showLogs bool) error {
	m := New(gw, u, showLogs)

	// Create TUI channel for receiving mirrors/system messages and register it with gateway
	tuiChannel := NewTUIChannel(m.mirrorChan, m.systemChan, u, gw)
	gw.RegisterChannel(tuiChannel)

	// Set up log hook to forward logs to TUI (exclusive - suppresses stderr)
	logging.SetHookExclusive(func(level, msg string) {
		timestamp := time.Now().Format("15:04:05")
		formatted := fmt.Sprintf("%s [%s] %s", timestamp, level, msg)
		select {
		case m.logChan <- formatted:
		default:
			// Drop log if channel is full
		}
	})

	// Send initial log to confirm hook is working
	logging.L_info("TUI started", "user", u.Name)

	// Clean up hook and unregister channel when done
	defer func() {
		gw.UnregisterChannel("tui")
		logging.SetHookExclusive(nil)
		close(m.logChan)
		close(m.mirrorChan)
	}()

	p := tea.NewProgram(m, tea.WithAltScreen())

	_, err := p.Run()
	return err
}

// TUI is a wrapper that implements ManagedChannel for the terminal user interface.
// TUI is special - it blocks the main thread. Use RunBlocking() instead of Start().
type TUI struct {
	gw     *gateway.Gateway
	users  *user.Registry
	config *Config

	// The channel that implements gateway.Channel (created on RunBlocking)
	channel *TUIChannel

	mu        sync.RWMutex
	running   bool
	startedAt time.Time
}

// NewTUI creates a new TUI wrapper
func NewTUI(gw *gateway.Gateway, users *user.Registry, cfg *Config) *TUI {
	return &TUI{
		gw:     gw,
		users:  users,
		config: cfg,
	}
}

// Start is a no-op for TUI - use RunBlocking() instead (implements ManagedChannel)
func (t *TUI) Start(ctx context.Context) error {
	// TUI blocks - this method is intentionally a no-op.
	// Manager should call RunBlocking() for TUI.
	return nil
}

// Stop is a no-op for TUI - it stops when user exits (implements ManagedChannel)
func (t *TUI) Stop() error {
	// TUI stops when user presses Ctrl+C or exits
	// Can't interrupt from outside
	return nil
}

// Reload updates config (implements ManagedChannel)
func (t *TUI) Reload(cfg any) error {
	newCfg, ok := cfg.(*Config)
	if !ok {
		return fmt.Errorf("expected *tui.Config, got %T", cfg)
	}

	t.mu.Lock()
	t.config = newCfg
	t.mu.Unlock()
	return nil
}

// Status returns current status (implements ManagedChannel)
func (t *TUI) Status() types.ChannelStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return types.ChannelStatus{
		Running:   t.running,
		Connected: t.running,
		StartedAt: t.startedAt,
		Info:      "terminal",
	}
}

// Name returns the channel name (implements gateway.Channel)
func (t *TUI) Name() string {
	return "tui"
}

// Send is not supported for TUI (implements gateway.Channel)
func (t *TUI) Send(ctx context.Context, msg string) error {
	if t.channel != nil {
		return t.channel.Send(ctx, msg)
	}
	return nil
}

// SendMirror sends a mirror message (implements gateway.Channel)
func (t *TUI) SendMirror(ctx context.Context, source, userMsg, response string) error {
	if t.channel != nil {
		return t.channel.SendMirror(ctx, source, userMsg, response)
	}
	return nil
}

// HasUser checks if user can use TUI (implements gateway.Channel)
func (t *TUI) HasUser(u *user.User) bool {
	if t.channel != nil {
		return t.channel.HasUser(u)
	}
	return false
}

// StreamEvent streams an event (implements gateway.Channel)
func (t *TUI) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	if t.channel != nil {
		return t.channel.StreamEvent(u, event)
	}
	return false
}

// DeliverGhostwrite delivers a ghostwrite message (implements gateway.Channel)
func (t *TUI) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	if t.channel != nil {
		return t.channel.DeliverGhostwrite(ctx, u, message)
	}
	return nil
}

// RunBlocking runs the TUI and blocks until the user exits.
// This is the TUI's special entry point - it takes over the terminal.
func (t *TUI) RunBlocking(ctx context.Context) error {
	t.mu.Lock()
	t.running = true
	t.startedAt = time.Now()
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.running = false
		t.channel = nil
		t.mu.Unlock()
	}()

	// Get the user to run as (owner)
	u := t.users.Owner()
	if u == nil {
		return fmt.Errorf("no owner user configured")
	}

	showLogs := true
	if t.config != nil {
		showLogs = t.config.ShowLogs
	}

	return Run(ctx, t.gw, u, showLogs)
}
