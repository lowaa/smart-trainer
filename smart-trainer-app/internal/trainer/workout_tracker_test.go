package trainer

import (
	"testing"
	"time"
)

// ts returns a pointer to base + offset seconds, for use as a controlled timestamp.
func ts(base time.Time, seconds int) *time.Time {
	t := base.Add(time.Duration(seconds) * time.Second)
	return &t
}

var epoch = time.Unix(0, 0)

func TestGetElapsedTime_NoEvents(t *testing.T) {
	tracker := NewWorkoutTracker()
	if got := tracker.GetElapsedTime(ts(epoch, 10)); got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

func TestGetElapsedTime_Running(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))

	got := tracker.GetElapsedTime(ts(epoch, 10))
	if got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestGetElapsedTime_Stopped(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Stop(ts(epoch, 10))

	// queried at T+20 — stop at T+10 should close the interval
	got := tracker.GetElapsedTime(ts(epoch, 20))
	if got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestGetElapsedTime_PauseGapExcluded(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Pause(ts(epoch, 5))

	// queried at T+10 — pause at T+5, so only 5s active
	got := tracker.GetElapsedTime(ts(epoch, 10))
	if got != 5*time.Second {
		t.Errorf("expected 5s, got %v", got)
	}
}

func TestGetElapsedTime_ResumeAfterPause(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))  // play  T0
	tracker.Pause(ts(epoch, 5))  // pause T5  → 5s active
	tracker.Resume(ts(epoch, 8)) // play  T8

	// queried at T12: 5 + (12-8) = 9s active
	got := tracker.GetElapsedTime(ts(epoch, 12))
	if got != 9*time.Second {
		t.Errorf("expected 9s, got %v", got)
	}
}

func TestGetElapsedTime_MultiplePlayPauseCycles(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))   // play  T0
	tracker.Pause(ts(epoch, 5))   // pause T5  → 5s
	tracker.Resume(ts(epoch, 10)) // play  T10
	tracker.Pause(ts(epoch, 13))  // pause T13 → 3s
	tracker.Resume(ts(epoch, 20)) // play  T20

	// queried at T22: 5 + 3 + (22-20) = 10s active
	got := tracker.GetElapsedTime(ts(epoch, 22))
	if got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestGetElapsedTime_StopAfterPause(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))   // play  T0
	tracker.Pause(ts(epoch, 5))   // pause T5  → 5s
	tracker.Resume(ts(epoch, 10)) // play  T10
	tracker.Stop(ts(epoch, 15))   // stop  T15 → 5s

	// 5 + 5 = 10s; queried at T20 should not grow further
	got := tracker.GetElapsedTime(ts(epoch, 20))
	if got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestGetWorkoutDuration_NoEvents(t *testing.T) {
	tracker := NewWorkoutTracker()
	if got := tracker.GetWorkoutDuration(); got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

func TestGetWorkoutDuration_Stopped(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Stop(ts(epoch, 30))

	if got := tracker.GetWorkoutDuration(); got != 30*time.Second {
		t.Errorf("expected 30s, got %v", got)
	}
}

func TestGetWorkoutDuration_PauseDoesNotAffectWallClock(t *testing.T) {
	// GetWorkoutDuration is wall-clock (first play → stop), not active time.
	// A mid-workout pause should still be included in the wall-clock duration.
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Pause(ts(epoch, 10))
	tracker.Resume(ts(epoch, 20))
	tracker.Stop(ts(epoch, 30))

	if got := tracker.GetWorkoutDuration(); got != 30*time.Second {
		t.Errorf("expected 30s (wall clock), got %v", got)
	}
}

func TestGetWorkoutAvgHeartRate_NoData(t *testing.T) {
	tracker := NewWorkoutTracker()
	if got := tracker.GetWorkoutAvgHeartRate(nil); got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

func TestGetWorkoutAvgHeartRate_EqualIntervals(t *testing.T) {
	// Both samples held for equal durations → simple mean
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 1), WorkoutData{HeartRate: 100})
	tracker.Submit(ts(epoch, 2), WorkoutData{HeartRate: 200})
	tracker.Stop(ts(epoch, 3))

	// 100 held 1s, 200 held 1s → avg = 150
	if got := tracker.GetWorkoutAvgHeartRate(ts(epoch, 3)); got != 150 {
		t.Errorf("expected 150, got %v", got)
	}
}

func TestGetWorkoutAvgHeartRate_IrregularIntervals(t *testing.T) {
	// Longer hold at lower HR should pull the average down vs a simple mean
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 0), WorkoutData{HeartRate: 120}) // held 20s (T0→T20)
	tracker.Submit(ts(epoch, 20), WorkoutData{HeartRate: 180}) // held 10s (T20→T30)
	tracker.Stop(ts(epoch, 30))

	// (120*20 + 180*10) / 30 = (2400 + 1800) / 30 = 140
	// Simple mean would be (120+180)/2 = 150 — time-weighting gives the correct lower value
	expected := (120.0*20 + 180.0*10) / 30
	if got := tracker.GetWorkoutAvgHeartRate(ts(epoch, 30)); got != expected {
		t.Errorf("expected %.2f, got %v", expected, got)
	}
}

func TestGetWorkoutAvgHeartRate_ZeroExcluded(t *testing.T) {
	// HR=0 means no sensor reading and must not contribute to the average
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 1), WorkoutData{HeartRate: 0})
	tracker.Submit(ts(epoch, 2), WorkoutData{HeartRate: 120})
	tracker.Stop(ts(epoch, 3))

	// only the 120bpm sample (held 1s) contributes
	if got := tracker.GetWorkoutAvgHeartRate(ts(epoch, 3)); got != 120 {
		t.Errorf("expected 120, got %v", got)
	}
}

func TestGetWorkoutAvgHeartRate_PauseStopsHold(t *testing.T) {
	// The hold period for a sample must end at the pause event, not at the next sample
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 5), WorkoutData{HeartRate: 100}) // held until pause at T10
	tracker.Pause(ts(epoch, 10))                              // closes 100bpm hold: 5s
	tracker.Resume(ts(epoch, 20))                             // gap T10→T20 excluded
	tracker.Submit(ts(epoch, 25), WorkoutData{HeartRate: 160}) // held until stop at T30
	tracker.Stop(ts(epoch, 30))                               // closes 160bpm hold: 5s

	// (100*5 + 160*5) / 10 = 1300/10 = 130
	expected := (100.0*5 + 160.0*5) / 10
	if got := tracker.GetWorkoutAvgHeartRate(ts(epoch, 30)); got != expected {
		t.Errorf("expected %.2f, got %v", expected, got)
	}
}

func TestGetWorkoutAvgPower_NoData(t *testing.T) {
	tracker := NewWorkoutTracker()
	if got := tracker.GetWorkoutAvgPower(nil); got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

func TestGetWorkoutAvgPower_EqualIntervals(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 1), WorkoutData{Power: 100})
	tracker.Submit(ts(epoch, 2), WorkoutData{Power: 300})
	tracker.Stop(ts(epoch, 3))

	// 100 held 1s, 300 held 1s → avg = 200
	if got := tracker.GetWorkoutAvgPower(ts(epoch, 3)); got != 200 {
		t.Errorf("expected 200, got %v", got)
	}
}

func TestGetWorkoutAvgPower_IrregularIntervals(t *testing.T) {
	// A longer block at lower power should dominate the weighted average
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 5), WorkoutData{Power: 100})  // held 10s (T5→T15)
	tracker.Submit(ts(epoch, 15), WorkoutData{Power: 200}) // held 5s  (T15→T20)
	tracker.Stop(ts(epoch, 20))

	// (100*10 + 200*5) / 15 = 2000/15 ≈ 133.33
	// Simple mean would be 150 — confirms weighting is applied
	expected := (100.0*10 + 200.0*5) / 15
	if got := tracker.GetWorkoutAvgPower(ts(epoch, 20)); got != expected {
		t.Errorf("expected %.4f, got %v", expected, got)
	}
}

func TestGetWorkoutAvgPower_PauseStopsHold(t *testing.T) {
	// Pause must close the current sample's hold period; the pause gap must not be counted
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Submit(ts(epoch, 5), WorkoutData{Power: 100})  // held until pause at T10: 5s
	tracker.Pause(ts(epoch, 10))                           // T10→T20 excluded
	tracker.Resume(ts(epoch, 20))
	tracker.Submit(ts(epoch, 25), WorkoutData{Power: 200}) // held until stop at T30: 5s
	tracker.Stop(ts(epoch, 30))

	// (100*5 + 200*5) / 10 = 150
	if got := tracker.GetWorkoutAvgPower(ts(epoch, 30)); got != 150 {
		t.Errorf("expected 150, got %v", got)
	}
}

func TestSubmit_AfterStop_Ignored(t *testing.T) {
	tracker := NewWorkoutTracker()
	tracker.Start(ts(epoch, 0))
	tracker.Stop(ts(epoch, 5))
	tracker.Submit(ts(epoch, 6), WorkoutData{Power: 300, HeartRate: 180})

	if got := tracker.GetWorkoutAvgPower(nil); got != 0 {
		t.Errorf("expected 0 (submit after stop should be ignored), got %v", got)
	}
	if got := tracker.GetWorkoutAvgHeartRate(nil); got != 0 {
		t.Errorf("expected 0 (submit after stop should be ignored), got %v", got)
	}
}
