package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewChannelEvent(t *testing.T) {
	event := NewChannelEvent[string](false)
	require.NotNil(t, event)
	assert.Equal(t, 0, event.ListenerCount())
	assert.False(t, event.sendLastEventOnListen)

	event2 := NewChannelEvent[int](true)
	require.NotNil(t, event2)
	assert.True(t, event2.sendLastEventOnListen)
}

func TestChannelEvent_Listen_Notify_Basic(t *testing.T) {
	event := NewChannelEvent[string](false)

	ch := make(chan string, 10)
	unregister := event.Listen(ch)

	assert.Equal(t, 1, event.ListenerCount())

	event.Notify("test1")
	event.Notify("test2")

	// Wait a bit for async sends
	time.Sleep(10 * time.Millisecond)

	received := make([]string, 0)
	for len(received) < 2 {
		select {
		case val := <-ch:
			received = append(received, val)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for events")
		}
	}

	assert.Equal(t, 2, len(received))
	assert.Contains(t, received, "test1")
	assert.Contains(t, received, "test2")

	unregister()
	assert.Equal(t, 0, event.ListenerCount())

	event.Notify("test3")
	time.Sleep(10 * time.Millisecond)

	// Should not receive test3 since listener was removed
	select {
	case val := <-ch:
		t.Errorf("Unexpected value received after unregister: %s", val)
	default:
		// Expected - no value should be received
	}
}

func TestChannelEvent_MultipleListeners(t *testing.T) {
	event := NewChannelEvent[int](false)

	ch1 := make(chan int, 10)
	ch2 := make(chan int, 10)
	unregister1 := event.Listen(ch1)
	unregister2 := event.Listen(ch2)

	assert.Equal(t, 2, event.ListenerCount())

	event.Notify(42)
	event.Notify(100)

	time.Sleep(10 * time.Millisecond)

	received1 := make([]int, 0)
	received2 := make([]int, 0)

	for len(received1) < 2 {
		select {
		case val := <-ch1:
			received1 = append(received1, val)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for events on ch1")
		}
	}

	for len(received2) < 2 {
		select {
		case val := <-ch2:
			received2 = append(received2, val)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for events on ch2")
		}
	}

	assert.Equal(t, 2, len(received1))
	assert.Contains(t, received1, 42)
	assert.Contains(t, received1, 100)
	assert.Equal(t, 2, len(received2))
	assert.Contains(t, received2, 42)
	assert.Contains(t, received2, 100)

	unregister1()
	unregister2()
	assert.Equal(t, 0, event.ListenerCount())
}

func TestChannelEvent_SendLastEventOnListen_True_NoNotifyYet(t *testing.T) {
	event := NewChannelEvent[string](true)

	ch := make(chan string, 10)
	unregister := event.Listen(ch)

	time.Sleep(10 * time.Millisecond)

	// Should not receive anything since Notify hasn't been called yet
	select {
	case val := <-ch:
		t.Errorf("Unexpected value received: %s", val)
	default:
		// Expected - no value should be received
	}

	unregister()
}

func TestChannelEvent_SendLastEventOnListen_True_AfterNotify(t *testing.T) {
	event := NewChannelEvent[string](true)

	// First listener
	ch1 := make(chan string, 10)
	unregister1 := event.Listen(ch1)

	time.Sleep(10 * time.Millisecond)
	select {
	case val := <-ch1:
		t.Errorf("Unexpected value received: %s", val)
	default:
		// Expected
	}

	// Notify with a value
	event.Notify("first-event")
	time.Sleep(10 * time.Millisecond)

	var received1 []string
	select {
	case val := <-ch1:
		received1 = append(received1, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for first event")
	}

	assert.Equal(t, 1, len(received1))
	assert.Equal(t, "first-event", received1[0])

	// Add a second listener - should receive the last event immediately
	ch2 := make(chan string, 10)
	unregister2 := event.Listen(ch2)

	time.Sleep(10 * time.Millisecond)

	var received2 []string
	select {
	case val := <-ch2:
		received2 = append(received2, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for last event on new listener")
	}

	assert.Equal(t, 1, len(received2))
	assert.Equal(t, "first-event", received2[0])

	// Notify again - both should receive
	event.Notify("second-event")
	time.Sleep(10 * time.Millisecond)

	select {
	case val := <-ch1:
		received1 = append(received1, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for second event on ch1")
	}

	select {
	case val := <-ch2:
		received2 = append(received2, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for second event on ch2")
	}

	assert.Equal(t, 2, len(received1))
	assert.Contains(t, received1, "second-event")
	assert.Equal(t, 2, len(received2))
	assert.Contains(t, received2, "second-event")

	unregister1()
	unregister2()
}

func TestChannelEvent_SendLastEventOnListen_False(t *testing.T) {
	event := NewChannelEvent[string](false)

	event.Notify("first-event")

	// Add listener after Notify - should NOT receive the last event
	ch := make(chan string, 10)
	unregister := event.Listen(ch)

	time.Sleep(10 * time.Millisecond)

	select {
	case val := <-ch:
		t.Errorf("Unexpected value received: %s", val)
	default:
		// Expected - should not receive last event
	}

	// Only new notifications should be received
	event.Notify("second-event")
	time.Sleep(10 * time.Millisecond)

	select {
	case val := <-ch:
		assert.Equal(t, "second-event", val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for second event")
	}

	unregister()
}

func TestChannelEvent_Listen_NilChannel(t *testing.T) {
	event := NewChannelEvent[string](false)

	assert.Panics(t, func() {
		event.Listen(nil)
	})
}

func TestChannelEvent_FullChannel(t *testing.T) {
	event := NewChannelEvent[string](false)

	// Create a channel with buffer size 1
	ch := make(chan string, 1)
	unregister := event.Listen(ch)

	// Fill the channel
	ch <- "blocking"

	// Notify - should be skipped since channel is full
	event.Notify("test1")
	event.Notify("test2")
	time.Sleep(10 * time.Millisecond)

	// Channel should still only have the blocking value
	assert.Equal(t, 1, len(ch))

	// Drain the channel
	<-ch

	// Next notify should work
	event.Notify("test3")
	time.Sleep(10 * time.Millisecond)

	select {
	case val := <-ch:
		assert.Equal(t, "test3", val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for test3")
	}

	unregister()
}

func TestChannelEvent_ConcurrentAccess(t *testing.T) {
	event := NewChannelEvent[int](false)

	var wg sync.WaitGroup
	channels := make([]chan int, 10)
	unregisters := make([]func(), 10)

	// Add multiple listeners concurrently
	for i := 0; i < 10; i++ {
		ch := make(chan int, 100)
		channels[i] = ch
		unregisters[i] = event.Listen(ch)
	}

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

	time.Sleep(50 * time.Millisecond)

	// Each channel should have received 5 values
	for i, ch := range channels {
		received := make([]int, 0)
		for len(received) < 5 {
			select {
			case val := <-ch:
				received = append(received, val)
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("Channel %d did not receive all values. Got %d", i, len(received))
			}
		}
		assert.Equal(t, 5, len(received))
	}

	// Clean up listeners
	for _, unregister := range unregisters {
		unregister()
	}
}

