// Package forms - wizard support for tview
package forms

import (
	"fmt"

	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// WizardStep defines a single step in the wizard
type WizardStep struct {
	Title   string                            // Step title (shown in frame)
	Content func(w *Wizard) tview.Primitive  // Builds the step's UI
	OnEnter func(w *Wizard)                  // Called when step becomes active
	OnExit  func(w *Wizard) error            // Called before leaving step (validate)
}

// WizardResult indicates how the wizard was closed
type WizardResult int

const (
	WizardCompleted WizardResult = iota
	WizardCancelled
	WizardError
)

// Wizard manages a multi-step wizard flow
type Wizard struct {
	app       *TviewApp
	steps     []WizardStep
	stepIndex int
	result    WizardResult

	// UI elements
	contentArea *tview.Flex
	buttonBar   *tview.Form
	stepLabel   *tview.TextView

	// Data storage for steps to share data
	Data map[string]any
}

// NewWizard creates a new wizard with the given steps
func NewWizard(title string, steps []WizardStep) *Wizard {
	w := &Wizard{
		app:       NewTviewApp(title),
		steps:     steps,
		stepIndex: 0,
		result:    WizardCancelled,
		Data:      make(map[string]any),
	}

	w.setupUI()
	return w
}

// setupUI creates the wizard UI structure
func (w *Wizard) setupUI() {
	// Step indicator
	w.stepLabel = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)

	// Content area for step content
	w.contentArea = tview.NewFlex().SetDirection(tview.FlexRow)

	// Button bar
	w.buttonBar = tview.NewForm()
	w.buttonBar.SetBorder(false)
	w.buttonBar.SetButtonsAlign(tview.AlignCenter)

	w.buttonBar.AddButton("< Back", func() {
		w.prevStep()
	})
	w.buttonBar.AddButton("Next >", func() {
		w.nextStep()
	})
	w.buttonBar.AddButton("Cancel", func() {
		w.result = WizardCancelled
		w.app.Stop()
	})

	// Layout: step label + content + button bar
	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(w.stepLabel, 1, 0, false).
		AddItem(w.contentArea, 0, 1, true).
		AddItem(w.buttonBar, 3, 0, false)

	w.app.SetContent(layout)
	w.app.SetStatusText("[gray]Tab navigate • Enter select • Ctrl+L logs • Esc cancel")

	// Handle Escape
	w.app.SetOnEscape(func() {
		w.result = WizardCancelled
		w.app.Stop()
	})

	// Set up button bar navigation
	w.setupButtonNavigation()
}

// setupButtonNavigation handles Tab cycling between content and buttons
func (w *Wizard) setupButtonNavigation() {
	// We need to intercept Tab at boundaries
	// This is simplified - the content area needs to cooperate
}

// Run executes the wizard
func (w *Wizard) Run() (WizardResult, error) {
	if len(w.steps) == 0 {
		return WizardError, fmt.Errorf("wizard has no steps")
	}

	// Show first step
	w.showStep(0)

	// Run the app
	if err := w.app.RunWithCleanup(); err != nil {
		return WizardError, err
	}

	return w.result, nil
}

// showStep displays the given step
func (w *Wizard) showStep(index int) {
	if index < 0 || index >= len(w.steps) {
		return
	}

	w.stepIndex = index
	step := w.steps[index]

	// Update title
	w.app.SetTitle(fmt.Sprintf("%s", step.Title))

	// Update step indicator
	w.stepLabel.SetText(fmt.Sprintf("[gray]Step %d of %d", index+1, len(w.steps)))

	// Clear and rebuild content
	w.contentArea.Clear()
	if step.Content != nil {
		content := step.Content(w)
		w.contentArea.AddItem(content, 0, 1, true)
		w.app.App().SetFocus(content)
	}

	// Update button states
	w.updateButtons()

	// Call OnEnter
	if step.OnEnter != nil {
		step.OnEnter(w)
	}

	logging.L_info("wizard: showing step", "step", index+1, "title", step.Title)
}

// updateButtons updates button labels and enabled state based on current step
func (w *Wizard) updateButtons() {
	// Remove and re-add buttons to update labels
	w.buttonBar.Clear(true)

	// Back button (disabled on first step)
	if w.stepIndex > 0 {
		w.buttonBar.AddButton("< Back", func() {
			w.prevStep()
		})
	} else {
		// Add disabled-looking back button
		w.buttonBar.AddButton("      ", nil) // spacer
	}

	// Next/Finish button
	if w.stepIndex >= len(w.steps)-1 {
		w.buttonBar.AddButton("Finish", func() {
			w.finish()
		})
	} else {
		w.buttonBar.AddButton("Next >", func() {
			w.nextStep()
		})
	}

	// Cancel button
	w.buttonBar.AddButton("Cancel", func() {
		w.result = WizardCancelled
		w.app.Stop()
	})
}

// nextStep advances to the next step
func (w *Wizard) nextStep() {
	// Validate current step
	step := w.steps[w.stepIndex]
	if step.OnExit != nil {
		if err := step.OnExit(w); err != nil {
			logging.L_warn("wizard: validation failed", "error", err)
			return
		}
	}

	if w.stepIndex < len(w.steps)-1 {
		w.showStep(w.stepIndex + 1)
	}
}

// prevStep goes back to the previous step
func (w *Wizard) prevStep() {
	if w.stepIndex > 0 {
		w.showStep(w.stepIndex - 1)
	}
}

// finish completes the wizard
func (w *Wizard) finish() {
	// Validate current step
	step := w.steps[w.stepIndex]
	if step.OnExit != nil {
		if err := step.OnExit(w); err != nil {
			logging.L_warn("wizard: validation failed", "error", err)
			return
		}
	}

	w.result = WizardCompleted
	w.app.Stop()
}

// CurrentStep returns the current step index
func (w *Wizard) CurrentStep() int {
	return w.stepIndex
}

// SetData stores data in the wizard's shared data map
func (w *Wizard) SetData(key string, value any) {
	w.Data[key] = value
}

// GetData retrieves data from the wizard's shared data map
func (w *Wizard) GetData(key string) any {
	return w.Data[key]
}

// GetStringData retrieves a string value from the wizard's data map
func (w *Wizard) GetStringData(key string) string {
	if v, ok := w.Data[key].(string); ok {
		return v
	}
	return ""
}

// App returns the underlying TviewApp
func (w *Wizard) App() *TviewApp {
	return w.app
}

// CreateTextStep creates a simple text display step
func CreateTextStep(title, text string) WizardStep {
	return WizardStep{
		Title: title,
		Content: func(w *Wizard) tview.Primitive {
			tv := tview.NewTextView().
				SetDynamicColors(true).
				SetText(text).
				SetTextAlign(tview.AlignCenter)
			tv.SetBorder(false)

			// Center vertically
			flex := tview.NewFlex().
				SetDirection(tview.FlexRow).
				AddItem(nil, 0, 1, false).
				AddItem(tv, 3, 0, false).
				AddItem(nil, 0, 1, false)
			return flex
		},
	}
}

// CreateInputStep creates a step with a single text input
func CreateInputStep(title, prompt, dataKey string) WizardStep {
	return WizardStep{
		Title: title,
		Content: func(w *Wizard) tview.Primitive {
			form := tview.NewForm()
			form.SetBorder(false)

			currentVal := w.GetStringData(dataKey)
			form.AddInputField(prompt, currentVal, 40, nil, func(text string) {
				w.SetData(dataKey, text)
			})

			// Center the form
			flex := tview.NewFlex().
				SetDirection(tview.FlexRow).
				AddItem(nil, 0, 1, false).
				AddItem(form, 3, 0, true).
				AddItem(nil, 0, 1, false)
			return flex
		},
	}
}
