// Package telegram: ratelimit.go implements a per-chat min-gap limiter used to
// proactively smooth outbound bursts and avoid Telegram's per-chat flood limits.
//
// Isolation guarantee: sends to different chats never block each other. The
// internal mutex is held only long enough to read/write the last-send map; the
// actual wait happens outside the lock so other chats can still take the
// mutex immediately.
package telegram

import (
	"context"
	"sync"
	"time"
)

// chatLimiterMaxEntries caps the per-chat last-send map to prevent unbounded
// growth in long-running bots that see many transient chats.
const chatLimiterMaxEntries = 1000

// chatLimiter gates outbound sends so that consecutive sends to the same chat
// are at least minGap apart. A zero or negative minGap disables the limiter.
type chatLimiter struct {
	mu       sync.Mutex
	lastSend map[int64]time.Time
	minGap   time.Duration
}

func newChatLimiter(minGap time.Duration) *chatLimiter {
	return &chatLimiter{
		lastSend: make(map[int64]time.Time),
		minGap:   minGap,
	}
}

// Wait blocks until at least minGap has elapsed since the last send to chatID,
// then records the current time as the new last-send stamp. Returns ctx.Err()
// if ctx is cancelled while waiting so that panic-stop / shutdown can abort
// the wait immediately rather than serving out the full gap.
func (l *chatLimiter) Wait(ctx context.Context, chatID int64) error {
	if l == nil || l.minGap <= 0 {
		return nil
	}

	l.mu.Lock()
	var wait time.Duration
	if last, ok := l.lastSend[chatID]; ok {
		if elapsed := time.Since(last); elapsed < l.minGap {
			wait = l.minGap - elapsed
		}
	}
	l.mu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	l.mu.Lock()
	l.lastSend[chatID] = time.Now()
	if len(l.lastSend) > chatLimiterMaxEntries {
		l.trimLocked()
	}
	l.mu.Unlock()
	return nil
}

// trimLocked drops entries whose last-send timestamp is older than one minute,
// which is far longer than any realistic per-chat min-gap. Must be called with
// l.mu held. This is an opportunistic cleanup; we don't care about being
// exact, only about preventing unbounded growth.
func (l *chatLimiter) trimLocked() {
	cutoff := time.Now().Add(-time.Minute)
	for id, t := range l.lastSend {
		if t.Before(cutoff) {
			delete(l.lastSend, id)
		}
	}
}
