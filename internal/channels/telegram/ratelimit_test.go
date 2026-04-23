package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
)

// ---------- chatLimiter tests ----------

func TestChatLimiterNilIsNoOp(t *testing.T) {
	t.Parallel()
	var l *chatLimiter
	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatalf("nil limiter should be a no-op, got err=%v", err)
	}
}

func TestChatLimiterZeroGapIsNoOp(t *testing.T) {
	t.Parallel()
	l := newChatLimiter(0)
	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := l.Wait(context.Background(), 42); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("zero-gap limiter should not sleep, took %v", elapsed)
	}
}

func TestChatLimiterEnforcesMinGapSameChat(t *testing.T) {
	t.Parallel()
	gap := 40 * time.Millisecond
	l := newChatLimiter(gap)

	start := time.Now()
	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatalf("first wait err: %v", err)
	}
	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatalf("second wait err: %v", err)
	}
	elapsed := time.Since(start)
	// Second wait should have slept for approximately `gap` since first.
	if elapsed < gap {
		t.Fatalf("expected at least %v elapsed for two same-chat sends, got %v", gap, elapsed)
	}
}

func TestChatLimiterDifferentChatsDoNotBlock(t *testing.T) {
	t.Parallel()
	gap := 200 * time.Millisecond
	l := newChatLimiter(gap)

	if err := l.Wait(context.Background(), 1); err != nil {
		t.Fatalf("seed wait err: %v", err)
	}

	// Wait on a different chatID should return immediately.
	start := time.Now()
	if err := l.Wait(context.Background(), 2); err != nil {
		t.Fatalf("different-chat wait err: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("different-chat wait should not block (took %v)", elapsed)
	}
}

func TestChatLimiterAbortsOnContextCancel(t *testing.T) {
	t.Parallel()
	l := newChatLimiter(500 * time.Millisecond)

	// Seed a recent timestamp so the next Wait must sleep.
	if err := l.Wait(context.Background(), 7); err != nil {
		t.Fatalf("seed err: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := l.Wait(ctx, 7)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("Wait did not unblock promptly on ctx cancel (took %v)", elapsed)
	}
}

// ---------- sendWithRetry tests ----------

// newTestBot builds a minimal Bot with only the fields sendWithRetry touches.
// No real telebot connection is established.
func newTestBot(cfg config.RateLimitConfig) *Bot {
	c := &config.Config{RateLimit: cfg}
	return &Bot{
		config:      c,
		chatLimiter: newChatLimiter(time.Duration(cfg.PerChatMinGapMs) * time.Millisecond),
	}
}

func TestSendWithRetrySuccessNoRetry(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{MaxRetries: 3, InitialBackoffMs: 1, MaxBackoffMs: 100})

	var calls int32
	wantMsg := &tele.Message{ID: 42}
	got, err := b.sendWithRetry(context.Background(), 1, "test", func() (*tele.Message, error) {
		atomic.AddInt32(&calls, 1)
		return wantMsg, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != wantMsg {
		t.Fatalf("returned wrong message")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("expected 1 call, got %d", n)
	}
}

func TestSendWithRetryNonFloodErrorNotRetried(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{MaxRetries: 5, InitialBackoffMs: 1, MaxBackoffMs: 100})

	var calls int32
	sentinel := errors.New("some other telegram error")
	_, err := b.sendWithRetry(context.Background(), 1, "test", func() (*tele.Message, error) {
		atomic.AddInt32(&calls, 1)
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel err, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("non-flood errors must not retry; got %d calls", n)
	}
}

func TestSendWithRetryHonorsFloodRetryAfter(t *testing.T) {
	t.Parallel()
	// Use millisecond-scale durations via the initialBackoff floor. RetryAfter
	// is in seconds per Telegram's API; a value of 1 here is still within the
	// cap but we clamp the actual wait via InitialBackoff floor, so we set
	// InitialBackoffMs above 1s isn't viable for fast tests. Use RetryAfter=0
	// so the floor (initial << attempt) dominates.
	b := newTestBot(config.RateLimitConfig{
		MaxRetries:       3,
		InitialBackoffMs: 10,
		MaxBackoffMs:     5000,
	})

	var calls int32
	wantMsg := &tele.Message{ID: 7}
	got, err := b.sendWithRetry(context.Background(), 1, "test", func() (*tele.Message, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return nil, tele.FloodError{RetryAfter: 0}
		}
		return wantMsg, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != wantMsg {
		t.Fatalf("wrong message returned")
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("expected 3 attempts, got %d", n)
	}
}

func TestSendWithRetryExceedsCapReturnsImmediately(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{
		MaxRetries:       5,
		InitialBackoffMs: 10,
		MaxBackoffMs:     500, // 0.5s cap
	})

	var calls int32
	floodErr := tele.FloodError{RetryAfter: 60} // 60s > 0.5s cap
	start := time.Now()
	_, err := b.sendWithRetry(context.Background(), 1, "test", func() (*tele.Message, error) {
		atomic.AddInt32(&calls, 1)
		return nil, floodErr
	})
	elapsed := time.Since(start)

	var gotFlood tele.FloodError
	if !errors.As(err, &gotFlood) {
		t.Fatalf("expected FloodError returned unchanged, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("retry_after > cap must not retry; got %d calls", n)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cap-exceeded path should not sleep, took %v", elapsed)
	}
}

func TestSendWithRetryExhaustsRetries(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{
		MaxRetries:       2,
		InitialBackoffMs: 5,
		MaxBackoffMs:     1000,
	})

	var calls int32
	floodErr := tele.FloodError{RetryAfter: 0}
	_, err := b.sendWithRetry(context.Background(), 1, "test", func() (*tele.Message, error) {
		atomic.AddInt32(&calls, 1)
		return nil, floodErr
	})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	// Exhaustion wraps the last error with an "exceeded N flood retries" message.
	if !errors.As(err, &tele.FloodError{}) {
		t.Fatalf("exhausted error must wrap the underlying FloodError, got %v", err)
	}
	// With MaxRetries=2, we expect initial + 2 retries = 3 attempts.
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("expected 3 total attempts (MaxRetries=2), got %d", n)
	}
}

func TestSendWithRetryAbortsOnCtxCancelDuringBackoff(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{
		MaxRetries:       5,
		InitialBackoffMs: 500, // long backoff so ctx-cancel wins
		MaxBackoffMs:     60000,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	var calls int32
	start := time.Now()
	_, err := b.sendWithRetry(ctx, 1, "test", func() (*tele.Message, error) {
		atomic.AddInt32(&calls, 1)
		return nil, tele.FloodError{RetryAfter: 0}
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("ctx cancel did not abort backoff promptly (took %v)", elapsed)
	}
	// We should have made at least one attempt.
	if n := atomic.LoadInt32(&calls); n < 1 {
		t.Fatalf("expected at least 1 attempt, got %d", n)
	}
}

func TestSendWithRetryAbortsOnCtxCancelAtLimiterGate(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{
		MaxRetries:       3,
		InitialBackoffMs: 10,
		MaxBackoffMs:     1000,
		PerChatMinGapMs:  500, // force a 500ms gate on second call
	})

	// Seed the limiter with a recent send so the next call must wait.
	if err := b.chatLimiter.Wait(context.Background(), 1); err != nil {
		t.Fatalf("seed err: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	var calls int32
	start := time.Now()
	_, err := b.sendWithRetry(ctx, 1, "test", func() (*tele.Message, error) {
		atomic.AddInt32(&calls, 1)
		return nil, fmt.Errorf("should not reach send()")
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("ctx cancel did not abort limiter gate promptly (took %v)", elapsed)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("send() must not run when ctx is cancelled at the gate; got %d calls", n)
	}
}

func TestSendWithRetryIsolatesChatsUnderLock(t *testing.T) {
	t.Parallel()
	b := newTestBot(config.RateLimitConfig{
		MaxRetries:       0,
		InitialBackoffMs: 1,
		MaxBackoffMs:     1000,
		PerChatMinGapMs:  100, // 100ms gap per chat
	})

	// Prime chat 1 with a successful send so its gap timer starts.
	_, err := b.sendWithRetry(context.Background(), 1, "prime", func() (*tele.Message, error) {
		return &tele.Message{ID: 1}, nil
	})
	if err != nil {
		t.Fatalf("prime err: %v", err)
	}

	// Now fire chat 1 and chat 2 concurrently: chat 2 should complete quickly
	// even though chat 1 is blocked on its gap.
	var wg sync.WaitGroup
	var chat1Done, chat2Done time.Time

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = b.sendWithRetry(context.Background(), 1, "c1", func() (*tele.Message, error) {
			return &tele.Message{ID: 2}, nil
		})
		chat1Done = time.Now()
	}()
	go func() {
		defer wg.Done()
		_, _ = b.sendWithRetry(context.Background(), 2, "c2", func() (*tele.Message, error) {
			return &tele.Message{ID: 3}, nil
		})
		chat2Done = time.Now()
	}()
	wg.Wait()

	// chat 2 should finish at least 50ms before chat 1 (chat 1 waits ~100ms,
	// chat 2 waits ~0ms). Give generous margin for scheduler jitter.
	diff := chat1Done.Sub(chat2Done)
	if diff < 50*time.Millisecond {
		t.Fatalf("chat 2 did not finish ahead of chat 1 as expected (diff=%v); "+
			"isolation between chats may be broken", diff)
	}
}

// ---------- config tests ----------

func TestConfigNormalizeFillsDefaults(t *testing.T) {
	t.Parallel()
	c := &config.Config{}
	c.Normalize()

	if c.RateLimit.MaxRetries != 3 {
		t.Errorf("MaxRetries: want 3, got %d", c.RateLimit.MaxRetries)
	}
	if c.RateLimit.InitialBackoffMs != 1000 {
		t.Errorf("InitialBackoffMs: want 1000, got %d", c.RateLimit.InitialBackoffMs)
	}
	if c.RateLimit.MaxBackoffMs != 120000 {
		t.Errorf("MaxBackoffMs: want 120000, got %d", c.RateLimit.MaxBackoffMs)
	}
	if c.RateLimit.PerChatMinGapMs != 35 {
		t.Errorf("PerChatMinGapMs: want 35, got %d", c.RateLimit.PerChatMinGapMs)
	}
}

func TestConfigNormalizePreservesExplicitValues(t *testing.T) {
	t.Parallel()
	c := &config.Config{
		RateLimit: config.RateLimitConfig{
			MaxRetries:       7,
			InitialBackoffMs: 2000,
			MaxBackoffMs:     60000,
			PerChatMinGapMs:  50,
		},
	}
	c.Normalize()
	if c.RateLimit.MaxRetries != 7 || c.RateLimit.InitialBackoffMs != 2000 ||
		c.RateLimit.MaxBackoffMs != 60000 || c.RateLimit.PerChatMinGapMs != 50 {
		t.Fatalf("Normalize overwrote explicit values: %+v", c.RateLimit)
	}
}
