package trainer

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"
)

// BaseUIView contains the base logic shared by all UI implementations
type BaseUIView struct {
	uiViewImpl   UIViewImpl
	uiModel      *UIModel
	uiController *UIController
	context      context.Context
	cancelFunc   context.CancelFunc
	waitGroup    sync.WaitGroup
	logger       *log.Logger
}

// NewBaseUIViewArg holds the arguments for creating a new BaseUIView
type NewBaseUIViewArg struct {
	UIViewImpl   UIViewImpl
	UIModel      *UIModel
	UIController *UIController
	Logger       *log.Logger
}

// NewBaseUIView creates a new BaseUIView with the given implementation
func NewBaseUIView(args NewBaseUIViewArg) *BaseUIView {
	if args.Logger == nil {
		panic("BaseUIView: logger cannot be nil")
	}
	if args.UIViewImpl == nil {
		panic("BaseUIView: UIViewImpl cannot be nil")
	}
	if args.UIController == nil {
		panic("BaseUIView: UIController cannot be nil")
	}
	ctx, cancel := context.WithCancel(context.Background())

	base := &BaseUIView{
		uiViewImpl:   args.UIViewImpl,
		uiModel:      args.UIModel,
		uiController: args.UIController,
		context:      ctx,
		cancelFunc:   cancel,
		waitGroup:    sync.WaitGroup{},
		logger:       args.Logger,
	}

	// Initialize framework-specific widgets
	args.UIViewImpl.Initialize(args.UIController)

	// Set up keyboard handlers
	args.UIViewImpl.SetupKeyboardHandlers(args.UIController)

	// Set initial mode from model
	if args.UIModel != nil {
		args.UIViewImpl.SetMode(args.UIModel.GetUIState().Mode)
	}

	// Set up periodic resize check and initial display
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() { base.monitorLogResize() })
	base.updateLogDisplay()

	// Listen to device list changes from model using Events
	base.setupEventListeners()

	return base
}

func (base *BaseUIView) setupEventListeners() {
	// Listen to log messages from model
	logChan := make(chan string, 1)
	logUnregister := base.uiModel.ListenToLog(logChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer logUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case _, ok := <-logChan:
				if !ok {
					return
				}
				// When a new log arrives, update the display to show the tail
				base.updateLogDisplay()
			}
		}
	})

	// Listen to device list changes from model
	scanChan := make(chan UIDeviceModelByDeviceType, 1)
	scanUnregister := base.uiModel.ListenToScanDevices(scanChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer scanUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case scanDevicesByDeviceType, ok := <-scanChan:
				if !ok {
					return
				}
				// Update device lists for each device type
				for deviceTypeID, scanDevices := range scanDevicesByDeviceType {
					if scanDevices == nil {
						// Skip nil device lists
						continue
					}
					devicesAsText := make([]string, 0)
					for _, d := range scanDevices {
						devicesAsText = append(devicesAsText, formatScanDeviceName(d))
					}
					base.uiViewImpl.SetScanDeviceList(deviceTypeID, devicesAsText)
				}

				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	})

	// Listen to connected device by device type changes from model
	connectedChan := make(chan UIDeviceModelByDeviceType, 1)
	connectedUnregister := base.uiModel.ListenToConnectedDeviceByDeviceType(connectedChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer connectedUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case devices, ok := <-connectedChan:
				if !ok {
					return
				}
				base.uiViewImpl.SetConnectedDeviceByDeviceType(devices)
				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	})

	// Listen to close application event from model
	closeChan := make(chan struct{}, 1)
	closeUnregister := base.uiModel.ListenToCloseApplication(closeChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer closeUnregister()
		select {
		case <-base.context.Done():
			return
		case _, ok := <-closeChan:
			if !ok {
				return
			}
			// Stop the UI implementation
			base.uiViewImpl.Stop()
		}
	})

	// Listen to UI state changes from model
	uiStateChan := make(chan UIState, 1)
	uiStateUnregister := base.uiModel.ListenToUIState(uiStateChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer uiStateUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case state, ok := <-uiStateChan:
				if !ok {
					return
				}
				// Update the view's mode
				base.uiViewImpl.SetMode(state.Mode)
				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	})

	// Listen to latest data changes from model
	latestDataChan := make(chan MetricData, 1)
	latestDataUnregister := base.uiModel.ListenToLatestData(latestDataChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer latestDataUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case data, ok := <-latestDataChan:
				if !ok {
					return
				}
				// Update the view's data display
				base.uiViewImpl.UpdateLatestData(data)
				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	})

	// Listen to trainer control state changes from model
	trainerControlChan := make(chan TrainerControlState, 1)
	trainerControlUnregister := base.uiModel.ListenToTrainerControl(trainerControlChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer trainerControlUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case state, ok := <-trainerControlChan:
				if !ok {
					return
				}
				// Update the view's trainer control display
				base.uiViewImpl.UpdateTrainerControl(state)
				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	})

	// Listen to workout state changes from model
	workoutStateChan := make(chan WorkoutState, 1)
	workoutStateUnregister := base.uiModel.ListenToWorkoutState(workoutStateChan)
	base.waitGroup.Add(1)
	go_func_utils.SafeGo(base.logger, func() {
		defer base.waitGroup.Done()
		defer workoutStateUnregister()
		for {
			select {
			case <-base.context.Done():
				return
			case state, ok := <-workoutStateChan:
				if !ok {
					return
				}
				// Update the view's workout state display
				base.uiViewImpl.UpdateWorkoutState(state)
				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	})
}

func (base *BaseUIView) updateLogDisplay() {
	// Get the visible height of the log view
	height := base.uiViewImpl.GetLogViewHeight()
	if height <= 0 {
		return
	}

	// Get the tail of logs that fit in the visible area
	logLines := base.uiModel.GetLogTail(height)

	// Clear and update the log view
	base.uiViewImpl.ClearLogView()
	for _, line := range logLines {
		if err := base.uiViewImpl.WriteLogLine(line); err != nil {
			base.logger.Printf("BaseUIView: Error writing to log view: %v", err)
		}
	}
}

func (base *BaseUIView) monitorLogResize() {
	defer base.waitGroup.Done()
	var lastHeight int
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-base.context.Done():
			return
		case <-ticker.C:
			height := base.uiViewImpl.GetLogViewHeight()
			if height != lastHeight && height > 0 {
				lastHeight = height
				base.updateLogDisplay()
				if err := base.uiViewImpl.Draw(); err != nil {
					base.logger.Printf("BaseUIView: Error drawing: %v", err)
				}
			}
		}
	}
}

// Shutdown stops all goroutines and waits for them to finish
func (base *BaseUIView) Shutdown() {
	base.logger.Println("BaseUIView: Shutting down")
	base.cancelFunc()
	base.waitGroup.Wait()
	base.logger.Println("BaseUIView: Shutdown complete")
}

// Run starts the UI and blocks until it exits
func (base *BaseUIView) Run() error {
	return base.uiViewImpl.Run()
}

func formatScanDeviceName(device *UIDeviceModel) string {
	return fmt.Sprintf("%s (%s)", device.Name, device.Address)
}
