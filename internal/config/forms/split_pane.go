// Package forms - SplitPane helper for list + preview layouts
package forms

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SplitItem represents an item in the split pane list
type SplitItem struct {
	Label       string // Display text
	Preview     string // Preview text shown when selected
	OnSelect    func() // Called when Enter is pressed
	OnDelete    func() // Called when 'd' is pressed (optional)
	OnRename    func() // Called when 'r' is pressed (optional)
	IsSeparator bool   // If true, renders as a visual separator
}

// SplitPaneConfig configures the split pane behavior
type SplitPaneConfig struct {
	Title     string // Frame title (shown in border)
	Items     []SplitItem
	OnBack    func()
	ListWidth int // Proportion of width for list (default 25)
}

// SplitPane provides a list + preview layout
type SplitPane struct {
	list       *tview.List
	preview    *tview.TextView
	flex       *tview.Flex
	frame      *tview.Frame
	items      []SplitItem
	onBack     func()
	selectOnly int // number of selectable items (excluding back/spacer)
	title      string
}

// NewSplitPane creates a new split pane layout
func NewSplitPane(cfg SplitPaneConfig) *SplitPane {
	s := &SplitPane{
		list:    tview.NewList(),
		preview: tview.NewTextView(),
		items:   cfg.Items,
		onBack:  cfg.OnBack,
		title:   cfg.Title,
	}

	// Configure list (compact, no secondary text)
	s.list.ShowSecondaryText(false)
	s.list.SetHighlightFullLine(true)
	s.list.SetSelectedBackgroundColor(tcell.ColorDodgerBlue)
	s.list.SetSelectedTextColor(tcell.ColorWhite)

	// Configure preview
	s.preview.SetDynamicColors(true)
	s.preview.SetBorder(true)
	s.preview.SetTitle(" Preview ")
	s.preview.SetTitleAlign(tview.AlignLeft)
	s.preview.SetBorderColor(tcell.ColorDimGray)

	// Count selectable items
	selectableCount := 0
	for _, item := range cfg.Items {
		if !item.IsSeparator {
			selectableCount++
		}
	}
	s.selectOnly = selectableCount

	// Add items to list
	for _, item := range cfg.Items {
		if item.IsSeparator {
			s.list.AddItem("", "", 0, nil)
			continue
		}

		itemCopy := item
		s.list.AddItem(item.Label, "", 0, func() {
			if itemCopy.OnSelect != nil {
				itemCopy.OnSelect()
			}
		})
	}

	// Add Back item
	if cfg.OnBack != nil {
		s.list.AddItem("", "", 0, nil) // separator
		s.list.AddItem("Back", "", 0, cfg.OnBack)
	}

	// Handle selection changes to update preview
	s.list.SetChangedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		if index < len(s.items) && !s.items[index].IsSeparator {
			s.preview.SetText(s.items[index].Preview)
		} else {
			s.preview.SetText("")
		}
	})

	// Initialize preview with first item
	if len(s.items) > 0 && !s.items[0].IsSeparator {
		s.preview.SetText(s.items[0].Preview)
	}

	// Handle keyboard shortcuts
	s.list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			if s.onBack != nil {
				s.onBack()
			}
			return nil
		}

		// Handle delete and rename for selectable items only
		idx := s.list.GetCurrentItem()
		if idx < len(s.items) && !s.items[idx].IsSeparator {
			item := s.items[idx]
			switch event.Rune() {
			case 'd', 'D':
				if item.OnDelete != nil {
					item.OnDelete()
					return nil
				}
			case 'r', 'R':
				if item.OnRename != nil {
					item.OnRename()
					return nil
				}
			}
		}
		return event
	})

	// Create flex layout
	listWidth := cfg.ListWidth
	if listWidth == 0 {
		listWidth = 25
	}

	s.flex = tview.NewFlex().
		AddItem(s.list, 0, listWidth, true).
		AddItem(s.preview, 0, 100-listWidth, false)

	// Wrap in frame for consistent look (with internal padding)
	// SetBorders(top, bottom, header, footer, left, right)
	s.frame = tview.NewFrame(s.flex).
		SetBorders(1, 0, 0, 0, 1, 1)
	s.frame.SetBorder(true)
	s.frame.SetBorderColor(tcell.ColorDodgerBlue)
	if s.title != "" {
		s.frame.SetTitle(" 🐾 " + s.title + " ")
		s.frame.SetTitleAlign(tview.AlignLeft)
	}

	return s
}

// Primitive returns the root primitive for this split pane (the frame)
func (s *SplitPane) Primitive() tview.Primitive {
	return s.frame
}

// Focusable returns the element that should receive focus (the list)
func (s *SplitPane) Focusable() tview.Primitive {
	return s.list
}

// SetPreviewTitle sets the preview panel title
func (s *SplitPane) SetPreviewTitle(title string) {
	s.preview.SetTitle(" " + title + " ")
}
