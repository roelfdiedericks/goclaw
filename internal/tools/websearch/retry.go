package websearch

import (
	"context"
	"math/rand"
	"time"
)

func retryBackoff(baseMs, maxMs, attempt int) time.Duration {
	if baseMs <= 0 {
		baseMs = 500
	}
	if maxMs <= 0 {
		maxMs = 5000
	}
	if attempt < 1 {
		attempt = 1
	}

	backoff := baseMs * (1 << (attempt - 1))
	if backoff > maxMs {
		backoff = maxMs
	}

	// Jitter: +/-20%
	jitterRange := backoff / 5
	if jitterRange <= 0 {
		return time.Duration(backoff) * time.Millisecond
	}
	jitter := rand.Intn(2*jitterRange+1) - jitterRange
	return time.Duration(backoff+jitter) * time.Millisecond
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
