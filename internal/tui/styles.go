package tui

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	primaryColor   = lipgloss.Color("39")  // Blue
	secondaryColor = lipgloss.Color("245") // Gray
	accentColor    = lipgloss.Color("212") // Pink
	errorColor     = lipgloss.Color("196") // Red
	successColor   = lipgloss.Color("82")  // Green
	warningColor   = lipgloss.Color("214") // Orange
)

// Styles
var (
	// Panel borders
	focusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor)

	unfocusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(secondaryColor)

	// Title styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Padding(0, 1)

	// Message styles
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")). // Cyan
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")) // Light yellow

	toolStyle = lipgloss.NewStyle().
			Foreground(warningColor).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(errorColor)

	// Input styles
	inputPromptStyle = lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true)

	// Log level styles
	logDebugStyle = lipgloss.NewStyle().Foreground(secondaryColor)
	logInfoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	logWarnStyle  = lipgloss.NewStyle().Foreground(warningColor)
	logErrorStyle = lipgloss.NewStyle().Foreground(errorColor)

	// Status bar
	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("252")).
			Padding(0, 1)

	// Help text
	helpStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Italic(true)

	// Mirror message style (for cross-channel visibility)
	mirrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("105")) // Purple for mirrored content
)
