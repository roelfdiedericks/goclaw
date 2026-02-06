// Package session provides conversation session management.
package session

import (
	"context"
	"sync"
	"time"
)

// SupervisionState tracks supervision status for a session.
// This allows an owner to watch, guide, and ghostwrite in any active session.
type SupervisionState struct {
	Supervised      bool       // Supervisor is watching this session
	LLMEnabled      bool       // Agent can respond (false = ghostwriting mode)
	SupervisorID    string     // Who is supervising (user ID)
	PendingGuidance []Guidance // Guidance messages waiting to be consumed
	InterruptFlag   bool       // Request to interrupt current generation

	// Cancel function for interrupting ongoing LLM generation
	cancelFunc context.CancelFunc

	// Event channel for real-time streaming to supervisor
	eventChan chan interface{}

	mu sync.RWMutex
}

// Guidance represents a supervisor instruction to the agent.
type Guidance struct {
	From      string    // Supervisor's user ID
	Content   string    // The guidance text
	Timestamp time.Time // When guidance was sent
}

// NewSupervisionState creates a new supervision state with defaults.
func NewSupervisionState() *SupervisionState {
	return &SupervisionState{
		Supervised:      false,
		LLMEnabled:      true, // LLM enabled by default
		PendingGuidance: make([]Guidance, 0),
	}
}

// SetSupervised marks the session as being supervised by the given supervisor.
func (s *SupervisionState) SetSupervised(supervisorID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Supervised = true
	s.SupervisorID = supervisorID
}

// ClearSupervised removes supervision from the session.
func (s *SupervisionState) ClearSupervised() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Supervised = false
	s.SupervisorID = ""
}

// IsSupervised returns whether the session is currently being supervised.
func (s *SupervisionState) IsSupervised() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Supervised
}

// GetSupervisorID returns the ID of the current supervisor.
func (s *SupervisionState) GetSupervisorID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SupervisorID
}

// SetLLMEnabled enables or disables LLM responses for this session.
// When disabled, the owner can ghostwrite messages instead of the agent.
func (s *SupervisionState) SetLLMEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LLMEnabled = enabled
}

// IsLLMEnabled returns whether LLM responses are enabled.
func (s *SupervisionState) IsLLMEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LLMEnabled
}

// AddGuidance adds a guidance message from the supervisor.
func (s *SupervisionState) AddGuidance(from, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.PendingGuidance = append(s.PendingGuidance, Guidance{
		From:      from,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// HasPendingGuidance returns whether there is guidance waiting to be consumed.
func (s *SupervisionState) HasPendingGuidance() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.PendingGuidance) > 0
}

// ConsumePendingGuidance returns and clears all pending guidance messages.
func (s *SupervisionState) ConsumePendingGuidance() []Guidance {
	s.mu.Lock()
	defer s.mu.Unlock()

	guidance := s.PendingGuidance
	s.PendingGuidance = make([]Guidance, 0)
	return guidance
}

// RequestInterrupt sets the interrupt flag to stop ongoing generation.
func (s *SupervisionState) RequestInterrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.InterruptFlag = true
	// Also call cancel if we have a cancel function
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}

// HasInterruptRequest returns and clears the interrupt flag.
func (s *SupervisionState) HasInterruptRequest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	requested := s.InterruptFlag
	s.InterruptFlag = false
	return requested
}

// SetCancelFunc sets the cancel function for the current generation.
func (s *SupervisionState) SetCancelFunc(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelFunc = cancel
}

// ClearCancelFunc clears the cancel function after generation completes.
func (s *SupervisionState) ClearCancelFunc() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelFunc = nil
}

// Subscribe creates and returns an event channel for real-time streaming.
// The supervisor SSE handler calls this to receive events.
func (s *SupervisionState) Subscribe() <-chan interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close existing channel if any
	if s.eventChan != nil {
		close(s.eventChan)
	}

	s.eventChan = make(chan interface{}, 100)
	return s.eventChan
}

// Unsubscribe closes the event channel.
func (s *SupervisionState) Unsubscribe() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.eventChan != nil {
		close(s.eventChan)
		s.eventChan = nil
	}
}

// SendEvent sends an event to the supervisor if subscribed.
// Non-blocking - drops event if channel is full or not subscribed.
func (s *SupervisionState) SendEvent(ev interface{}) {
	s.mu.RLock()
	ch := s.eventChan
	s.mu.RUnlock()

	if ch == nil {
		return
	}

	// Non-blocking send
	select {
	case ch <- ev:
	default:
		// Channel full, drop event
	}
}

// HasSubscriber returns whether there's an active event subscriber.
func (s *SupervisionState) HasSubscriber() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventChan != nil
}
