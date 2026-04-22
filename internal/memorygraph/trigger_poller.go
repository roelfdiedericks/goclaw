package memorygraph

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// TriggerInvoker is the minimal interface the trigger poller needs from the
// gateway. The gateway implements RunAgentForMemoryTrigger, which wakes the
// agent on the user's primary session with a system-injected preamble.
//
// Returns (runID, err). runID may be "" when the agent produced no output
// (e.g. SILENT_OK or empty final text) — that's still a successful firing.
type TriggerInvoker interface {
	RunAgentForMemoryTrigger(ctx context.Context, ownerUsername, memoryUUID, preamble string) (string, error)
}

// TriggerPoller wakes the agent on due routine memories.
//
// It's a lightweight single-goroutine poller started by the Maintainer at boot
// and stopped via context cancellation. Claiming is idempotent (UNIQUE on
// memory_uuid, scheduled_for in the memory_triggers_fired table), so running
// multiple pollers concurrently is safe but unnecessary.
type TriggerPoller struct {
	store    *Store
	invoker  TriggerInvoker
	cfg      TriggerConfig
	interval time.Duration
	grace    time.Duration

	mu      sync.Mutex
	running bool
}

// NewTriggerPoller constructs a poller. cfg defaults are applied by
// NormalizeTriggerConfig; callers usually pass cfg straight from
// memorygraph.Config.Trigger.
func NewTriggerPoller(store *Store, invoker TriggerInvoker, cfg TriggerConfig) *TriggerPoller {
	NormalizeTriggerConfig(&cfg)
	interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	grace := time.Duration(cfg.MissedGraceMinutes) * time.Minute
	return &TriggerPoller{
		store:    store,
		invoker:  invoker,
		cfg:      cfg,
		interval: interval,
		grace:    grace,
	}
}

// Start blocks the current goroutine and runs the poll loop until ctx is
// cancelled. Intended to be invoked as `go poller.Start(ctx)` from the
// Maintainer boot path.
func (p *TriggerPoller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	if !p.cfg.Enabled {
		L_info("memtrigger: poller disabled, not starting")
		return
	}
	if p.invoker == nil {
		L_warn("memtrigger: no invoker bound, poller not starting")
		return
	}
	if p.store == nil {
		L_warn("memtrigger: no store bound, poller not starting")
		return
	}

	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		L_warn("memtrigger: poller already running")
		return
	}
	p.running = true
	p.mu.Unlock()

	L_info("memtrigger: poller starting",
		"pollInterval", p.interval,
		"missedGrace", p.grace,
	)

	// Immediate first tick so just-stored routines with a near-term due time
	// don't wait a full interval on fresh boots.
	p.tick(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			L_info("memtrigger: poller stopping", "reason", ctx.Err())
			p.mu.Lock()
			p.running = false
			p.mu.Unlock()
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// tick runs a single poll cycle. Errors are logged but never fatal — the
// poller keeps running on the next interval.
func (p *TriggerPoller) tick(ctx context.Context) {
	now := time.Now()

	due, err := p.store.ListDueRoutines(now)
	if err != nil {
		L_warn("memtrigger: ListDueRoutines failed", "error", err)
		return
	}
	if len(due) == 0 {
		L_trace("memtrigger: no due routines", "at", now.Format(time.RFC3339))
		return
	}

	L_debug("memtrigger: processing due routines",
		"count", len(due),
		"at", now.Format(time.RFC3339),
	)

	for _, dr := range due {
		if err := ctx.Err(); err != nil {
			return
		}
		p.processOne(ctx, dr, now)
	}
}

// processOne handles a single DueRoutine:
//  1. Enforce bounds / skip_dates (defensive, in case the row wasn't cleaned
//     up yet after a config change).
//  2. Honour MissedGrace — skip without firing when the routine is overdue by
//     more than the grace window, but still recompute next_trigger_at so we
//     don't spam about stale entries forever.
//  3. Claim the (memory, scheduled_for) pair via the UNIQUE constraint.
//  4. Invoke the gateway with a built preamble.
//  5. Record outcome and advance next_trigger_at.
func (p *TriggerPoller) processOne(ctx context.Context, dr *DueRoutine, now time.Time) {
	if dr == nil || dr.Memory == nil || dr.Meta == nil {
		return
	}
	memoryUUID := dr.Memory.UUID
	scheduledFor := dr.DueAt
	username := dr.OwnerID

	// Skip if scheduled_for falls outside bounds or on a skip_date. This
	// shouldn't normally happen (next_trigger_at would already be past bounds
	// for the last event), but guard defensively.
	if !dr.Meta.inBounds(scheduledFor) || dr.Meta.isSkipped(scheduledFor) {
		L_debug("memtrigger: due outside bounds / skipped, recomputing",
			"memory", memoryUUID,
			"scheduledFor", scheduledFor.Format(time.RFC3339),
		)
		p.advanceNext(memoryUUID, dr.Meta, scheduledFor)
		return
	}

	// MissedGrace: overdue beyond the grace window → drop this firing, just
	// recompute next so we won't keep tripping on it.
	if p.grace > 0 && now.Sub(scheduledFor) > p.grace {
		L_info("memtrigger: skipping missed (past grace window)",
			"memory", memoryUUID,
			"scheduledFor", scheduledFor.Format(time.RFC3339),
			"now", now.Format(time.RFC3339),
			"grace", p.grace,
		)
		// Still claim it so audit shows we acknowledged this scheduled_for.
		sessionKey := sessionKeyForUser(username)
		if claimed, err := p.store.ClaimTrigger(memoryUUID, scheduledFor, username, sessionKey); err != nil {
			L_warn("memtrigger: claim failed during missed-grace skip",
				"memory", memoryUUID,
				"error", err,
			)
		} else if claimed {
			_ = p.store.MarkTriggerOutcome(memoryUUID, scheduledFor, "missed_grace", "")
		}
		p.advanceNext(memoryUUID, dr.Meta, scheduledFor)
		return
	}

	sessionKey := sessionKeyForUser(username)
	claimed, err := p.store.ClaimTrigger(memoryUUID, scheduledFor, username, sessionKey)
	if err != nil {
		L_warn("memtrigger: claim failed",
			"memory", memoryUUID,
			"scheduledFor", scheduledFor.Format(time.RFC3339),
			"error", err,
		)
		return
	}
	if !claimed {
		// Another worker (or a prior run) already claimed this instant.
		L_trace("memtrigger: claim rejected (already fired)",
			"memory", memoryUUID,
			"scheduledFor", scheduledFor.Format(time.RFC3339),
		)
		// Still advance next_trigger_at so we don't revisit this row every tick.
		p.advanceNext(memoryUUID, dr.Meta, scheduledFor)
		return
	}

	preamble := buildMemTriggerPreamble(dr, scheduledFor, now)

	L_info("memtrigger: firing",
		"memory", memoryUUID,
		"user", username,
		"scheduledFor", scheduledFor.Format(time.RFC3339),
		"lag", now.Sub(scheduledFor),
	)

	runID, runErr := p.invoker.RunAgentForMemoryTrigger(ctx, username, memoryUUID, preamble)
	outcome := "fired"
	if runErr != nil {
		outcome = "error"
		L_error("memtrigger: invoker failed",
			"memory", memoryUUID,
			"error", runErr,
		)
	} else if runID == "" {
		outcome = "silent"
	}

	if err := p.store.MarkTriggerOutcome(memoryUUID, scheduledFor, outcome, runID); err != nil {
		L_warn("memtrigger: mark outcome failed",
			"memory", memoryUUID,
			"outcome", outcome,
			"error", err,
		)
	}

	p.advanceNext(memoryUUID, dr.Meta, scheduledFor)
}

// advanceNext recomputes the next_trigger_at for a routine after firing or
// skipping. The anchor is scheduledFor + 1 minute (so same-minute retries are
// avoided) — NextOccurrence will then jump to the next matching day/time in
// bounds.
func (p *TriggerPoller) advanceNext(memoryUUID string, meta *RoutineMetadata, scheduledFor time.Time) {
	if meta == nil {
		return
	}
	anchor := scheduledFor.Add(time.Minute)
	next := meta.NextOccurrence(anchor)
	if next.IsZero() {
		// No next occurrence (past EndsOn / bad config). Clear it so we stop
		// polling this row.
		if err := p.store.SetNextTriggerAt(memoryUUID, nil); err != nil {
			L_warn("memtrigger: clear next_trigger_at failed",
				"memory", memoryUUID,
				"error", err,
			)
		} else {
			L_debug("memtrigger: no next occurrence, cleared",
				"memory", memoryUUID,
			)
		}
		return
	}
	if err := p.store.SetNextTriggerAt(memoryUUID, &next); err != nil {
		L_warn("memtrigger: set next_trigger_at failed",
			"memory", memoryUUID,
			"next", next.Format(time.RFC3339),
			"error", err,
		)
		return
	}
	L_debug("memtrigger: advanced next",
		"memory", memoryUUID,
		"next", next.Format(time.RFC3339),
	)
}

// sessionKeyForUser returns the agent session key the trigger should run on.
// V1: always the user's primary persisted session. Stored in
// memory_triggers_fired for audit.
func sessionKeyForUser(username string) string {
	if username == "" {
		return "primary"
	}
	return "user:" + username
}

// buildMemTriggerPreamble renders the system-injected user message for the
// agent turn. Kept short — the bulletin (Today's Schedule + Active Routines)
// carries the bulk of the scheduling context.
func buildMemTriggerPreamble(dr *DueRoutine, scheduledFor, now time.Time) string {
	if dr == nil || dr.Memory == nil {
		return "A routine memory just came due. Check the bulletin and respond, or reply exactly with SILENT_OK if nothing is needed."
	}
	m := dr.Memory
	meta := dr.Meta

	var sb strings.Builder
	sb.WriteString("[memtrigger] A routine memory is due: ")
	sb.WriteString(strings.TrimSpace(m.Content))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Scheduled for %s (local time).", scheduledFor.Format("Mon 15:04")))

	if meta != nil {
		if meta.Location != "" {
			sb.WriteString(" Location: ")
			sb.WriteString(meta.Location)
			sb.WriteString(".")
		}
		if meta.Person != "" {
			sb.WriteString(" Person: ")
			sb.WriteString(meta.Person)
			sb.WriteString(".")
		}
	}

	lag := now.Sub(scheduledFor)
	if lag > 2*time.Minute {
		sb.WriteString(fmt.Sprintf(" (overdue by %s)", humaniseLag(lag)))
	}

	sb.WriteString("\n\nRespond to the user about this routine, or reply exactly with SILENT_OK if nothing is needed.")
	return sb.String()
}

// humaniseLag returns a short human-readable duration ("12m", "1h5m", "2h").
func humaniseLag(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) - h*60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
