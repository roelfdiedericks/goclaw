// Package forms - reusable tview application shell
package forms

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// TviewApp is the reusable application shell providing:
// - Outer frame with title
// - Log panel with capture
// - Status bar
// - Ctrl+L switching between content and log panel
// - Common keyboard handling (Escape, etc.)
type TviewApp struct {
	app       *tview.Application
	frame     *tview.Frame
	logPanel  *tview.TextView
	statusBar *tview.TextView
	layout    *tview.Flex

	// Content area (set via SetContent)
	contentFlex  *tview.Flex
	innerContent tview.Primitive // the actual content inside contentFlex

	title      string
	appRunning bool

	// Callbacks
	onEscape func() // called when Escape is pressed
}

// NewTviewApp creates a new application shell
func NewTviewApp(title string) *TviewApp {
	a := &TviewApp{
		app:   tview.NewApplication(),
		title: title,
	}

	// Status bar
	a.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[gray]Tab/PgUp/PgDn navigate • Enter/Space edit • Ctrl+L logs • Esc cancel")

	// Log panel
	a.logPanel = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetMaxLines(100)
	a.logPanel.SetBorder(true).
		SetTitle(" Log ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDimGray)

	// Content flex (will hold the actual content)
	a.contentFlex = tview.NewFlex().SetDirection(tview.FlexRow)

	// Outer frame
	a.frame = tview.NewFrame(a.contentFlex).
		SetBorders(0, 0, 0, 0, 0, 0)
	a.frame.SetBorder(true).
		SetTitle(fmt.Sprintf(" 🐾 %s ", title)).
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDodgerBlue)

	// Main layout
	a.layout = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(a.frame, 0, 1, true).
		AddItem(a.logPanel, 6, 0, false).
		AddItem(a.statusBar, 1, 0, false)

	// Set up log hook
	a.setupLogHook()

	// Set up input capture
	a.setupInputCapture()

	return a
}

// SetContent sets the main content area
func (a *TviewApp) SetContent(content tview.Primitive) {
	a.innerContent = content
	a.contentFlex.Clear()
	a.contentFlex.AddItem(content, 0, 1, true)
}

// SetTitle updates the frame title
func (a *TviewApp) SetTitle(title string) {
	a.title = title
	a.frame.SetTitle(fmt.Sprintf(" 🐾 %s ", title))
}

// SetStatusText updates the status bar text
func (a *TviewApp) SetStatusText(text string) {
	a.statusBar.SetText(text)
}

// SetOnEscape sets the callback for Escape key
func (a *TviewApp) SetOnEscape(fn func()) {
	a.onEscape = fn
}

// App returns the underlying tview.Application
func (a *TviewApp) App() *tview.Application {
	return a.app
}

// LogPanel returns the log panel for direct access
func (a *TviewApp) LogPanel() *tview.TextView {
	return a.logPanel
}

// Focus sets focus to the content area
func (a *TviewApp) FocusContent() {
	if a.innerContent != nil {
		a.app.SetFocus(a.innerContent)
	}
}

// FocusLog sets focus to the log panel
func (a *TviewApp) FocusLog() {
	a.app.SetFocus(a.logPanel)
}

// Stop stops the application
func (a *TviewApp) Stop() {
	a.app.Stop()
}

// Run starts the application
func (a *TviewApp) Run() error {
	logging.L_info("setup: opened", "title", a.title)
	a.appRunning = true
	return a.app.SetRoot(a.layout, true).EnableMouse(true).Run()
}

// RunWithCleanup runs and ensures log hook is cleared on exit
func (a *TviewApp) RunWithCleanup() error {
	defer logging.SetHookExclusive(nil)
	return a.Run()
}

// setupLogHook configures log capture
func (a *TviewApp) setupLogHook() {
	logging.SetHookExclusive(func(level, msg string) {
		var color string
		switch level {
		case "ERROR", "FATAL":
			color = "[red]"
		case "WARN":
			color = "[yellow]"
		case "INFO":
			color = "[green]"
		case "DEBUG", "TRAC":
			color = "[gray]"
		default:
			color = "[white]"
		}

		timestamp := time.Now().Format("2006/01/02 15:04:05")
		line := fmt.Sprintf("[gray]%s %s%s:[white] %s\n", timestamp, color, level, msg)

		if a.appRunning {
			go func() {
				a.app.QueueUpdateDraw(func() {
					_, _ = fmt.Fprint(a.logPanel, line)
					a.logPanel.ScrollToEnd()
				})
			}()
		} else {
			_, _ = fmt.Fprint(a.logPanel, line)
		}
	})
}

// setupInputCapture configures global key handling
func (a *TviewApp) setupInputCapture() {
	a.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			if a.onEscape != nil {
				a.onEscape()
			} else {
				a.app.Stop()
			}
			return nil

		case tcell.KeyCtrlL:
			// Toggle focus between log panel and content
			if a.logPanel.HasFocus() {
				a.FocusContent()
			} else {
				a.FocusLog()
			}
			return nil
		}
		return event
	})
}
