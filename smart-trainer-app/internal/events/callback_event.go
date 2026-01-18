package events

import (
	"sync"
)

// CallbackEvent provides pub/sub behavior with type-safe callbacks
// T is the type of the argument passed to callback functions
type CallbackEvent[T any] struct {
	mu                    sync.RWMutex
	listeners             map[uint64]func(T)
	nextID                uint64
	sendLastEventOnListen bool
	lastEvent             *T
	hasNotified           bool
}

// NewCallbackEvent creates a new CallbackEvent instance
// sendLastEventOnListen: if true, the CallbackEvent will remember the last Notify parameter
// and call new listeners immediately with that value if Notify has been called at least once
func NewCallbackEvent[T any](sendLastEventOnListen bool) *CallbackEvent[T] {
	return &CallbackEvent[T]{
		listeners:             make(map[uint64]func(T)),
		sendLastEventOnListen: sendLastEventOnListen,
	}
}

// Listen registers a callback function to be called when Notify is invoked
// Returns a deregistration function that can be called to remove the listener
// If sendLastEventOnListen is true and Notify has been called at least once,
// the callback will be called immediately with the last event value
func (e *CallbackEvent[T]) Listen(callback func(T)) func() {
	if callback == nil {
		panic("callback cannot be nil")
	}

	e.mu.Lock()
	id := e.nextID
	e.nextID++
	e.listeners[id] = callback
	shouldSendLastEvent := e.sendLastEventOnListen && e.hasNotified && e.lastEvent != nil
	var lastEventCopy *T
	if shouldSendLastEvent {
		// Create a copy of the last event to send
		lastEventCopy = new(T)
		*lastEventCopy = *e.lastEvent
	}
	e.mu.Unlock()

	// Call the callback immediately if needed (outside the lock to avoid deadlock)
	if shouldSendLastEvent && lastEventCopy != nil {
		callback(*lastEventCopy)
	}

	// Return deregistration function
	return func() {
		e.mu.Lock()
		delete(e.listeners, id)
		e.mu.Unlock()
	}
}

// Notify calls all registered listener callbacks with the provided value
// This operation is thread-safe
func (e *CallbackEvent[T]) Notify(value T) {
	e.mu.Lock()
	// Store the last event if needed
	if e.sendLastEventOnListen {
		if e.lastEvent == nil {
			e.lastEvent = new(T)
		}
		*e.lastEvent = value
		e.hasNotified = true
	}

	// Create a copy of listeners to call outside the lock
	listenersCopy := make(map[uint64]func(T), len(e.listeners))
	for id, callback := range e.listeners {
		listenersCopy[id] = callback
	}
	e.mu.Unlock()

	// Call all callbacks outside the lock to avoid deadlock
	for _, callback := range listenersCopy {
		callback(value)
	}
}

// ListenerCount returns the current number of registered listeners
// This is useful for testing and debugging
func (e *CallbackEvent[T]) ListenerCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.listeners)
}

