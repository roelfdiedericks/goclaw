package memorygraph

import (
	"context"
	"errors"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

// ErrNoUsername is returned when username cannot be determined from context.
// Memory tools require a username for privacy isolation.
var ErrNoUsername = errors.New("username required: memory tools require user context for privacy isolation")

// getUsernameFromContext extracts username from context.
// Checks ContextKeyUsername first (used by extraction loop),
// then falls back to SessionContext.User (used by gateway).
// Returns error if username cannot be determined.
func getUsernameFromContext(ctx context.Context) (string, error) {
	// First try ContextKeyUsername (used by extraction loop)
	if u, ok := ctx.Value(ContextKeyUsername).(string); ok && u != "" {
		return u, nil
	}

	// Then try SessionContext.User (used by gateway)
	if sessCtx := types.GetSessionContext(ctx); sessCtx != nil {
		if sessCtx.User != nil && sessCtx.User.ID != "" {
			return sessCtx.User.ID, nil
		}
	}

	return "", ErrNoUsername
}
