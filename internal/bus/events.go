package bus

import (
	"sync"
	"sync/atomic"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Event represents a notification broadcast to subscribers (pub/sub pattern)
type Event struct {
	Topic     string    // Event topic: "llm.config.applied", "config.saved", etc.
	Data      any       // Optional payload data
	Timestamp time.Time // When the event was published
	Source    string    // Origin: "tui", "web", "system", etc.
}

// EventHandler processes an event (no return value - fire and forget)
type EventHandler func(Event)

// SubscriptionID uniquely identifies an event subscription
type SubscriptionID uint64

// subscription holds a single event handler
type subscription struct {
	id      SubscriptionID
	handler EventHandler
}

var (
	// eventSubscriptions maps topics to their subscribers
	eventSubscriptions   = make(map[string][]subscription)
	eventSubscriptionsMu sync.RWMutex

	// nextSubscriptionID generates unique subscription IDs
	nextSubscriptionID uint64
)

// SubscribeEvent registers a handler for an event topic.
// Returns a SubscriptionID that can be used to unsubscribe.
func SubscribeEvent(topic string, handler EventHandler) SubscriptionID {
	id := SubscriptionID(atomic.AddUint64(&nextSubscriptionID, 1))

	eventSubscriptionsMu.Lock()
	defer eventSubscriptionsMu.Unlock()

	eventSubscriptions[topic] = append(eventSubscriptions[topic], subscription{
		id:      id,
		handler: handler,
	})

	L_debug("bus: event subscribed", "topic", topic, "subscriptionID", id)
	return id
}

// UnsubscribeEvent removes a subscription by its ID.
// Returns true if the subscription was found and removed.
func UnsubscribeEvent(id SubscriptionID) bool {
	eventSubscriptionsMu.Lock()
	defer eventSubscriptionsMu.Unlock()

	for topic, subs := range eventSubscriptions {
		for i, sub := range subs {
			if sub.id == id {
				// Remove subscription by swapping with last and truncating
				eventSubscriptions[topic] = append(subs[:i], subs[i+1:]...)
				if len(eventSubscriptions[topic]) == 0 {
					delete(eventSubscriptions, topic)
				}
				L_debug("bus: event unsubscribed", "topic", topic, "subscriptionID", id)
				return true
			}
		}
	}
	return false
}

// PublishEvent broadcasts an event to all subscribers of the topic.
// Handlers are called asynchronously in separate goroutines.
func PublishEvent(topic string, data any) {
	PublishEventWithSource(topic, data, "system")
}

// PublishEventWithSource broadcasts an event with source information.
func PublishEventWithSource(topic string, data any, source string) {
	event := Event{
		Topic:     topic,
		Data:      data,
		Timestamp: time.Now(),
		Source:    source,
	}

	eventSubscriptionsMu.RLock()
	subs := eventSubscriptions[topic]
	// Copy slice to avoid holding lock during handler execution
	subsCopy := make([]subscription, len(subs))
	copy(subsCopy, subs)
	eventSubscriptionsMu.RUnlock()

	if len(subsCopy) == 0 {
		L_debug("bus: event published (no subscribers)", "topic", topic)
		return
	}

	L_info("bus: event published", "topic", topic, "subscribers", len(subsCopy), "source", source)

	// Call handlers asynchronously
	for _, sub := range subsCopy {
		go func(s subscription) {
			defer func() {
				if r := recover(); r != nil {
					L_error("bus: event handler panic", "topic", topic, "subscriptionID", s.id, "panic", r)
				}
			}()
			s.handler(event)
		}(sub)
	}
}

// ListEventTopics returns all topics with active subscriptions
func ListEventTopics() []string {
	eventSubscriptionsMu.RLock()
	defer eventSubscriptionsMu.RUnlock()

	topics := make([]string, 0, len(eventSubscriptions))
	for topic := range eventSubscriptions {
		topics = append(topics, topic)
	}
	return topics
}

// CountEventSubscribers returns the number of subscribers for a topic
func CountEventSubscribers(topic string) int {
	eventSubscriptionsMu.RLock()
	defer eventSubscriptionsMu.RUnlock()

	return len(eventSubscriptions[topic])
}
