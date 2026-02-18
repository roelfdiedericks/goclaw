// Package setup - onboarding wizard
package setup

import (
	"fmt"

	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// RunOnboardWizard runs the onboarding wizard
func RunOnboardWizard() error {
	L_debug("setup: starting onboard wizard")

	steps := []forms.WizardStep{
		{
			Title: "Welcome to GoClaw",
			Content: func(w *forms.Wizard) tview.Primitive {
				text := `[white]Welcome to [cyan]GoClaw[white]!

This wizard will help you set up your personal AI assistant.

We'll walk through:
  • Basic configuration
  • API keys setup
  • Communication channels

Press [yellow]Next[white] to begin.`

				tv := tview.NewTextView().
					SetDynamicColors(true).
					SetText(text)
				tv.SetBorder(false)
				return tv
			},
		},
		{
			Title: "Your Name",
			Content: func(w *forms.Wizard) tview.Primitive {
				form := tview.NewForm()
				form.SetBorder(false)

				currentVal := w.GetStringData("user_name")
				form.AddInputField("What should I call you?", currentVal, 40, nil, func(text string) {
					w.SetData("user_name", text)
				})

				return form
			},
			OnExit: func(w *forms.Wizard) error {
				name := w.GetStringData("user_name")
				if name == "" {
					return fmt.Errorf("please enter your name")
				}
				L_info("onboard: name set", "name", name)
				return nil
			},
		},
		{
			Title: "Setup Complete",
			Content: func(w *forms.Wizard) tview.Primitive {
				name := w.GetStringData("user_name")
				text := fmt.Sprintf(`[white]Great to meet you, [cyan]%s[white]!

Your basic setup is complete.

Press [yellow]Finish[white] to save and exit.

[gray](This is just a demo wizard - no actual config is saved yet)`, name)

				tv := tview.NewTextView().
					SetDynamicColors(true).
					SetText(text)
				tv.SetBorder(false)
				return tv
			},
		},
	}

	wizard := forms.NewWizard("GoClaw Onboarding", steps)
	result, err := wizard.Run()

	if err != nil {
		return fmt.Errorf("wizard error: %w", err)
	}

	switch result {
	case forms.WizardCompleted:
		name := wizard.GetStringData("user_name")
		fmt.Printf("Onboarding complete! Welcome, %s.\n", name)
	case forms.WizardCancelled:
		fmt.Println("Onboarding cancelled.")
	}

	return nil
}
