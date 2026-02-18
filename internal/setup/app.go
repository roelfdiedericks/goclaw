// Package setup provides the interactive setup wizard for GoClaw.
package setup

import (
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Frame title constants
const (
	FrameTitleSetup      = "🐾 GoClaw Setup"
	FrameTitleOnboarding = "🐾 GoClaw Onboarding"
	FrameTitleUsers      = "🐾 GoClaw Users"
)

// App colors - match the main TUI
var (
	appPrimaryColor   = lipgloss.Color("39")  // Blue
	appSecondaryColor = lipgloss.Color("245") // Gray
	appAccentColor    = lipgloss.Color("87")  // Cyan/Light Blue
)

// App styles
var (
	appFrameTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(appAccentColor).
				Background(lipgloss.Color("0"))

	appSubtitleStyle = lipgloss.NewStyle().
				Foreground(appSecondaryColor).
				Italic(true).
				MarginBottom(1)

	appHelpStyle = lipgloss.NewStyle().
			Foreground(appSecondaryColor).
			MarginTop(1)
)

// AppKeyMap defines key bindings for the app
type AppKeyMap struct {
	Quit key.Binding
}

// DefaultAppKeyMap returns the default key bindings
func DefaultAppKeyMap() AppKeyMap {
	return AppKeyMap{
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
	}
}

// ============================================================================
// Session - Persistent UI session that eliminates screen flashing
// ============================================================================

// Session runs a single persistent tea.Program for the entire setup flow.
// Forms are swapped within the program without exiting, eliminating flicker.
type Session struct {
	frameTitle string
	program    *tea.Program
	model      *sessionModel
	mu         sync.Mutex
	active     bool
	done       chan struct{}
}

// sessionModel is the bubbletea model for the persistent session
type sessionModel struct {
	frameTitle string
	subtitle   string
	form       *huh.Form
	width      int
	height     int
	quitting   bool

	// Channel for returning form results
	formResult chan formResultMsg
}

type formResultMsg struct {
	aborted bool
}

// Messages for the session
type sessionSetFormMsg struct {
	form     *huh.Form
	subtitle string
}

type sessionQuitMsg struct{}

// NewSession creates a new session
func NewSession(frameTitle string) *Session {
	return &Session{
		frameTitle: frameTitle,
		done:       make(chan struct{}),
	}
}

// Start initializes and starts the persistent tea.Program
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active {
		return nil
	}

	s.model = &sessionModel{
		frameTitle: s.frameTitle,
		formResult: make(chan formResultMsg),
	}

	s.program = tea.NewProgram(s.model, tea.WithAltScreen())

	// Run the program in a goroutine
	go func() {
		s.program.Run() //nolint:errcheck
		close(s.done)
	}()

	s.active = true
	return nil
}

// End signals the program to quit and waits for it to finish
func (s *Session) End() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	prog := s.program
	done := s.done
	s.mu.Unlock()

	if prog != nil {
		prog.Send(sessionQuitMsg{})
		<-done // Wait for program to exit
	}
}

// IsActive returns true if the session is running
func (s *Session) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// RunForm displays a form and waits for it to complete
func (s *Session) RunForm(subtitle string, form *huh.Form) error {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		// Fall back to standalone mode
		return runFormStandalone(s.frameTitle, subtitle, form)
	}
	prog := s.program
	model := s.model
	s.mu.Unlock()

	// Send the form to the program
	prog.Send(sessionSetFormMsg{form: form, subtitle: subtitle})

	// Wait for result
	result := <-model.formResult
	if result.aborted {
		return huh.ErrUserAborted
	}
	return nil
}

// runFormStandalone runs a form without a session (fallback)
func runFormStandalone(frameTitle, subtitle string, form *huh.Form) error {
	app := NewApp(frameTitle, form).WithSubtitle(subtitle)
	return app.Run()
}

func (m *sessionModel) Init() tea.Cmd {
	return nil
}

func (m *sessionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			// Signal abort if form is active
			if m.form != nil {
				select {
				case m.formResult <- formResultMsg{aborted: true}:
				default:
				}
				m.form = nil
			}
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case sessionSetFormMsg:
		m.form = msg.form
		m.subtitle = msg.subtitle
		return m, m.form.Init()

	case sessionQuitMsg:
		m.quitting = true
		return m, tea.Quit
	}

	// Update form if active
	if m.form != nil {
		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f
		}

		// Check form state
		if m.form.State == huh.StateCompleted {
			select {
			case m.formResult <- formResultMsg{aborted: false}:
			default:
			}
			m.form = nil
		} else if m.form.State == huh.StateAborted {
			select {
			case m.formResult <- formResultMsg{aborted: true}:
			default:
			}
			m.form = nil
		}

		return m, cmd
	}

	return m, nil
}

func (m *sessionModel) View() string {
	if m.quitting {
		return ""
	}

	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	// Frame dimensions
	frameWidth := m.width - 4
	frameHeight := m.height - 3

	// Build content
	var content string
	if m.form != nil {
		if m.subtitle != "" {
			content = lipgloss.JoinVertical(lipgloss.Left,
				appSubtitleStyle.Render(m.subtitle),
				m.form.View(),
			)
		} else {
			content = m.form.View()
		}
	} else {
		// No form active - show waiting indicator
		content = appSubtitleStyle.Render("Loading...")
	}

	// Render frame with title
	framedContent := renderFrameWithTitle(m.frameTitle, content, frameWidth, frameHeight)

	// Help text
	helpText := appHelpStyle.Render("↑/↓ navigate • enter select • esc back • ctrl+c quit")

	fullContent := lipgloss.JoinVertical(lipgloss.Center,
		framedContent,
		lipgloss.NewStyle().Width(frameWidth).Align(lipgloss.Center).Render(helpText),
	)

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		fullContent,
	)
}

// Global session for the current setup context
var (
	activeSession   *Session
	activeSessionMu sync.Mutex
)

// StartSession begins a new UI session with the given frame title
func StartSession(frameTitle string) {
	activeSessionMu.Lock()
	defer activeSessionMu.Unlock()

	if activeSession != nil {
		activeSession.End()
	}

	activeSession = NewSession(frameTitle)
	activeSession.Start() //nolint:errcheck
}

// EndSession cleanly ends the current UI session
func EndSession() {
	activeSessionMu.Lock()
	defer activeSessionMu.Unlock()

	if activeSession != nil {
		activeSession.End()
		activeSession = nil
	}
}

// GetSession returns the active session, or nil if none
func GetSession() *Session {
	activeSessionMu.Lock()
	defer activeSessionMu.Unlock()
	return activeSession
}

// App wraps a huh form in a framed, centered bubbletea application
// Used for standalone mode when no session is active
type App struct {
	form       *huh.Form
	frameTitle string // Title in the frame border (e.g., "🐾 GoClaw Setup")
	subtitle   string // Subtitle inside the content
	width      int
	height     int
	keyMap     AppKeyMap
	quitting   bool

	// Callback when form completes
	onComplete func() tea.Cmd
}

// NewApp creates a new setup app with the given form and frame title
func NewApp(frameTitle string, form *huh.Form) *App {
	return &App{
		form:       form,
		frameTitle: frameTitle,
		keyMap:     DefaultAppKeyMap(),
	}
}

// WithSubtitle sets a subtitle inside the frame
func (a *App) WithSubtitle(subtitle string) *App {
	a.subtitle = subtitle
	return a
}

// WithOnComplete sets a callback when the form completes
func (a *App) WithOnComplete(fn func() tea.Cmd) *App {
	a.onComplete = fn
	return a
}

// Init initializes the app
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		a.form.Init(),
	)
}

// Update handles messages
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			a.quitting = true
			return a, tea.Quit
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
	}

	// Pass to form
	form, cmd := a.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		a.form = f
	}

	// Check if form is complete
	if a.form.State == huh.StateCompleted {
		if a.onComplete != nil {
			return a, a.onComplete()
		}
		return a, tea.Quit
	}

	// Check if form was aborted (Escape)
	if a.form.State == huh.StateAborted {
		return a, tea.Quit
	}

	return a, cmd
}

// View renders the app
func (a *App) View() string {
	if a.quitting {
		return ""
	}

	if a.width == 0 || a.height == 0 {
		return "Loading..."
	}

	// Frame dimensions - fill the screen with small margin
	frameWidth := a.width - 4   // 2 char margin each side
	frameHeight := a.height - 3 // margin for help text below

	// Build the content (subtitle and form - title is in frame border)
	var content string
	if a.subtitle != "" {
		content = lipgloss.JoinVertical(lipgloss.Left,
			appSubtitleStyle.Render(a.subtitle),
			a.form.View(),
		)
	} else {
		content = a.form.View()
	}

	// Render the frame with title in the border
	framedContent := renderFrameWithTitle(a.frameTitle, content, frameWidth, frameHeight)

	// Help text below the frame
	helpText := appHelpStyle.Render("↑/↓ navigate • enter select • esc back • ctrl+c quit")

	// Join frame and help text
	fullContent := lipgloss.JoinVertical(lipgloss.Center,
		framedContent,
		lipgloss.NewStyle().Width(frameWidth).Align(lipgloss.Center).Render(helpText),
	)

	// Center in terminal
	return lipgloss.Place(a.width, a.height,
		lipgloss.Center, lipgloss.Center,
		fullContent,
	)
}

// Run executes the app and returns an error if the user quit
func (a *App) Run() error {
	p := tea.NewProgram(a, tea.WithAltScreen())
	_, err := p.Run()
	if a.quitting {
		return huh.ErrUserAborted
	}
	return err
}

// RunForm runs a huh form inside a framed app
// If a session is active, uses the persistent program to avoid flashing
func RunForm(title string, form *huh.Form) error {
	if s := GetSession(); s != nil && s.frameTitle == title && s.IsActive() {
		return s.RunForm("", form)
	}
	app := NewApp(title, form)
	return app.Run()
}

// RunFormWithSubtitle runs a huh form inside a framed app with subtitle
// If a session is active, uses the persistent program to avoid flashing
func RunFormWithSubtitle(title, subtitle string, form *huh.Form) error {
	if s := GetSession(); s != nil && s.frameTitle == title && s.IsActive() {
		return s.RunForm(subtitle, form)
	}
	app := NewApp(title, form).WithSubtitle(subtitle)
	return app.Run()
}

// renderFrameWithTitle renders content inside a frame with a title in the top border
func renderFrameWithTitle(title string, content string, width, height int) string {
	// Border characters for rounded border
	topLeft := "╭"
	topRight := "╮"
	bottomLeft := "╰"
	bottomRight := "╯"
	horizontal := "─"
	vertical := "│"

	// Calculate inner width (minus borders and padding)
	innerWidth := width - 2        // -2 for left/right borders
	contentWidth := innerWidth - 4 // -4 for padding (2 each side)

	// Build the title portion of the top border
	titleText := " " + title + " "
	titleLen := lipgloss.Width(titleText)
	styledTitle := appFrameTitleStyle.Render(titleText)

	// Calculate horizontal lines on each side of title
	leftLineLen := 2 // Small gap after corner
	rightLineLen := innerWidth - leftLineLen - titleLen
	if rightLineLen < 0 {
		rightLineLen = 0
	}

	// Build top border with title
	topBorder := lipgloss.NewStyle().Foreground(appPrimaryColor).Render(
		topLeft +
			strings.Repeat(horizontal, leftLineLen) +
			styledTitle +
			lipgloss.NewStyle().Foreground(appPrimaryColor).Render(strings.Repeat(horizontal, rightLineLen)) +
			topRight,
	)

	// Style for border pieces
	borderStyle := lipgloss.NewStyle().Foreground(appPrimaryColor)

	// Build content area with padding
	paddedContent := lipgloss.NewStyle().
		Width(contentWidth).
		Padding(1, 2).
		Render(content)

	// Split content into lines and add side borders
	contentLines := strings.Split(paddedContent, "\n")
	var middleLines []string
	for _, line := range contentLines {
		// Pad line to inner width
		lineWidth := lipgloss.Width(line)
		padding := innerWidth - lineWidth
		if padding < 0 {
			padding = 0
		}
		paddedLine := line + strings.Repeat(" ", padding)
		middleLines = append(middleLines, borderStyle.Render(vertical)+paddedLine+borderStyle.Render(vertical))
	}

	// Ensure we have enough lines to fill height
	contentHeight := height - 2 // -2 for top/bottom borders
	for len(middleLines) < contentHeight {
		emptyLine := strings.Repeat(" ", innerWidth)
		middleLines = append(middleLines, borderStyle.Render(vertical)+emptyLine+borderStyle.Render(vertical))
	}

	// Build bottom border
	bottomBorder := borderStyle.Render(
		bottomLeft + strings.Repeat(horizontal, innerWidth) + bottomRight,
	)

	// Join all parts
	return topBorder + "\n" + strings.Join(middleLines, "\n") + "\n" + bottomBorder
}

// MenuApp is a specialized app for menu selection (standalone mode only)
type MenuApp struct {
	frameTitle string // Title in the frame border
	title      string // Title inside the content
	subtitle   string
	options    []huh.Option[string]
	choice     *string
	width      int
	height     int
	quitting   bool
	completed  bool
	form       *huh.Form
}

// NewMenuApp creates a new menu app
func NewMenuApp(frameTitle, title, subtitle string, options []huh.Option[string], choice *string) *MenuApp {
	m := &MenuApp{
		frameTitle: frameTitle,
		title:      title,
		subtitle:   subtitle,
		options:    options,
		choice:     choice,
	}
	m.rebuildForm()
	return m
}

func (m *MenuApp) rebuildForm() {
	m.form = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("").
				Options(m.options...).
				Value(m.choice),
		),
	).WithShowHelp(false).WithKeyMap(escKeyMap()).WithTheme(blueTheme())
}

// Init initializes the menu app
func (m *MenuApp) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		m.form.Init(),
	)
}

// Update handles messages
func (m *MenuApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	// Pass to form
	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f
	}

	// Check if form is complete
	if m.form.State == huh.StateCompleted {
		m.completed = true
		return m, tea.Quit
	}

	// Check if form was aborted (Escape)
	if m.form.State == huh.StateAborted {
		m.quitting = true
		return m, tea.Quit
	}

	return m, cmd
}

// View renders the menu app
func (m *MenuApp) View() string {
	if m.quitting || m.completed {
		return ""
	}

	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	// Frame dimensions - fill the screen with small margin
	frameWidth := m.width - 4   // 2 char margin each side
	frameHeight := m.height - 3 // margin for help text below

	// Build the content (subtitle and form only - title is in frame border)
	var content string
	if m.subtitle != "" {
		content = lipgloss.JoinVertical(lipgloss.Left,
			appSubtitleStyle.Render(m.subtitle),
			m.form.View(),
		)
	} else {
		content = m.form.View()
	}

	// Render the frame with title in the border
	framedContent := renderFrameWithTitle(m.frameTitle, content, frameWidth, frameHeight)

	// Help text below the frame
	helpText := appHelpStyle.Render("↑/↓ navigate • enter select • esc back • ctrl+c quit")

	// Join frame and help text
	fullContent := lipgloss.JoinVertical(lipgloss.Center,
		framedContent,
		lipgloss.NewStyle().Width(frameWidth).Align(lipgloss.Center).Render(helpText),
	)

	// Center in terminal
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		fullContent,
	)
}

// RunMenu runs a menu selection and returns the choice
// frameTitle appears in the border, subtitle appears inside below
// Returns error if user quit (ctrl+c) or escaped
// If a session is active, uses the persistent program to avoid flashing
func RunMenu(frameTitle, subtitle string, options []huh.Option[string], choice *string) error {
	// If session is active, create a form and run it through the session
	if s := GetSession(); s != nil && s.frameTitle == frameTitle && s.IsActive() {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("").
					Options(options...).
					Value(choice),
			),
		).WithShowHelp(false).WithKeyMap(escKeyMap()).WithTheme(blueTheme())
		return s.RunForm(subtitle, form)
	}

	// Standalone mode
	app := NewMenuApp(frameTitle, "", subtitle, options, choice)
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return err
	}
	if app.quitting {
		return huh.ErrUserAborted
	}
	return nil
}
