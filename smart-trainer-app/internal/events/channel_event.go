package events

import (
	"sync"
)

// ChannelEvent provides pub/sub behavior using channels
// T is the type of the value sent to channels
type ChannelEvent[T any] struct {
	mu                    sync.RWMutex
	channels              map[uint64]chan<- T
	nextID                uint64
	sendLastEventOnListen bool
	lastEvent             *T
	hasNotified           bool
}

// NewChannelEvent creates a new ChannelEvent instance
// sendLastEventOnListen: if true, the ChannelEvent will remember the last Notify parameter
// and send it to new listeners immediately if Notify has been called at least once
func NewChannelEvent[T any](sendLastEventOnListen bool) *ChannelEvent[T] {
	return &ChannelEvent[T]{
		channels:              make(map[uint64]chan<- T),
		sendLastEventOnListen: sendLastEventOnListen,
	}
}

// Listen registers a channel to receive values when Notify is invoked
// Returns a deregistration function that can be called to remove the listener
// If sendLastEventOnListen is true and Notify has been called at least once,
// the last event value will be sent to the channel immediately
func (e *ChannelEvent[T]) Listen(ch chan<- T) func() {
	if ch == nil {
		panic("channel cannot be nil")
	}

	e.mu.Lock()
	id := e.nextID
	e.nextID++
	e.channels[id] = ch
	shouldSendLastEvent := e.sendLastEventOnListen && e.hasNotified && e.lastEvent != nil
	var lastEventCopy *T
	if shouldSendLastEvent {
		// Create a copy of the last event to send
		lastEventCopy = new(T)
		*lastEventCopy = *e.lastEvent
	}
	e.mu.Unlock()

	// Send the last event immediately if needed (outside the lock to avoid deadlock)
	if shouldSendLastEvent && lastEventCopy != nil {
		select {
		case ch <- *lastEventCopy:
		default:
			// Channel is full, skip sending last event
		}
	}

	// Return deregistration function
	return func() {
		e.mu.Lock()
		delete(e.channels, id)
		e.mu.Unlock()
	}
}

// Notify sends the provided value to all registered channels
// This operation is thread-safe. Sends are non-blocking - if a channel is full, it is skipped.
func (e *ChannelEvent[T]) Notify(value T) {
	e.mu.Lock()
	// Store the last event if needed
	if e.sendLastEventOnListen {
		if e.lastEvent == nil {
			e.lastEvent = new(T)
		}
		*e.lastEvent = value
		e.hasNotified = true
	}

	// Create a copy of channels to send to outside the lock
	channelsCopy := make(map[uint64]chan<- T, len(e.channels))
	for id, ch := range e.channels {
		channelsCopy[id] = ch
	}
	e.mu.Unlock()

	// Send to all channels outside the lock (non-blocking)
	for _, ch := range channelsCopy {
		select {
		case ch <- value:
		default:
			// Channel is full, skip this channel
		}
	}
}

// ListenerCount returns the current number of registered listeners
// This is useful for testing and debugging
func (e *ChannelEvent[T]) ListenerCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.channels)
}
