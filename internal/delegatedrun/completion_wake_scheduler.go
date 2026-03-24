package delegatedrun

import (
	"strings"
	"sync"
	"time"
)

const defaultCompletionWakeDelay = 500 * time.Millisecond

// CompletionWakeScheduler schedules deferred completion retries by run ID.
// Scheduling the same run ID again replaces the previous timer.
type CompletionWakeScheduler struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
	workCh chan func()
}

func NewCompletionWakeScheduler() *CompletionWakeScheduler {
	s := &CompletionWakeScheduler{
		timers: make(map[string]*time.Timer),
		workCh: make(chan func(), 128),
	}
	go s.runWorker()
	return s
}

var sharedCompletionWakeScheduler = NewCompletionWakeScheduler()

func SharedCompletionWakeScheduler() *CompletionWakeScheduler {
	return sharedCompletionWakeScheduler
}

func (s *CompletionWakeScheduler) Schedule(runID string, wakeAt *time.Time, fn func()) {
	key := strings.TrimSpace(runID)
	if key == "" || fn == nil {
		return
	}
	when := time.Now().Add(defaultCompletionWakeDelay)
	if wakeAt != nil && !wakeAt.IsZero() {
		when = *wakeAt
	}
	delay := time.Until(when)
	if delay < 0 {
		delay = 0
	}

	s.mu.Lock()
	if prev, ok := s.timers[key]; ok {
		prev.Stop()
		delete(s.timers, key)
	}
	timer := time.AfterFunc(delay, func() {
		s.mu.Lock()
		delete(s.timers, key)
		s.mu.Unlock()
		s.workCh <- fn
	})
	s.timers[key] = timer
	s.mu.Unlock()
}

func (s *CompletionWakeScheduler) runWorker() {
	for fn := range s.workCh {
		if fn != nil {
			fn()
		}
	}
}

