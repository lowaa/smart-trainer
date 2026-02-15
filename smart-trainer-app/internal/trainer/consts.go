package trainer

import "time"

// Bluetooth Service and Characteristic UUIDs for bike training
const (
	// Heart Rate Service
	ServiceUUIDHeartRate         = "0000180d-0000-1000-8000-00805f9b34fb"
	CharUUIDHeartRateMeasurement = "00002a37-0000-1000-8000-00805f9b34fb"

	// Cycling Speed and Cadence Service (CSC)
	ServiceUUIDCyclingSpeedCadence = "00001816-0000-1000-8000-00805f9b34fb"
	CharUUIDCSCMeasurement         = "00002a5b-0000-1000-8000-00805f9b34fb"

	// Cycling Power Service
	ServiceUUIDCyclingPower         = "00001818-0000-1000-8000-00805f9b34fb"
	CharUUIDCyclingPowerMeasurement = "00002a63-0000-1000-8000-00805f9b34fb"

	// Fitness Machine Service (FTMS)
	ServiceUUIDFTMS             = "00001826-0000-1000-8000-00805f9b34fb"
	CharUUIDIndoorBikeData      = "00002ad2-0000-1000-8000-00805f9b34fb"
	CharUUIDFTMSControlPoint    = "00002ad9-0000-1000-8000-00805f9b34fb"
	CharUUIDFTMSFeature         = "00002acc-0000-1000-8000-00805f9b34fb"
	CharUUIDSupportedPowerRange = "00002ad8-0000-1000-8000-00805f9b34fb"
)

// CharacteristicMode defines how we interact with a characteristic
type CharacteristicMode int

const (
	ModeNotify CharacteristicMode = iota // Subscribe to notifications
	ModeRead                             // One-time read
	ModeWrite                            // Write commands
)

// UIMode represents the current UI mode/screen
type UIMode int

const (
	UIModeDeviceManagement  UIMode = iota // Device scanning and connection
	UIModeTrainerDashboard                // Live training metrics and controls
	UIModeWorkoutSelection                // Workout selection and management
)

// UIModeInfo contains display information for a UI mode
type UIModeInfo struct {
	Mode        UIMode
	DisplayName string
	KeyBinding  rune // The number key to activate this mode (1-9)
}

// AllUIModes defines all available UI modes in order
var AllUIModes = []UIModeInfo{
	{Mode: UIModeDeviceManagement, DisplayName: "Device Management", KeyBinding: '1'},
	{Mode: UIModeWorkoutSelection, DisplayName: "Workout Selection", KeyBinding: '2'},
	{Mode: UIModeTrainerDashboard, DisplayName: "Trainer Dashboard", KeyBinding: '3'},
}

// GetUIModeByKey returns the mode for a given key binding
func GetUIModeByKey(key rune) (UIMode, bool) {
	for _, info := range AllUIModes {
		if info.KeyBinding == key {
			return info.Mode, true
		}
	}
	return 0, false
}

// GetUIModeInfo returns the info for a given mode
func GetUIModeInfo(mode UIMode) (UIModeInfo, bool) {
	for _, info := range AllUIModes {
		if info.Mode == mode {
			return info, true
		}
	}
	return UIModeInfo{}, false
}

// DataStreamID uniquely identifies a data stream
type DataStreamID string

const (
	StreamHeartRate           DataStreamID = "heart_rate"
	StreamCadence             DataStreamID = "cadence"
	StreamCyclingPower        DataStreamID = "cycling_power"
	StreamIndoorBikeData      DataStreamID = "indoor_bike_data"
	StreamFTMSControl         DataStreamID = "ftms_control"
	StreamFTMSFeatures        DataStreamID = "ftms_features"
	StreamSupportedPowerRange DataStreamID = "supported_power_range"
)

// DataStream defines a service/characteristic combo for a specific data need
type DataStream struct {
	ID                 DataStreamID
	DisplayName        string
	Description        string
	ServiceUUID        string
	CharacteristicUUID string
	Mode               CharacteristicMode
}

// Named DataStream constants for each supported stream
var (
	DataStreamHeartRate = DataStream{
		ID:                 StreamHeartRate,
		DisplayName:        "Heart Rate",
		Description:        "Heart rate from a connected sensor",
		ServiceUUID:        ServiceUUIDHeartRate,
		CharacteristicUUID: CharUUIDHeartRateMeasurement,
		Mode:               ModeNotify,
	}
	DataStreamCadence = DataStream{
		ID:                 StreamCadence,
		DisplayName:        "Cadence",
		Description:        "Cadence from a cycling speed and cadence sensor",
		ServiceUUID:        ServiceUUIDCyclingSpeedCadence,
		CharacteristicUUID: CharUUIDCSCMeasurement,
		Mode:               ModeNotify,
	}
	DataStreamCyclingPower = DataStream{
		ID:                 StreamCyclingPower,
		DisplayName:        "Cycling Power",
		Description:        "Power and cadence from power meter or trainer",
		ServiceUUID:        ServiceUUIDCyclingPower,
		CharacteristicUUID: CharUUIDCyclingPowerMeasurement,
		Mode:               ModeNotify,
	}
	DataStreamIndoorBikeData = DataStream{
		ID:                 StreamIndoorBikeData,
		DisplayName:        "Indoor Bike Data",
		Description:        "Speed, cadence, and power from smart trainer",
		ServiceUUID:        ServiceUUIDFTMS,
		CharacteristicUUID: CharUUIDIndoorBikeData,
		Mode:               ModeNotify,
	}
	DataStreamFTMSControl = DataStream{
		ID:                 StreamFTMSControl,
		DisplayName:        "Trainer Control",
		Description:        "Control resistance, set ERG/SIM mode",
		ServiceUUID:        ServiceUUIDFTMS,
		CharacteristicUUID: CharUUIDFTMSControlPoint,
		Mode:               ModeWrite,
	}
	DataStreamFTMSFeatures = DataStream{
		ID:                 StreamFTMSFeatures,
		DisplayName:        "Trainer Features",
		Description:        "Query trainer capabilities",
		ServiceUUID:        ServiceUUIDFTMS,
		CharacteristicUUID: CharUUIDFTMSFeature,
		Mode:               ModeRead,
	}
	DataStreamSupportedPowerRange = DataStream{
		ID:                 StreamSupportedPowerRange,
		DisplayName:        "Power Range",
		Description:        "Min/max watts supported by trainer",
		ServiceUUID:        ServiceUUIDFTMS,
		CharacteristicUUID: CharUUIDSupportedPowerRange,
		Mode:               ModeRead,
	}
)

// AllDataStreams is the registry of all supported data streams
var AllDataStreams = []DataStream{
	DataStreamHeartRate,
	DataStreamCadence,
	DataStreamCyclingPower,
	DataStreamIndoorBikeData,
	DataStreamFTMSControl,
	DataStreamFTMSFeatures,
	DataStreamSupportedPowerRange,
}

// GetNotifyStreams returns all streams that use notifications
func GetNotifyStreams() []DataStream {
	var result []DataStream
	for _, s := range AllDataStreams {
		if s.Mode == ModeNotify {
			result = append(result, s)
		}
	}
	return result
}

// GetStreamByID returns a stream by its ID
func GetStreamByID(id DataStreamID) (DataStream, bool) {
	for _, s := range AllDataStreams {
		if s.ID == id {
			return s, true
		}
	}
	return DataStream{}, false
}

// GetStreamsByServiceUUID returns all streams for a given service
func GetStreamsByServiceUUID(serviceUUID string) []DataStream {
	var result []DataStream
	for _, s := range AllDataStreams {
		if s.ServiceUUID == serviceUUID {
			result = append(result, s)
		}
	}
	return result
}

// DeviceTypeID uniquely identifies a device type
type DeviceTypeID string

const (
	DeviceTypeHeartRateMonitor DeviceTypeID = "heart_rate_monitor"
	DeviceTypeCadenceSensor    DeviceTypeID = "cadence_sensor"
	DeviceTypePowerMeter       DeviceTypeID = "power_meter"
	DeviceTypeSmartTrainer     DeviceTypeID = "smart_trainer"
)

// DeviceType represents a category of fitness device that users interact with
type DeviceType struct {
	ID               DeviceTypeID
	DisplayName      string
	Description      string
	ScanServiceUUIDs []string                    // Service UUIDs that qualify a device for this type
	DataStreams      map[DataStreamID]DataStream // Possible streams to subscribe to (filtered by device support)
}

// AllDeviceTypes defines all supported device types
var AllDeviceTypes = []DeviceType{
	{
		ID:               DeviceTypeHeartRateMonitor,
		DisplayName:      "Heart Rate Monitor",
		Description:      "Chest strap or optical heart rate sensor",
		ScanServiceUUIDs: []string{ServiceUUIDHeartRate},
		DataStreams: map[DataStreamID]DataStream{
			StreamHeartRate: DataStreamHeartRate,
		},
	},
	{
		ID:               DeviceTypeCadenceSensor,
		DisplayName:      "Cadence Sensor",
		Description:      "Cadence sensor or smart trainer providing cadence",
		ScanServiceUUIDs: []string{ServiceUUIDCyclingSpeedCadence, ServiceUUIDFTMS},
		DataStreams: map[DataStreamID]DataStream{
			StreamCadence:        DataStreamCadence,        // From dedicated CSC sensor
			StreamIndoorBikeData: DataStreamIndoorBikeData, // From smart trainer (cadence included)
		},
	},
	{
		ID:               DeviceTypePowerMeter,
		DisplayName:      "Power Meter",
		Description:      "Crank, pedal, or hub-based power meter",
		ScanServiceUUIDs: []string{ServiceUUIDCyclingPower},
		DataStreams: map[DataStreamID]DataStream{
			StreamCyclingPower: DataStreamCyclingPower,
		},
	},
	{
		ID:               DeviceTypeSmartTrainer,
		DisplayName:      "Smart Trainer",
		Description:      "Controllable indoor bike trainer with FTMS support",
		ScanServiceUUIDs: []string{ServiceUUIDFTMS},
		DataStreams: map[DataStreamID]DataStream{
			StreamIndoorBikeData:      DataStreamIndoorBikeData,
			StreamFTMSControl:         DataStreamFTMSControl,
			StreamFTMSFeatures:        DataStreamFTMSFeatures,
			StreamSupportedPowerRange: DataStreamSupportedPowerRange,
		},
	},
}

// GetDeviceTypeByID returns a device type by its ID
func GetDeviceTypeByID(id DeviceTypeID) (DeviceType, bool) {
	for _, dt := range AllDeviceTypes {
		if dt.ID == id {
			return dt, true
		}
	}
	return DeviceType{}, false
}

// GetPrimaryServiceUUID returns the first scan service UUID for this device type
func (dt DeviceType) GetPrimaryServiceUUID() string {
	if len(dt.ScanServiceUUIDs) > 0 {
		return dt.ScanServiceUUIDs[0]
	}
	return ""
}

// MatchesServiceUUID returns true if the given service UUID qualifies a device for this type
func (dt DeviceType) MatchesServiceUUID(serviceUUID string) bool {
	for _, uuid := range dt.ScanServiceUUIDs {
		if uuid == serviceUUID {
			return true
		}
	}
	return false
}

// GetNotifyStreams returns all streams in this device type that use notifications
func (dt DeviceType) GetNotifyStreams() []DataStream {
	var result []DataStream
	for _, stream := range dt.DataStreams {
		if stream.Mode == ModeNotify {
			result = append(result, stream)
		}
	}
	return result
}

// HasFTMSControl returns true if this device type supports FTMS control
func (dt DeviceType) HasFTMSControl() bool {
	_, ok := dt.DataStreams[StreamFTMSControl]
	return ok
}

// GetUniqueServiceUUIDs returns a deduplicated list of service UUIDs
func GetUniqueServiceUUIDs() []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range AllDataStreams {
		if !seen[s.ServiceUUID] {
			seen[s.ServiceUUID] = true
			result = append(result, s.ServiceUUID)
		}
	}
	return result
}

// MetricID identifies an individual displayable metric value
// This is separate from DataStreamID because a single BLE characteristic
// can contain multiple metrics (e.g., Indoor Bike Data has speed, cadence, power, etc.)
type MetricID string

const (
	MetricHeartRate            MetricID = "heart_rate"
	MetricInstantaneousPower   MetricID = "instantaneous_power"
	MetricAveragePower         MetricID = "average_power"
	MetricInstantaneousSpeed   MetricID = "instantaneous_speed"
	MetricAverageSpeed         MetricID = "average_speed"
	MetricInstantaneousCadence MetricID = "instantaneous_cadence"
	MetricAverageCadence       MetricID = "average_cadence"
	MetricTotalDistance        MetricID = "total_distance"
	MetricResistanceLevel      MetricID = "resistance_level"
	MetricTotalEnergy          MetricID = "total_energy"
	MetricEnergyPerHour        MetricID = "energy_per_hour"
	MetricEnergyPerMinute      MetricID = "energy_per_minute"
	MetricMetabolicEquivalent  MetricID = "metabolic_equivalent"
	MetricElapsedTime          MetricID = "elapsed_time"
	MetricRemainingTime        MetricID = "remaining_time"
)

// MetricInfo contains display information for a metric
type MetricInfo struct {
	ID          MetricID
	DisplayName string
	Unit        string
	FormatStr   string // Printf format string for the value
}

// AllMetrics defines metadata for all supported metrics
var AllMetrics = map[MetricID]MetricInfo{
	MetricHeartRate: {
		ID:          MetricHeartRate,
		DisplayName: "Heart Rate",
		Unit:        "bpm",
		FormatStr:   "%.0f",
	},
	MetricInstantaneousPower: {
		ID:          MetricInstantaneousPower,
		DisplayName: "Power",
		Unit:        "W",
		FormatStr:   "%.0f",
	},
	MetricAveragePower: {
		ID:          MetricAveragePower,
		DisplayName: "Avg Power",
		Unit:        "W",
		FormatStr:   "%.0f",
	},
	MetricInstantaneousSpeed: {
		ID:          MetricInstantaneousSpeed,
		DisplayName: "Speed",
		Unit:        "km/h",
		FormatStr:   "%.1f",
	},
	MetricAverageSpeed: {
		ID:          MetricAverageSpeed,
		DisplayName: "Avg Speed",
		Unit:        "km/h",
		FormatStr:   "%.1f",
	},
	MetricInstantaneousCadence: {
		ID:          MetricInstantaneousCadence,
		DisplayName: "Cadence",
		Unit:        "rpm",
		FormatStr:   "%.0f",
	},
	MetricAverageCadence: {
		ID:          MetricAverageCadence,
		DisplayName: "Avg Cadence",
		Unit:        "rpm",
		FormatStr:   "%.0f",
	},
	MetricTotalDistance: {
		ID:          MetricTotalDistance,
		DisplayName: "Distance",
		Unit:        "m",
		FormatStr:   "%.0f",
	},
	MetricResistanceLevel: {
		ID:          MetricResistanceLevel,
		DisplayName: "Resistance",
		Unit:        "",
		FormatStr:   "%.0f",
	},
	MetricTotalEnergy: {
		ID:          MetricTotalEnergy,
		DisplayName: "Energy",
		Unit:        "kJ",
		FormatStr:   "%.0f",
	},
	MetricEnergyPerHour: {
		ID:          MetricEnergyPerHour,
		DisplayName: "Energy/hr",
		Unit:        "kJ/h",
		FormatStr:   "%.0f",
	},
	MetricEnergyPerMinute: {
		ID:          MetricEnergyPerMinute,
		DisplayName: "Energy/min",
		Unit:        "kJ/min",
		FormatStr:   "%.0f",
	},
	MetricMetabolicEquivalent: {
		ID:          MetricMetabolicEquivalent,
		DisplayName: "MET",
		Unit:        "",
		FormatStr:   "%.1f",
	},
	MetricElapsedTime: {
		ID:          MetricElapsedTime,
		DisplayName: "Elapsed",
		Unit:        "s",
		FormatStr:   "%.0f",
	},
	MetricRemainingTime: {
		ID:          MetricRemainingTime,
		DisplayName: "Remaining",
		Unit:        "s",
		FormatStr:   "%.0f",
	},
}

// GetMetricInfo returns the metadata for a given metric ID
func GetMetricInfo(id MetricID) (MetricInfo, bool) {
	info, ok := AllMetrics[id]
	return info, ok
}

// FTMS Control Point Op Codes (Fitness Machine Service 1.0 spec)
// See: https://www.bluetooth.com/specifications/specs/fitness-machine-service-1-0/
const (
	FTMSOpCodeRequestControl       byte = 0x00
	FTMSOpCodeReset                byte = 0x01
	FTMSOpCodeSetTargetSpeed       byte = 0x02
	FTMSOpCodeSetTargetInclination byte = 0x03
	FTMSOpCodeSetTargetResistance  byte = 0x04
	FTMSOpCodeSetTargetPower       byte = 0x05
	FTMSOpCodeSetTargetHeartRate   byte = 0x06
	FTMSOpCodeStartOrResume        byte = 0x07
	FTMSOpCodeStopOrPause          byte = 0x08
	FTMSOpCodeResponseCode         byte = 0x80
)

// FTMS Control Point Result Codes
const (
	FTMSResultSuccess             byte = 0x01
	FTMSResultOpCodeNotSupported  byte = 0x02
	FTMSResultInvalidParameter    byte = 0x03
	FTMSResultOperationFailed     byte = 0x04
	FTMSResultControlNotPermitted byte = 0x05
)

// TrainerControlMode represents the current control mode of the trainer
type TrainerControlMode int

const (
	TrainerControlModeNone       TrainerControlMode = iota // No control active
	TrainerControlModeERG                                  // ERG mode (target power)
	TrainerControlModeResistance                           // Resistance level mode
	TrainerControlModeSimulation                           // Simulation mode (grade/wind)
)

// TrainerControlState holds the current state of trainer control
type TrainerControlState struct {
	Mode             TrainerControlMode
	TargetPowerWatts int16  // Target power in ERG mode
	ResistanceLevel  int16  // Target resistance level (0.1 resolution)
	ControlAcquired  bool   // Whether we have control of the trainer
	ConnectedAddress string // Address of the connected trainer (empty if none)
}

// Default power adjustment step in watts
const DefaultPowerStepWatts = 10

// Power limits
const (
	MinTargetPowerWatts = 25
	MaxTargetPowerWatts = 2000
)

// NotSet is a sentinel value for unused float64 fields in WorkoutBlock
const NotSet float64 = -1

// BlockTargetMode defines what metric a workout block targets
type BlockTargetMode int

const (
	BlockTargetModeFTP       BlockTargetMode = iota // Target power as FTP multiplier
	BlockTargetModeHeartRate                        // Target heart rate zone
)

// WorkoutBlock represents a single block/interval in a workout
type WorkoutBlock struct {
	TargetMode BlockTargetMode // What metric this block targets

	// FTP mode fields (use NotSet when TargetMode is not FTP)
	StartFTPMult float64 // Starting power as FTP multiplier (e.g., 0.75 = 75% FTP)
	EndFTPMult   float64 // Ending power as FTP multiplier (for ramps)

	// HeartRate mode fields (use NotSet when TargetMode is not HeartRate)
	TargetMaxHRMult float64 // Target as max heart rate multiplier (e.g., 0.65 = 65% max HR)

	// Cadence target (applies to all modes)
	TargetCadence int // Target cadence in RPM (0 means no specific target)

	Duration time.Duration // Duration of this block
}

// Workout represents a structured workout with multiple blocks
type Workout struct {
	Name   string         // Display name of the workout
	Blocks []WorkoutBlock // Ordered list of workout blocks
}

// TotalDuration returns the total duration of all blocks in the workout
func (w *Workout) TotalDuration() time.Duration {
	var total time.Duration
	for _, block := range w.Blocks {
		total += block.Duration
	}
	return total
}

const HEART_RATE_ZONE_2_MAX_HR_RATIO = 0.67
const HEART_RATE_ZONE_3_MAX_HR_RATIO = 0.75

// AllWorkouts defines the available workouts
var AllWorkouts = []Workout{
	{
		Name: "30 Min Endurance",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 5 * time.Minute},  // Warmup
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 20 * time.Minute}, // Main set
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 5 * time.Minute},  // Cooldown
		},
	},
	{
		Name: "20 Min FTP Test",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 5 * time.Minute},  // Warmup
			{StartFTPMult: 0.70, EndFTPMult: 0.70, TargetCadence: 90, Duration: 3 * time.Minute},  // Opener
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 2 * time.Minute},  // Recovery
			{StartFTPMult: 1.05, EndFTPMult: 1.05, TargetCadence: 90, Duration: 20 * time.Minute}, // FTP Test (aim for max sustainable)
			{StartFTPMult: 0.40, EndFTPMult: 0.40, TargetCadence: 90, Duration: 5 * time.Minute},  // Cooldown
		},
	},
	{
		Name: "5x5 Threshold Intervals",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 5 * time.Minute}, // Warmup
			// Interval 1
			{StartFTPMult: 1.00, EndFTPMult: 1.00, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 2
			{StartFTPMult: 1.00, EndFTPMult: 1.00, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 3
			{StartFTPMult: 1.00, EndFTPMult: 1.00, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 4
			{StartFTPMult: 1.00, EndFTPMult: 1.00, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 5
			{StartFTPMult: 1.00, EndFTPMult: 1.00, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 5 * time.Minute}, // Cooldown
		},
	},
	{
		Name: "Recovery Spin",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.40, EndFTPMult: 0.45, TargetCadence: 90, Duration: 10 * time.Minute}, // Gradual warmup
			{StartFTPMult: 0.45, EndFTPMult: 0.45, TargetCadence: 90, Duration: 25 * time.Minute}, // Easy spinning
			{StartFTPMult: 0.45, EndFTPMult: 0.35, TargetCadence: 90, Duration: 10 * time.Minute}, // Gradual cooldown
		},
	},
	{
		Name: "VO2max 4x4",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 10 * time.Minute}, // Warmup
			// Interval 1
			{StartFTPMult: 1.20, EndFTPMult: 1.20, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 4 * time.Minute},
			// Interval 2
			{StartFTPMult: 1.20, EndFTPMult: 1.20, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 4 * time.Minute},
			// Interval 3
			{StartFTPMult: 1.20, EndFTPMult: 1.20, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 4 * time.Minute},
			// Interval 4
			{StartFTPMult: 1.20, EndFTPMult: 1.20, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.50, EndFTPMult: 0.50, TargetCadence: 90, Duration: 10 * time.Minute}, // Cooldown
		},
	},
	{
		Name: "Intervals - 30m",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute}, // Warmup
			// Interval 1
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 2
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 3
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 4
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
		},
	},
	{
		Name: "Intervals - 60m",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute}, // Warmup
			// Interval 1
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 2
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 3
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 4
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 5
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 6
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 7
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 8
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
		},
	},
	{
		Name: "Intervals - 30m, HR Zone 2 - 60m",
		Blocks: []WorkoutBlock{
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute}, // Warmup
			// Interval 1
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 5 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 2
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 3
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			// Interval 4
			{StartFTPMult: 0.9, EndFTPMult: 0.9, TargetCadence: 90, Duration: 4 * time.Minute},
			{StartFTPMult: 0.65, EndFTPMult: 0.65, TargetCadence: 90, Duration: 3 * time.Minute},
			{
				TargetMode:      BlockTargetModeHeartRate,
				TargetMaxHRMult: HEART_RATE_ZONE_2_MAX_HR_RATIO,
				StartFTPMult:    NotSet,
				EndFTPMult:      NotSet,
				TargetCadence:   90,
				Duration:        60 * time.Minute,
			},
		},
	},
	{
		Name: "HR Zone 2 - 30 Min",
		Blocks: []WorkoutBlock{
			{
				TargetMode:      BlockTargetModeHeartRate,
				TargetMaxHRMult: HEART_RATE_ZONE_2_MAX_HR_RATIO,
				StartFTPMult:    NotSet,
				EndFTPMult:      NotSet,
				TargetCadence:   90,
				Duration:        30 * time.Minute,
			},
		},
	},
	{
		Name: "HR Zone 2 - 60 Min",
		Blocks: []WorkoutBlock{
			{
				TargetMode:      BlockTargetModeHeartRate,
				TargetMaxHRMult: HEART_RATE_ZONE_2_MAX_HR_RATIO,
				StartFTPMult:    NotSet,
				EndFTPMult:      NotSet,
				TargetCadence:   90,
				Duration:        60 * time.Minute,
			},
		},
	},
	{
		Name: "HR Zone 3 - 60 Min",
		Blocks: []WorkoutBlock{
			{
				TargetMode:      BlockTargetModeHeartRate,
				TargetMaxHRMult: HEART_RATE_ZONE_3_MAX_HR_RATIO,
				StartFTPMult:    NotSet,
				EndFTPMult:      NotSet,
				TargetCadence:   90,
				Duration:        60 * time.Minute,
			},
		},
	},
}

// WorkoutStatus represents the current status of a workout
type WorkoutStatus int

const (
	WorkoutStatusIdle    WorkoutStatus = iota // No workout loaded or stopped
	WorkoutStatusReady                        // Workout loaded but not started
	WorkoutStatusRunning                      // Workout in progress
	WorkoutStatusPaused                       // Workout paused
)

// WorkoutState holds the current state of a workout execution
type WorkoutState struct {
	Status           WorkoutStatus // Current workout status
	Workout          *Workout      // The loaded workout (nil if none)
	CurrentBlockIdx  int           // Index of the current block (0-based)
	ElapsedTime      time.Duration // Total elapsed time in workout
	RemainingTime    time.Duration // Time remaining in workout
	BlockElapsedTime time.Duration // Elapsed time in current block
	BlockRemainingTime time.Duration // Time remaining in current block
	CurrentTargetFTP float64       // Current target FTP multiplier (interpolated for ramps)
	TargetPowerWatts int16         // Computed target power in watts (for UI display)
	TargetHeartRate  int16         // Target heart rate in bpm (for HR mode blocks, 0 otherwise)
}
