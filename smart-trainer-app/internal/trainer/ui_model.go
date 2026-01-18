package trainer

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/bt"
	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/events"
	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"
)

type UIDeviceModel struct {
	Name                   string
	Address                string
	RSSI                   int16
	DeviceStateDescription string
}

// UIState holds the current state of the UI that views need to render
type UIState struct {
	Mode UIMode
}

type UIDeviceModelByDeviceType map[DeviceTypeID][]*UIDeviceModel
type btDevicesByDeviceType map[DeviceTypeID][]bt.BTDevice

// MetricData holds the most recent value for each metric
type MetricData map[MetricID]float64

type UIModel struct {
	logEvent                        *events.ChannelEvent[string]
	scanDevicesEvent                *events.ChannelEvent[UIDeviceModelByDeviceType]
	scanDevicesByDeviceType         btDevicesByDeviceType
	connectedDeviceByDeviceTypeEvent *events.ChannelEvent[UIDeviceModelByDeviceType]
	connectedDeviceByDeviceType     map[DeviceTypeID]*UIDeviceModel // User-assigned device per device type
	closeApplicationEvent           *events.ChannelEvent[struct{}]
	uiStateEvent                    *events.ChannelEvent[UIState]
	uiState                         UIState
	latestDataEvent                 *events.ChannelEvent[MetricData]
	latestData                      MetricData
	trainerControlEvent             *events.ChannelEvent[TrainerControlState]
	trainerControlState             TrainerControlState
	workoutStateEvent               *events.ChannelEvent[WorkoutState]
	workoutState                    WorkoutState
	logLines                        []string
	logMu                           sync.RWMutex
	mu                              sync.RWMutex
	ctx                             context.Context
	cancel                          context.CancelFunc
	wg                              sync.WaitGroup
	logger                          *log.Logger
}

const maxLogLines = 1000

func NewUIModel(manager bt.BTManagerInterface, logger *log.Logger, uiLogChan <-chan string) *UIModel {
	if logger == nil {
		panic("UIModel: logger cannot be nil")
	}
	if uiLogChan == nil {
		panic("UIModel: uiLogChan cannot be nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	model := &UIModel{
		logEvent:                         events.NewChannelEvent[string](false),
		scanDevicesEvent:                 events.NewChannelEvent[UIDeviceModelByDeviceType](true),
		connectedDeviceByDeviceTypeEvent: events.NewChannelEvent[UIDeviceModelByDeviceType](true),
		closeApplicationEvent:            events.NewChannelEvent[struct{}](true),
		uiStateEvent:                     events.NewChannelEvent[UIState](true),
		uiState:                          UIState{Mode: UIModeDeviceManagement},
		latestDataEvent:                  events.NewChannelEvent[MetricData](true),
		latestData:                       make(MetricData),
		trainerControlEvent:              events.NewChannelEvent[TrainerControlState](true),
		trainerControlState:              TrainerControlState{Mode: TrainerControlModeNone, TargetPowerWatts: 100},
		workoutStateEvent:                events.NewChannelEvent[WorkoutState](true),
		workoutState:                     WorkoutState{Status: WorkoutStatusIdle},
		scanDevicesByDeviceType:          make(btDevicesByDeviceType),
		connectedDeviceByDeviceType:      make(map[DeviceTypeID]*UIDeviceModel),
		logLines:                         make([]string, 0, maxLogLines),
		ctx:                              ctx,
		cancel:                           cancel,
		logger:                           logger,
	}

	// Listen to device list changes from BTManager
	model.wg.Add(1)
	go_func_utils.SafeGo(model.logger, func() { model.listenToScanDevices(ctx, manager) })

	// Listen to physical device connection changes from BTManager
	// This is used to clear stream assignments when devices disconnect
	model.wg.Add(1)
	go_func_utils.SafeGo(model.logger, func() { model.listenToPhysicalConnections(ctx, manager) })

	// Read from the UI log channel and populate logLines
	model.wg.Add(1)
	go_func_utils.SafeGo(model.logger, func() { model.readFromLogChannel(ctx, uiLogChan) })

	return model
}

// Shutdown stops all goroutines and waits for them to finish
func (m *UIModel) Shutdown() {
	m.logger.Println("UIModel: Shutting down")
	m.cancel()
	m.wg.Wait()
	m.logger.Println("UIModel: Shutdown complete")
}

// ListenToLog registers a channel to receive log messages
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToLog(ch chan<- string) func() {
	return m.logEvent.Listen(ch)
}

// ListenToScanDevices registers a channel to receive scan device list changes
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToScanDevices(ch chan<- UIDeviceModelByDeviceType) func() {
	return m.scanDevicesEvent.Listen(ch)
}

// ListenToConnectedDeviceByDeviceType registers a channel to receive connected device by device type changes
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToConnectedDeviceByDeviceType(ch chan<- UIDeviceModelByDeviceType) func() {
	return m.connectedDeviceByDeviceTypeEvent.Listen(ch)
}

// ListenToCloseApplication registers a channel to receive close application signals
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToCloseApplication(ch chan<- struct{}) func() {
	return m.closeApplicationEvent.Listen(ch)
}

// RequestCloseApplication signals that the application should close
func (m *UIModel) RequestCloseApplication() {
	m.closeApplicationEvent.Notify(struct{}{})
}

// ListenToUIState registers a channel to receive UI state changes
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToUIState(ch chan<- UIState) func() {
	return m.uiStateEvent.Listen(ch)
}

// GetUIState returns the current UI state
func (m *UIModel) GetUIState() UIState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.uiState
}

// SetMode updates the current UI mode and notifies listeners
func (m *UIModel) SetMode(mode UIMode) {
	m.mu.Lock()
	if m.uiState.Mode == mode {
		m.mu.Unlock()
		return
	}
	m.uiState.Mode = mode
	state := m.uiState
	m.mu.Unlock()

	m.uiStateEvent.Notify(state)
}

// ListenToLatestData registers a channel to receive latest data updates
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToLatestData(ch chan<- MetricData) func() {
	return m.latestDataEvent.Listen(ch)
}

// GetLatestData returns a copy of the current latest data map
func (m *UIModel) GetLatestData() MetricData {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy to avoid concurrent access issues
	result := make(MetricData)
	for k, v := range m.latestData {
		result[k] = v
	}
	return result
}

// SetMetric updates a single metric value and notifies listeners
func (m *UIModel) SetMetric(metricID MetricID, value float64) {
	m.mu.Lock()
	m.latestData[metricID] = value
	// Create a copy for notification
	dataCopy := make(MetricData)
	for k, v := range m.latestData {
		dataCopy[k] = v
	}
	m.mu.Unlock()

	m.latestDataEvent.Notify(dataCopy)
}

// SetMetrics updates multiple metric values and notifies listeners once
func (m *UIModel) SetMetrics(metrics MetricData) {
	if len(metrics) == 0 {
		return
	}

	m.mu.Lock()
	for k, v := range metrics {
		m.latestData[k] = v
	}
	// Create a copy for notification
	dataCopy := make(MetricData)
	for k, v := range m.latestData {
		dataCopy[k] = v
	}
	m.mu.Unlock()

	m.latestDataEvent.Notify(dataCopy)
}

// ListenToTrainerControl registers a channel to receive trainer control state updates
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToTrainerControl(ch chan<- TrainerControlState) func() {
	return m.trainerControlEvent.Listen(ch)
}

// GetTrainerControlState returns the current trainer control state
func (m *UIModel) GetTrainerControlState() TrainerControlState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trainerControlState
}

// SetTrainerControlState updates the trainer control state and notifies listeners
func (m *UIModel) SetTrainerControlState(state TrainerControlState) {
	m.mu.Lock()
	m.trainerControlState = state
	stateCopy := m.trainerControlState
	m.mu.Unlock()

	m.trainerControlEvent.Notify(stateCopy)
}

// SetTargetPower updates just the target power and notifies listeners
func (m *UIModel) SetTargetPower(watts int16) {
	m.mu.Lock()
	m.trainerControlState.TargetPowerWatts = watts
	stateCopy := m.trainerControlState
	m.mu.Unlock()

	m.trainerControlEvent.Notify(stateCopy)
}

// SetTrainerControlAcquired updates whether control has been acquired
func (m *UIModel) SetTrainerControlAcquired(acquired bool, address string) {
	m.mu.Lock()
	m.trainerControlState.ControlAcquired = acquired
	m.trainerControlState.ConnectedAddress = address
	if acquired && m.trainerControlState.Mode == TrainerControlModeNone {
		m.trainerControlState.Mode = TrainerControlModeERG
	}
	if !acquired {
		m.trainerControlState.Mode = TrainerControlModeNone
		m.trainerControlState.ConnectedAddress = ""
	}
	stateCopy := m.trainerControlState
	m.mu.Unlock()

	m.trainerControlEvent.Notify(stateCopy)
}

// ListenToWorkoutState registers a channel to receive workout state updates
// Returns a deregistration function that can be called to remove the listener
func (m *UIModel) ListenToWorkoutState(ch chan<- WorkoutState) func() {
	return m.workoutStateEvent.Listen(ch)
}

// GetWorkoutState returns the current workout state
func (m *UIModel) GetWorkoutState() WorkoutState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workoutState
}

// SetWorkoutState updates the workout state and notifies listeners
func (m *UIModel) SetWorkoutState(state WorkoutState) {
	m.mu.Lock()
	m.workoutState = state
	stateCopy := m.workoutState
	m.mu.Unlock()

	m.workoutStateEvent.Notify(stateCopy)
}

// GetScanDevices returns the current sorted scan device list
func (m *UIModel) GetScanDevices() UIDeviceModelByDeviceType {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return convertDevicesByDeviceTypeToUIDeviceModelByDeviceType(m.scanDevicesByDeviceType)
}

// GetConnectedDeviceByDeviceType returns a copy of the current connected device by device type map
func (m *UIModel) GetConnectedDeviceByDeviceType() UIDeviceModelByDeviceType {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(UIDeviceModelByDeviceType)
	for deviceTypeID, device := range m.connectedDeviceByDeviceType {
		if device != nil {
			// Return a slice with single device for compatibility with UIDeviceModelByDeviceType type
			result[deviceTypeID] = []*UIDeviceModel{device}
		}
	}
	return result
}

// GetConnectedDeviceForDeviceType returns the connected device for a specific device type
func (m *UIModel) GetConnectedDeviceForDeviceType(deviceTypeID DeviceTypeID) *UIDeviceModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connectedDeviceByDeviceType[deviceTypeID]
}

// SetConnectedDeviceForDeviceType sets the connected device for a specific device type and notifies listeners
func (m *UIModel) SetConnectedDeviceForDeviceType(deviceTypeID DeviceTypeID, device *UIDeviceModel) {
	m.mu.Lock()
	m.connectedDeviceByDeviceType[deviceTypeID] = device
	result := m.buildConnectedDeviceByDeviceTypeSnapshot()
	m.mu.Unlock()

	m.connectedDeviceByDeviceTypeEvent.Notify(result)
}

// ClearConnectedDeviceForDeviceType clears the connected device for a specific device type and notifies listeners
func (m *UIModel) ClearConnectedDeviceForDeviceType(deviceTypeID DeviceTypeID) {
	m.mu.Lock()
	delete(m.connectedDeviceByDeviceType, deviceTypeID)
	result := m.buildConnectedDeviceByDeviceTypeSnapshot()
	m.mu.Unlock()

	m.connectedDeviceByDeviceTypeEvent.Notify(result)
}

// buildConnectedDeviceByDeviceTypeSnapshot creates a snapshot of the connected devices map
// Must be called with mu held
func (m *UIModel) buildConnectedDeviceByDeviceTypeSnapshot() UIDeviceModelByDeviceType {
	result := make(UIDeviceModelByDeviceType)
	for deviceTypeID, device := range m.connectedDeviceByDeviceType {
		if device != nil {
			result[deviceTypeID] = []*UIDeviceModel{device}
		}
	}
	return result
}

// readFromLogChannel reads log lines from the channel and populates logLines
func (m *UIModel) readFromLogChannel(ctx context.Context, logChan <-chan string) {
	defer m.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-logChan:
			if !ok {
				// Channel closed
				return
			}

			// Store in log lines buffer (max 1000 lines)
			m.logMu.Lock()
			m.logLines = append(m.logLines, line)
			if len(m.logLines) > maxLogLines {
				// Remove oldest lines, keep the most recent maxLogLines
				m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
			}
			m.logMu.Unlock()

			// Notify listeners for immediate display
			m.logEvent.Notify(line)
		}
	}
}

// GetLogTail returns the last n lines of logs
func (m *UIModel) GetLogTail(n int) []string {
	m.logMu.RLock()
	defer m.logMu.RUnlock()

	if n <= 0 {
		return []string{}
	}

	if n >= len(m.logLines) {
		// Return all lines
		result := make([]string, len(m.logLines))
		copy(result, m.logLines)
		return result
	}

	// Return last n lines
	result := make([]string, n)
	copy(result, m.logLines[len(m.logLines)-n:])
	return result
}

// listenToScanDevices listens to BTManager's device list event and updates
// the internal sorted device list, then emits it to the view event
func (m *UIModel) listenToScanDevices(ctx context.Context, manager bt.BTManagerInterface) {
	defer m.wg.Done()

	// Create a channel to receive device updates
	deviceChan := make(chan []bt.BTDevice, 1)
	unregister := manager.ListenToDeviceList(deviceChan)
	defer unregister()

	for {
		select {
		case <-ctx.Done():
			return
		case devices, ok := <-deviceChan:
			if !ok {
				return
			}

			sortedBTDevices := *sortBTDevices(devices)

			devicesByDeviceType := make(btDevicesByDeviceType)

			// Group devices by device type based on scan service UUIDs
			for _, deviceType := range AllDeviceTypes {
				if _, ok := devicesByDeviceType[deviceType.ID]; !ok {
					devicesByDeviceType[deviceType.ID] = make([]bt.BTDevice, 0)
				}
				for _, btdevice := range sortedBTDevices {
					// Check if device has any of the scan service UUIDs for this device type
					for _, scanUUID := range deviceType.ScanServiceUUIDs {
						if btdevice.HasServiceUUID(scanUUID) {
							devicesByDeviceType[deviceType.ID] = append(devicesByDeviceType[deviceType.ID], btdevice)
							break // Don't add same device twice
						}
					}
				}
			}

			// Update internal copy
			m.mu.Lock()
			m.scanDevicesByDeviceType = devicesByDeviceType
			result := convertDevicesByDeviceTypeToUIDeviceModelByDeviceType(m.scanDevicesByDeviceType)
			m.mu.Unlock()

			// Notify listeners
			m.scanDevicesEvent.Notify(result)
		}
	}
}

// listenToPhysicalConnections listens to BTManager's connected devices event
// When a device disconnects physically, we clear any device type assignments for it
func (m *UIModel) listenToPhysicalConnections(ctx context.Context, manager bt.BTManagerInterface) {
	defer m.wg.Done()

	// Create a channel to receive device updates
	deviceChan := make(chan []bt.BTDevice, 1)
	unregister := manager.ListenToConnectedDevices(deviceChan)
	defer unregister()

	// Track previously connected device addresses
	prevConnectedAddresses := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return
		case devices, ok := <-deviceChan:
			if !ok {
				return
			}

			// Build current connected addresses set
			currentConnectedAddresses := make(map[string]bool)
			for _, dev := range devices {
				currentConnectedAddresses[dev.GetAddressString()] = true
			}

			// Find devices that disconnected
			disconnectedAddresses := make([]string, 0)
			for addr := range prevConnectedAddresses {
				if !currentConnectedAddresses[addr] {
					disconnectedAddresses = append(disconnectedAddresses, addr)
				}
			}

			// Clear device type assignments for disconnected devices
			if len(disconnectedAddresses) > 0 {
				m.mu.Lock()
				changed := false
				for deviceTypeID, device := range m.connectedDeviceByDeviceType {
					if device != nil {
						for _, addr := range disconnectedAddresses {
							if device.Address == addr {
								delete(m.connectedDeviceByDeviceType, deviceTypeID)
								changed = true
								m.logger.Printf("UIModel: Cleared device type %s assignment due to device disconnect: %s", deviceTypeID, addr)
							}
						}
					}
				}
				var result UIDeviceModelByDeviceType
				if changed {
					result = m.buildConnectedDeviceByDeviceTypeSnapshot()
				}
				m.mu.Unlock()

				// Notify listeners if assignments changed
				if changed {
					m.connectedDeviceByDeviceTypeEvent.Notify(result)
				}
			}

			// Update previous state
			prevConnectedAddresses = currentConnectedAddresses
		}
	}
}

func sortBTDevices(btDevices []bt.BTDevice) *[]bt.BTDevice {
	sortedBTDevice := make([]bt.BTDevice, len(btDevices))
	copy(sortedBTDevice, btDevices)
	sort.Slice(sortedBTDevice, func(i, j int) bool {
		return sortedBTDevice[i].GetAddressString() < sortedBTDevice[j].GetAddressString()
	})
	return &sortedBTDevice
}

func convertBTDevicesToUIModels(btDevices *[]bt.BTDevice) *[]*UIDeviceModel {
	uiDeviceModels := make([]*UIDeviceModel, 0)
	for _, btDevice := range *btDevices {

		rssi, err := btDevice.GetScanRSSI()
		if err != nil {
			rssi = 0
		}

		model := &UIDeviceModel{
			Name:    btDevice.GetLocalName(),
			Address: btDevice.GetAddressString(),
			RSSI:    rssi,
		}
		uiDeviceModels = append(uiDeviceModels, model)
	}
	return &uiDeviceModels
}

func convertDevicesByDeviceTypeToUIDeviceModelByDeviceType(devicesByDeviceType btDevicesByDeviceType) UIDeviceModelByDeviceType {
	result := make(UIDeviceModelByDeviceType)
	for deviceTypeID, btDeviceSlice := range devicesByDeviceType {
		result[deviceTypeID] = *convertBTDevicesToUIModels(&btDeviceSlice)
	}
	return result
}
