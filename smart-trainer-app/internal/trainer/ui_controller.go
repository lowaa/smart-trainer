package trainer

import (
	"context"
	"log"
	"sync"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"
)

// UIController handles UI events and coordinates with the UIModel
type UIController struct {
	model          *UIModel
	deviceHandler  *DeviceHandler
	workoutManager *WorkoutManager
	logger         *log.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

// NewUIController creates a new UIController with the given dependencies
func NewUIController(model *UIModel, deviceHandler *DeviceHandler, workoutManager *WorkoutManager, logger *log.Logger) *UIController {
	if model == nil {
		panic("UIController: model cannot be nil")
	}
	if deviceHandler == nil {
		panic("UIController: deviceHandler cannot be nil")
	}
	if workoutManager == nil {
		panic("UIController: workoutManager cannot be nil")
	}
	if logger == nil {
		panic("UIController: logger cannot be nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &UIController{
		model:          model,
		deviceHandler:  deviceHandler,
		workoutManager: workoutManager,
		logger:         logger,
		ctx:            ctx,
		cancel:         cancel,
	}

	c.wg.Add(1)
	go_func_utils.SafeGo(logger, func() { c.listenToAutoConnect() })

	return c
}

func (c *UIController) listenToAutoConnect() {
	defer c.wg.Done()

	ch := make(chan AutoConnectRequest, 1)
	unregister := c.model.ListenToAutoConnect(ch)
	defer unregister()

	for {
		select {
		case <-c.ctx.Done():
			return
		case req, ok := <-ch:
			if !ok {
				return
			}
			c.logger.Printf("Auto-connecting %s (%s) from persistence", req.Device.Address, req.DeviceTypeID)
			c.ScanDeviceSelected(req.DeviceTypeID, req.Device)
		}
	}
}

// ScanDeviceSelected handles when a scan device is selected from the UI
// deviceTypeID identifies which device type the device was selected from
func (c *UIController) ScanDeviceSelected(deviceTypeID DeviceTypeID, uiDeviceModel *UIDeviceModel) {
	err := c.deviceHandler.ConnectAndSubscribe(deviceTypeID, uiDeviceModel.Address)
	if err != nil {
		c.logger.Printf("Connection failed: %v", err)
		return
	}
	// Set the connected device for this device type in the model
	c.model.SetConnectedDeviceForDeviceType(deviceTypeID, uiDeviceModel)
}

// DisconnectDeviceForDeviceType unsubscribes a device from a specific device type.
// If this was the last device type using the device, the device will be disconnected.
func (c *UIController) DisconnectDeviceForDeviceType(deviceTypeID DeviceTypeID) {
	device := c.model.GetConnectedDeviceForDeviceType(deviceTypeID)
	if device == nil {
		c.logger.Printf("No device connected for device type %s", deviceTypeID)
		return
	}

	// Unsubscribe from the device type (may or may not disconnect device)
	disconnected, err := c.deviceHandler.UnsubscribeDeviceType(deviceTypeID, device.Address)
	if err != nil {
		c.logger.Printf("Unsubscribe failed: %v", err)
		return
	}

	// Clear the device type assignment in the model
	c.model.ClearConnectedDeviceForDeviceType(deviceTypeID)

	if disconnected {
		c.logger.Printf("Device %s disconnected (no device types remaining)", device.Address)
	} else {
		c.logger.Printf("Device type %s unsubscribed from %s (device still connected for other device types)", deviceTypeID, device.Address)
	}
}

// OnEscapeKey handles when the Escape key is pressed
func (c *UIController) OnEscapeKey() {
	c.model.RequestCloseApplication()
}

func (c *UIController) StartDeviceScan() {
	if c.deviceHandler.IsScanning() {
		c.logger.Printf("already scanning")
		return
	}
	c.deviceHandler.StartScan()
}

func (c *UIController) StopDeviceScan() {
	if !c.deviceHandler.IsScanning() {
		c.logger.Printf("already not scanning")
		return
	}
	err := c.deviceHandler.StopScan()
	if err != nil {
		c.logger.Printf("error stopping scan: %v", err)
	}
}

func (c *UIController) ToggleDeviceScan() {
	if c.deviceHandler.IsScanning() {
		c.StopDeviceScan()
	} else {
		c.StartDeviceScan()
	}
}

// OnModeChange handles when the user requests a mode change
func (c *UIController) OnModeChange(mode UIMode) {
	if info, ok := GetUIModeInfo(mode); ok {
		c.logger.Printf("Switching to %s mode", info.DisplayName)
	}
	// We want to scan whenever we are in device mgmt mode
	if mode == UIModeDeviceManagement {
		c.StartDeviceScan()
	} else {
		c.StopDeviceScan()
	}
	c.model.SetMode(mode)
}

// --- Workout Selection Methods ---

// OnWorkoutSelected handles when a workout is selected from the list
func (c *UIController) OnWorkoutSelected(index int) {
	if index < 0 || index >= len(AllWorkouts) {
		c.logger.Printf("Invalid workout index: %d", index)
		return
	}

	workout := &AllWorkouts[index]
	c.logger.Printf("Workout selected: %s", workout.Name)
	c.workoutManager.SetWorkout(workout)
}

// StartWorkout starts or resumes the loaded workout
func (c *UIController) StartWorkout() {
	c.workoutManager.Start()
}

// PauseWorkout pauses the running workout
func (c *UIController) PauseWorkout() {
	c.workoutManager.Pause()
}

// StopWorkout stops the workout and resets to ready state
func (c *UIController) StopWorkout() {
	c.workoutManager.Stop()
}

// ToggleWorkout starts, pauses, or resumes the workout based on current state
func (c *UIController) ToggleWorkout() {
	state := c.model.GetWorkoutState()
	switch state.Status {
	case WorkoutStatusReady, WorkoutStatusPaused:
		c.workoutManager.Start()
	case WorkoutStatusRunning:
		c.workoutManager.Pause()
	default:
		c.logger.Printf("No workout loaded - select one in Workout Selection mode (press 2)")
	}
}

// --- Trainer Control Methods ---

// IncreaseTargetPower increases the target power by the default step
func (c *UIController) IncreaseTargetPower() {
	state := c.model.GetTrainerControlState()
	if !state.ControlAcquired {
		c.logger.Printf("No trainer control - press 'c' to acquire")
		return
	}

	newPower := state.TargetPowerWatts + DefaultPowerStepWatts
	if newPower > MaxTargetPowerWatts {
		newPower = MaxTargetPowerWatts
	}

	c.setTargetPower(newPower)
}

// DecreaseTargetPower decreases the target power by the default step
func (c *UIController) DecreaseTargetPower() {
	state := c.model.GetTrainerControlState()
	if !state.ControlAcquired {
		c.logger.Printf("No trainer control - press 'c' to acquire")
		return
	}

	newPower := state.TargetPowerWatts - DefaultPowerStepWatts
	if newPower < MinTargetPowerWatts {
		newPower = MinTargetPowerWatts
	}

	c.setTargetPower(newPower)
}

// setTargetPower sets the target power on the trainer and updates the model
func (c *UIController) setTargetPower(watts int16) {
	state := c.model.GetTrainerControlState()
	if state.ConnectedAddress == "" {
		c.logger.Printf("No trainer connected")
		return
	}

	err := c.deviceHandler.SetTargetPower(state.ConnectedAddress, watts)
	if err != nil {
		c.logger.Printf("Failed to set power: %v", err)
		return
	}

	c.model.SetTargetPower(watts)
	c.logger.Printf("Target power: %d W", watts)
}

// Shutdown stops the workout manager, device handler and cleans up resources
func (c *UIController) Shutdown() {
	c.cancel()
	c.wg.Wait()
	c.workoutManager.Shutdown()
	c.deviceHandler.Shutdown()
}
