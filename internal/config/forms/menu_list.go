// Package forms - MenuList helper for consistent menu rendering
package forms

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// MenuItem represents a single menu item
type MenuItem struct {
	Label       string // Display text
	OnSelect    func() // Called when item is selected
	IsSeparator bool   // If true, renders as a visual separator
}

// MenuListConfig configures the menu list behavior
type MenuListConfig struct {
	Title     string     // Border title
	Items     []MenuItem // Menu items
	OnBack    func()     // Called when Back is selected or Escape pressed
	ShowBack  bool       // Whether to show explicit Back item (default true if OnBack set)
	BackLabel string     // Custom back label (default "Back")
	MinWidth  int        // Minimum width (default 30)
}

// MenuListResult wraps the menu primitive and its focusable element
type MenuListResult struct {
	primitive tview.Primitive
	focusable tview.Primitive
}

// Primitive returns the root primitive for layout
func (m *MenuListResult) Primitive() tview.Primitive {
	return m.primitive
}

// Focusable returns the element that should receive keyboard focus
func (m *MenuListResult) Focusable() tview.Primitive {
	return m.focusable
}

// menuState tracks selection for > prefix updates
type menuState struct {
	list       *tview.List
	items      []MenuItem
	itemCount  int // number of actual items (before back)
	lastIndex  int
	hasBack    bool
	backIndex  int
}

// NewMenuList creates a consistently styled, centered menu list
func NewMenuList(cfg MenuListConfig) *MenuListResult {
	list := tview.NewList()
	list.ShowSecondaryText(false)

	state := &menuState{
		list:      list,
		items:     cfg.Items,
		lastIndex: -1,
	}

	// Calculate max width from items
	maxWidth := len(cfg.Title) + 4 // title + border padding
	for _, item := range cfg.Items {
		if !item.IsSeparator && len(item.Label)+4 > maxWidth { // +4 for "> " prefix and padding
			maxWidth = len(item.Label) + 4
		}
	}

	// Check back label width
	backLabel := cfg.BackLabel
	if backLabel == "" {
		backLabel = "Back"
	}
	if len(backLabel)+4 > maxWidth {
		maxWidth = len(backLabel) + 4
	}

	// Apply minimum width
	minWidth := cfg.MinWidth
	if minWidth == 0 {
		minWidth = 30
	}
	if maxWidth < minWidth {
		maxWidth = minWidth
	}

	// Add menu items
	separatorLine := strings.Repeat("─", maxWidth-2)
	for _, item := range cfg.Items {
		if item.IsSeparator {
			list.AddItem(separatorLine, "", 0, nil)
			state.itemCount++
			continue
		}

		itemCopy := item
		list.AddItem("  "+item.Label, "", 0, func() {
			if itemCopy.OnSelect != nil {
				itemCopy.OnSelect()
			}
		})
		state.itemCount++
	}

	// Add Back item if enabled
	showBack := cfg.ShowBack || cfg.OnBack != nil
	if showBack && cfg.OnBack != nil {
		// Add separator before Back
		list.AddItem(separatorLine, "", 0, nil)
		state.itemCount++

		list.AddItem("  "+backLabel, "", 0, func() {
			if cfg.OnBack != nil {
				cfg.OnBack()
			}
		})
		state.hasBack = true
		state.backIndex = state.itemCount
		state.itemCount++
	}

	// Styling
	list.SetHighlightFullLine(true)
	list.SetSelectedBackgroundColor(tcell.ColorDodgerBlue)
	list.SetSelectedTextColor(tcell.ColorWhite)
	list.SetMainTextColor(tcell.ColorWhite)

	// Update > prefix on selection change
	list.SetChangedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		state.updatePrefix(index)
	})

	// Initialize first item with > prefix
	if state.itemCount > 0 {
		state.updatePrefix(0)
	}

	// Border and title
	list.SetBorder(true)
	list.SetBorderColor(tcell.ColorDimGray)
	if cfg.Title != "" {
		list.SetTitle(" " + cfg.Title + " ")
		list.SetTitleAlign(tview.AlignLeft)
	}

	// Handle Escape for back navigation
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			if cfg.OnBack != nil {
				cfg.OnBack()
			}
			return nil
		}
		return event
	})

	// Calculate height: items + 2 for border
	height := state.itemCount + 2

	// Create centered layout
	innerFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(list, height, 0, true).
		AddItem(nil, 0, 1, false)

	outerFlex := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(innerFlex, maxWidth+2, 0, true). // +2 for border
		AddItem(nil, 0, 1, false)

	// Set background color on outer flex to prevent bleed-through
	outerFlex.SetBackgroundColor(tcell.ColorDefault)

	return &MenuListResult{
		primitive: outerFlex,
		focusable: list,
	}
}

// updatePrefix updates the > prefix for the selected item
func (s *menuState) updatePrefix(newIndex int) {
	// Remove > from previous selection
	if s.lastIndex >= 0 && s.lastIndex < s.list.GetItemCount() {
		mainText, secondaryText := s.list.GetItemText(s.lastIndex)
		if len(mainText) > 0 && mainText[0] == '>' {
			s.list.SetItemText(s.lastIndex, "  "+mainText[2:], secondaryText)
		}
	}

	// Add > to new selection (skip separators)
	if newIndex >= 0 && newIndex < s.list.GetItemCount() {
		mainText, secondaryText := s.list.GetItemText(newIndex)
		// Don't add prefix to separator lines
		if !strings.HasPrefix(mainText, "─") && len(mainText) >= 2 && mainText[0] == ' ' {
			s.list.SetItemText(newIndex, "> "+mainText[2:], secondaryText)
		}
	}

	s.lastIndex = newIndex
}

// NewSimpleMenuList creates a menu list with just items and back handler
func NewSimpleMenuList(items []MenuItem, onBack func()) *MenuListResult {
	return NewMenuList(MenuListConfig{
		Items:    items,
		OnBack:   onBack,
		ShowBack: true,
	})
}
