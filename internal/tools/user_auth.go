package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/config"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// SessionElevator is the interface for elevating user roles in a session
type SessionElevator interface {
	ElevateUser(name, username, role, id string)
}

// UserAuthTool handles user authentication and role elevation
type UserAuthTool struct {
	config      config.AuthConfig
	rolesConfig config.RolesConfig

	// Rate limiting
	mu           sync.Mutex
	attempts     []time.Time
	rateLimitMsg string
}

// AuthInput is the input from the agent
type AuthInput struct {
	Credentials map[string]string `json:"credentials"` // Flexible key-value pairs
}

// AuthResult is the expected output from the auth script
type AuthResult struct {
	Success bool `json:"success"`
	User    *struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Role     string `json:"role"`
		ID       string `json:"id"`
	} `json:"user,omitempty"`
	Message string `json:"message"` // Message for agent to interpret
}

// NewUserAuthTool creates a new user_auth tool
func NewUserAuthTool(authConfig config.AuthConfig, rolesConfig config.RolesConfig) *UserAuthTool {
	return &UserAuthTool{
		config:      authConfig,
		rolesConfig: rolesConfig,
		attempts:    make([]time.Time, 0),
	}
}

func (t *UserAuthTool) Name() string {
	return "user_auth"
}

func (t *UserAuthTool) Description() string {
	return "Authenticate a user and elevate their role. Use when a guest user provides credentials (ID, phone, email, etc.) to identify themselves. Returns authentication result with user info or error message."
}

func (t *UserAuthTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"credentials": map[string]any{
				"type":        "object",
				"description": "Key-value pairs of credentials to validate (e.g., {\"phone\": \"+1234567890\", \"id\": \"CUS-123\"})",
				"additionalProperties": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"credentials"},
	}
}

func (t *UserAuthTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params AuthInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if len(params.Credentials) == 0 {
		return t.formatResult(false, "", "No credentials provided. Ask the user for their identifying information."), nil
	}

	// Check rate limit
	if blocked, msg := t.checkRateLimit(); blocked {
		L_warn("user_auth: rate limited", "credentials", params.Credentials)
		return t.formatResult(false, "", msg), nil
	}

	// Record attempt
	t.recordAttempt()

	// Get session context for elevation
	sessionCtx := GetSessionContext(ctx)
	if sessionCtx == nil {
		L_error("user_auth: no session context")
		return "", fmt.Errorf("user_auth requires session context")
	}

	// Run auth script
	result, err := t.runScript(ctx, params.Credentials)
	if err != nil {
		L_error("user_auth: script error", "error", err)
		return t.formatResult(false, "", fmt.Sprintf("Authentication failed: %v", err)), nil
	}

	// Handle failure
	if !result.Success {
		L_info("user_auth: authentication failed", "message", result.Message)
		return t.formatResult(false, "", result.Message), nil
	}

	// Validate result
	if result.User == nil {
		return t.formatResult(false, "", "Authentication script returned success but no user info."), nil
	}

	// Security: Cannot elevate to owner
	if strings.ToLower(result.User.Role) == "owner" {
		L_warn("user_auth: attempted elevation to owner blocked", "user", result.User.Username)
		return t.formatResult(false, "", "Cannot elevate to owner role."), nil
	}

	// Security: Role must be in allowedRoles
	if len(t.config.AllowedRoles) > 0 && !slices.Contains(t.config.AllowedRoles, result.User.Role) {
		L_warn("user_auth: role not in allowedRoles", "role", result.User.Role, "allowed", t.config.AllowedRoles)
		return t.formatResult(false, "", fmt.Sprintf("Role '%s' is not permitted for elevation.", result.User.Role)), nil
	}

	// Security: Role must exist in roles config (unless empty = no roles defined = fail)
	if _, ok := t.rolesConfig[result.User.Role]; !ok {
		L_warn("user_auth: role not defined in config", "role", result.User.Role)
		return t.formatResult(false, "", fmt.Sprintf("Role '%s' is not defined in configuration.", result.User.Role)), nil
	}

	// Get session from context and elevate
	// Note: We need SessionElevator interface to be passed through context
	// For now, we'll return success and let the gateway handle elevation
	L_info("user_auth: authentication successful",
		"name", result.User.Name,
		"username", result.User.Username,
		"role", result.User.Role,
		"id", result.User.ID,
	)

	return t.formatSuccessResult(result), nil
}

// runScript executes the auth script with credentials
func (t *UserAuthTool) runScript(ctx context.Context, credentials map[string]string) (*AuthResult, error) {
	if t.config.Script == "" {
		return nil, fmt.Errorf("no auth script configured")
	}

	// Set timeout
	timeout := time.Duration(t.config.Timeout) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prepare input JSON
	inputJSON, err := json.Marshal(credentials)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal credentials: %w", err)
	}

	// Run script
	cmd := exec.CommandContext(ctx, t.config.Script) //nolint:gosec // G204: script path from admin config
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	L_debug("user_auth: running script", "script", t.config.Script, "credentials", credentials)

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("script timeout after %v", timeout)
		}
		// Script returned non-zero but might have valid JSON output
		L_debug("user_auth: script returned error", "error", err, "stderr", stderr.String())
	}

	// Parse output
	var result AuthResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("invalid script output: %w (output: %s)", err, stdout.String())
	}

	return &result, nil
}

// checkRateLimit checks if rate limit is exceeded
func (t *UserAuthTool) checkRateLimit() (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	rateLimit := t.config.RateLimit
	if rateLimit == 0 {
		rateLimit = 3 // Default 3 per minute
	}

	// Clean old attempts (older than 1 minute)
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	var recent []time.Time
	for _, at := range t.attempts {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	t.attempts = recent

	if len(t.attempts) >= rateLimit {
		return true, fmt.Sprintf("Too many authentication attempts. Please wait a minute before trying again. (Limit: %d per minute)", rateLimit)
	}

	return false, ""
}

// recordAttempt records an authentication attempt
func (t *UserAuthTool) recordAttempt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts = append(t.attempts, time.Now())
}

// formatResult formats a tool result for the agent
func (t *UserAuthTool) formatResult(success bool, userInfo, message string) string {
	result := map[string]any{
		"success": success,
		"message": message,
	}
	if userInfo != "" {
		result["user"] = userInfo
	}
	b, _ := json.Marshal(result)
	return string(b)
}

// formatSuccessResult formats a successful auth result
func (t *UserAuthTool) formatSuccessResult(result *AuthResult) string {
	output := map[string]any{
		"success": true,
		"user": map[string]any{
			"name":     result.User.Name,
			"username": result.User.Username,
			"role":     result.User.Role,
			"id":       result.User.ID,
		},
		"message": result.Message,
	}
	b, _ := json.Marshal(output)
	return string(b)
}
