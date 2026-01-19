package trainer

import (
	"log"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"
)

// workoutCommand represents commands sent to the workout goroutine
type workoutCommand int

const (
	cmdStart workoutCommand = iota
	cmdPause
	cmdStop
)

const (
	hrPidKp          = 2.5
	hrPidKi          = 0.15
	hrPidKd          = 0.5
	hrPidOutputMin   = 50
	hrPidIntegralMax = 150
	hrPidMaxFTPMult  = 1.0
)

// hrPIDState holds the state for the heart rate PID controller
type hrPIDState struct {
	integral      float64 // Accumulated integral term
	lastError     float64 // Previous error for derivative calculation
	currentOutput float64 // Current power output
	initialized   bool    // Whether the PID has been initialized
}

// reset clears the PID state for a new workout or block
func (p *hrPIDState) reset() {
	p.integral = 0
	p.lastError = 0
	p.currentOutput = 0
	p.initialized = false
}

// update runs one iteration of the PID controller
// targetHR: desired heart rate in bpm
// currentHR: actual heart rate in bpm
// maxOutput: maximum power output in watts (typically FTP * hrPidMaxFTPMult)
// returns: power output in watts
func (p *hrPIDState) update(targetHR, currentHR, maxOutput float64) float64 {
	// Initialize output to a reasonable starting power if first run
	if !p.initialized {
		p.currentOutput = 100 // Start at 100W
		p.initialized = true
	}

	// Error: positive when HR is below target (need more power)
	err := targetHR - currentHR

	// Proportional term
	pTerm := hrPidKp * err

	// Integral term with anti-windup
	p.integral += err
	if p.integral > hrPidIntegralMax {
		p.integral = hrPidIntegralMax
	} else if p.integral < -hrPidIntegralMax {
		p.integral = -hrPidIntegralMax
	}
	iTerm := hrPidKi * p.integral

	// Derivative term (disabled by default, but included for completeness)
	dTerm := hrPidKd * (err - p.lastError)
	p.lastError = err

	// Calculate new output
	p.currentOutput += pTerm + iTerm + dTerm

	// Clamp output to valid range (max is based on FTP to prevent excessive power)
	if p.currentOutput < hrPidOutputMin {
		p.currentOutput = hrPidOutputMin
	} else if p.currentOutput > maxOutput {
		p.currentOutput = maxOutput
	}

	return p.currentOutput
}

// WorkoutManager manages workout execution and communicates with UIModel and DeviceHandler
type WorkoutManager struct {
	model         *UIModel
	deviceHandler *DeviceHandler
	logger        *log.Logger

	// Current workout state (protected by mu)
	mu          sync.RWMutex
	workout     *Workout
	status      WorkoutStatus
	elapsedTime time.Duration
	ftp         int16 // Functional Threshold Power in watts
	maxHR       int16 // Maximum heart rate in bpm

	// PID controller state for HR mode (protected by mu)
	hrPID        hrPIDState
	lastBlockIdx int // Track block changes to reset PID

	// Goroutine management
	cmdChan      chan workoutCommand
	doneChan     chan struct{} // Closed to signal shutdown
	wg           sync.WaitGroup
	shutdownOnce sync.Once
}

// Default values
const (
	DefaultFTP   = 220
	DefaultMaxHR = 185
)

// NewWorkoutManager creates a new WorkoutManager
func NewWorkoutManager(model *UIModel, deviceHandler *DeviceHandler, logger *log.Logger) *WorkoutManager {
	if model == nil {
		panic("WorkoutManager: model cannot be nil")
	}
	if deviceHandler == nil {
		panic("WorkoutManager: deviceHandler cannot be nil")
	}
	if logger == nil {
		panic("WorkoutManager: logger cannot be nil")
	}

	wm := &WorkoutManager{
		model:         model,
		deviceHandler: deviceHandler,
		logger:        logger,
		status:        WorkoutStatusIdle,
		ftp:           DefaultFTP,
		maxHR:         DefaultMaxHR,
		lastBlockIdx:  -1,
		cmdChan:       make(chan workoutCommand, 1),
		doneChan:      make(chan struct{}),
	}

	// Start the workout execution goroutine
	wm.wg.Add(1)
	go_func_utils.SafeGo(logger, func() { wm.runWorkoutLoop() })

	return wm
}

// SetFTP sets the Functional Threshold Power value
func (wm *WorkoutManager) SetFTP(ftp int16) {
	wm.mu.Lock()
	wm.ftp = ftp
	wm.mu.Unlock()
	wm.logger.Printf("WorkoutManager: FTP set to %d W", ftp)
}

// GetFTP returns the current FTP value
func (wm *WorkoutManager) GetFTP() int16 {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.ftp
}

// SetMaxHR sets the maximum heart rate value
func (wm *WorkoutManager) SetMaxHR(maxHR int16) {
	wm.mu.Lock()
	wm.maxHR = maxHR
	wm.mu.Unlock()
	wm.logger.Printf("WorkoutManager: Max HR set to %d bpm", maxHR)
}

// GetMaxHR returns the current max HR value
func (wm *WorkoutManager) GetMaxHR() int16 {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.maxHR
}

// SetWorkout loads a workout for execution
func (wm *WorkoutManager) SetWorkout(workout *Workout) {
	wm.mu.Lock()

	// Can only set workout when idle or ready
	if wm.status == WorkoutStatusRunning || wm.status == WorkoutStatusPaused {
		wm.mu.Unlock()
		wm.logger.Printf("WorkoutManager: Cannot set workout while running or paused")
		return
	}

	wm.workout = workout
	wm.elapsedTime = 0
	wm.hrPID.reset()
	wm.lastBlockIdx = -1

	if workout != nil {
		wm.status = WorkoutStatusReady
		wm.logger.Printf("WorkoutManager: Workout '%s' loaded (duration: %v)", workout.Name, workout.TotalDuration())
	} else {
		wm.status = WorkoutStatusIdle
		wm.logger.Printf("WorkoutManager: Workout cleared")
	}

	state := wm.buildState()
	wm.mu.Unlock()

	// External call after releasing lock
	wm.model.SetWorkoutState(state)
}

// Start begins or resumes workout execution
func (wm *WorkoutManager) Start() {
	wm.mu.RLock()
	status := wm.status
	workout := wm.workout
	wm.mu.RUnlock()

	if workout == nil {
		wm.logger.Printf("WorkoutManager: No workout loaded")
		return
	}

	if status == WorkoutStatusRunning {
		wm.logger.Printf("WorkoutManager: Workout already running")
		return
	}

	if status != WorkoutStatusReady && status != WorkoutStatusPaused {
		wm.logger.Printf("WorkoutManager: Cannot start workout in current state")
		return
	}

	wm.logger.Printf("WorkoutManager: Starting workout")
	wm.cmdChan <- cmdStart
}

// Pause pauses the workout execution
func (wm *WorkoutManager) Pause() {
	wm.mu.RLock()
	status := wm.status
	wm.mu.RUnlock()

	if status != WorkoutStatusRunning {
		wm.logger.Printf("WorkoutManager: Cannot pause - workout not running")
		return
	}

	wm.logger.Printf("WorkoutManager: Pausing workout")
	wm.cmdChan <- cmdPause
}

// Stop stops the workout and resets state
func (wm *WorkoutManager) Stop() {
	wm.mu.RLock()
	status := wm.status
	wm.mu.RUnlock()

	if status == WorkoutStatusIdle {
		wm.logger.Printf("WorkoutManager: No workout to stop")
		return
	}

	wm.logger.Printf("WorkoutManager: Stopping workout")
	wm.cmdChan <- cmdStop
}

// Shutdown stops the workout manager and cleans up resources
// Safe to call multiple times - only the first call has effect
func (wm *WorkoutManager) Shutdown() {
	wm.shutdownOnce.Do(func() {
		wm.logger.Printf("WorkoutManager: Shutting down")
		close(wm.doneChan) // Signal goroutine to exit
		wm.wg.Wait()
		wm.logger.Printf("WorkoutManager: Shutdown complete")
	})
}

// --- Private Methods (no locks - caller must handle locking or only external calls) ---

// buildState computes the current workout state based on internal fields.
// MUST be called with mu held (at least read lock).
func (wm *WorkoutManager) buildState() WorkoutState {
	state := WorkoutState{
		Status:  wm.status,
		Workout: wm.workout,
	}

	if wm.workout == nil {
		return state
	}

	if wm.status == WorkoutStatusIdle {
		return state
	}

	state.ElapsedTime = wm.elapsedTime
	state.RemainingTime = wm.workout.TotalDuration() - wm.elapsedTime

	if len(wm.workout.Blocks) == 0 {
		return state
	}

	// Find current block and time within block
	var blockStartTime time.Duration
	for i, block := range wm.workout.Blocks {
		blockEndTime := blockStartTime + block.Duration
		if wm.elapsedTime < blockEndTime {
			state.CurrentBlockIdx = i
			state.BlockElapsedTime = wm.elapsedTime - blockStartTime
			state.BlockRemainingTime = blockEndTime - wm.elapsedTime

			// Calculate targets based on block mode
			if block.TargetMode == BlockTargetModeHeartRate {
				// HR mode: compute target heart rate
				state.TargetHeartRate = int16(block.TargetMaxHRMult * float64(wm.maxHR))
				// For HR mode, we use the PID output which is tracked separately
				// Set a placeholder - actual power is determined by PID controller
				state.TargetPowerWatts = int16(wm.hrPID.currentOutput)
			} else {
				// FTP mode: calculate interpolated FTP multiplier for ramps
				if block.Duration > 0 {
					progress := float64(state.BlockElapsedTime) / float64(block.Duration)
					state.CurrentTargetFTP = block.StartFTPMult + (block.EndFTPMult-block.StartFTPMult)*progress
				} else {
					state.CurrentTargetFTP = block.StartFTPMult
				}
				state.TargetPowerWatts = int16(state.CurrentTargetFTP * float64(wm.ftp))
				state.TargetHeartRate = 0
			}
			return state
		}
		blockStartTime = blockEndTime
	}

	// Past the end - use last block's end value
	lastBlock := wm.workout.Blocks[len(wm.workout.Blocks)-1]
	state.CurrentBlockIdx = len(wm.workout.Blocks) - 1
	state.CurrentTargetFTP = lastBlock.EndFTPMult
	state.TargetPowerWatts = int16(lastBlock.EndFTPMult * float64(wm.ftp))
	return state
}

// getCurrentBlock returns the current workout block, or nil if none.
// MUST be called with mu held.
func (wm *WorkoutManager) getCurrentBlock() *WorkoutBlock {
	if wm.workout == nil || len(wm.workout.Blocks) == 0 {
		return nil
	}

	var blockStartTime time.Duration
	for i, block := range wm.workout.Blocks {
		blockEndTime := blockStartTime + block.Duration
		if wm.elapsedTime < blockEndTime {
			return &wm.workout.Blocks[i]
		}
		blockStartTime = blockEndTime
	}

	return nil
}

// sendTargetPower sends the target power to the trainer via DeviceHandler.
// No lock needed - only makes external calls.
func (wm *WorkoutManager) sendTargetPower(watts int16) {
	// Get trainer address from control state
	trainerState := wm.model.GetTrainerControlState()
	if !trainerState.ControlAcquired {
		wm.logger.Printf("WorkoutManager: Cannot set power - trainer control not acquired")
		return
	}

	if trainerState.ConnectedAddress == "" {
		wm.logger.Printf("WorkoutManager: Cannot set power - no trainer connected")
		return
	}

	err := wm.deviceHandler.SetTargetPower(trainerState.ConnectedAddress, watts)
	if err != nil {
		wm.logger.Printf("WorkoutManager: Failed to set target power: %v", err)
		return
	}

	// Update model with new target power
	wm.model.SetTargetPower(watts)
	wm.logger.Printf("WorkoutManager: Set target power to %d W", watts)
}

// tickResult holds the result of processing a timer tick
type tickResult struct {
	state        WorkoutState
	skip         bool          // status wasn't running, skip this tick
	completed    bool          // workout just completed
	block        *WorkoutBlock // current block (nil if none)
	blockIdx     int           // current block index
	ftp          int16         // FTP value
	maxHR        int16         // Max HR value
	pidOutput    float64       // PID output power (for HR mode)
	blockChanged bool          // true if we moved to a new block
}

// handleTick processes a timer tick under lock and returns what actions to take.
// Uses defer for safe lock management.
func (wm *WorkoutManager) handleTick(currentHR float64) tickResult {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wm.status != WorkoutStatusRunning {
		return tickResult{skip: true}
	}

	wm.elapsedTime += 1 * time.Second

	totalDuration := wm.workout.TotalDuration()
	if wm.elapsedTime >= totalDuration {
		wm.elapsedTime = totalDuration
		wm.status = WorkoutStatusIdle
		return tickResult{state: wm.buildState(), completed: true}
	}

	state := wm.buildState()
	block := wm.getCurrentBlock()

	// Check if block changed
	blockChanged := state.CurrentBlockIdx != wm.lastBlockIdx
	if blockChanged {
		wm.logger.Printf("WorkoutManager: Moved to block %d", state.CurrentBlockIdx)
		wm.hrPID.reset() // Reset PID when entering new block
		wm.lastBlockIdx = state.CurrentBlockIdx
	}

	result := tickResult{
		state:        state,
		block:        block,
		blockIdx:     state.CurrentBlockIdx,
		ftp:          wm.ftp,
		maxHR:        wm.maxHR,
		blockChanged: blockChanged,
	}

	// If this is a heart rate mode block, run the PID controller
	if block != nil && block.TargetMode == BlockTargetModeHeartRate {
		targetHR := block.TargetMaxHRMult * float64(wm.maxHR)
		maxPower := hrPidMaxFTPMult * float64(wm.ftp)
		result.pidOutput = wm.hrPID.update(targetHR, currentHR, maxPower)
		wm.logger.Printf("WorkoutManager: HR PID - target=%.0f, current=%.0f, output=%.0fW (max=%.0fW)",
			targetHR, currentHR, result.pidOutput, maxPower)
	}

	return result
}

// runWorkoutLoop is the main goroutine that manages workout execution.
func (wm *WorkoutManager) runWorkoutLoop() {
	defer wm.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	ticker.Stop() // Start stopped, will be started when workout starts

	var lastTargetPower int16 = -1

	for {
		select {
		case <-wm.doneChan:
			ticker.Stop()
			wm.logger.Printf("WorkoutManager: Goroutine exiting")
			return

		case cmd := <-wm.cmdChan:
			switch cmd {
			case cmdStart:
				state := func() WorkoutState {
					wm.mu.Lock()
					defer wm.mu.Unlock()
					wm.status = WorkoutStatusRunning
					wm.hrPID.reset()
					wm.lastBlockIdx = -1
					return wm.buildState()
				}()

				ticker.Reset(1 * time.Second)
				wm.model.SetWorkoutState(state)
				wm.logger.Printf("WorkoutManager: Workout started")

			case cmdPause:
				ticker.Stop()
				state := func() WorkoutState {
					wm.mu.Lock()
					defer wm.mu.Unlock()
					wm.status = WorkoutStatusPaused
					return wm.buildState()
				}()

				wm.model.SetWorkoutState(state)
				wm.logger.Printf("WorkoutManager: Workout paused")

			case cmdStop:
				ticker.Stop()
				state := func() WorkoutState {
					wm.mu.Lock()
					defer wm.mu.Unlock()
					wm.status = WorkoutStatusReady
					wm.elapsedTime = 0
					wm.hrPID.reset()
					wm.lastBlockIdx = -1
					return wm.buildState()
				}()

				lastTargetPower = -1
				wm.model.SetWorkoutState(state)
				wm.logger.Printf("WorkoutManager: Workout stopped and reset")
			}

		case <-ticker.C:
			// Get current heart rate from model (for HR mode blocks)
			currentHR := wm.model.GetLatestData()[MetricHeartRate]

			result := wm.handleTick(currentHR)

			if result.skip {
				continue
			}

			if result.completed {
				ticker.Stop()
				wm.model.SetWorkoutState(result.state)
				wm.logger.Printf("WorkoutManager: Workout complete!")
				continue
			}

			// Determine target power based on block mode
			var targetPower int16
			if result.block != nil && result.block.TargetMode == BlockTargetModeHeartRate {
				// HR mode: use PID output
				targetPower = int16(result.pidOutput)
			} else {
				// FTP mode: use FTP multiplier
				targetPower = int16(float64(result.ftp) * result.state.CurrentTargetFTP)
			}

			// Send power if changed
			if targetPower != lastTargetPower {
				wm.sendTargetPower(targetPower)
				lastTargetPower = targetPower
			}

			wm.model.SetWorkoutState(result.state)
		}
	}
}
