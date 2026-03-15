package trainer

import (
	"sync"
	"time"
)

type WorkoutEventType string

const (
	WorkoutEventPlay  WorkoutEventType = "play"
	WorkoutEventPause WorkoutEventType = "pause"
	WorkoutEventStop  WorkoutEventType = "stop"
	WorkoutEventData  WorkoutEventType = "data"
)

type WorkoutData struct {
	Power     float64
	HeartRate float64
}

type WorkoutEvent struct {
	EventType   WorkoutEventType
	Timestamp   time.Time
	WorkoutData *WorkoutData // non-nil only for WorkoutEventData
}

// WorkoutTracker records workout lifecycle events and metric data over time.
type WorkoutTracker struct {
	mu     sync.RWMutex
	events []WorkoutEvent
	active bool
}

func NewWorkoutTracker() *WorkoutTracker {
	return &WorkoutTracker{}
}

func resolveTimestamp(ts *time.Time) time.Time {
	if ts != nil {
		return *ts
	}
	return time.Now()
}

// Start resets all recorded data and appends a play event. Use for a fresh workout start.
func (t *WorkoutTracker) Start(ts *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = nil
	t.active = true
	t.events = append(t.events, WorkoutEvent{EventType: WorkoutEventPlay, Timestamp: resolveTimestamp(ts)})
}

// Resume appends a play event without resetting recorded data. Use when resuming from pause.
func (t *WorkoutTracker) Resume(ts *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	t.events = append(t.events, WorkoutEvent{EventType: WorkoutEventPlay, Timestamp: resolveTimestamp(ts)})
}

// Pause appends a pause event.
func (t *WorkoutTracker) Pause(ts *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	t.events = append(t.events, WorkoutEvent{EventType: WorkoutEventPause, Timestamp: resolveTimestamp(ts)})
}

// Stop appends a stop event and marks the tracker inactive.
func (t *WorkoutTracker) Stop(ts *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	t.events = append(t.events, WorkoutEvent{EventType: WorkoutEventStop, Timestamp: resolveTimestamp(ts)})
	t.active = false
}

// Submit records a data sample.
func (t *WorkoutTracker) Submit(ts *time.Time, data WorkoutData) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	d := data
	t.events = append(t.events, WorkoutEvent{EventType: WorkoutEventData, Timestamp: resolveTimestamp(ts), WorkoutData: &d})
}

// GetElapsedTime returns the total active (playing) duration by summing play→pause/stop
// intervals. If the tracker is currently active, the open interval is measured to ts
// (or time.Now() if ts is nil).
func (t *WorkoutTracker) GetElapsedTime(ts *time.Time) time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	now := resolveTimestamp(ts)
	var elapsed time.Duration
	var lastPlay time.Time
	for _, e := range t.events {
		switch e.EventType {
		case WorkoutEventPlay:
			lastPlay = e.Timestamp
		case WorkoutEventPause, WorkoutEventStop:
			if !lastPlay.IsZero() {
				elapsed += e.Timestamp.Sub(lastPlay)
				lastPlay = time.Time{}
			}
		}
	}
	if !lastPlay.IsZero() {
		elapsed += now.Sub(lastPlay)
	}
	return elapsed
}

// GetEvents returns a copy of all recorded events.
func (t *WorkoutTracker) GetEvents() []WorkoutEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]WorkoutEvent, len(t.events))
	copy(result, t.events)
	return result
}

// timeWeightedAvg computes a time-weighted average over data events.
// Each sample is weighted by how long it is held — from its timestamp until the next
// data event, pause, stop, or (if still playing) the query time now.
// getValue returns (value, include); include=false skips the sample (e.g. HR=0).
func timeWeightedAvg(events []WorkoutEvent, now time.Time, getValue func(*WorkoutData) (float64, bool)) float64 {
	var weightedSum, totalWeight float64
	var currentData *WorkoutData
	var currentDataTime time.Time
	playing := false

	accumulate := func(until time.Time) {
		if currentData == nil {
			return
		}
		weight := until.Sub(currentDataTime).Seconds()
		if weight <= 0 {
			return
		}
		if value, ok := getValue(currentData); ok {
			weightedSum += value * weight
			totalWeight += weight
		}
	}

	for _, e := range events {
		switch e.EventType {
		case WorkoutEventPlay:
			playing = true
		case WorkoutEventData:
			if playing {
				accumulate(e.Timestamp)
				currentData = e.WorkoutData
				currentDataTime = e.Timestamp
			}
		case WorkoutEventPause, WorkoutEventStop:
			if playing {
				accumulate(e.Timestamp)
				currentData = nil
			}
			playing = false
		}
	}

	if playing {
		accumulate(now)
	}

	if totalWeight == 0 {
		return 0
	}
	return weightedSum / totalWeight
}

// GetWorkoutAvgHeartRate returns the time-weighted mean heart rate.
// Each sample is weighted by how long it was held before the next sample, pause, or stop.
// Samples with HR=0 are excluded. ts controls the end of any open play interval
// (nil defaults to time.Now()).
func (t *WorkoutTracker) GetWorkoutAvgHeartRate(ts *time.Time) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return timeWeightedAvg(t.events, resolveTimestamp(ts), func(d *WorkoutData) (float64, bool) {
		return d.HeartRate, d.HeartRate > 0
	})
}

// GetWorkoutAvgPower returns the time-weighted mean power.
// Each sample is weighted by how long it was held before the next sample, pause, or stop.
// ts controls the end of any open play interval (nil defaults to time.Now()).
func (t *WorkoutTracker) GetWorkoutAvgPower(ts *time.Time) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return timeWeightedAvg(t.events, resolveTimestamp(ts), func(d *WorkoutData) (float64, bool) {
		return d.Power, true
	})
}

// GetWorkoutDuration returns the wall-clock duration from the first play event to the
// stop event (or now if the workout has not been stopped).
// Returns 0 if no play event has been recorded.
func (t *WorkoutTracker) GetWorkoutDuration() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var start time.Time
	for _, e := range t.events {
		if e.EventType == WorkoutEventPlay {
			start = e.Timestamp
			break
		}
	}
	if start.IsZero() {
		return 0
	}
	for i := len(t.events) - 1; i >= 0; i-- {
		if t.events[i].EventType == WorkoutEventStop {
			return t.events[i].Timestamp.Sub(start)
		}
	}
	return time.Since(start)
}
