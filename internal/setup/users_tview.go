// Package setup - tview-based user management editor
package setup

import (
	"fmt"
	"sort"

	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
	"golang.org/x/crypto/bcrypt"
)

// UserEditorTview manages the tview-based user management UI
type UserEditorTview struct {
	app       *forms.TviewApp
	usersPath string
	users     user.UsersConfig
	modified  bool
}

// NewUserEditorTview creates a new tview user editor
func NewUserEditorTview() *UserEditorTview {
	return &UserEditorTview{
		usersPath: user.GetUsersFilePath(),
	}
}

// Run executes the user editor
func (e *UserEditorTview) Run() error {
	// Load existing users
	users, err := user.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}
	e.users = users

	// Create UI
	e.app = forms.NewTviewApp("GoClaw User Management")

	// Show main menu
	e.showMainMenu()

	e.app.SetOnEscape(func() {
		e.confirmExit()
	})

	// Run
	return e.app.RunWithCleanup()
}

// showMainMenu displays the main user management menu
func (e *UserEditorTview) showMainMenu() {
	e.app.SetBreadcrumbs([]string{"User Management"})

	// Build status text
	status := e.getUserSummary()
	if e.modified {
		status += " (modified)"
	}
	e.app.SetStatusText(status)

	// Build menu items
	items := []forms.MenuItem{
		{Label: "Add new user", OnSelect: e.addUser},
	}

	if len(e.users) > 0 {
		items = append(items,
			forms.MenuItem{Label: "Edit user", OnSelect: e.editUser},
			forms.MenuItem{Label: "Delete user", OnSelect: e.deleteUser},
			forms.MenuItem{Label: "Set Telegram ID", OnSelect: e.setTelegramID},
			forms.MenuItem{Label: "Set HTTP password", OnSelect: e.setPassword},
		)
	}

	items = append(items, forms.MenuItem{IsSeparator: true})

	if e.modified {
		items = append(items,
			forms.MenuItem{Label: "Save and exit", OnSelect: e.saveAndExit},
			forms.MenuItem{Label: "Exit without saving", OnSelect: e.confirmExit},
		)
	} else {
		items = append(items,
			forms.MenuItem{Label: "Exit", OnSelect: func() { e.app.Stop() }},
		)
	}

	e.app.SetMenuContent(forms.NewMenuList(forms.MenuListConfig{
		Items:    items,
		OnBack:   e.confirmExit,
		ShowBack: false,
	}))
}

// getUserSummary returns a summary of configured users
func (e *UserEditorTview) getUserSummary() string {
	if len(e.users) == 0 {
		return "No users configured"
	}

	// Find owner
	for username, entry := range e.users {
		if entry.Role == "owner" {
			return fmt.Sprintf("%d user(s): %s (owner)", len(e.users), username)
		}
	}

	return fmt.Sprintf("%d user(s)", len(e.users))
}

// addUser shows the add user form
func (e *UserEditorTview) addUser() {
	e.app.SetBreadcrumbs([]string{"User Management", "Add User"})
	e.app.SetStatusText("Fill in user details")

	var username, displayName, telegramID string
	var roleIndex int

	form := tview.NewForm()
	form.AddInputField("Username", "", 30, nil, func(text string) {
		username = text
	})
	form.AddInputField("Display Name", "", 30, nil, func(text string) {
		displayName = text
	})
	form.AddDropDown("Role", []string{"user", "owner"}, 0, func(option string, index int) {
		roleIndex = index
	})
	form.AddInputField("Telegram ID (optional)", "", 20, nil, func(text string) {
		telegramID = text
	})

	form.AddButton("Save", func() {
		// Validate
		if username == "" || displayName == "" {
			e.app.SetStatusText("Username and display name are required")
			return
		}
		if err := user.ValidateUsername(username); err != nil {
			e.app.SetStatusText(fmt.Sprintf("Invalid username: %v", err))
			return
		}
		if _, exists := e.users[username]; exists {
			e.app.SetStatusText(fmt.Sprintf("User '%s' already exists", username))
			return
		}

		role := "user"
		if roleIndex == 1 {
			role = "owner"
		}

		// Create user
		e.users[username] = &user.UserEntry{
			Name:       displayName,
			Role:       role,
			TelegramID: telegramID,
		}
		e.modified = true
		L_info("users: added user", "username", username, "role", role)
		e.app.SetStatusText(fmt.Sprintf("User '%s' added", username))
		e.showMainMenu()
	})

	form.AddButton("Cancel", func() {
		e.showMainMenu()
	})

	form.SetBorder(true).SetTitle(" Add User ").SetTitleAlign(tview.AlignLeft)
	e.app.SetContent(form)
	e.app.App().SetFocus(form)
}

// editUser shows the edit user form
func (e *UserEditorTview) editUser() {
	e.selectUser("Select user to edit", func(username string) {
		if username == "" {
			e.showMainMenu()
			return
		}

		entry := e.users[username]
		e.app.SetBreadcrumbs([]string{"User Management", "Edit User", username})
		e.app.SetStatusText("Edit user details")

		displayName := entry.Name
		telegramID := entry.TelegramID
		roleIndex := 0
		if entry.Role == "owner" {
			roleIndex = 1
		}

		form := tview.NewForm()
		form.AddInputField("Display Name", displayName, 30, nil, func(text string) {
			displayName = text
		})
		form.AddDropDown("Role", []string{"user", "owner"}, roleIndex, func(option string, index int) {
			roleIndex = index
		})
		form.AddInputField("Telegram ID", telegramID, 20, nil, func(text string) {
			telegramID = text
		})

		form.AddButton("Save", func() {
			role := "user"
			if roleIndex == 1 {
				role = "owner"
			}

			changed := false
			if displayName != entry.Name {
				entry.Name = displayName
				changed = true
			}
			if role != entry.Role {
				entry.Role = role
				changed = true
			}
			if telegramID != entry.TelegramID {
				entry.TelegramID = telegramID
				changed = true
			}

			if changed {
				e.modified = true
				L_info("users: updated user", "username", username)
				e.app.SetStatusText(fmt.Sprintf("User '%s' updated", username))
			}
			e.showMainMenu()
		})

		form.AddButton("Cancel", func() {
			e.showMainMenu()
		})

		form.SetBorder(true).SetTitle(fmt.Sprintf(" Edit: %s ", username)).SetTitleAlign(tview.AlignLeft)
		e.app.SetContent(form)
		e.app.App().SetFocus(form)
	})
}

// deleteUser shows the delete user confirmation
func (e *UserEditorTview) deleteUser() {
	e.selectUser("Select user to delete", func(username string) {
		if username == "" {
			e.showMainMenu()
			return
		}

		entry := e.users[username]
		e.app.SetBreadcrumbs([]string{"User Management", "Delete User"})

		warning := ""
		if entry.Role == "owner" {
			warning = " [WARNING: Owner account!]"
		}

		form := tview.NewForm()
		form.AddCheckbox(fmt.Sprintf("Confirm deletion of '%s'%s", username, warning), false, nil)

		form.AddButton("Delete", func() {
			// Check if checkbox is checked
			checkbox := form.GetFormItem(0).(*tview.Checkbox)
			if checkbox.IsChecked() {
				delete(e.users, username)
				e.modified = true
				L_info("users: deleted user", "username", username)
				e.app.SetStatusText(fmt.Sprintf("User '%s' deleted", username))
			}
			e.showMainMenu()
		})

		form.AddButton("Cancel", func() {
			e.showMainMenu()
		})

		form.SetBorder(true).SetTitle(" Delete User ").SetTitleAlign(tview.AlignLeft)
		e.app.SetContent(form)
		e.app.App().SetFocus(form)
	})
}

// setTelegramID shows the set Telegram ID form
func (e *UserEditorTview) setTelegramID() {
	e.selectUser("Select user", func(username string) {
		if username == "" {
			e.showMainMenu()
			return
		}

		entry := e.users[username]
		e.app.SetBreadcrumbs([]string{"User Management", "Telegram ID", username})
		e.app.SetStatusText("Set or clear Telegram ID")

		telegramID := entry.TelegramID

		form := tview.NewForm()
		form.AddInputField("Telegram ID", telegramID, 20, nil, func(text string) {
			telegramID = text
		})

		form.AddButton("Save", func() {
			if telegramID != entry.TelegramID {
				entry.TelegramID = telegramID
				e.modified = true
				if telegramID == "" {
					e.app.SetStatusText(fmt.Sprintf("Telegram ID cleared for '%s'", username))
				} else {
					e.app.SetStatusText(fmt.Sprintf("Telegram ID set for '%s'", username))
				}
			}
			e.showMainMenu()
		})

		form.AddButton("Cancel", func() {
			e.showMainMenu()
		})

		form.SetBorder(true).SetTitle(fmt.Sprintf(" Telegram ID: %s ", username)).SetTitleAlign(tview.AlignLeft)
		e.app.SetContent(form)
		e.app.App().SetFocus(form)
	})
}

// setPassword shows the set password form
func (e *UserEditorTview) setPassword() {
	e.selectUser("Select user", func(username string) {
		if username == "" {
			e.showMainMenu()
			return
		}

		entry := e.users[username]
		e.app.SetBreadcrumbs([]string{"User Management", "Set Password", username})
		e.app.SetStatusText("Set HTTP password (leave empty to clear)")

		var password, confirm string

		form := tview.NewForm()
		form.AddPasswordField("New Password", "", 30, '*', func(text string) {
			password = text
		})
		form.AddPasswordField("Confirm Password", "", 30, '*', func(text string) {
			confirm = text
		})

		form.AddButton("Save", func() {
			if password == "" {
				// Clear password
				entry.HTTPPasswordHash = ""
				e.modified = true
				e.app.SetStatusText(fmt.Sprintf("Password cleared for '%s'", username))
				e.showMainMenu()
				return
			}

			if password != confirm {
				e.app.SetStatusText("Passwords do not match")
				return
			}

			// Hash password
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				e.app.SetStatusText("Failed to hash password")
				return
			}

			entry.HTTPPasswordHash = string(hash)
			e.modified = true
			e.app.SetStatusText(fmt.Sprintf("Password set for '%s'", username))
			e.showMainMenu()
		})

		form.AddButton("Cancel", func() {
			e.showMainMenu()
		})

		form.SetBorder(true).SetTitle(fmt.Sprintf(" Password: %s ", username)).SetTitleAlign(tview.AlignLeft)
		e.app.SetContent(form)
		e.app.App().SetFocus(form)
	})
}

// selectUser shows a menu to select a user, then calls the callback
func (e *UserEditorTview) selectUser(title string, callback func(username string)) {
	if len(e.users) == 0 {
		e.app.SetStatusText("No users to select")
		e.showMainMenu()
		return
	}

	e.app.SetBreadcrumbs([]string{"User Management", title})
	e.app.SetStatusText("Select a user")

	// Build sorted user list
	var usernames []string
	for username := range e.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	// Build menu items
	items := make([]forms.MenuItem, 0, len(usernames)+2)
	for _, username := range usernames {
		u := username // capture for closure
		entry := e.users[username]
		label := fmt.Sprintf("%s (%s)", username, entry.Role)
		items = append(items, forms.MenuItem{
			Label:    label,
			OnSelect: func() { callback(u) },
		})
	}
	items = append(items, forms.MenuItem{IsSeparator: true})
	items = append(items, forms.MenuItem{
		Label:    "Cancel",
		OnSelect: func() { callback("") },
	})

	e.app.SetMenuContent(forms.NewMenuList(forms.MenuListConfig{
		Items:  items,
		OnBack: func() { callback("") },
	}))
}

// saveAndExit saves users and exits
func (e *UserEditorTview) saveAndExit() {
	if err := config.BackupAndWriteJSON(e.usersPath, e.users, config.DefaultBackupCount); err != nil {
		L_error("users: failed to save", "error", err)
		e.app.SetStatusText("Failed to save users")
		return
	}

	L_info("users: saved", "path", e.usersPath, "count", len(e.users))
	e.modified = false
	e.app.Stop()
}

// confirmExit handles exit with unsaved changes check
func (e *UserEditorTview) confirmExit() {
	if !e.modified {
		e.app.Stop()
		return
	}

	e.app.SetBreadcrumbs([]string{"User Management", "Exit"})
	e.app.SetStatusText("You have unsaved changes")

	form := tview.NewForm()
	form.AddCheckbox("Confirm exit without saving?", false, nil)

	form.AddButton("Exit", func() {
		checkbox := form.GetFormItem(0).(*tview.Checkbox)
		if checkbox.IsChecked() {
			e.app.Stop()
		} else {
			e.showMainMenu()
		}
	})

	form.AddButton("Go Back", func() {
		e.showMainMenu()
	})

	form.SetBorder(true).SetTitle(" Unsaved Changes ").SetTitleAlign(tview.AlignLeft)
	e.app.SetContent(form)
	e.app.App().SetFocus(form)
}

// RunUserEditorTview is the entry point for the tview user editor
func RunUserEditorTview() error {
	editor := NewUserEditorTview()
	return editor.Run()
}
