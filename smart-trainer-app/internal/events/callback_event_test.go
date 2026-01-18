package events

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCallbackEvent(t *testing.T) {
	event := NewCallbackEvent[string](false)
	require.NotNil(t, event)
	assert.Equal(t, 0, event.ListenerCount())
	assert.False(t, event.sendLastEventOnListen)

	event2 := NewCallbackEvent[int](true)
	require.NotNil(t, event2)
	assert.True(t, event2.sendLastEventOnListen)
}

func TestEvent_Listen_Notify_Basic(t *testing.T) {
	event := NewCallbackEvent[string](false)

	received := make([]string, 0)
	var mu sync.Mutex

	unregister := event.Listen(func(value string) {
		mu.Lock()
		received = append(received, value)
		mu.Unlock()
	})

	assert.Equal(t, 1, event.ListenerCount())

	event.Notify("test1")
	event.Notify("test2")

	mu.Lock()
	assert.Equal(t, 2, len(received))
	assert.Equal(t, "test1", received[0])
	assert.Equal(t, "test2", received[1])
	mu.Unlock()

	unregister()
	assert.Equal(t, 0, event.ListenerCount())

	event.Notify("test3")
	mu.Lock()
	// Should still be 2 since listener was removed
	assert.Equal(t, 2, len(received))
	mu.Unlock()
}

func TestEvent_MultipleListeners(t *testing.T) {
	event := NewCallbackEvent[int](false)

	received1 := make([]int, 0)
	received2 := make([]int, 0)
	var mu sync.Mutex

	unregister1 := event.Listen(func(value int) {
		mu.Lock()
		received1 = append(received1, value)
		mu.Unlock()
	})

	unregister2 := event.Listen(func(value int) {
		mu.Lock()
		received2 = append(received2, value)
		mu.Unlock()
	})

	assert.Equal(t, 2, event.ListenerCount())

	event.Notify(42)
	event.Notify(100)

	mu.Lock()
	assert.Equal(t, 2, len(received1))
	assert.Equal(t, 42, received1[0])
	assert.Equal(t, 100, received1[1])
	assert.Equal(t, 2, len(received2))
	assert.Equal(t, 42, received2[0])
	assert.Equal(t, 100, received2[1])
	mu.Unlock()

	unregister1()
	unregister2()
	assert.Equal(t, 0, event.ListenerCount())
}

func TestEvent_SendLastEventOnListen_True_NoNotifyYet(t *testing.T) {
	event := NewCallbackEvent[string](true)

	received := make([]string, 0)
	var mu sync.Mutex

	unregister := event.Listen(func(value string) {
		mu.Lock()
		received = append(received, value)
		mu.Unlock()
	})

	// Should not receive anything since Notify hasn't been called yet
	mu.Lock()
	assert.Equal(t, 0, len(received))
	mu.Unlock()

	unregister()
}

func TestEvent_SendLastEventOnListen_True_AfterNotify(t *testing.T) {
	event := NewCallbackEvent[string](true)

	// First listener - should not receive anything initially
	received1 := make([]string, 0)
	var mu1 sync.Mutex
	unregister1 := event.Listen(func(value string) {
		mu1.Lock()
		received1 = append(received1, value)
		mu1.Unlock()
	})

	mu1.Lock()
	assert.Equal(t, 0, len(received1))
	mu1.Unlock()

	// Notify with a value
	event.Notify("first-event")

	mu1.Lock()
	assert.Equal(t, 1, len(received1))
	assert.Equal(t, "first-event", received1[0])
	mu1.Unlock()

	// Add a second listener - should receive the last event immediately
	received2 := make([]string, 0)
	var mu2 sync.Mutex
	unregister2 := event.Listen(func(value string) {
		mu2.Lock()
		received2 = append(received2, value)
		mu2.Unlock()
	})

	mu2.Lock()
	assert.Equal(t, 1, len(received2))
	assert.Equal(t, "first-event", received2[0])
	mu2.Unlock()

	// Notify again - both should receive
	event.Notify("second-event")

	mu1.Lock()
	assert.Equal(t, 2, len(received1))
	assert.Equal(t, "second-event", received1[1])
	mu1.Unlock()

	mu2.Lock()
	assert.Equal(t, 2, len(received2))
	assert.Equal(t, "second-event", received2[1])
	mu2.Unlock()

	unregister1()
	unregister2()
}

func TestEvent_SendLastEventOnListen_False(t *testing.T) {
	event := NewCallbackEvent[string](false)

	event.Notify("first-event")

	// Add listener after Notify - should NOT receive the last event
	received := make([]string, 0)
	var mu sync.Mutex
	unregister := event.Listen(func(value string) {
		mu.Lock()
		received = append(received, value)
		mu.Unlock()
	})

	mu.Lock()
	assert.Equal(t, 0, len(received))
	mu.Unlock()

	// Only new notifications should be received
	event.Notify("second-event")

	mu.Lock()
	assert.Equal(t, 1, len(received))
	assert.Equal(t, "second-event", received[0])
	mu.Unlock()

	unregister()
}

func TestEvent_ConcurrentAccess(t *testing.T) {
	event := NewCallbackEvent[int](false)

	var wg sync.WaitGroup
	received := make([]int, 0)
	var mu sync.Mutex
	unregisters := make([]func(), 0)
	var unregisterMu sync.Mutex

	// Add multiple listeners concurrently
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer wg.Done()
			unregister := event.Listen(func(value int) {
				mu.Lock()
				received = append(received, value)
				mu.Unlock()
			})
			unregisterMu.Lock()
			unregisters = append(unregisters, unregister)
			unregisterMu.Unlock()
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 10, event.ListenerCount())

	// Notify concurrently
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func(value int) {
			defer wg.Done()
			event.Notify(value)
		}(i)
	}
	wg.Wait()

	// Should have received 5 * 10 = 50 notifications
	mu.Lock()
	assert.Equal(t, 50, len(received))
	mu.Unlock()

	// Clean up listeners
	unregisterMu.Lock()
	for _, unregister := range unregisters {
		unregister()
	}
	unregisterMu.Unlock()
}

func TestEvent_Listen_NilCallback(t *testing.T) {
	event := NewCallbackEvent[string](false)

	assert.Panics(t, func() {
		event.Listen(nil)
	})
}

func TestEvent_ComplexType(t *testing.T) {
	type Person struct {
		Name string
		Age  int
	}

	event := NewCallbackEvent[Person](true)

	received := make([]Person, 0)
	var mu sync.Mutex

	unregister := event.Listen(func(p Person) {
		mu.Lock()
		received = append(received, p)
		mu.Unlock()
	})

	person1 := Person{Name: "Alice", Age: 30}
	event.Notify(person1)

	mu.Lock()
	assert.Equal(t, 1, len(received))
	assert.Equal(t, "Alice", received[0].Name)
	assert.Equal(t, 30, received[0].Age)
	mu.Unlock()

	// Add another listener - should receive last event
	received2 := make([]Person, 0)
	var mu2 sync.Mutex
	unregister2 := event.Listen(func(p Person) {
		mu2.Lock()
		received2 = append(received2, p)
		mu2.Unlock()
	})

	mu2.Lock()
	assert.Equal(t, 1, len(received2))
	assert.Equal(t, "Alice", received2[0].Name)
	mu2.Unlock()

	person2 := Person{Name: "Bob", Age: 25}
	event.Notify(person2)

	mu.Lock()
	assert.Equal(t, 2, len(received))
	assert.Equal(t, "Bob", received[1].Name)
	mu.Unlock()

	mu2.Lock()
	assert.Equal(t, 2, len(received2))
	assert.Equal(t, "Bob", received2[1].Name)
	mu2.Unlock()

	unregister()
	unregister2()
}

func TestEvent_UnregisterDuringNotify(t *testing.T) {
	event := NewCallbackEvent[string](false)

	received := make([]string, 0)
	var mu sync.Mutex
	var unregister func()

	unregister = event.Listen(func(value string) {
		mu.Lock()
		received = append(received, value)
		mu.Unlock()
		// Unregister during notification
		if value == "unregister" {
			unregister()
		}
	})

	event.Notify("test1")
	event.Notify("unregister")
	event.Notify("test2")

	mu.Lock()
	// Should have received "test1" and "unregister", but not "test2"
	assert.Equal(t, 2, len(received))
	assert.Equal(t, "test1", received[0])
	assert.Equal(t, "unregister", received[1])
	mu.Unlock()

	assert.Equal(t, 0, event.ListenerCount())
}

func TestEvent_MultipleUnregisterCalls(t *testing.T) {
	event := NewCallbackEvent[string](false)

	unregister := event.Listen(func(value string) {})

	assert.Equal(t, 1, event.ListenerCount())

	unregister()
	assert.Equal(t, 0, event.ListenerCount())

	// Calling unregister multiple times should be safe
	unregister()
	unregister()
	assert.Equal(t, 0, event.ListenerCount())
}

