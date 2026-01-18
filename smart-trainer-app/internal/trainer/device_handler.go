package trainer

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/bt"
)

// DeviceHandler manages BT device connections and data collection
type DeviceHandler struct {
	model     *UIModel
	btManager bt.BTManagerInterface
	logger    *log.Logger

	// Subscription tracking: device address -> set of subscribed device type IDs
	subscriptionsMu       sync.RWMutex
	subscriptionsByDevice map[string]map[DeviceTypeID]bool

	// FTMS device tracking: address of the connected device that supports FTMS control
	ftmsDeviceAddress string

	// CSC (Cycling Speed and Cadence) state for cadence calculation
	cscMu                   sync.Mutex
	lastCrankRevolutions    uint16
	lastCrankEventTime      uint16
	hasPreviousCrankReading bool

	// Poll goroutine management
	pollMu        sync.Mutex
	pollStopChans map[string]chan struct{}
	pollWg        sync.WaitGroup
}

// NewDeviceHandler creates a new DeviceHandler
func NewDeviceHandler(model *UIModel, btManager bt.BTManagerInterface, logger *log.Logger) *DeviceHandler {
	if model == nil {
		panic("DeviceHandler: model cannot be nil")
	}
	if btManager == nil {
		panic("DeviceHandler: btManager cannot be nil")
	}
	if logger == nil {
		panic("DeviceHandler: logger cannot be nil")
	}
	return &DeviceHandler{
		model:                 model,
		btManager:             btManager,
		logger:                logger,
		subscriptionsByDevice: make(map[string]map[DeviceTypeID]bool),
		pollStopChans:         make(map[string]chan struct{}),
	}
}

// ConnectAndSubscribe connects to a device and subscribes to all notification streams for the specified device type
func (h *DeviceHandler) ConnectAndSubscribe(deviceTypeID DeviceTypeID, address string) error {
	btDevice := h.btManager.GetBTDeviceByAddressString(address)
	if btDevice == nil {
		return fmt.Errorf("device not found: %s", address)
	}

	deviceName := fmt.Sprintf("%s (%s)", btDevice.GetLocalName(), btDevice.GetAddressString())

	// Look up the device type configuration
	deviceType, ok := GetDeviceTypeByID(deviceTypeID)
	if !ok {
		return fmt.Errorf("unknown device type ID: %s", deviceTypeID)
	}

	// Check if already connected - if so, just subscribe to the new device type's streams
	alreadyConnected := btDevice.IsConnected()
	if alreadyConnected {
		h.logger.Printf("Device %s already connected, subscribing to %s streams", deviceName, deviceType.DisplayName)
	} else {
		h.logger.Printf("Connecting to device: %s for %s", deviceName, deviceType.DisplayName)

		// Connect to the device
		if err := h.btManager.Connect(btDevice); err != nil {
			h.logger.Printf("DeviceHandler: Error initiating connection: %v", err)
			return fmt.Errorf("failed to initiate connection: %w", err)
		}

		// Wait for connection to complete
		if err := btDevice.WaitForConnection(10 * time.Second); err != nil {
			h.logger.Printf("DeviceHandler: Connection timeout: %v", err)
			return fmt.Errorf("connection timeout: %w", err)
		}

		h.logger.Printf("DeviceHandler: Connected to %s", deviceName)
	}

	// Subscribe to notification streams that the device actually supports
	notifyStreams := deviceType.GetNotifyStreams()
	subscribedCount := 0
	for _, stream := range notifyStreams {
		// Check if the device supports this stream's service
		if !btDevice.HasServiceUUID(stream.ServiceUUID) {
			h.logger.Printf("DeviceHandler: Skipping %s - device doesn't support service %s",
				stream.DisplayName, stream.ServiceUUID)
			continue
		}

		// Enable notifications
		h.logger.Printf("DeviceHandler: Enabling notifications on %s (service: %s, char: %s)",
			stream.DisplayName, stream.ServiceUUID, stream.CharacteristicUUID)

		err := btDevice.EnableNotifications(
			stream.ServiceUUID,
			stream.CharacteristicUUID,
			h.createNotificationHandler(stream.ID),
		)
		if err != nil {
			h.logger.Printf("DeviceHandler: Failed to enable notifications for %s: %v", stream.DisplayName, err)
			return fmt.Errorf("failed to enable notifications for %s: %w", stream.DisplayName, err)
		}
		h.logger.Printf("Subscribed to %s", stream.DisplayName)
		subscribedCount++
	}

	if subscribedCount == 0 {
		return fmt.Errorf("no supported notification streams found on device for %s", deviceType.DisplayName)
	}

	// Track this subscription
	h.subscriptionsMu.Lock()
	if h.subscriptionsByDevice[address] == nil {
		h.subscriptionsByDevice[address] = make(map[DeviceTypeID]bool)
	}
	h.subscriptionsByDevice[address][deviceTypeID] = true

	// Track FTMS device address if this device type supports FTMS control
	if deviceType.HasFTMSControl() {
		h.ftmsDeviceAddress = address
		h.logger.Printf("DeviceHandler: Tracked FTMS device address: %s", address)
	}
	h.subscriptionsMu.Unlock()

	h.logger.Printf("Subscribed to device type: %s", deviceType.DisplayName)

	// Request FTMS control if this device type supports it
	if deviceType.HasFTMSControl() {
		if err := h.RequestFTMSControl(address); err != nil {
			h.logger.Printf("DeviceHandler: Failed to request FTMS control: %v", err)
			// Don't fail the connection - control can be retried
		} else {
			h.model.SetTrainerControlAcquired(true, address)
			h.logger.Printf("DeviceHandler: FTMS control acquired for %s", address)
		}
	}

	return nil
}

// Disconnect disconnects a device by address and clears all subscriptions for it
func (h *DeviceHandler) Disconnect(address string) error {
	btDevice := h.btManager.GetBTDeviceByAddressString(address)
	if btDevice == nil {
		return fmt.Errorf("device not found: %s", address)
	}

	h.logger.Printf("Disconnecting: %s", btDevice.GetLocalName())

	// Clear all subscription tracking for this device
	h.subscriptionsMu.Lock()
	delete(h.subscriptionsByDevice, address)
	// Clear FTMS device address and trainer control state if this was the FTMS device
	if h.ftmsDeviceAddress == address {
		h.ftmsDeviceAddress = ""
		h.model.SetTrainerControlAcquired(false, "")
		h.logger.Printf("DeviceHandler: Cleared FTMS device address and trainer control on disconnect")
	}
	h.subscriptionsMu.Unlock()

	if err := h.btManager.Disconnect(btDevice); err != nil {
		h.logger.Printf("DeviceHandler: Error disconnecting: %v", err)
		return fmt.Errorf("failed to disconnect: %w", err)
	}

	return nil
}

// UnsubscribeDeviceType removes a device type subscription for a device.
// It disables notifications for all of that device type's notification streams.
// If this was the last device type using the device, it will disconnect the device completely.
// Returns true if the device was disconnected, false if other device types still use it.
func (h *DeviceHandler) UnsubscribeDeviceType(deviceTypeID DeviceTypeID, address string) (bool, error) {
	h.subscriptionsMu.Lock()

	deviceSubs, exists := h.subscriptionsByDevice[address]
	if !exists || !deviceSubs[deviceTypeID] {
		h.subscriptionsMu.Unlock()
		h.logger.Printf("DeviceHandler: No subscription found for device type %s on device %s", deviceTypeID, address)
		return false, nil
	}

	// Remove this device type from subscriptions
	delete(deviceSubs, deviceTypeID)

	// Check if any device types remain for this device
	remainingDeviceTypes := len(deviceSubs)
	if remainingDeviceTypes == 0 {
		delete(h.subscriptionsByDevice, address)
	}

	// Clear FTMS device address and trainer control state if this was the FTMS device type
	deviceType, _ := GetDeviceTypeByID(deviceTypeID)
	if deviceType.HasFTMSControl() && h.ftmsDeviceAddress == address {
		h.ftmsDeviceAddress = ""
		h.model.SetTrainerControlAcquired(false, "")
		h.logger.Printf("DeviceHandler: Cleared FTMS device address and trainer control")
	}
	h.subscriptionsMu.Unlock()

	h.logger.Printf("DeviceHandler: Unsubscribed from device type %s on device %s (remaining device types: %d)", deviceTypeID, address, remainingDeviceTypes)

	// Disable notifications for this device type's notification streams that the device supports
	deviceType, ok := GetDeviceTypeByID(deviceTypeID)
	if ok {
		btDevice := h.btManager.GetBTDeviceByAddressString(address)
		if btDevice != nil && btDevice.IsConnected() {
			for _, stream := range deviceType.GetNotifyStreams() {
				// Only try to disable if device supports this stream's service
				if !btDevice.HasServiceUUID(stream.ServiceUUID) {
					continue
				}
				h.logger.Printf("DeviceHandler: Disabling notifications for %s on device %s", stream.ID, address)
				if err := btDevice.DisableNotifications(stream.ServiceUUID, stream.CharacteristicUUID); err != nil {
					h.logger.Printf("DeviceHandler: Failed to disable notifications for %s: %v", stream.ID, err)
					// Continue anyway - the subscription tracking is already updated
				}
			}
		}
	}

	// If no device types remain, disconnect the device
	if remainingDeviceTypes == 0 {
		h.logger.Printf("DeviceHandler: No device types remaining, disconnecting device %s", address)
		err := h.Disconnect(address)
		return true, err
	}

	h.logger.Printf("DeviceHandler: Device %s still has %d other device type(s), keeping connected", address, remainingDeviceTypes)
	return false, nil
}

// GetSubscribedDeviceTypesForDevice returns the list of device type IDs subscribed on a device
func (h *DeviceHandler) GetSubscribedDeviceTypesForDevice(address string) []DeviceTypeID {
	h.subscriptionsMu.RLock()
	defer h.subscriptionsMu.RUnlock()

	deviceSubs, exists := h.subscriptionsByDevice[address]
	if !exists {
		return nil
	}

	deviceTypes := make([]DeviceTypeID, 0, len(deviceSubs))
	for deviceTypeID := range deviceSubs {
		deviceTypes = append(deviceTypes, deviceTypeID)
	}
	return deviceTypes
}

// IsDeviceTypeSubscribed returns true if a device type is currently subscribed on any device
func (h *DeviceHandler) IsDeviceTypeSubscribed(deviceTypeID DeviceTypeID) bool {
	h.subscriptionsMu.RLock()
	defer h.subscriptionsMu.RUnlock()

	for _, deviceSubs := range h.subscriptionsByDevice {
		if deviceSubs[deviceTypeID] {
			return true
		}
	}
	return false
}

// StartScan starts scanning for BT devices
func (h *DeviceHandler) StartScan() {
	serviceUuidFilter := GetUniqueServiceUUIDs()
	h.logger.Printf("Starting BLE scan...")
	h.btManager.StartScan(serviceUuidFilter)
}

// StopScan stops scanning for BT devices
func (h *DeviceHandler) StopScan() error {
	if err := h.btManager.StopScan(); err != nil {
		h.logger.Printf("DeviceHandler: Error stopping scan: %v", err)
		return err
	}
	h.logger.Printf("Scanning stopped")
	return nil
}

// IsScanning returns true if currently scanning
func (h *DeviceHandler) IsScanning() bool {
	return h.btManager.IsScanning()
}

// createNotificationHandler returns a callback function for handling BT notifications
func (h *DeviceHandler) createNotificationHandler(streamID DataStreamID) func(buf []byte) {
	// Log that the handler was created
	h.logger.Printf("DeviceHandler: Created notification handler for %s", streamID)

	return func(buf []byte) {
		h.logger.Printf("[%s] Received %d bytes: %v", streamID, len(buf), buf)

		metrics, err := h.parseNotificationData(streamID, buf)
		if err != nil {
			h.logger.Printf("[%s] Parse error: %v (raw: %v)", streamID, err, buf)
			return
		}

		if len(metrics) == 0 {
			h.logger.Printf("[%s] No metrics in notification (flags may indicate no data)", streamID)
			return
		}

		h.model.SetMetrics(metrics)
		for metricID, value := range metrics {
			h.logger.Printf("[%s] %s: %.1f", streamID, metricID, value)
		}
	}
}

// parseNotificationData parses raw BT notification data based on the stream type
// Returns a map of MetricID to value for all metrics present in the data
func (h *DeviceHandler) parseNotificationData(streamID DataStreamID, buf []byte) (MetricData, error) {
	switch streamID {
	case StreamHeartRate:
		return parseHeartRateData(buf)
	case StreamCadence:
		return h.parseCSCData(buf)
	case StreamCyclingPower:
		return parseCyclingPowerData(buf)
	case StreamIndoorBikeData:
		return parseIndoorBikeData(buf)
	default:
		return nil, fmt.Errorf("unknown stream type: %s", streamID)
	}
}

// parseHeartRateData parses heart rate measurement characteristic data
// See: https://www.bluetooth.com/specifications/specs/heart-rate-service-1-0/
func parseHeartRateData(buf []byte) (MetricData, error) {
	if len(buf) < 2 {
		return nil, fmt.Errorf("heart rate data too short: %d bytes", len(buf))
	}

	flags := buf[0]
	// Bit 0: 0 = UINT8, 1 = UINT16
	isUint16 := (flags & 0x01) != 0

	var heartRate uint16
	if isUint16 {
		if len(buf) < 3 {
			return nil, fmt.Errorf("heart rate UINT16 data too short: %d bytes", len(buf))
		}
		heartRate = uint16(buf[1]) | (uint16(buf[2]) << 8)
	} else {
		heartRate = uint16(buf[1])
	}

	return MetricData{
		MetricHeartRate: float64(heartRate),
	}, nil
}

// parseCSCData parses Cycling Speed and Cadence measurement characteristic data
// See: https://www.bluetooth.com/specifications/specs/cycling-speed-and-cadence-service-1-0/
// Cadence is calculated from the difference in cumulative crank revolutions and event times
func (h *DeviceHandler) parseCSCData(buf []byte) (MetricData, error) {
	if len(buf) < 1 {
		return nil, fmt.Errorf("CSC data too short: %d bytes", len(buf))
	}

	flags := buf[0]
	// Bit 0: Wheel Revolution Data Present
	// Bit 1: Crank Revolution Data Present
	hasWheelData := (flags & 0x01) != 0
	hasCrankData := (flags & 0x02) != 0

	offset := 1

	// Skip wheel revolution data if present (4 bytes revolutions + 2 bytes event time)
	if hasWheelData {
		offset += 6
	}

	// Parse crank revolution data if present
	if !hasCrankData {
		return nil, nil // No cadence data in this notification
	}

	if offset+4 > len(buf) {
		return nil, fmt.Errorf("CSC data too short for crank data at offset %d", offset)
	}

	// Cumulative Crank Revolutions (UINT16)
	crankRevolutions := uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
	offset += 2

	// Last Crank Event Time (UINT16, 1/1024 second resolution)
	crankEventTime := uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)

	h.cscMu.Lock()
	defer h.cscMu.Unlock()

	if !h.hasPreviousCrankReading {
		// First reading, store values and wait for next
		h.lastCrankRevolutions = crankRevolutions
		h.lastCrankEventTime = crankEventTime
		h.hasPreviousCrankReading = true
		return nil, nil
	}

	// Calculate cadence from the difference
	// Handle rollover for UINT16 values
	revDiff := crankRevolutions - h.lastCrankRevolutions
	timeDiff := crankEventTime - h.lastCrankEventTime

	// Store current values for next calculation
	h.lastCrankRevolutions = crankRevolutions
	h.lastCrankEventTime = crankEventTime

	if timeDiff == 0 {
		// No time has passed, can't calculate cadence
		return nil, nil
	}

	// Convert time difference from 1/1024 seconds to minutes
	// timeDiff is in 1/1024 second units
	// cadence = (revolutions / time_in_minutes) = revolutions / (time_in_seconds / 60)
	// = revolutions * 60 / time_in_seconds
	// = revolutions * 60 / (timeDiff / 1024)
	// = revolutions * 60 * 1024 / timeDiff
	cadenceRPM := float64(revDiff) * 60.0 * 1024.0 / float64(timeDiff)

	// Sanity check - cadence should be reasonable (0-300 RPM)
	if cadenceRPM < 0 || cadenceRPM > 300 {
		return nil, nil
	}

	return MetricData{
		MetricInstantaneousCadence: cadenceRPM,
	}, nil
}

// parseCyclingPowerData parses cycling power measurement characteristic data
// See: https://www.bluetooth.com/specifications/specs/cycling-power-service-1-1/
func parseCyclingPowerData(buf []byte) (MetricData, error) {
	if len(buf) < 4 {
		return nil, fmt.Errorf("cycling power data too short: %d bytes", len(buf))
	}

	// Bytes 2-3: Instantaneous Power (SINT16, watts)
	power := int16(uint16(buf[2]) | (uint16(buf[3]) << 8))
	return MetricData{
		MetricInstantaneousPower: float64(power),
	}, nil
}

// IndoorBikeData holds all fields from the FTMS Indoor Bike Data characteristic
type IndoorBikeData struct {
	// Present flags
	HasInstantaneousSpeed   bool
	HasAverageSpeed         bool
	HasInstantaneousCadence bool
	HasAverageCadence       bool
	HasTotalDistance        bool
	HasResistanceLevel      bool
	HasInstantaneousPower   bool
	HasAveragePower         bool
	HasExpendedEnergy       bool
	HasHeartRate            bool
	HasMetabolicEquivalent  bool
	HasElapsedTime          bool
	HasRemainingTime        bool

	// Data fields (scaled to human-readable units)
	InstantaneousSpeedKmh   float64 // km/h
	AverageSpeedKmh         float64 // km/h
	InstantaneousCadenceRpm float64 // rpm
	AverageCadenceRpm       float64 // rpm
	TotalDistanceMeters     uint32  // meters
	ResistanceLevel         int16   // unitless
	InstantaneousPowerWatts int16   // watts
	AveragePowerWatts       int16   // watts
	TotalEnergyKJ           uint16  // kJ
	EnergyPerHourKJ         uint16  // kJ/hour
	EnergyPerMinuteKJ       uint8   // kJ/min
	HeartRateBpm            uint8   // bpm
	MetabolicEquivalent     float64 // MET
	ElapsedTimeSeconds      uint16  // seconds
	RemainingTimeSeconds    uint16  // seconds
}

// Indoor Bike Data flag bit positions (FTMS 1.0 spec)
const (
	ibdFlagMoreData             = 1 << 0  // Bit 0: 0 = Instantaneous Speed present, 1 = not present
	ibdFlagAverageSpeed         = 1 << 1  // Bit 1: Average Speed present
	ibdFlagInstantaneousCadence = 1 << 2  // Bit 2: Instantaneous Cadence present
	ibdFlagAverageCadence       = 1 << 3  // Bit 3: Average Cadence present
	ibdFlagTotalDistance        = 1 << 4  // Bit 4: Total Distance present
	ibdFlagResistanceLevel      = 1 << 5  // Bit 5: Resistance Level present
	ibdFlagInstantaneousPower   = 1 << 6  // Bit 6: Instantaneous Power present
	ibdFlagAveragePower         = 1 << 7  // Bit 7: Average Power present
	ibdFlagExpendedEnergy       = 1 << 8  // Bit 8: Expended Energy present
	ibdFlagHeartRate            = 1 << 9  // Bit 9: Heart Rate present
	ibdFlagMetabolicEquivalent  = 1 << 10 // Bit 10: Metabolic Equivalent present
	ibdFlagElapsedTime          = 1 << 11 // Bit 11: Elapsed Time present
	ibdFlagRemainingTime        = 1 << 12 // Bit 12: Remaining Time present
)

// parseIndoorBikeData parses FTMS indoor bike data characteristic
// See: https://www.bluetooth.com/specifications/specs/fitness-machine-service-1-0/
// Returns all available metrics from the characteristic
func parseIndoorBikeData(buf []byte) (MetricData, error) {
	data, err := ParseIndoorBikeDataFull(buf)
	if err != nil {
		return nil, err
	}

	metrics := make(MetricData)

	if data.HasInstantaneousSpeed {
		metrics[MetricInstantaneousSpeed] = data.InstantaneousSpeedKmh
	}
	if data.HasAverageSpeed {
		metrics[MetricAverageSpeed] = data.AverageSpeedKmh
	}
	if data.HasInstantaneousCadence {
		metrics[MetricInstantaneousCadence] = data.InstantaneousCadenceRpm
	}
	if data.HasAverageCadence {
		metrics[MetricAverageCadence] = data.AverageCadenceRpm
	}
	if data.HasTotalDistance {
		metrics[MetricTotalDistance] = float64(data.TotalDistanceMeters)
	}
	if data.HasResistanceLevel {
		metrics[MetricResistanceLevel] = float64(data.ResistanceLevel)
	}
	if data.HasInstantaneousPower {
		metrics[MetricInstantaneousPower] = float64(data.InstantaneousPowerWatts)
	}
	if data.HasAveragePower {
		metrics[MetricAveragePower] = float64(data.AveragePowerWatts)
	}
	if data.HasExpendedEnergy {
		metrics[MetricTotalEnergy] = float64(data.TotalEnergyKJ)
		metrics[MetricEnergyPerHour] = float64(data.EnergyPerHourKJ)
		metrics[MetricEnergyPerMinute] = float64(data.EnergyPerMinuteKJ)
	}
	if data.HasHeartRate {
		metrics[MetricHeartRate] = float64(data.HeartRateBpm)
	}
	if data.HasMetabolicEquivalent {
		metrics[MetricMetabolicEquivalent] = data.MetabolicEquivalent
	}
	if data.HasElapsedTime {
		metrics[MetricElapsedTime] = float64(data.ElapsedTimeSeconds)
	}
	if data.HasRemainingTime {
		metrics[MetricRemainingTime] = float64(data.RemainingTimeSeconds)
	}

	return metrics, nil
}

// ParseIndoorBikeDataFull parses all fields from the FTMS Indoor Bike Data characteristic
// See: https://www.bluetooth.com/specifications/specs/fitness-machine-service-1-0/
func ParseIndoorBikeDataFull(buf []byte) (*IndoorBikeData, error) {
	if len(buf) < 2 {
		return nil, fmt.Errorf("indoor bike data too short: %d bytes", len(buf))
	}

	// Flags are first 2 bytes (little-endian UINT16)
	flags := uint16(buf[0]) | (uint16(buf[1]) << 8)
	offset := 2

	data := &IndoorBikeData{}

	// Bit 0 (More Data) is inverted: 0 means Instantaneous Speed IS present
	data.HasInstantaneousSpeed = (flags & ibdFlagMoreData) == 0
	data.HasAverageSpeed = (flags & ibdFlagAverageSpeed) != 0
	data.HasInstantaneousCadence = (flags & ibdFlagInstantaneousCadence) != 0
	data.HasAverageCadence = (flags & ibdFlagAverageCadence) != 0
	data.HasTotalDistance = (flags & ibdFlagTotalDistance) != 0
	data.HasResistanceLevel = (flags & ibdFlagResistanceLevel) != 0
	data.HasInstantaneousPower = (flags & ibdFlagInstantaneousPower) != 0
	data.HasAveragePower = (flags & ibdFlagAveragePower) != 0
	data.HasExpendedEnergy = (flags & ibdFlagExpendedEnergy) != 0
	data.HasHeartRate = (flags & ibdFlagHeartRate) != 0
	data.HasMetabolicEquivalent = (flags & ibdFlagMetabolicEquivalent) != 0
	data.HasElapsedTime = (flags & ibdFlagElapsedTime) != 0
	data.HasRemainingTime = (flags & ibdFlagRemainingTime) != 0

	// Parse fields in order according to spec

	// 1. Instantaneous Speed (UINT16, 0.01 km/h resolution)
	if data.HasInstantaneousSpeed {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for instantaneous speed at offset %d", offset)
		}
		rawSpeed := uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		data.InstantaneousSpeedKmh = float64(rawSpeed) * 0.01
		offset += 2
	}

	// 2. Average Speed (UINT16, 0.01 km/h resolution)
	if data.HasAverageSpeed {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for average speed at offset %d", offset)
		}
		rawSpeed := uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		data.AverageSpeedKmh = float64(rawSpeed) * 0.01
		offset += 2
	}

	// 3. Instantaneous Cadence (UINT16, 0.5 rpm resolution)
	if data.HasInstantaneousCadence {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for instantaneous cadence at offset %d", offset)
		}
		rawCadence := uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		data.InstantaneousCadenceRpm = float64(rawCadence) * 0.5
		offset += 2
	}

	// 4. Average Cadence (UINT16, 0.5 rpm resolution)
	if data.HasAverageCadence {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for average cadence at offset %d", offset)
		}
		rawCadence := uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		data.AverageCadenceRpm = float64(rawCadence) * 0.5
		offset += 2
	}

	// 5. Total Distance (UINT24, 1 meter resolution)
	if data.HasTotalDistance {
		if offset+3 > len(buf) {
			return nil, fmt.Errorf("buffer too short for total distance at offset %d", offset)
		}
		data.TotalDistanceMeters = uint32(buf[offset]) | (uint32(buf[offset+1]) << 8) | (uint32(buf[offset+2]) << 16)
		offset += 3
	}

	// 6. Resistance Level (SINT16, unitless)
	if data.HasResistanceLevel {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for resistance level at offset %d", offset)
		}
		data.ResistanceLevel = int16(uint16(buf[offset]) | (uint16(buf[offset+1]) << 8))
		offset += 2
	}

	// 7. Instantaneous Power (SINT16, 1 watt resolution)
	if data.HasInstantaneousPower {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for instantaneous power at offset %d", offset)
		}
		data.InstantaneousPowerWatts = int16(uint16(buf[offset]) | (uint16(buf[offset+1]) << 8))
		offset += 2
	}

	// 8. Average Power (SINT16, 1 watt resolution)
	if data.HasAveragePower {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for average power at offset %d", offset)
		}
		data.AveragePowerWatts = int16(uint16(buf[offset]) | (uint16(buf[offset+1]) << 8))
		offset += 2
	}

	// 9. Expended Energy (UINT16 Total + UINT16 Per Hour + UINT8 Per Minute)
	if data.HasExpendedEnergy {
		if offset+5 > len(buf) {
			return nil, fmt.Errorf("buffer too short for expended energy at offset %d", offset)
		}
		data.TotalEnergyKJ = uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		offset += 2
		data.EnergyPerHourKJ = uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		offset += 2
		data.EnergyPerMinuteKJ = buf[offset]
		offset += 1
	}

	// 10. Heart Rate (UINT8, 1 bpm resolution)
	if data.HasHeartRate {
		if offset+1 > len(buf) {
			return nil, fmt.Errorf("buffer too short for heart rate at offset %d", offset)
		}
		data.HeartRateBpm = buf[offset]
		offset += 1
	}

	// 11. Metabolic Equivalent (UINT8, 0.1 MET resolution)
	if data.HasMetabolicEquivalent {
		if offset+1 > len(buf) {
			return nil, fmt.Errorf("buffer too short for metabolic equivalent at offset %d", offset)
		}
		data.MetabolicEquivalent = float64(buf[offset]) * 0.1
		offset += 1
	}

	// 12. Elapsed Time (UINT16, 1 second resolution)
	if data.HasElapsedTime {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for elapsed time at offset %d", offset)
		}
		data.ElapsedTimeSeconds = uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		offset += 2
	}

	// 13. Remaining Time (UINT16, 1 second resolution)
	if data.HasRemainingTime {
		if offset+2 > len(buf) {
			return nil, fmt.Errorf("buffer too short for remaining time at offset %d", offset)
		}
		data.RemainingTimeSeconds = uint16(buf[offset]) | (uint16(buf[offset+1]) << 8)
		// offset += 2 // Not needed, last field
	}

	return data, nil
}

// --- FTMS Control Point Methods ---

// requestFTMSControlIfSupported checks if the device supports FTMS and requests control if so.
// This is called automatically after connection to ensure we can control the trainer.
func (h *DeviceHandler) requestFTMSControlIfSupported(btDevice bt.BTDevice) error {
	stream, ok := GetStreamByID(StreamFTMSControl)
	if !ok {
		return nil // FTMS not configured, nothing to do
	}

	// Check if device has FTMS service
	if !btDevice.HasServiceUUID(stream.ServiceUUID) {
		h.logger.Printf("DeviceHandler: Device does not support FTMS control")
		return nil
	}

	address := btDevice.GetAddressString()
	h.logger.Printf("DeviceHandler: Device supports FTMS, requesting control...")

	// NOTE: We skip enabling indications on the Control Point for now.
	// Enabling indications on some BLE stacks can interfere with other notifications.
	// Control commands still work, we just won't get response confirmations.
	// TODO: Re-enable once we verify Indoor Bike Data notifications work reliably.

	h.logger.Printf("DeviceHandler: Requesting FTMS control...")

	// Request Control command: [0x00]
	data := []byte{FTMSOpCodeRequestControl}
	err := btDevice.WriteCharacteristic(stream.ServiceUUID, stream.CharacteristicUUID, data)
	if err != nil {
		return fmt.Errorf("failed to request FTMS control: %w", err)
	}

	h.logger.Printf("DeviceHandler: FTMS control request sent, sending Start command...")

	// Start or Resume command: [0x07]
	// Some trainers require this before accepting target power commands
	startData := []byte{FTMSOpCodeStartOrResume}
	err = btDevice.WriteCharacteristic(stream.ServiceUUID, stream.CharacteristicUUID, startData)
	if err != nil {
		// Log but don't fail - some trainers don't require Start
		h.logger.Printf("DeviceHandler: Start command failed (may not be required): %v", err)
	}

	// Update model state - control acquired
	h.model.SetTrainerControlAcquired(true, address)
	h.logger.Printf("Trainer control acquired")
	return nil
}

// createFTMSControlPointHandler returns a callback for FTMS Control Point indications
// This processes response codes from the trainer to confirm commands were accepted
func (h *DeviceHandler) createFTMSControlPointHandler() func(buf []byte) {
	return func(buf []byte) {
		if len(buf) < 3 {
			h.logger.Printf("FTMS Control Point: response too short: %v", buf)
			return
		}

		// Response format: [0x80, RequestOpCode, ResultCode, ...]
		if buf[0] != FTMSOpCodeResponseCode {
			h.logger.Printf("FTMS Control Point: unexpected op code: 0x%02X", buf[0])
			return
		}

		requestOpCode := buf[1]
		resultCode := buf[2]

		var opCodeName string
		switch requestOpCode {
		case FTMSOpCodeRequestControl:
			opCodeName = "Request Control"
		case FTMSOpCodeStartOrResume:
			opCodeName = "Start/Resume"
		case FTMSOpCodeSetTargetPower:
			opCodeName = "Set Target Power"
		case FTMSOpCodeSetTargetResistance:
			opCodeName = "Set Target Resistance"
		case FTMSOpCodeReset:
			opCodeName = "Reset"
		default:
			opCodeName = fmt.Sprintf("OpCode 0x%02X", requestOpCode)
		}

		var resultName string
		switch resultCode {
		case FTMSResultSuccess:
			resultName = "Success"
		case FTMSResultOpCodeNotSupported:
			resultName = "Op Code Not Supported"
		case FTMSResultInvalidParameter:
			resultName = "Invalid Parameter"
		case FTMSResultOperationFailed:
			resultName = "Operation Failed"
		case FTMSResultControlNotPermitted:
			resultName = "Control Not Permitted"
		default:
			resultName = fmt.Sprintf("Result 0x%02X", resultCode)
		}

		h.logger.Printf("FTMS Control Point: %s -> %s", opCodeName, resultName)

		// If control was not permitted, update model state
		if resultCode == FTMSResultControlNotPermitted && requestOpCode == FTMSOpCodeRequestControl {
			h.model.SetTrainerControlAcquired(false, "")
			h.logger.Printf("Trainer rejected control request")
		}
	}
}

// RequestFTMSControl sends the Request Control op code to the trainer
// This must be called before sending any other control commands
func (h *DeviceHandler) RequestFTMSControl(address string) error {
	btDevice := h.btManager.GetBTDeviceByAddressString(address)
	if btDevice == nil {
		return fmt.Errorf("device not found: %s", address)
	}

	if !btDevice.IsConnected() {
		return fmt.Errorf("device not connected: %s", address)
	}

	stream, ok := GetStreamByID(StreamFTMSControl)
	if !ok {
		return fmt.Errorf("FTMS control stream not configured")
	}

	// Request Control command: [0x00]
	data := []byte{FTMSOpCodeRequestControl}

	h.logger.Printf("Requesting FTMS control from %s", address)
	err := btDevice.WriteCharacteristic(stream.ServiceUUID, stream.CharacteristicUUID, data)
	if err != nil {
		return fmt.Errorf("failed to request FTMS control: %w", err)
	}

	h.logger.Printf("Requested trainer control")
	return nil
}

// SetTargetPower sends the Set Target Power command to the trainer (ERG mode)
// power is in watts
func (h *DeviceHandler) SetTargetPower(address string, power int16) error {
	btDevice := h.btManager.GetBTDeviceByAddressString(address)
	if btDevice == nil {
		return fmt.Errorf("device not found: %s", address)
	}

	if !btDevice.IsConnected() {
		return fmt.Errorf("device not connected: %s", address)
	}

	// Clamp power to valid range
	if power < MinTargetPowerWatts {
		power = MinTargetPowerWatts
	}
	if power > MaxTargetPowerWatts {
		power = MaxTargetPowerWatts
	}

	stream, ok := GetStreamByID(StreamFTMSControl)
	if !ok {
		return fmt.Errorf("FTMS control stream not configured")
	}

	// Set Target Power command: [0x05, power_low, power_high]
	// Power is SINT16 in watts
	data := []byte{
		FTMSOpCodeSetTargetPower,
		byte(power & 0xFF),
		byte((power >> 8) & 0xFF),
	}

	h.logger.Printf("DeviceHandler: Setting target power to %d W on %s", power, address)
	err := btDevice.WriteCharacteristic(stream.ServiceUUID, stream.CharacteristicUUID, data)
	if err != nil {
		return fmt.Errorf("failed to set target power: %w", err)
	}

	return nil
}

// SetTargetResistance sends the Set Target Resistance Level command to the trainer
// level is in 0.1 units (e.g., 100 = 10.0 resistance level)
func (h *DeviceHandler) SetTargetResistance(address string, level int16) error {
	btDevice := h.btManager.GetBTDeviceByAddressString(address)
	if btDevice == nil {
		return fmt.Errorf("device not found: %s", address)
	}

	if !btDevice.IsConnected() {
		return fmt.Errorf("device not connected: %s", address)
	}

	stream, ok := GetStreamByID(StreamFTMSControl)
	if !ok {
		return fmt.Errorf("FTMS control stream not configured")
	}

	// Set Target Resistance Level command: [0x04, level_low, level_high]
	// Resistance is SINT16 with 0.1 resolution
	data := []byte{
		FTMSOpCodeSetTargetResistance,
		byte(level & 0xFF),
		byte((level >> 8) & 0xFF),
	}

	h.logger.Printf("DeviceHandler: Setting target resistance to %.1f on %s", float64(level)/10.0, address)
	err := btDevice.WriteCharacteristic(stream.ServiceUUID, stream.CharacteristicUUID, data)
	if err != nil {
		return fmt.Errorf("failed to set target resistance: %w", err)
	}

	return nil
}

// GetFTMSDeviceAddress returns the address of the connected device that supports FTMS control
// Returns empty string if no FTMS-capable device type is connected
func (h *DeviceHandler) GetFTMSDeviceAddress() string {
	h.subscriptionsMu.RLock()
	defer h.subscriptionsMu.RUnlock()
	return h.ftmsDeviceAddress
}

// --- Polling Methods ---

// pollKey generates a unique key for tracking a poll goroutine
func pollKey(streamID DataStreamID, address string) string {
	return string(streamID) + ":" + address
}

// EnablePoll starts a goroutine that periodically reads a characteristic and
// passes the data through the same handler as EnableNotifications would use.
// This allows polling to appear like notification data to the rest of the application.
// If the device is not connected, it will connect first.
func (h *DeviceHandler) EnablePoll(streamID DataStreamID, address string, pollPeriod time.Duration) error {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	key := pollKey(streamID, address)

	// Check if already polling this stream/address combo
	if _, exists := h.pollStopChans[key]; exists {
		return fmt.Errorf("poll already active for stream %s on device %s", streamID, address)
	}

	btDevice := h.btManager.GetBTDeviceByAddressString(address)
	if btDevice == nil {
		return fmt.Errorf("device not found: %s", address)
	}

	deviceName := fmt.Sprintf("%s (%s)", btDevice.GetLocalName(), btDevice.GetAddressString())

	// Connect if not already connected
	if !btDevice.IsConnected() {
		h.logger.Printf("DeviceHandler: Connecting to device: %s", deviceName)

		if err := h.btManager.Connect(btDevice); err != nil {
			h.logger.Printf("DeviceHandler: Error initiating connection: %v", err)
			return fmt.Errorf("failed to initiate connection: %w", err)
		}

		if err := btDevice.WaitForConnection(10 * time.Second); err != nil {
			h.logger.Printf("DeviceHandler: Connection timeout: %v", err)
			return fmt.Errorf("connection timeout: %w", err)
		}

		h.logger.Printf("DeviceHandler: Connected to %s", deviceName)
	}

	// Look up the stream configuration
	stream, ok := GetStreamByID(streamID)
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	// Create the handler that will process polled data (same as notification handler)
	handler := h.createNotificationHandler(streamID)

	// Create stop channel for this poll goroutine
	stopChan := make(chan struct{})
	h.pollStopChans[key] = stopChan

	h.pollWg.Add(1)
	go func() {
		defer h.pollWg.Done()
		defer func() {
			h.pollMu.Lock()
			delete(h.pollStopChans, key)
			h.pollMu.Unlock()
		}()

		h.logger.Printf("DeviceHandler: Starting poll for %s on %s (period: %v)", streamID, address, pollPeriod)

		ticker := time.NewTicker(pollPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				h.logger.Printf("DeviceHandler: Stopping poll for %s on %s", streamID, address)
				return
			case <-ticker.C:
				// Check if device is still connected
				if !btDevice.IsConnected() {
					h.logger.Printf("DeviceHandler: Device disconnected, stopping poll for %s on %s", streamID, address)
					return
				}

				// Read the characteristic
				data, err := btDevice.ReadCharacteristic(stream.ServiceUUID, stream.CharacteristicUUID)
				if err != nil {
					h.logger.Printf("DeviceHandler: Poll read error for %s: %v", streamID, err)
					continue
				}

				// Pass the data to the handler (same as notification would)
				handler(data)
			}
		}
	}()

	h.logger.Printf("DeviceHandler: Poll enabled for %s on %s", streamID, address)
	return nil
}

// StopPoll stops a specific poll goroutine
func (h *DeviceHandler) StopPoll(streamID DataStreamID, address string) {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	key := pollKey(streamID, address)
	if stopChan, exists := h.pollStopChans[key]; exists {
		close(stopChan)
		// The goroutine will remove itself from the map when it exits
	}
}

// Shutdown stops all poll goroutines and waits for them to finish
func (h *DeviceHandler) Shutdown() {
	h.pollMu.Lock()
	// Close all stop channels to signal goroutines to exit
	for key, stopChan := range h.pollStopChans {
		h.logger.Printf("DeviceHandler: Signaling stop for poll %s", key)
		close(stopChan)
	}
	h.pollMu.Unlock()

	// Wait for all poll goroutines to finish
	h.pollWg.Wait()
	h.logger.Printf("DeviceHandler: All poll goroutines stopped")
}
