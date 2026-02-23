package userauth

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

	"github.com/roelfdiedericks/goclaw/internal/auth"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Tool handles user authentication and role elevation.
type Tool struct {
	config      auth.AuthConfig
	rolesConfig user.RolesConfig

	// Rate limiting
	mu       sync.Mutex
	attempts []time.Time
}

// AuthInput is the input from the agent.
type AuthInput struct {
	Credentials map[string]string `json:"credentials"` // Flexible key-value pairs
}

// AuthResult is the expected output from the auth script.
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

// NewTool creates a new user_auth tool.
func NewTool(authConfig auth.AuthConfig, rolesConfig user.RolesConfig) *Tool {
	return &Tool{
		config:      authConfig,
		rolesConfig: rolesConfig,
		attempts:    make([]time.Time, 0),
	}
}

func (t *Tool) Name() string {
	return "user_auth"
}

func (t *Tool) Description() string {
	base := "Authenticate a user and elevate their role. Use when a guest user provides identifying information. Returns authentication result with user info or error message."
	if len(t.config.CredentialHints) == 0 {
		return base
	}

	// Format credential hints for the agent
	var hints []string
	for _, h := range t.config.CredentialHints {
		label := h.Label
		if label == "" {
			label = h.Key
		}
		hint := fmt.Sprintf("%s (%s)", label, h.Key)
		if h.Required {
			hint += " [required]"
		}
		hints = append(hints, hint)
	}
	return base + " Accepted credentials: " + strings.Join(hints, ", ") + "."
}

func (t *Tool) Schema() map[string]any {
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

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (*types.ToolResult, error) {
	var params AuthInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	if len(params.Credentials) == 0 {
		return types.TextResult(t.formatResult(false, "", "No credentials provided. Ask the user for their identifying information.")), nil
	}

	// Check rate limit
	if blocked, msg := t.checkRateLimit(); blocked {
		L_warn("user_auth: rate limited", "credentials", params.Credentials)
		return types.TextResult(t.formatResult(false, "", msg)), nil
	}

	// Record attempt
	t.recordAttempt()

	// Get session context for elevation
	sessionCtx := types.GetSessionContext(ctx)
	if sessionCtx == nil {
		L_error("user_auth: no session context")
		return nil, fmt.Errorf("user_auth requires session context")
	}

	// Run auth script
	result, err := t.runScript(ctx, params.Credentials)
	if err != nil {
		L_error("user_auth: script error", "error", err)
		return types.TextResult(t.formatResult(false, "", fmt.Sprintf("Authentication failed: %v", err))), nil
	}

	// Handle failure
	if !result.Success {
		L_info("user_auth: authentication failed", "message", result.Message)
		return types.TextResult(t.formatResult(false, "", result.Message)), nil
	}

	// Validate result
	if result.User == nil {
		return types.TextResult(t.formatResult(false, "", "Authentication script returned success but no user info.")), nil
	}

	// Security: Cannot elevate to owner
	if strings.ToLower(result.User.Role) == "owner" {
		L_warn("user_auth: attempted elevation to owner blocked", "user", result.User.Username)
		return types.TextResult(t.formatResult(false, "", "Cannot elevate to owner role.")), nil
	}

	// Security: Role must be in allowedRoles
	if len(t.config.AllowedRoles) > 0 && !slices.Contains(t.config.AllowedRoles, result.User.Role) {
		L_warn("user_auth: role not in allowedRoles", "role", result.User.Role, "allowed", t.config.AllowedRoles)
		return types.TextResult(t.formatResult(false, "", fmt.Sprintf("Role '%s' is not permitted for elevation.", result.User.Role))), nil
	}

	// Security: Role must exist in roles config (unless empty = no roles defined = fail)
	if _, ok := t.rolesConfig[result.User.Role]; !ok {
		L_warn("user_auth: role not defined in config", "role", result.User.Role)
		return types.TextResult(t.formatResult(false, "", fmt.Sprintf("Role '%s' is not defined in configuration.", result.User.Role))), nil
	}

	// Elevate the session
	if sessionCtx.Session != nil {
		sessionCtx.Session.ElevateUser(result.User.Name, result.User.Username, result.User.Role, result.User.ID)
		L_info("user_auth: session elevated",
			"name", result.User.Name,
			"username", result.User.Username,
			"role", result.User.Role,
			"id", result.User.ID,
		)
	} else {
		L_warn("user_auth: no session in context, elevation not applied")
	}

	return types.TextResult(t.formatSuccessResult(result)), nil
}

// runScript executes the auth script with credentials.
func (t *Tool) runScript(ctx context.Context, credentials map[string]string) (*AuthResult, error) {
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

// checkRateLimit checks if rate limit is exceeded.
func (t *Tool) checkRateLimit() (bool, string) {
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

// recordAttempt records an authentication attempt.
func (t *Tool) recordAttempt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts = append(t.attempts, time.Now())
}

// formatResult formats a tool result for the agent.
func (t *Tool) formatResult(success bool, userInfo, message string) string {
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

// formatSuccessResult formats a successful auth result.
func (t *Tool) formatSuccessResult(result *AuthResult) string {
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
