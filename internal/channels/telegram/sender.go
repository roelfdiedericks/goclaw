// Package telegram: sender.go implements sendWithRetry, which honors
// Telegram's 429 FloodError.RetryAfter hint with a hard cap so outbound
// messages survive transient per-chat or per-bot rate limits.
//
// Layering:
//   - chatLimiter.Wait proactively spaces sends to avoid 429s
//   - sendWithRetry reactively backs off when a 429 still sneaks through
//
// Both are ctx-aware: panic-stop, per-user cancellation, or bot shutdown all
// wake the sleep immediately via ctx.Done().
package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"

	tele "gopkg.in/telebot.v4"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// sendWithRetry gates the call on the per-chat limiter and then invokes send,
// retrying on tele.FloodError while honoring RetryAfter up to MaxBackoffMs.
// Non-FloodError errors are returned immediately so other retry layers (e.g.
// the HTML split-retry in sendChunkWithRetry) can handle them.
//
// op is a short label used purely for log context ("text", "photo", "edit").
func (b *Bot) sendWithRetry(
	ctx context.Context,
	chatID int64,
	op string,
	send func() (*tele.Message, error),
) (*tele.Message, error) {
	return retryOn429(ctx, b, chatID, op, send)
}

// retryOn429 is the generic core shared by sendWithRetry and any other helper
// (e.g. album sends that return []tele.Message) that needs 429-aware retries.
func retryOn429[T any](
	ctx context.Context,
	b *Bot,
	chatID int64,
	op string,
	send func() (T, error),
) (T, error) {
	var zero T
	if err := b.chatLimiter.Wait(ctx, chatID); err != nil {
		return zero, err
	}

	cfg := b.config.RateLimit
	maxRetries := cfg.MaxRetries
	initialBackoff := time.Duration(cfg.InitialBackoffMs) * time.Millisecond
	maxBackoff := time.Duration(cfg.MaxBackoffMs) * time.Millisecond

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		val, err := send()
		if err == nil {
			return val, nil
		}

		var flood tele.FloodError
		if !errors.As(err, &flood) {
			return zero, err
		}

		if attempt >= maxRetries {
			lastErr = err
			break
		}

		wait := time.Duration(flood.RetryAfter) * time.Second
		if wait > maxBackoff {
			L_warn("telegram: flood retry_after exceeds cap, giving up",
				"chatID", chatID, "op", op,
				"retryAfterSec", flood.RetryAfter,
				"capMs", cfg.MaxBackoffMs,
			)
			return zero, err
		}
		floor := initialBackoff << attempt
		if wait < floor {
			wait = floor
		}

		L_warn("telegram: flood-limited, backing off",
			"chatID", chatID, "op", op,
			"retryAfterSec", flood.RetryAfter,
			"wait", wait,
			"attempt", attempt+1, "maxRetries", maxRetries,
		)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
		lastErr = err
	}

	return zero, fmt.Errorf("telegram: %s exceeded %d flood retries: %w", op, maxRetries, lastErr)
}
