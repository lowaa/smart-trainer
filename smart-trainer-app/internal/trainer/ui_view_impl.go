package trainer

// UIViewImpl defines the interface for framework-specific UI implementations
type UIViewImpl interface {
	// Initialize is called after construction to set up framework-specific widgets
	// controller is used to handle UI events
	Initialize(controller *UIController)

	// SetupKeyboardHandlers sets up keyboard event handlers
	// controller is used to handle keyboard events
	SetupKeyboardHandlers(controller *UIController)

	// Run starts the UI framework and blocks until it exits
	Run() error

	// Stop stops the UI framework
	Stop()

	// Draw refreshes/redraws the UI
	Draw() error

	// --- Mode Management ---

	// SetMode switches the UI to the specified mode
	SetMode(mode UIMode)

	// GetCurrentMode returns the currently active UI mode
	GetCurrentMode() UIMode

	// --- Log View (shared across modes) ---

	// GetLogViewHeight returns the visible height of the log view
	GetLogViewHeight() int

	// ClearLogView clears the log view
	ClearLogView()

	// WriteLogLine writes a line to the log view
	WriteLogLine(line string) error

	// --- Device Management Mode ---

	// SetScanDeviceList updates the scan device list for a specific device type
	SetScanDeviceList(deviceTypeID DeviceTypeID, items []string)

	// SetConnectedDeviceByDeviceType updates the connected device display for each device type
	SetConnectedDeviceByDeviceType(devices UIDeviceModelByDeviceType)

	// --- Trainer Dashboard Mode ---

	// UpdateLatestData updates the data display in the trainer dashboard
	UpdateLatestData(data MetricData)

	// UpdateTrainerControl updates the trainer control display
	UpdateTrainerControl(state TrainerControlState)

	// UpdateWorkoutState updates the workout status display in the trainer dashboard
	UpdateWorkoutState(state WorkoutState)

	// --- Workout Selection Mode ---

	// SetWorkoutList populates the workout selection list
	SetWorkoutList(workouts []Workout)
}
