// Package web provides browser-based setup wizard and configuration editor
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// UsersAPI provides API endpoints for user management
type UsersAPI struct {
	configPath string
}

// NewUsersAPI creates a new users API handler
func NewUsersAPI(configPath string) *UsersAPI {
	return &UsersAPI{
		configPath: configPath,
	}
}

// usersPath returns the users.json path based on the config path
func (u *UsersAPI) usersPath() string {
	return user.GetUsersFilePathForConfig(u.configPath)
}

// loadUsers loads users from the correct path for this config
func (u *UsersAPI) loadUsers() (user.UsersConfig, error) {
	return user.LoadUsersFromPath(u.usersPath())
}

// UserResponse represents a user in API responses (without password hash)
type UserResponse struct {
	Username      string  `json:"username"`
	Name          string  `json:"name"`
	Role          string  `json:"role"`
	TelegramID    string  `json:"telegram_id,omitempty"`
	WhatsAppID    string  `json:"whatsapp_id,omitempty"`
	HasPassword   bool    `json:"has_password"`
	ACPAllowed    *bool   `json:"acpAllowed,omitempty"`
	Thinking      *bool   `json:"thinking,omitempty"`
	ThinkingLevel *string `json:"thinking_level,omitempty"`
	Sandbox       *bool   `json:"sandbox,omitempty"`
}

// CreateUserRequest represents a request to create a new user
type CreateUserRequest struct {
	Username      string  `json:"username"`
	Name          string  `json:"name"`
	Role          string  `json:"role"`
	TelegramID    string  `json:"telegram_id,omitempty"`
	WhatsAppID    string  `json:"whatsapp_id,omitempty"`
	Password      string  `json:"password,omitempty"`
	ACPAllowed    *bool   `json:"acpAllowed,omitempty"`
	Thinking      *bool   `json:"thinking,omitempty"`
	ThinkingLevel *string `json:"thinking_level,omitempty"`
	Sandbox       *bool   `json:"sandbox,omitempty"`
}

// UpdateUserRequest represents a request to update a user
type UpdateUserRequest struct {
	Name          string  `json:"name"`
	Role          string  `json:"role"`
	TelegramID    string  `json:"telegram_id,omitempty"`
	WhatsAppID    string  `json:"whatsapp_id,omitempty"`
	Password      *string `json:"password,omitempty"`
	ClearPassword bool    `json:"clear_password,omitempty"`
	ACPAllowed    *bool   `json:"acpAllowed,omitempty"`
	Thinking      *bool   `json:"thinking,omitempty"`
	ThinkingLevel *string `json:"thinking_level,omitempty"`
	Sandbox       *bool   `json:"sandbox,omitempty"`
}

// SetPasswordRequest represents a request to set a user's password
type SetPasswordRequest struct {
	Password string `json:"password"`
}

// UpdateOwnerPairingRequest stages resolved owner channel identities at save time.
type UpdateOwnerPairingRequest struct {
	TelegramID string `json:"telegram_id,omitempty"`
	WhatsAppID string `json:"whatsapp_id,omitempty"`
}

// HandleListUsers returns all users (GET /setup/api/users)
func (u *UsersAPI) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	// Convert to response format (sorted by username)
	var userList []UserResponse
	for username, entry := range users {
		userList = append(userList, userEntryToResponse(username, entry))
	}
	sort.Slice(userList, func(i, j int) bool {
		return userList[i].Username < userList[j].Username
	})

	// Get available roles
	roles := u.getAvailableRoles()

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"users": userList,
			"roles": roles,
		},
	})
}

// HandleUpdateOwnerPairing applies staged owner channel identities.
func (u *UsersAPI) HandleUpdateOwnerPairing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req UpdateOwnerPairingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}

	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	ownerUsername := users.GetOwner()
	if ownerUsername == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "No owner user found in users.json"})
		return
	}
	entry, ok := users[ownerUsername]
	if !ok || entry == nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Owner user could not be loaded"})
		return
	}

	if req.TelegramID != "" {
		entry.TelegramID = strings.TrimSpace(req.TelegramID)
	}
	if req.WhatsAppID != "" {
		entry.WhatsAppID = strings.TrimSpace(req.WhatsAppID)
	}

	if err := u.saveUsers(users); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save owner pairing identities"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    userEntryToResponse(ownerUsername, entry),
		Message: "Owner pairing identities saved",
	})
}

// HandleCreateUser creates a new user (POST /setup/api/users)
func (u *UsersAPI) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}

	// Validate username
	if err := user.ValidateUsername(req.Username); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  map[string]string{"username": err.Error()},
		})
		return
	}

	// Validate required fields
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  map[string]string{"name": "Display name is required"},
		})
		return
	}
	if req.Role == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  map[string]string{"role": "Role is required"},
		})
		return
	}
	if err := u.validateRole(req.Role); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Errors:  map[string]string{"role": err.Error()},
		})
		return
	}

	// Load existing users
	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	// Check if user already exists
	if _, exists := users[req.Username]; exists {
		writeJSON(w, http.StatusConflict, APIResponse{
			Success: false,
			Errors:  map[string]string{"username": "User already exists"},
		})
		return
	}

	// Create new user entry
	entry := &user.UserEntry{
		Name:          req.Name,
		Role:          req.Role,
		TelegramID:    req.TelegramID,
		WhatsAppID:    req.WhatsAppID,
		ACPAllowed:    req.ACPAllowed,
		Thinking:      req.Thinking,
		ThinkingLevel: req.ThinkingLevel,
		Sandbox:       req.Sandbox,
	}

	// Hash password if provided
	if req.Password != "" {
		hash, err := user.HashPassword(req.Password)
		if err != nil {
			L_error("users-api: failed to hash password", "error", err)
			writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash password"})
			return
		}
		entry.HTTPPasswordHash = hash
	}

	users[req.Username] = entry

	// Save users
	if err := u.saveUsers(users); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save users"})
		return
	}

	L_info("users-api: created user", "username", req.Username)

	writeJSON(w, http.StatusCreated, APIResponse{
		Success: true,
		Data:    userEntryToResponse(req.Username, entry),
		Message: "User created",
	})
}

// HandleUser handles GET, PUT, DELETE for a specific user
func (u *UsersAPI) HandleUser(w http.ResponseWriter, r *http.Request) {
	// Extract username from path
	path := strings.TrimPrefix(r.URL.Path, "/setup/api/users/")
	parts := strings.Split(path, "/")
	username := parts[0]

	if username == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Username required"})
		return
	}

	// Check for password endpoint
	if len(parts) > 1 && parts[1] == "password" {
		u.handlePassword(w, r, username)
		return
	}

	switch r.Method {
	case http.MethodGet:
		u.getUser(w, username)
	case http.MethodPut:
		u.updateUser(w, r, username)
	case http.MethodDelete:
		u.deleteUser(w, username)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func (u *UsersAPI) getUser(w http.ResponseWriter, username string) {
	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	entry, exists := users[username]
	if !exists {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "User not found"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    userEntryToResponse(username, entry),
	})
}

func (u *UsersAPI) updateUser(w http.ResponseWriter, r *http.Request, username string) {
	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}

	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	entry, exists := users[username]
	if !exists {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "User not found"})
		return
	}

	if req.Role != "" {
		if err := u.validateRole(req.Role); err != nil {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Errors:  map[string]string{"role": err.Error()},
			})
			return
		}
		if entry.Role == "owner" && req.Role != "owner" && ownerCount(users) <= 1 {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Errors:  map[string]string{"role": "Cannot demote the last owner account"},
			})
			return
		}
	}

	// Update fields
	if req.Name != "" {
		entry.Name = req.Name
	}
	if req.Role != "" {
		entry.Role = req.Role
	}
	entry.TelegramID = req.TelegramID
	entry.WhatsAppID = req.WhatsAppID
	if req.ClearPassword {
		entry.HTTPPasswordHash = ""
		L_info("users-api: cleared password", "username", username)
	} else if req.Password != nil {
		if strings.TrimSpace(*req.Password) == "" {
			entry.HTTPPasswordHash = ""
			L_info("users-api: cleared password", "username", username)
		} else {
			hash, err := user.HashPassword(*req.Password)
			if err != nil {
				L_error("users-api: failed to hash password", "error", err)
				writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash password"})
				return
			}
			entry.HTTPPasswordHash = hash
			L_info("users-api: set password", "username", username)
		}
	}
	entry.Thinking = req.Thinking
	entry.ThinkingLevel = req.ThinkingLevel
	entry.ACPAllowed = req.ACPAllowed
	entry.Sandbox = req.Sandbox

	// Save users
	if err := u.saveUsers(users); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save users"})
		return
	}

	L_info("users-api: updated user", "username", username)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    userEntryToResponse(username, entry),
		Message: "User updated",
	})
}

func (u *UsersAPI) deleteUser(w http.ResponseWriter, username string) {
	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	entry, exists := users[username]
	if !exists {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "User not found"})
		return
	}

	// Check if this is the last owner
	if entry.Role == "owner" {
		ownerCount := 0
		for _, e := range users {
			if e.Role == "owner" {
				ownerCount++
			}
		}
		if ownerCount <= 1 {
			writeJSON(w, http.StatusBadRequest, APIResponse{
				Success: false,
				Message: "Cannot delete the last owner account",
			})
			return
		}
	}

	delete(users, username)

	// Save users
	if err := u.saveUsers(users); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save users"})
		return
	}

	L_info("users-api: deleted user", "username", username)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "User deleted",
	})
}

func (u *UsersAPI) handlePassword(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req SetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid JSON"})
		return
	}

	users, err := u.loadUsers()
	if err != nil {
		L_error("users-api: failed to load users", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to load users"})
		return
	}

	entry, exists := users[username]
	if !exists {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "User not found"})
		return
	}

	// Set or clear password
	if req.Password == "" {
		entry.HTTPPasswordHash = ""
		L_info("users-api: cleared password", "username", username)
	} else {
		hash, err := user.HashPassword(req.Password)
		if err != nil {
			L_error("users-api: failed to hash password", "error", err)
			writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash password"})
			return
		}
		entry.HTTPPasswordHash = hash
		L_info("users-api: set password", "username", username)
	}

	// Save users
	if err := u.saveUsers(users); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save users"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Password updated",
	})
}

// HandleRoles returns available roles (GET /setup/api/roles)
func (u *UsersAPI) HandleRoles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	roles := u.getAvailableRoles()

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    roles,
	})
}

// getAvailableRoles returns built-in roles plus custom roles from config
func (u *UsersAPI) getAvailableRoles() []string {
	roles := []string{"owner", "user", "guest"}

	// Try to load custom roles from config
	result, err := u.loadConfig()
	if err == nil && result.Config != nil && result.Config.Roles != nil {
		for roleName := range result.Config.Roles {
			// Avoid duplicates
			found := false
			for _, r := range roles {
				if r == roleName {
					found = true
					break
				}
			}
			if !found {
				roles = append(roles, roleName)
			}
		}
	}

	sort.Strings(roles)
	return roles
}

func (u *UsersAPI) validateRole(roleName string) error {
	rolesConfig := user.RolesConfig{}
	if result, err := u.loadConfig(); err == nil && result.Config != nil && result.Config.Roles != nil {
		rolesConfig = result.Config.Roles
	}
	if _, err := user.ResolveRole(roleName, rolesConfig); err != nil {
		return err
	}
	return nil
}

func ownerCount(users user.UsersConfig) int {
	count := 0
	for _, entry := range users {
		if entry != nil && entry.Role == "owner" {
			count++
		}
	}
	return count
}

func (u *UsersAPI) loadConfig() (*config.LoadResult, error) {
	if strings.TrimSpace(u.configPath) == "" {
		return config.Load()
	}
	result, err := config.LoadFromPath(u.configPath)
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", u.configPath, err)
	}
	return result, nil
}

// saveUsers saves users to the users.json file
func (u *UsersAPI) saveUsers(users user.UsersConfig) error {
	path := u.usersPath()
	if err := config.BackupAndWriteJSON(path, users, config.DefaultBackupCount); err != nil {
		L_error("users-api: failed to save users", "path", path, "error", err)
		return err
	}
	return nil
}

// userEntryToResponse converts a UserEntry to a UserResponse (without password hash)
func userEntryToResponse(username string, entry *user.UserEntry) UserResponse {
	return UserResponse{
		Username:      username,
		Name:          entry.Name,
		Role:          entry.Role,
		TelegramID:    entry.TelegramID,
		WhatsAppID:    entry.WhatsAppID,
		HasPassword:   entry.HTTPPasswordHash != "",
		ACPAllowed:    entry.ACPAllowed,
		Thinking:      entry.Thinking,
		ThinkingLevel: entry.ThinkingLevel,
		Sandbox:       entry.Sandbox,
	}
}
