// Package forms - tview renderer for FormDef
package forms

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/actions"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// TviewResult indicates how the form was closed
type TviewResult int

const (
	ResultSaved TviewResult = iota
	ResultCancelled
	ResultError
)

// RenderTview renders a FormDef using tview
// - def: the form definition
// - value: pointer to the config struct (for get/set via reflection)
// - component: name for action bus (e.g., "transcript")
// Returns the result and any error
func RenderTview(def FormDef, value any, component string) (TviewResult, error) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return ResultError, fmt.Errorf("value must be a pointer to struct, got %T", value)
	}
	rv = rv.Elem()

	app := tview.NewApplication()
	form := tview.NewForm()

	// Track result
	result := ResultCancelled
	var resultErr error

	// Status bar for messages
	statusBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[gray]Tab/PgUp/PgDn navigate • Enter/Space edit • Ctrl+L logs • Esc cancel")

	// Log panel for capturing log output
	logPanel := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetMaxLines(100) // Keep last 100 lines
	logPanel.SetBorder(true).
		SetTitle(" Log ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDimGray)

	// Track if app is running
	appRunning := false

	// Set up log hook to capture messages
	logging.SetHookExclusive(func(level, msg string) {
		// Color based on level
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

		// Format the line with date and time
		timestamp := time.Now().Format("2006/01/02 15:04:05")
		line := fmt.Sprintf("[gray]%s %s%s:[white] %s\n", timestamp, color, level, msg)

		// Only update if app is running
		if appRunning {
			go func() {
				app.QueueUpdateDraw(func() {
					_, _ = fmt.Fprint(logPanel, line)
					logPanel.ScrollToEnd()
				})
			}()
		} else {
			// Before app starts, write directly
			_, _ = fmt.Fprint(logPanel, line)
		}
	})

	// Ensure we clear the hook when done (deferred)
	defer logging.SetHookExclusive(nil)

	// Add fields for each section
	for _, section := range def.Sections {
		// Add section header as a separator
		if section.Title != "" {
			form.AddTextView(fmt.Sprintf("─── %s ", section.Title), section.Desc, 0, 1, false, false)
		}

		// Handle nested FormDef
		if section.Nested != nil {
			// Use FieldName if specified, otherwise try section title
			fieldName := section.FieldName
			if fieldName == "" {
				fieldName = section.Title
			}
			nestedField := findFieldByName(rv, fieldName)
			if !nestedField.IsValid() {
				nestedField = findFieldByJSONTag(rv, fieldName)
			}
			if nestedField.IsValid() {
				addFieldsToForm(form, section.Nested.Sections, nestedField)
			}
			continue
		}

		// Add fields
		addFieldsToForm(form, []Section{section}, rv)
	}

	// Fields form - borderless
	form.SetBorder(false)

	// Create separate button bar form - borderless
	buttonBar := tview.NewForm()
	buttonBar.SetBorder(false)
	buttonBar.SetButtonsAlign(tview.AlignCenter)

	// Add action buttons to button bar
	for _, action := range def.Actions {
		actionCopy := action // capture for closure
		buttonBar.AddButton(action.Label, func() {
			logging.L_info("action: running", "action", actionCopy.Label)

			res := actions.Send(component, actionCopy.Name, value)
			if res.Error != nil {
				logging.L_error("action: failed", "action", actionCopy.Label, "error", res.Message)
			} else {
				logging.L_info("action: completed", "action", actionCopy.Label, "result", res.Message)
			}
		})
	}

	// Add Save button
	buttonBar.AddButton("Save", func() {
		result = ResultSaved
		app.Stop()
	})

	// Add Cancel button
	buttonBar.AddButton("Cancel", func() {
		result = ResultCancelled
		app.Stop()
	})

	// Helper to navigate to first/last item after a delay (lets tview settle)
	navigateAfterFocus := func(targetForm *tview.Form, toFirst bool, isButtonBar bool) {
		go func() {
			time.Sleep(20 * time.Millisecond) // let tview process focus

			var currentIdx int
			if isButtonBar {
				_, currentIdx = targetForm.GetFocusedItemIndex()
			} else {
				currentIdx, _ = targetForm.GetFocusedItemIndex()
			}

			var count int
			var key tcell.Key
			if toFirst {
				count = currentIdx
				key = tcell.KeyBacktab
			} else {
				var lastIdx int
				if isButtonBar {
					lastIdx = targetForm.GetButtonCount() - 1
				} else {
					lastIdx = targetForm.GetFormItemCount() - 1
				}
				count = lastIdx - currentIdx
				key = tcell.KeyTab
			}

			for i := 0; i < count; i++ {
				app.QueueEvent(tcell.NewEventKey(key, 0, tcell.ModNone))
			}
		}()
	}

	// Inner layout: fields form + button bar
	innerLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(form, 0, 1, true).      // fields expand
		AddItem(buttonBar, 3, 0, false) // buttons fixed height (reduced since no border)

	// Outer frame with title and border
	frame := tview.NewFrame(innerLayout).
		SetBorders(0, 0, 0, 0, 0, 0) // no inner padding from frame
	frame.SetBorder(true).
		SetTitle(fmt.Sprintf(" 🐾 %s ", def.Title)).
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(tcell.ColorDodgerBlue)

	// Final layout: frame + log panel + status bar
	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(frame, 0, 1, true).     // frame expands
		AddItem(logPanel, 6, 0, false). // log panel fixed height (4 lines + border)
		AddItem(statusBar, 1, 0, false) // status bar at bottom

	// Cross-form navigation: intercept Tab/BackTab only at form boundaries
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			result = ResultCancelled
			app.Stop()
			return nil

		case tcell.KeyCtrlL:
			// Toggle focus between log panel and form
			if logPanel.HasFocus() {
				app.SetFocus(form)
			} else {
				app.SetFocus(logPanel)
			}
			return nil

		case tcell.KeyTab, tcell.KeyPgDn:
			// Check if we're at the last item of fields form → switch to buttonBar (first button)
			if form.HasFocus() {
				idx, _ := form.GetFocusedItemIndex()
				lastIdx := form.GetFormItemCount() - 1
				if idx == lastIdx {
					app.SetFocus(buttonBar)
					navigateAfterFocus(buttonBar, true, true) // go to first button
					return nil
				}
			}
			// Check if we're at the last button of buttonBar → wrap to fields form (first field)
			if buttonBar.HasFocus() {
				_, buttonIdx := buttonBar.GetFocusedItemIndex()
				lastBtnIdx := buttonBar.GetButtonCount() - 1
				if buttonIdx == lastBtnIdx {
					app.SetFocus(form)
					navigateAfterFocus(form, true, false) // go to first field
					return nil
				}
			}
			// Not at boundary - let tview handle it (but transform PgDn to Tab)
			if event.Key() == tcell.KeyPgDn {
				return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
			}
			return event

		case tcell.KeyBacktab, tcell.KeyPgUp:
			// Check if we're at the first item of fields form → switch to buttonBar (last button)
			if form.HasFocus() {
				idx, _ := form.GetFocusedItemIndex()
				if idx == 0 {
					app.SetFocus(buttonBar)
					navigateAfterFocus(buttonBar, false, true) // go to last button
					return nil
				}
			}
			// Check if we're at the first button of buttonBar → switch to fields form (last field)
			if buttonBar.HasFocus() {
				_, buttonIdx := buttonBar.GetFocusedItemIndex()
				if buttonIdx == 0 {
					app.SetFocus(form)
					navigateAfterFocus(form, false, false) // go to last field
					return nil
				}
			}
			// Not at boundary - let tview handle it (but transform PgUp to BackTab)
			if event.Key() == tcell.KeyPgUp {
				return tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)
			}
			return event
		}
		return event
	})

	// Mouse scroll wheel navigation - inject Tab/BackTab events asynchronously
	form.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		switch action {
		case tview.MouseScrollUp:
			go func() {
				app.QueueEvent(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone))
			}()
			return 0, nil
		case tview.MouseScrollDown:
			go func() {
				app.QueueEvent(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
			}()
			return 0, nil
		}
		return action, event
	})

	// Log form startup (before Run so it appears immediately)
	logging.L_info("setup: opened form", "title", def.Title)

	// Run
	appRunning = true
	if err := app.SetRoot(layout, true).EnableMouse(true).Run(); err != nil {
		return ResultError, err
	}

	return result, resultErr
}

// addFieldsToForm adds fields from sections to the tview form
func addFieldsToForm(form *tview.Form, sections []Section, rv reflect.Value) {
	for _, section := range sections {
		for _, field := range section.Fields {
			fv := findFieldByJSONTag(rv, field.Name)
			if !fv.IsValid() {
				fv = findFieldByName(rv, field.Name)
			}
			if !fv.IsValid() {
				continue
			}

			addFieldToForm(form, field, fv)
		}
	}
}

// addFieldToForm adds a single field to the form
func addFieldToForm(form *tview.Form, field Field, fv reflect.Value) {
	label := field.Title
	if field.Desc != "" {
		label = fmt.Sprintf("%s [gray](%s)", field.Title, truncate(field.Desc, 40))
	}

	switch field.Type {
	case Toggle:
		val := fv.Bool()
		checkbox := tview.NewCheckbox().
			SetLabel(label + " ").
			SetChecked(val).
			SetCheckedString("[✓]").
			SetChangedFunc(func(checked bool) {
				if fv.CanSet() {
					fv.SetBool(checked)
				}
			})
		form.AddFormItem(checkbox)

	case Text:
		val := fmt.Sprintf("%v", fv.Interface())
		form.AddInputField(label, val, 60, nil, func(text string) {
			if fv.CanSet() {
				fv.SetString(text)
			}
		})

	case Secret:
		// Secret fields rendered as plain text in TUI (if user can run setup, they can cat config)
		// Secret type retained for semantic purposes and potential web UI masking
		// Use max width (0) since tokens/keys are typically long
		val := fmt.Sprintf("%v", fv.Interface())
		form.AddInputField(label, val, 0, nil, func(text string) {
			if fv.CanSet() {
				fv.SetString(text)
			}
		})

	case Number:
		val := fmt.Sprintf("%v", fv.Interface())
		form.AddInputField(label, val, 20, func(text string, lastChar rune) bool {
			// Allow digits, minus, and decimal point
			if lastChar == '-' || lastChar == '.' || (lastChar >= '0' && lastChar <= '9') {
				return true
			}
			return false
		}, func(text string) {
			if !fv.CanSet() {
				return
			}
			setNumericValue(fv, text)
		})

	case Select:
		options := make([]string, len(field.Options))
		currentIdx := 0
		currentVal := fmt.Sprintf("%v", fv.Interface())
		for i, opt := range field.Options {
			options[i] = opt.Label
			if opt.Value == currentVal {
				currentIdx = i
			}
		}
		form.AddDropDown(label, options, currentIdx, func(option string, index int) {
			if fv.CanSet() && index >= 0 && index < len(field.Options) {
				fv.SetString(field.Options[index].Value)
			}
		})

	case TextArea:
		val := fmt.Sprintf("%v", fv.Interface())
		form.AddTextArea(label, val, 40, 3, 0, func(text string) {
			if fv.CanSet() {
				fv.SetString(text)
			}
		})
	}
}

// setNumericValue sets a numeric value from string
func setNumericValue(fv reflect.Value, text string) {
	switch fv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			fv.SetInt(i)
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			fv.SetFloat(f)
		}
	}
}

// Note: findFieldByJSONTag is defined in render.go
// findFieldByName finds a struct field by name
func findFieldByName(rv reflect.Value, name string) reflect.Value {
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	return rv.FieldByName(name)
}

// truncate truncates a string to max length
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
