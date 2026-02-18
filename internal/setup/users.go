package setup

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"golang.org/x/crypto/bcrypt"
)

// isAbort checks if the error is a user abort (Escape pressed)
func isAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

// RunUserEditor launches the TUI user management menu
func RunUserEditor() error {
	e := NewUserEditor()
	return e.Run()
}

// UserEditor manages the user management TUI
type UserEditor struct {
	usersPath string
	users     config.UsersConfig
	modified  bool
}

// NewUserEditor creates a new user editor
func NewUserEditor() *UserEditor {
	return &UserEditor{
		usersPath: config.GetUsersFilePath(),
	}
}

// Run executes the user editor
func (e *UserEditor) Run() error {
	// Load existing users
	users, err := config.LoadUsers()
	if err != nil {
		return fmt.Errorf("failed to load users: %w", err)
	}
	e.users = users

	// Suppress non-error logs during TUI
	prevLevel := suppressLogs()
	defer restoreLogs(prevLevel)

	// Start a session if not already in one (might be called from setup editor)
	needsSession := GetSession() == nil
	if needsSession {
		StartSession(FrameTitleUsers)
		defer EndSession()
	}

	return e.mainMenu()
}

func (e *UserEditor) mainMenu() error {
	for {
		// Build user summary
		userSummary := e.getUserSummary()

		// Build menu options
		options := []huh.Option[string]{
			huh.NewOption("Add new user", "add"),
		}

		if len(e.users) > 0 {
			options = append(options,
				huh.NewOption("Edit user", "edit"),
				huh.NewOption("Delete user", "delete"),
				huh.NewOption("Set Telegram ID", "telegram"),
				huh.NewOption("Set HTTP password", "password"),
			)
		}

		options = append(options,
			huh.NewOption("───────────────────", "---"),
		)

		if e.modified {
			options = append(options,
				huh.NewOption("Save and exit", "save"),
				huh.NewOption("Exit without saving", "exit"),
			)
		} else {
			options = append(options,
				huh.NewOption("Back", "exit"),
			)
		}

		var choice string
		subtitle := userSummary
		if e.modified {
			subtitle += " (modified)"
		}
		if err := RunMenu(FrameTitleUsers, subtitle, options, &choice); err != nil {
			if isAbort(err) {
				// Escape pressed - treat as exit/back
				choice = "exit"
			} else {
				return err
			}
		}

		switch choice {
		case "---":
			continue
		case "add":
			if err := e.addUser(); err != nil {
				return err
			}
		case "edit":
			if err := e.editUser(); err != nil {
				return err
			}
		case "delete":
			if err := e.deleteUser(); err != nil {
				return err
			}
		case "telegram":
			if err := e.setTelegramID(); err != nil {
				return err
			}
		case "password":
			if err := e.setPassword(); err != nil {
				return err
			}
		case "save":
			return e.save()
		case "exit":
			if e.modified {
				var confirm bool
				form := newForm(
					huh.NewGroup(
						huh.NewConfirm().
							Title("You have unsaved changes. Exit anyway?").
							Value(&confirm),
					),
				)
				if err := RunForm(FrameTitleUsers, form); err != nil {
					return err
				}
				if !confirm {
					continue
				}
			}
			return nil
		}
	}
}

func (e *UserEditor) getUserSummary() string {
	if len(e.users) == 0 {
		return "No users configured"
	}

	// Sort usernames for consistent display
	var usernames []string
	for username := range e.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	// Find owner
	for _, username := range usernames {
		entry := e.users[username]
		if entry.Role == "owner" {
			return fmt.Sprintf("%d user(s): %s (owner)", len(e.users), username)
		}
	}

	return fmt.Sprintf("%d user(s)", len(e.users))
}

func (e *UserEditor) showUserList() {
	if len(e.users) == 0 {
		fmt.Println("No users configured.")
		fmt.Println()
		return
	}

	// Sort usernames for consistent display
	var usernames []string
	for username := range e.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	fmt.Println("Current users:")
	for _, username := range usernames {
		entry := e.users[username]

		// Build status indicators
		var indicators []string
		if entry.TelegramID != "" {
			indicators = append(indicators, fmt.Sprintf("TG:%s", entry.TelegramID))
		}
		if entry.HTTPPasswordHash != "" {
			indicators = append(indicators, "HTTP:✓")
		}
		if len(indicators) == 0 {
			indicators = append(indicators, "no credentials")
		}

		fmt.Printf("  %s (%s) - %s\n", username, entry.Role, strings.Join(indicators, ", "))
	}
	fmt.Println()
}

func (e *UserEditor) addUser() error {
	var username, displayName, role, telegramID string

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Username").
				Description("Lowercase, alphanumeric + underscore, starts with letter").
				Placeholder("john_doe").
				Value(&username).
				Validate(func(s string) error {
					return config.ValidateUsername(s)
				}),
			huh.NewInput().
				Title("Display name").
				Placeholder("John Doe").
				Value(&displayName),
			huh.NewSelect[string]().
				Title("Role").
				Options(
					huh.NewOption("User (limited access)", "user"),
					huh.NewOption("Owner (full access)", "owner"),
				).
				Value(&role),
			huh.NewInput().
				Title("Telegram ID (optional)").
				Description("Numeric Telegram user ID").
				Placeholder("123456789").
				Value(&telegramID),
		),
	)

	if err := RunFormWithSubtitle(FrameTitleUsers, "Add User", form); err != nil {
		if isAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if username == "" || displayName == "" {
		fmt.Println("Cancelled - username and display name are required.")
		return nil
	}

	// Check if user already exists
	if _, exists := e.users[username]; exists {
		fmt.Printf("User '%s' already exists.\n", username)
		return nil
	}

	// Warn about multiple owners
	if role == "owner" {
		for _, entry := range e.users {
			if entry.Role == "owner" {
				fmt.Println("⚠ Warning: Another owner already exists. Multiple owners is unusual.")
				break
			}
		}
	}

	// Create user entry
	e.users[username] = &config.UserEntry{
		Name:       displayName,
		Role:       role,
		TelegramID: telegramID,
	}
	e.modified = true

	fmt.Printf("✓ User '%s' added.\n", username)

	// Offer to set password
	var setPassword bool
	form = newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Set HTTP password now?").
				Value(&setPassword),
		),
	)
	if err := RunForm(FrameTitleUsers, form); err != nil {
		if isAbort(err) {
			return nil // Escape pressed, skip password setup
		}
		return err
	}

	if setPassword {
		return e.setPasswordFor(username)
	}

	return nil
}

func (e *UserEditor) editUser() error {
	username, err := e.selectUser("Select user to edit")
	if err != nil {
		return err
	}
	if username == "" {
		return nil
	}

	entry := e.users[username]

	var displayName, role, telegramID string
	displayName = entry.Name
	role = entry.Role
	telegramID = entry.TelegramID

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Display name").
				Value(&displayName),
			huh.NewSelect[string]().
				Title("Role").
				Options(
					huh.NewOption("User (limited access)", "user"),
					huh.NewOption("Owner (full access)", "owner"),
				).
				Value(&role),
			huh.NewInput().
				Title("Telegram ID").
				Description("Numeric Telegram user ID (leave empty to clear)").
				Value(&telegramID),
		),
	)

	if err := RunFormWithSubtitle(FrameTitleUsers, fmt.Sprintf("Edit: %s", username), form); err != nil {
		if isAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	// Apply changes
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
		fmt.Printf("✓ User '%s' updated.\n", username)
	} else {
		fmt.Println("No changes made.")
	}

	return nil
}

func (e *UserEditor) deleteUser() error {
	username, err := e.selectUser("Select user to delete")
	if err != nil {
		return err
	}
	if username == "" {
		return nil
	}

	entry := e.users[username]

	// Warn about deleting owner
	if entry.Role == "owner" {
		fmt.Println("⚠ Warning: You are about to delete an owner account.")
	}

	var confirm bool
	form := newForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete user '%s'?", username)).
				Description("This cannot be undone.").
				Value(&confirm),
		),
	)

	if err := RunFormWithSubtitle(FrameTitleUsers, "Delete User", form); err != nil {
		if isAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	delete(e.users, username)
	e.modified = true
	fmt.Printf("✓ User '%s' deleted.\n", username)

	return nil
}

func (e *UserEditor) setTelegramID() error {
	username, err := e.selectUser("Select user")
	if err != nil {
		return err
	}
	if username == "" {
		return nil
	}

	entry := e.users[username]
	telegramID := entry.TelegramID

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Telegram ID").
				Description("Numeric Telegram user ID (leave empty to clear)").
				Value(&telegramID),
		),
	)

	if err := RunFormWithSubtitle(FrameTitleUsers, fmt.Sprintf("Telegram ID: %s", username), form); err != nil {
		if isAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if telegramID != entry.TelegramID {
		entry.TelegramID = telegramID
		e.modified = true
		if telegramID == "" {
			fmt.Printf("✓ Telegram ID cleared for '%s'.\n", username)
		} else {
			fmt.Printf("✓ Telegram ID set for '%s'.\n", username)
		}
	}

	return nil
}

func (e *UserEditor) setPassword() error {
	username, err := e.selectUser("Select user")
	if err != nil {
		return err
	}
	if username == "" {
		return nil
	}

	return e.setPasswordFor(username)
}

func (e *UserEditor) setPasswordFor(username string) error {
	entry := e.users[username]

	var password, confirm string

	form := newForm(
		huh.NewGroup(
			huh.NewInput().
				Title("New password").
				Description("For HTTP authentication").
				EchoMode(huh.EchoModePassword).
				Value(&password),
			huh.NewInput().
				Title("Confirm password").
				EchoMode(huh.EchoModePassword).
				Value(&confirm),
		),
	)

	if err := RunFormWithSubtitle(FrameTitleUsers, fmt.Sprintf("Password: %s", username), form); err != nil {
		if isAbort(err) {
			return nil // Escape pressed, go back
		}
		return err
	}

	if password == "" {
		// Clear password
		var clearConfirm bool
		form := newForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Clear HTTP password?").
					Description("User will no longer be able to authenticate via HTTP.").
					Value(&clearConfirm),
			),
		)
		if err := RunForm(FrameTitleUsers, form); err != nil {
			if isAbort(err) {
				return nil // Escape pressed, go back
			}
			return err
		}
		if clearConfirm {
			entry.HTTPPasswordHash = ""
			e.modified = true
			fmt.Printf("✓ HTTP password cleared for '%s'.\n", username)
		}
		return nil
	}

	if password != confirm {
		fmt.Println("✗ Passwords do not match.")
		return nil
	}

	// Hash password with bcrypt
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	entry.HTTPPasswordHash = string(hash)
	e.modified = true
	fmt.Printf("✓ HTTP password set for '%s'.\n", username)

	return nil
}

func (e *UserEditor) selectUser(title string) (string, error) {
	if len(e.users) == 0 {
		fmt.Println("No users to select.")
		return "", nil
	}

	// Build options from users
	var usernames []string
	for username := range e.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	options := make([]huh.Option[string], 0, len(usernames)+1)
	for _, username := range usernames {
		entry := e.users[username]
		label := fmt.Sprintf("%s (%s)", username, entry.Role)
		options = append(options, huh.NewOption(label, username))
	}
	options = append(options, huh.NewOption("Cancel", "_cancel"))

	var choice string
	form := newForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(title).
				Options(options...).
				Value(&choice),
		),
	)

	if err := RunForm(FrameTitleUsers, form); err != nil {
		if isAbort(err) {
			return "", nil // Escape pressed, go back
		}
		return "", err
	}

	if choice == "_cancel" {
		return "", nil
	}
	return choice, nil
}

func (e *UserEditor) save() error {
	if err := config.SaveUsers(e.users, e.usersPath); err != nil {
		return fmt.Errorf("failed to save users: %w", err)
	}

	L_info("users: saved via TUI", "path", e.usersPath, "count", len(e.users))
	fmt.Printf("\n✓ Users saved to %s\n", e.usersPath)
	e.modified = false

	return nil
}
