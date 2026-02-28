package trainer

import (
	"fmt"
	"log"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Page names for tview.Pages
const (
	pageDeviceManagement = "device_management"
	pageTrainerDashboard = "trainer_dashboard"
	pageWorkoutSelection = "workout_selection"
)

// CursesUIViewImpl implements UIViewImpl using tview (curses-based terminal UI)
type CursesUIViewImpl struct {
	logger      *log.Logger
	app         *tview.Application
	model       *UIModel
	currentMode UIMode

	// Root container that holds all pages
	pages *tview.Pages

	// Shared components (visible in all modes)
	logView  *tview.TextView
	mainFlex *tview.Flex // Main layout: mode content on left, logs on right

	// Device Management mode components
	deviceMgmtFlex          *tview.Flex
	scanDeviceLists         map[DeviceTypeID]*tview.List
	connectedDeviceTexts    map[DeviceTypeID]*tview.TextView // Per-device-type connected device display
	deviceMgmtTabWidgets    []*tview.Box

	// Trainer Dashboard mode components
	trainerDashboardFlex       *tview.Flex
	trainerDashboardTabWidgets []*tview.Box
	metricsPanel               *tview.TextView
	controlsPanel              *tview.TextView
	workoutPanel               *tview.TextView

	// Workout Selection mode components
	workoutSelectionFlex       *tview.Flex
	workoutSelectionTabWidgets []*tview.Box
	workoutList                *tview.List
	workoutDetailsPanel        *tview.TextView
	workouts                   []Workout // Available workouts
}

func NewCursesUIView(logger *log.Logger, app *tview.Application, model *UIModel) *CursesUIViewImpl {
	return &CursesUIViewImpl{
		logger:               logger,
		app:                  app,
		model:                model,
		currentMode:          UIModeDeviceManagement,
		scanDeviceLists:      make(map[DeviceTypeID]*tview.List),
		connectedDeviceTexts: make(map[DeviceTypeID]*tview.TextView),
	}
}

// Initialize sets up the tview widgets
func (ui *CursesUIViewImpl) Initialize(controller *UIController) {
	// Create shared log view
	// Note: Don't use SetChangedFunc with app.Draw() - it can cause hangs during shutdown
	// when the app has been stopped but log messages are still being written.
	// The BaseUIView's event listeners already call Draw() after updating content.
	ui.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	ui.logView.SetBorder(true).SetTitle(" Logs ")

	// Create pages container for mode switching
	ui.pages = tview.NewPages()

	// Initialize each mode
	ui.initDeviceManagementMode(controller)
	ui.initTrainerDashboardMode(controller)
	ui.initWorkoutSelectionMode(controller)

	// Add pages
	ui.pages.AddPage(pageDeviceManagement, ui.deviceMgmtFlex, true, true)
	ui.pages.AddPage(pageTrainerDashboard, ui.trainerDashboardFlex, true, false)
	ui.pages.AddPage(pageWorkoutSelection, ui.workoutSelectionFlex, true, false)

	// Create main layout: pages on left, logs on right
	ui.mainFlex = tview.NewFlex().
		AddItem(ui.pages, 0, 1, true).
		AddItem(ui.logView, 0, 1, false)

	// Set initial focus
	ui.setFocusForCurrentMode()
}

// initDeviceManagementMode sets up the Device Management mode UI
func (ui *CursesUIViewImpl) initDeviceManagementMode(controller *UIController) {
	// Create instructions box at the top
	instructionsText := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	instructionsText.SetText("[yellow]S[white] Toggle Scan  |  [yellow]Tab[white] Cycle Devices  |  [yellow]Enter[white] Connect  |  [yellow]D[white] Disconnect\n[yellow]1[white] Devices  |  [yellow]2[white] Workouts  |  [yellow]3[white] Dashboard")

	// Create a horizontal flex to hold each device type's column
	deviceTypesRowFlex := tview.NewFlex().SetDirection(tview.FlexColumn)

	for _, deviceType := range AllDeviceTypes {
		deviceType := deviceType // Capture loop variable

		// Create a vertical flex for this device type: scan list + connected device display
		deviceTypeColumnFlex := tview.NewFlex().SetDirection(tview.FlexRow)

		// Scan device list for this device type
		deviceTypeList := tview.NewList().
			ShowSecondaryText(false).
			SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
				ui.logger.Printf("UI: Device selected from %s list: index=%d, text=%s", deviceType.ID, index, mainText)
				scanDevicesByDeviceType := ui.model.GetScanDevices()
				devices, ok := scanDevicesByDeviceType[deviceType.ID]
				if !ok {
					ui.logger.Printf("UI: No devices found for device type %s", deviceType.ID)
					return
				}
				ui.logger.Printf("UI: Found %d devices for device type %s", len(devices), deviceType.ID)
				if index < len(devices) {
					selected := devices[index]
					ui.logger.Printf("UI: Connecting to %s (%s) for device type %s", selected.Name, selected.Address, deviceType.ID)
					controller.ScanDeviceSelected(deviceType.ID, selected)
				} else {
					ui.logger.Printf("UI: Index %d out of range (have %d devices)", index, len(devices))
				}
			})
		deviceTypeList.SetBorder(true).SetTitle(fmt.Sprintf(" %s ", deviceType.DisplayName))
		ui.scanDeviceLists[deviceType.ID] = deviceTypeList

		// Connected device text for this device type
		connectedText := tview.NewTextView().
			SetDynamicColors(true).
			SetTextAlign(tview.AlignLeft)
		connectedText.SetBorder(true).SetTitle(" Connected ")
		connectedText.SetText(" [gray]None[white]")
		ui.connectedDeviceTexts[deviceType.ID] = connectedText

		// Add to column: scan list takes most space, connected indicator is small
		deviceTypeColumnFlex.AddItem(deviceTypeList, 0, 4, true)
		deviceTypeColumnFlex.AddItem(connectedText, 3, 0, false)

		// Add column to row
		deviceTypesRowFlex.AddItem(deviceTypeColumnFlex, 0, 1, false)

		// Add scan list to tab widgets for focus cycling
		ui.deviceMgmtTabWidgets = append(ui.deviceMgmtTabWidgets, deviceTypeList.Box)
	}

	// Create device management layout: instructions at top, device types below
	ui.deviceMgmtFlex = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(instructionsText, 2, 0, false).
		AddItem(deviceTypesRowFlex, 0, 1, true)
}

// initTrainerDashboardMode sets up the Trainer Dashboard mode UI
func (ui *CursesUIViewImpl) initTrainerDashboardMode(controller *UIController) {
	// Create metrics panel for displaying live data
	ui.metricsPanel = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	ui.metricsPanel.SetBorder(true).SetTitle(" Metrics ")
	ui.updateMetricsDisplay(nil) // Initialize with no data

	// Create controls panel for trainer control
	ui.controlsPanel = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	ui.controlsPanel.SetBorder(true).SetTitle(" Controls ")
	ui.updateControlsDisplay(TrainerControlState{Mode: TrainerControlModeNone})

	// Create workout panel for displaying workout status
	ui.workoutPanel = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	ui.workoutPanel.SetBorder(true).SetTitle(" Workout ")
	ui.updateWorkoutDisplay(WorkoutState{Status: WorkoutStatusIdle})

	ui.trainerDashboardTabWidgets = append(ui.trainerDashboardTabWidgets, ui.metricsPanel.Box)
	ui.trainerDashboardTabWidgets = append(ui.trainerDashboardTabWidgets, ui.controlsPanel.Box)
	ui.trainerDashboardTabWidgets = append(ui.trainerDashboardTabWidgets, ui.workoutPanel.Box)

	// Create left column: metrics + controls stacked vertically
	leftColumn := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(ui.metricsPanel, 0, 2, true).
		AddItem(ui.controlsPanel, 0, 1, false)

	// Create trainer dashboard layout: left column + workout panel side by side
	ui.trainerDashboardFlex = tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(leftColumn, 0, 1, true).
		AddItem(ui.workoutPanel, 0, 1, false)
}

// initWorkoutSelectionMode sets up the Workout Selection mode UI
func (ui *CursesUIViewImpl) initWorkoutSelectionMode(controller *UIController) {
	// Create workout list for selecting workouts
	ui.workoutList = tview.NewList().
		ShowSecondaryText(true).
		SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
			ui.logger.Printf("UI: Workout selected: index=%d, name=%s", index, mainText)
			controller.OnWorkoutSelected(index)
		}).
		SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
			// Update details panel when selection changes
			ui.updateWorkoutDetailsDisplay(index)
		})
	ui.workoutList.SetBorder(true).SetTitle(" Workouts ")

	// Create workout details panel
	ui.workoutDetailsPanel = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	ui.workoutDetailsPanel.SetBorder(true).SetTitle(" Workout Details ")
	ui.updateWorkoutDetailsDisplay(-1) // Initialize with no selection

	ui.workoutSelectionTabWidgets = append(ui.workoutSelectionTabWidgets, ui.workoutList.Box)
	ui.workoutSelectionTabWidgets = append(ui.workoutSelectionTabWidgets, ui.workoutDetailsPanel.Box)

	// Create workout selection layout
	ui.workoutSelectionFlex = tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(ui.workoutList, 0, 1, true).
		AddItem(ui.workoutDetailsPanel, 0, 1, false)
}

// SetWorkoutList populates the workout selection list
func (ui *CursesUIViewImpl) SetWorkoutList(workouts []Workout) {
	ui.workouts = workouts
	ui.workoutList.Clear()

	for _, workout := range workouts {
		duration := workout.TotalDuration()
		durationStr := formatDuration(duration)
		ui.workoutList.AddItem(workout.Name, durationStr, 0, nil)
	}

	// Update details for first item if list is not empty
	if len(workouts) > 0 {
		ui.updateWorkoutDetailsDisplay(0)
	}
}

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes >= 60 {
		hours := minutes / 60
		mins := minutes % 60
		if mins > 0 {
			return fmt.Sprintf("%dh %dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%d min", minutes)
}

// updateWorkoutDetailsDisplay formats and displays the workout details
func (ui *CursesUIViewImpl) updateWorkoutDetailsDisplay(index int) {
	if ui.workoutDetailsPanel == nil {
		return
	}

	var text string

	if index < 0 || index >= len(ui.workouts) {
		text = "\n\n  [yellow]Workout Selection[white]\n\n"
		text += "  Select a workout from the list to view details.\n\n"
		text += "  [gray]Press Enter to start the selected workout.[white]\n"
	} else {
		workout := ui.workouts[index]
		text = "\n"
		text += fmt.Sprintf("  [yellow]%s[white]\n\n", workout.Name)
		text += fmt.Sprintf("  [gray]Duration:[white] %s\n", formatDuration(workout.TotalDuration()))
		text += fmt.Sprintf("  [gray]Blocks:[white] %d\n\n", len(workout.Blocks))

		// Show block breakdown
		text += "  [gray]Structure:[white]\n"
		for i, block := range workout.Blocks {
			blockDur := formatDuration(block.Duration)
			if block.StartFTPMult == block.EndFTPMult {
				text += fmt.Sprintf("    %d. %.0f%% FTP for %s\n", i+1, block.StartFTPMult*100, blockDur)
			} else {
				text += fmt.Sprintf("    %d. %.0f%% → %.0f%% FTP for %s\n", i+1, block.StartFTPMult*100, block.EndFTPMult*100, blockDur)
			}
		}
		text += "\n  [green]Press Enter to start this workout[white]\n"
	}

	ui.workoutDetailsPanel.SetText(text)
}

// SetMode switches the UI to the specified mode
func (ui *CursesUIViewImpl) SetMode(mode UIMode) {
	if ui.currentMode == mode {
		return
	}

	ui.currentMode = mode

	switch mode {
	case UIModeDeviceManagement:
		ui.pages.SwitchToPage(pageDeviceManagement)
	case UIModeTrainerDashboard:
		ui.pages.SwitchToPage(pageTrainerDashboard)
	case UIModeWorkoutSelection:
		ui.pages.SwitchToPage(pageWorkoutSelection)
	}

	ui.setFocusForCurrentMode()
	ui.app.Draw()
}

// GetCurrentMode returns the currently active UI mode
func (ui *CursesUIViewImpl) GetCurrentMode() UIMode {
	return ui.currentMode
}

// setFocusForCurrentMode sets focus to the first widget in the current mode
func (ui *CursesUIViewImpl) setFocusForCurrentMode() {
	var widgets []*tview.Box
	switch ui.currentMode {
	case UIModeDeviceManagement:
		widgets = ui.deviceMgmtTabWidgets
	case UIModeTrainerDashboard:
		widgets = ui.trainerDashboardTabWidgets
	case UIModeWorkoutSelection:
		widgets = ui.workoutSelectionTabWidgets
	}

	if len(widgets) > 0 {
		ui.app.SetFocus(widgets[0])
	}
}

// getTabWidgetsForCurrentMode returns the tab widgets for the current mode
func (ui *CursesUIViewImpl) getTabWidgetsForCurrentMode() []*tview.Box {
	switch ui.currentMode {
	case UIModeDeviceManagement:
		return ui.deviceMgmtTabWidgets
	case UIModeTrainerDashboard:
		return ui.trainerDashboardTabWidgets
	case UIModeWorkoutSelection:
		return ui.workoutSelectionTabWidgets
	default:
		return nil
	}
}

// getFocusedDeviceTypeID returns the DeviceTypeID of the currently focused device type list, or empty string if none
func (ui *CursesUIViewImpl) getFocusedDeviceTypeID() DeviceTypeID {
	for deviceTypeID, list := range ui.scanDeviceLists {
		if list.Box.HasFocus() {
			return deviceTypeID
		}
	}
	return ""
}

// SetupKeyboardHandlers sets up keyboard event handlers
func (ui *CursesUIViewImpl) SetupKeyboardHandlers(controller *UIController) {
	ui.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Number keys for mode switching (1-9)
		if event.Key() == tcell.KeyRune {
			if mode, ok := GetUIModeByKey(event.Rune()); ok {
				// Delegate to controller - it will update the model, which will notify us
				controller.OnModeChange(mode)
				return nil
			}
		}

		// Tab to switch focus between widgets in current mode
		if event.Key() == tcell.KeyTab {
			widgets := ui.getTabWidgetsForCurrentMode()
			widgetCount := len(widgets)
			if widgetCount > 0 {
				for i := 0; i < widgetCount+1; i++ {
					idx := i % widgetCount
					if widgets[idx].HasFocus() {
						nextIdx := (idx + 1) % widgetCount
						ui.app.SetFocus(widgets[nextIdx])
						break
					}
				}
			}
			return nil
		}

		// Escape to quit
		if event.Key() == tcell.KeyEscape {
			controller.OnEscapeKey()
			return nil
		}

		// Mode-specific key handlers
		switch ui.currentMode {
		case UIModeDeviceManagement:
			// 's' key to toggle scanning (only in device management mode)
			if event.Key() == tcell.KeyRune && event.Rune() == 's' {
				controller.ToggleDeviceScan()
				return nil
			}
			// 'd' key to disconnect the device for the focused device type
			if event.Key() == tcell.KeyRune && event.Rune() == 'd' {
				if deviceTypeID := ui.getFocusedDeviceTypeID(); deviceTypeID != "" {
					controller.DisconnectDeviceForDeviceType(deviceTypeID)
				}
				return nil
			}
		case UIModeTrainerDashboard:
			// '+' or '=' or Up arrow to increase power
			if event.Key() == tcell.KeyRune && (event.Rune() == '+' || event.Rune() == '=') {
				controller.IncreaseTargetPower()
				return nil
			}
			if event.Key() == tcell.KeyUp {
				controller.IncreaseTargetPower()
				return nil
			}
			// '-' or Down arrow to decrease power
			if event.Key() == tcell.KeyRune && event.Rune() == '-' {
				controller.DecreaseTargetPower()
				return nil
			}
			if event.Key() == tcell.KeyDown {
				controller.DecreaseTargetPower()
				return nil
			}
			// Space to start/pause workout
			if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
				controller.ToggleWorkout()
				return nil
			}
			// 'x' to stop workout
			if event.Key() == tcell.KeyRune && event.Rune() == 'x' {
				controller.StopWorkout()
				return nil
			}
		}

		return event
	})
}

// GetLogViewHeight returns the visible height of the log view
func (ui *CursesUIViewImpl) GetLogViewHeight() int {
	_, _, _, height := ui.logView.GetInnerRect()
	return height
}

// ClearLogView clears the log view
func (ui *CursesUIViewImpl) ClearLogView() {
	ui.logView.Clear()
}

// WriteLogLine writes a line to the log view
func (ui *CursesUIViewImpl) WriteLogLine(line string) error {
	_, err := fmt.Fprint(ui.logView, line)
	return err
}

// SetScanDeviceList updates the device list for a specific device type
func (ui *CursesUIViewImpl) SetScanDeviceList(deviceTypeID DeviceTypeID, devices []string) {
	deviceTypeList, ok := ui.scanDeviceLists[deviceTypeID]
	if !ok {
		return
	}

	currentSelectionIndex := deviceTypeList.GetCurrentItem()

	var currentSelectionText *string
	if currentSelectionIndex < deviceTypeList.GetItemCount() {
		main, _ := deviceTypeList.GetItemText(currentSelectionIndex)
		currentSelectionText = &main
	}

	deviceTypeList.Clear()

	selectedIdx := -1
	for i, dev := range devices {
		if currentSelectionText != nil && *currentSelectionText == dev {
			selectedIdx = i
		}
		deviceTypeList.AddItem(dev, "", 0, nil)
	}
	if selectedIdx > -1 {
		deviceTypeList.SetCurrentItem(selectedIdx)
	}
}

// SetConnectedDeviceByDeviceType updates the connected device display for each device type
func (ui *CursesUIViewImpl) SetConnectedDeviceByDeviceType(devices UIDeviceModelByDeviceType) {
	// Update each device type's connected device text
	for deviceTypeID, textView := range ui.connectedDeviceTexts {
		if deviceList, ok := devices[deviceTypeID]; ok && len(deviceList) > 0 {
			device := deviceList[0]
			textView.SetText(fmt.Sprintf(" [green]●[white] %s", device.Name))
		} else {
			textView.SetText(" [gray]None[white]")
		}
	}
}

// Draw refreshes/redraws the UI
func (ui *CursesUIViewImpl) Draw() error {
	ui.app.Draw()
	return nil
}

// Run starts the UI and blocks until it exits
func (ui *CursesUIViewImpl) Run() error {
	// SetRoot must be called before setting focus, otherwise focus may be reset
	ui.app.SetRoot(ui.mainFlex, true)
	ui.setFocusForCurrentMode()
	return ui.app.Run()
}

// Stop stops the UI framework
func (ui *CursesUIViewImpl) Stop() {
	ui.app.Stop()
}

// UpdateLatestData updates the metrics display with the latest data
func (ui *CursesUIViewImpl) UpdateLatestData(data MetricData) {
	ui.updateMetricsDisplay(data)
}

// updateMetricsDisplay formats and displays the latest data in the metrics panel
func (ui *CursesUIViewImpl) updateMetricsDisplay(data MetricData) {
	if ui.metricsPanel == nil {
		return
	}

	var text string

	if data == nil || len(data) == 0 {
		text = "\n\n  [yellow]Trainer Dashboard[white]\n\n  Connect a device in Device Management mode (press 1)\n  to see live metrics here."
	} else {
		text = "\n"

		// Heart Rate
		if hr, ok := data[MetricHeartRate]; ok {
			text += fmt.Sprintf("  [red]♥[white] Heart Rate:    [yellow]%.0f[white] bpm\n\n", hr)
		}

		// Power
		if power, ok := data[MetricInstantaneousPower]; ok {
			text += fmt.Sprintf("  [blue]⚡[white] Power:         [yellow]%.0f[white] W\n\n", power)
		}

		// Average Power
		if avgPower, ok := data[MetricAveragePower]; ok {
			text += fmt.Sprintf("  [blue]⚡[white] Avg Power:     [yellow]%.0f[white] W\n\n", avgPower)
		}

		// Speed
		if speed, ok := data[MetricInstantaneousSpeed]; ok {
			text += fmt.Sprintf("  [green]→[white] Speed:         [yellow]%.1f[white] km/h\n\n", speed)
		}

		// Cadence
		if cadence, ok := data[MetricInstantaneousCadence]; ok {
			text += fmt.Sprintf("  [cyan]↻[white] Cadence:       [yellow]%.0f[white] rpm\n\n", cadence)
		}

		// Distance
		if distance, ok := data[MetricTotalDistance]; ok {
			// Convert meters to km if > 1000m
			if distance >= 1000 {
				text += fmt.Sprintf("  [purple]📏[white] Distance:      [yellow]%.2f[white] km\n\n", distance/1000)
			} else {
				text += fmt.Sprintf("  [purple]📏[white] Distance:      [yellow]%.0f[white] m\n\n", distance)
			}
		}

		// Elapsed Time
		if elapsed, ok := data[MetricElapsedTime]; ok {
			minutes := int(elapsed) / 60
			seconds := int(elapsed) % 60
			text += fmt.Sprintf("  [white]⏱[white] Elapsed:       [yellow]%02d:%02d[white]\n\n", minutes, seconds)
		}

		if text == "\n" {
			text = "\n\n  [gray]Waiting for data...[white]"
		}
	}

	ui.metricsPanel.SetText(text)
}

// UpdateTrainerControl updates the trainer control display
func (ui *CursesUIViewImpl) UpdateTrainerControl(state TrainerControlState) {
	ui.updateControlsDisplay(state)
}

// updateControlsDisplay formats and displays the trainer control state
func (ui *CursesUIViewImpl) updateControlsDisplay(state TrainerControlState) {
	if ui.controlsPanel == nil {
		return
	}

	var text string

	if !state.ControlAcquired {
		text = "\n  [gray]No trainer control active[white]\n\n"
		text += "  Connect a Smart Trainer in Device Management mode (press 1)\n"
		text += "  to acquire control automatically.\n"
	} else {
		text = "\n"

		// Show control mode
		switch state.Mode {
		case TrainerControlModeERG:
			text += "  [green]●[white] Mode: [yellow]ERG (Target Power)[white]\n\n"
			text += fmt.Sprintf("  [blue]⚡[white] Target Power:  [yellow]%d[white] W\n\n", state.TargetPowerWatts)
		case TrainerControlModeResistance:
			text += "  [green]●[white] Mode: [yellow]Resistance[white]\n\n"
			text += fmt.Sprintf("  [blue]⚙[white] Resistance:    [yellow]%.1f[white]\n\n", float64(state.ResistanceLevel)/10.0)
		default:
			text += "  [green]●[white] Control Active\n\n"
		}

		text += "  [gray]Controls:[white]\n"
		text += "  [yellow]+[white]/[yellow]↑[white] Increase power    [yellow]-[white]/[yellow]↓[white] Decrease power\n"
	}

	ui.controlsPanel.SetText(text)
}

// UpdateWorkoutState updates the workout status display
func (ui *CursesUIViewImpl) UpdateWorkoutState(state WorkoutState) {
	ui.updateWorkoutDisplay(state)
}

// updateWorkoutDisplay formats and displays the workout state
func (ui *CursesUIViewImpl) updateWorkoutDisplay(state WorkoutState) {
	if ui.workoutPanel == nil {
		return
	}

	var text string

	switch state.Status {
	case WorkoutStatusIdle:
		text = "\n  [gray]No workout loaded[white]\n\n"
		text += "  Go to Workout Selection (press 2) to load a workout.\n"

	case WorkoutStatusReady:
		if state.Workout != nil {
			text = "\n"
			text += fmt.Sprintf("  [yellow]%s[white]\n\n", state.Workout.Name)
			text += fmt.Sprintf("  [gray]Duration:[white] %s\n\n", formatDuration(state.Workout.TotalDuration()))
			text += "  [green]Ready to start[white]\n\n"
			text += "  [gray]Press[white] [yellow]Space[white] [gray]to start[white]\n"
		}

	case WorkoutStatusPaused:
		text = ui.formatActiveWorkoutDisplay(state, true)

	case WorkoutStatusRunning:
		text = ui.formatActiveWorkoutDisplay(state, false)
	}

	ui.workoutPanel.SetText(text)
}

// formatActiveWorkoutDisplay formats the display for a running or paused workout
func (ui *CursesUIViewImpl) formatActiveWorkoutDisplay(state WorkoutState, paused bool) string {
	if state.Workout == nil {
		return "\n  [gray]No workout data[white]\n"
	}

	var text string
	text = "\n"

	// Workout name and status
	if paused {
		text += fmt.Sprintf("  [yellow]%s[white] [gray](PAUSED)[white]\n\n", state.Workout.Name)
	} else {
		text += fmt.Sprintf("  [yellow]%s[white]\n\n", state.Workout.Name)
	}

	// Overall timing
	text += fmt.Sprintf("  [gray]Elapsed:[white]   %s\n", formatDurationMMSS(state.ElapsedTime))
	text += fmt.Sprintf("  [gray]Remaining:[white] %s\n\n", formatDurationMMSS(state.RemainingTime))

	// Current block info
	if len(state.Workout.Blocks) > 0 && state.CurrentBlockIdx < len(state.Workout.Blocks) {
		currentBlock := state.Workout.Blocks[state.CurrentBlockIdx]

		text += fmt.Sprintf("  [cyan]Current Block[white] (%d/%d)\n", state.CurrentBlockIdx+1, len(state.Workout.Blocks))
		text += fmt.Sprintf("  [gray]Block Time:[white] %s / %s\n", formatDurationMMSS(state.BlockElapsedTime), formatDurationMMSS(currentBlock.Duration))

		// Target based on block mode
		if currentBlock.TargetMode == BlockTargetModeHeartRate {
			text += fmt.Sprintf("  [red]♥[white] Target HR:    [yellow]%d[white] bpm\n", state.TargetHeartRate)
			text += fmt.Sprintf("  [blue]⚡[white] Target Power: [yellow]%d[white] W [gray](auto)[white]\n", state.TargetPowerWatts)
		} else {
			// FTP mode
			text += fmt.Sprintf("  [blue]⚡[white] Target Power: [yellow]%d[white] W", state.TargetPowerWatts)
			if currentBlock.StartFTPMult != currentBlock.EndFTPMult {
				text += fmt.Sprintf(" [gray](%.0f%% → %.0f%% FTP)[white]", currentBlock.StartFTPMult*100, currentBlock.EndFTPMult*100)
			} else {
				text += fmt.Sprintf(" [gray](%.0f%% FTP)[white]", currentBlock.StartFTPMult*100)
			}
			text += "\n"
		}

		// Target cadence (if set)
		if currentBlock.TargetCadence > 0 {
			text += fmt.Sprintf("  [green]⟳[white] Target Cadence: [yellow]%d[white] rpm\n", currentBlock.TargetCadence)
		}

		// Next block info
		nextBlockIdx := state.CurrentBlockIdx + 1
		if nextBlockIdx < len(state.Workout.Blocks) {
			nextBlock := state.Workout.Blocks[nextBlockIdx]
			text += "\n  [gray]Next Block:[white]\n"
			if nextBlock.TargetMode == BlockTargetModeHeartRate {
				text += fmt.Sprintf("    [gray]%.0f%% Max HR for %s[white]\n", nextBlock.TargetMaxHRMult*100, formatDuration(nextBlock.Duration))
			} else if nextBlock.StartFTPMult == nextBlock.EndFTPMult {
				text += fmt.Sprintf("    [gray]%.0f%% FTP for %s[white]\n", nextBlock.StartFTPMult*100, formatDuration(nextBlock.Duration))
			} else {
				text += fmt.Sprintf("    [gray]%.0f%% → %.0f%% FTP for %s[white]\n", nextBlock.StartFTPMult*100, nextBlock.EndFTPMult*100, formatDuration(nextBlock.Duration))
			}
		} else {
			text += "\n  [gray]Next Block:[white] [green]Finish![white]\n"
		}
	}

	// Controls hint
	text += "\n  [gray]─────────────────────────[white]\n"
	if paused {
		text += "  [yellow]Space[white] Resume  |  [yellow]X[white] Stop\n"
	} else {
		text += "  [yellow]Space[white] Pause  |  [yellow]X[white] Stop\n"
	}

	return text
}

// formatDurationMMSS formats a duration as MM:SS
func formatDurationMMSS(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
