package trainer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/bt"
	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/events"
	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"
)

// MockBTDevice implements bt.BTDevice for testing without real Bluetooth hardware
type MockBTDevice struct {
	logger    *log.Logger
	address   string
	localName string
	state     bt.BTDeviceState

	// Supported services for this device
	serviceUUIDs []string

	// Notification callbacks (protected by mu)
	mu                     sync.RWMutex
	heartRateCallback      func([]byte)
	cadenceCallback        func([]byte) // CSC (Cycling Speed and Cadence)
	cyclingPowerCallback   func([]byte)
	indoorBikeDataCallback func([]byte)
	ftmsControlCallback    func([]byte)

	// Current values for notifications
	heartRate         uint8
	power             int16
	cadence           uint16
	speed             uint16 // 0.01 km/h resolution

	// CSC cumulative values for realistic cadence simulation
	cscCrankRevolutions uint16
	cscCrankEventTime   uint16
	cscLastUpdate       time.Time
	cscCrankRemainder   float64

	// Written values (for inspection via web UI)
	writtenValues     []WrittenValue
	writtenValuesMu   sync.RWMutex

	// Web server management
	server     *http.Server
	serverPort int
	doneChan   chan struct{}
	wg         sync.WaitGroup
}

// WrittenValue records a value written to a characteristic
type WrittenValue struct {
	Timestamp        time.Time `json:"timestamp"`
	ServiceUUID      string    `json:"serviceUuid"`
	CharacteristicUUID string  `json:"characteristicUuid"`
	Data             []byte    `json:"data"`
	DataHex          string    `json:"dataHex"`
	Description      string    `json:"description"`
}

// MockDeviceState represents the current state for the web API
type MockDeviceState struct {
	HeartRate     uint8  `json:"heartRate"`
	Power         int16  `json:"power"`
	Cadence       uint16 `json:"cadence"`
	SpeedKmh      float64 `json:"speedKmh"`
	Connected     bool   `json:"connected"`
	Address       string `json:"address"`
	LocalName     string `json:"localName"`
}

// MockBTDeviceConfig holds configuration for creating a mock device
type MockBTDeviceConfig struct {
	Address      string
	LocalName    string
	ServerPort   int
	ServiceUUIDs []string
}

// NewMockBTDevice creates a new mock Bluetooth device
func NewMockBTDevice(logger *log.Logger, config MockBTDeviceConfig) *MockBTDevice {
	if logger == nil {
		panic("MockBTDevice: logger cannot be nil")
	}

	mock := &MockBTDevice{
		logger:        logger,
		address:       config.Address,
		localName:     config.LocalName,
		state:         bt.Disconnected,
		serviceUUIDs:  config.ServiceUUIDs,
		heartRate:     70,
		power:         100,
		cadence:       80,
		speed:         2500, // 25.00 km/h
		writtenValues: make([]WrittenValue, 0),
		serverPort:    config.ServerPort,
		doneChan:      make(chan struct{}),
	}

	return mock
}

// Start starts the mock device and its web server
func (m *MockBTDevice) Start() error {
	m.logger.Printf("MockBTDevice: Starting mock device %s (%s)", m.localName, m.address)

	// Start web server
	mux := http.NewServeMux()
	mux.HandleFunc("/", m.handleIndex)
	mux.HandleFunc("/api/state", m.handleGetState)
	mux.HandleFunc("/api/set", m.handleSetValues)
	mux.HandleFunc("/api/writes", m.handleGetWrites)
	mux.HandleFunc("/api/trigger-notification", m.handleTriggerNotification)

	m.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", m.serverPort),
		Handler: mux,
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.logger.Printf("MockBTDevice: Web server starting on http://localhost:%d", m.serverPort)
		if err := m.server.ListenAndServe(); err != http.ErrServerClosed {
			m.logger.Printf("MockBTDevice: Web server error: %v", err)
		}
	}()

	// Device starts disconnected - will be connected via MockBTManager.Connect()
	m.state = bt.Disconnected

	return nil
}

// SetConnected changes the connection state of the mock device
func (m *MockBTDevice) SetConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if connected {
		m.state = bt.Connected
		m.logger.Printf("MockBTDevice: State changed to Connected")
	} else {
		m.state = bt.Disconnected
		m.logger.Printf("MockBTDevice: State changed to Disconnected")
	}
}

// Shutdown stops the mock device and cleans up resources
func (m *MockBTDevice) Shutdown() {
	m.logger.Printf("MockBTDevice: Shutting down")
	close(m.doneChan)

	if m.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.server.Shutdown(ctx); err != nil {
			m.logger.Printf("MockBTDevice: Error shutting down web server: %v", err)
		}
	}

	m.wg.Wait()
	m.logger.Printf("MockBTDevice: Shutdown complete")
}

// --- bt.BTDevice Interface Implementation ---

func (m *MockBTDevice) GetAddressString() string {
	return m.address
}

func (m *MockBTDevice) GetScanRSSI() (int16, error) {
	return -50, nil // Good signal strength
}

func (m *MockBTDevice) GetScanLastSeen() time.Time {
	return time.Now()
}

func (m *MockBTDevice) SetScanLastSeen(t time.Time) {
	// No-op for mock
}

func (m *MockBTDevice) GetLocalName() string {
	return m.localName
}

func (m *MockBTDevice) IsConnected() bool {
	return m.state == bt.Connected
}

func (m *MockBTDevice) GetState() bt.BTDeviceState {
	return m.state
}

func (m *MockBTDevice) GetStateDescription() string {
	switch m.state {
	case bt.Connected:
		return "Connected"
	case bt.Disconnected:
		return "Disconnected"
	case bt.Connecting:
		return "Connecting"
	default:
		return "Unknown"
	}
}

func (m *MockBTDevice) IsRecentlyScanned() bool {
	return true
}

func (m *MockBTDevice) WaitForConnection(timeout time.Duration) error {
	// Mock is always immediately connected
	return nil
}

func (m *MockBTDevice) EnableNotifications(serviceUuid string, characteristicUuid string, callbackFunc func(buf []byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if this device supports the requested service
	if !m.hasServiceUUIDLocked(serviceUuid) {
		return fmt.Errorf("service not supported by this device: %s", serviceUuid)
	}

	key := serviceUuid + "_" + characteristicUuid
	m.logger.Printf("MockBTDevice [%s]: EnableNotifications for %s", m.localName, key)

	switch {
	case serviceUuid == ServiceUUIDHeartRate && characteristicUuid == CharUUIDHeartRateMeasurement:
		m.heartRateCallback = callbackFunc
		m.logger.Printf("MockBTDevice [%s]: Heart rate notifications enabled", m.localName)
	case serviceUuid == ServiceUUIDCyclingSpeedCadence && characteristicUuid == CharUUIDCSCMeasurement:
		m.cadenceCallback = callbackFunc
		m.cscLastUpdate = time.Now()
		m.logger.Printf("MockBTDevice [%s]: Cadence (CSC) notifications enabled", m.localName)
	case serviceUuid == ServiceUUIDCyclingPower && characteristicUuid == CharUUIDCyclingPowerMeasurement:
		m.cyclingPowerCallback = callbackFunc
		m.logger.Printf("MockBTDevice [%s]: Cycling power notifications enabled", m.localName)
	case serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDIndoorBikeData:
		m.indoorBikeDataCallback = callbackFunc
		m.logger.Printf("MockBTDevice [%s]: Indoor bike data notifications enabled", m.localName)
	case serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDFTMSControlPoint:
		m.ftmsControlCallback = callbackFunc
		m.logger.Printf("MockBTDevice [%s]: FTMS control notifications enabled", m.localName)
	default:
		return fmt.Errorf("unknown service/characteristic: %s/%s", serviceUuid, characteristicUuid)
	}

	return nil
}

// hasServiceUUIDLocked checks if service is supported (must hold mu lock)
func (m *MockBTDevice) hasServiceUUIDLocked(uuid string) bool {
	for _, u := range m.serviceUUIDs {
		if u == uuid {
			return true
		}
	}
	return false
}

func (m *MockBTDevice) DisableNotifications(serviceUuid string, characteristicUuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if this device supports the requested service
	if !m.hasServiceUUIDLocked(serviceUuid) {
		return fmt.Errorf("service not supported by this device: %s", serviceUuid)
	}

	key := serviceUuid + "_" + characteristicUuid
	m.logger.Printf("MockBTDevice [%s]: DisableNotifications for %s", m.localName, key)

	switch {
	case serviceUuid == ServiceUUIDHeartRate && characteristicUuid == CharUUIDHeartRateMeasurement:
		m.heartRateCallback = nil
		m.logger.Printf("MockBTDevice [%s]: Heart rate notifications disabled", m.localName)
	case serviceUuid == ServiceUUIDCyclingSpeedCadence && characteristicUuid == CharUUIDCSCMeasurement:
		m.cadenceCallback = nil
		m.logger.Printf("MockBTDevice [%s]: Cadence (CSC) notifications disabled", m.localName)
	case serviceUuid == ServiceUUIDCyclingPower && characteristicUuid == CharUUIDCyclingPowerMeasurement:
		m.cyclingPowerCallback = nil
		m.logger.Printf("MockBTDevice [%s]: Cycling power notifications disabled", m.localName)
	case serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDIndoorBikeData:
		m.indoorBikeDataCallback = nil
		m.logger.Printf("MockBTDevice [%s]: Indoor bike data notifications disabled", m.localName)
	case serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDFTMSControlPoint:
		m.ftmsControlCallback = nil
		m.logger.Printf("MockBTDevice [%s]: FTMS control notifications disabled", m.localName)
	default:
		return fmt.Errorf("unknown service/characteristic: %s/%s", serviceUuid, characteristicUuid)
	}

	return nil
}

func (m *MockBTDevice) ReadCharacteristic(serviceUuid string, characteristicUuid string) ([]byte, error) {
	m.logger.Printf("MockBTDevice [%s]: ReadCharacteristic %s/%s", m.localName, serviceUuid, characteristicUuid)

	// Check if this device supports the requested service
	if !m.HasServiceUUID(serviceUuid) {
		return nil, fmt.Errorf("service not supported by this device: %s", serviceUuid)
	}

	switch {
	case serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDFTMSFeature:
		// Return mock FTMS features (supports target power)
		return []byte{0x00, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00}, nil
	case serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDSupportedPowerRange:
		// Return mock power range: min 25W, max 2000W, step 1W
		return []byte{0x19, 0x00, 0xD0, 0x07, 0x01, 0x00}, nil
	default:
		return nil, fmt.Errorf("unknown service/characteristic: %s/%s", serviceUuid, characteristicUuid)
	}
}

func (m *MockBTDevice) WriteCharacteristic(serviceUuid string, characteristicUuid string, data []byte) error {
	return m.writeCharacteristicInternal(serviceUuid, characteristicUuid, data)
}

func (m *MockBTDevice) WriteCharacteristicWithoutResponse(serviceUuid string, characteristicUuid string, data []byte) error {
	return m.writeCharacteristicInternal(serviceUuid, characteristicUuid, data)
}

func (m *MockBTDevice) writeCharacteristicInternal(serviceUuid string, characteristicUuid string, data []byte) error {
	m.logger.Printf("MockBTDevice: WriteCharacteristic %s/%s data=%v", serviceUuid, characteristicUuid, data)

	description := ""
	if serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDFTMSControlPoint {
		description = m.describeFTMSControl(data)
	}

	// Record the written value
	m.writtenValuesMu.Lock()
	m.writtenValues = append(m.writtenValues, WrittenValue{
		Timestamp:          time.Now(),
		ServiceUUID:        serviceUuid,
		CharacteristicUUID: characteristicUuid,
		Data:               data,
		DataHex:            hex.EncodeToString(data),
		Description:        description,
	})
	// Keep only last 100 writes
	if len(m.writtenValues) > 100 {
		m.writtenValues = m.writtenValues[len(m.writtenValues)-100:]
	}
	m.writtenValuesMu.Unlock()

	// Handle FTMS control commands
	if serviceUuid == ServiceUUIDFTMS && characteristicUuid == CharUUIDFTMSControlPoint {
		m.handleFTMSControl(data)
	}

	return nil
}

func (m *MockBTDevice) describeFTMSControl(data []byte) string {
	if len(data) == 0 {
		return "empty"
	}
	switch data[0] {
	case 0x00:
		return "Request Control"
	case 0x01:
		return "Reset"
	case 0x05:
		if len(data) >= 3 {
			power := int16(data[1]) | int16(data[2])<<8
			return fmt.Sprintf("Set Target Power: %dW", power)
		}
		return "Set Target Power (malformed)"
	case 0x07:
		return "Start/Resume"
	case 0x08:
		return "Stop/Pause"
	default:
		return fmt.Sprintf("Unknown opcode: 0x%02X", data[0])
	}
}

func (m *MockBTDevice) handleFTMSControl(data []byte) {
	if len(data) == 0 {
		return
	}

	// Send response via control point callback
	m.mu.RLock()
	callback := m.ftmsControlCallback
	m.mu.RUnlock()

	if callback != nil {
		// Response: [0x80, RequestOpCode, ResultCode=Success]
		response := []byte{0x80, data[0], 0x01}
		callback(response)
	}
}

func (m *MockBTDevice) GetServiceUUIDs() []string {
	return m.serviceUUIDs
}

func (m *MockBTDevice) HasServiceUUID(uuid string) bool {
	for _, u := range m.serviceUUIDs {
		if u == uuid {
			return true
		}
	}
	return false
}

// --- Notification Triggering ---

// TriggerHeartRateNotification sends a heart rate notification
func (m *MockBTDevice) TriggerHeartRateNotification() {
	m.mu.RLock()
	callback := m.heartRateCallback
	hr := m.heartRate
	m.mu.RUnlock()

	if callback != nil {
		// HR format: [flags, hr_value]
		data := []byte{0x00, hr}
		callback(data)
		m.logger.Printf("MockBTDevice: Sent HR notification: %d bpm", hr)
	}
}

// TriggerCyclingPowerNotification sends a cycling power notification
func (m *MockBTDevice) TriggerCyclingPowerNotification() {
	m.mu.RLock()
	callback := m.cyclingPowerCallback
	power := m.power
	m.mu.RUnlock()

	if callback != nil {
		// Cycling Power format: [flags_lo, flags_hi, power_lo, power_hi]
		data := []byte{0x00, 0x00, byte(power & 0xFF), byte((power >> 8) & 0xFF)}
		callback(data)
		m.logger.Printf("MockBTDevice: Sent cycling power notification: %d W", power)
	}
}

// TriggerCadenceNotification sends a CSC cadence notification
func (m *MockBTDevice) TriggerCadenceNotification() {
	m.mu.Lock()
	callback := m.cadenceCallback
	cadence := m.cadence
	now := time.Now()
	lastUpdate := m.cscLastUpdate
	if lastUpdate.IsZero() {
		lastUpdate = now
	}
	elapsedSeconds := now.Sub(lastUpdate).Seconds()
	if elapsedSeconds < 0 {
		elapsedSeconds = 0
	}

	if cadence > 0 && elapsedSeconds > 0 {
		rpm := float64(cadence) / 2.0
		revsPerSecond := rpm / 60.0
		revsTotal := revsPerSecond*elapsedSeconds + m.cscCrankRemainder
		revsInt := uint16(revsTotal)
		m.cscCrankRemainder = revsTotal - float64(revsInt)
		m.cscCrankRevolutions += revsInt

		timeTicks := uint16(elapsedSeconds * 1024.0)
		if timeTicks > 0 {
			m.cscCrankEventTime += timeTicks
		}
	}

	m.cscLastUpdate = now
	crankRevolutions := m.cscCrankRevolutions
	crankEventTime := m.cscCrankEventTime
	m.mu.Unlock()

	if callback != nil {
		// CSC format: [flags, crank_rev_lo, crank_rev_hi, crank_time_lo, crank_time_hi]
		data := []byte{
			0x02, // crank revolution data present
			byte(crankRevolutions & 0xFF),
			byte((crankRevolutions >> 8) & 0xFF),
			byte(crankEventTime & 0xFF),
			byte((crankEventTime >> 8) & 0xFF),
		}
		callback(data)
		m.logger.Printf("MockBTDevice: Sent cadence notification: %d rpm", cadence/2)
	}
}

// TriggerIndoorBikeDataNotification sends an indoor bike data notification
func (m *MockBTDevice) TriggerIndoorBikeDataNotification() {
	m.mu.RLock()
	callback := m.indoorBikeDataCallback
	speed := m.speed
	cadence := m.cadence
	power := m.power
	m.mu.RUnlock()

	if callback != nil {
		// Indoor Bike Data with speed, cadence, and power
		// Flags: bit 0=0 (speed present), bit 2=1 (cadence present), bit 6=1 (power present)
		flags := uint16(0x0044) // cadence + power present, speed present (bit 0 = 0 means present)
		data := make([]byte, 10)
		data[0] = byte(flags & 0xFF)
		data[1] = byte((flags >> 8) & 0xFF)
		data[2] = byte(speed & 0xFF)
		data[3] = byte((speed >> 8) & 0xFF)
		data[4] = byte(cadence & 0xFF)
		data[5] = byte((cadence >> 8) & 0xFF)
		data[6] = byte(power & 0xFF)
		data[7] = byte((power >> 8) & 0xFF)
		callback(data)
		m.logger.Printf("MockBTDevice: Sent indoor bike data: speed=%.2f km/h, cadence=%d rpm, power=%d W",
			float64(speed)*0.01, cadence/2, power)
	}
}

// TriggerAllNotifications sends all notification types
func (m *MockBTDevice) TriggerAllNotifications() {
	m.TriggerHeartRateNotification()
	m.TriggerCadenceNotification()
	m.TriggerCyclingPowerNotification()
	m.TriggerIndoorBikeDataNotification()
}

// --- Web Server Handlers ---

func (m *MockBTDevice) handleIndex(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>Mock BT Device Control</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 800px; margin: 0 auto; padding: 20px; }
        .section { margin: 20px 0; padding: 15px; border: 1px solid #ccc; border-radius: 5px; }
        h2 { margin-top: 0; }
        label { display: inline-block; width: 120px; }
        input[type="number"] { width: 100px; padding: 5px; }
        button { padding: 10px 20px; margin: 5px; cursor: pointer; }
        .status { padding: 10px; background: #e0e0e0; border-radius: 5px; margin: 10px 0; }
        #writes { max-height: 300px; overflow-y: auto; font-family: monospace; font-size: 12px; }
        .write-entry { padding: 5px; border-bottom: 1px solid #eee; }
        .write-time { color: #666; }
        .write-desc { color: #009; font-weight: bold; }
    </style>
</head>
<body>
    <h1>Mock BT Device Control</h1>
    
    <div class="section">
        <h2>Current State</h2>
        <div id="state" class="status">Loading...</div>
        <button onclick="refreshState()">Refresh</button>
    </div>
    
    <div class="section">
        <h2>Set Values</h2>
        <div>
            <label>Heart Rate:</label>
            <input type="number" id="heartRate" min="40" max="220" value="70"> bpm
        </div>
        <div>
            <label>Power:</label>
            <input type="number" id="power" min="0" max="2000" value="100"> W
        </div>
        <div>
            <label>Cadence:</label>
            <input type="number" id="cadence" min="0" max="200" value="80"> rpm
        </div>
        <div>
            <label>Speed:</label>
            <input type="number" id="speed" min="0" max="80" step="0.1" value="25.0"> km/h
        </div>
        <button onclick="setValues()">Set Values</button>
        <button onclick="triggerNotifications()">Send Notifications</button>
    </div>
    
    <div class="section">
        <h2>Written Values (from app)</h2>
        <div id="writes">Loading...</div>
        <button onclick="refreshWrites()">Refresh</button>
    </div>
    
    <script>
        function refreshState() {
            fetch('/api/state')
                .then(r => r.json())
                .then(data => {
                    document.getElementById('state').innerHTML = 
                        'Address: ' + data.address + '<br>' +
                        'Name: ' + data.localName + '<br>' +
                        'Connected: ' + data.connected + '<br>' +
                        'Heart Rate: ' + data.heartRate + ' bpm<br>' +
                        'Power: ' + data.power + ' W<br>' +
                        'Cadence: ' + data.cadence + ' rpm<br>' +
                        'Speed: ' + data.speedKmh.toFixed(2) + ' km/h';
                    document.getElementById('heartRate').value = data.heartRate;
                    document.getElementById('power').value = data.power;
                    document.getElementById('cadence').value = data.cadence;
                    document.getElementById('speed').value = data.speedKmh.toFixed(1);
                });
        }
        
        function setValues() {
            const params = new URLSearchParams({
                heartRate: document.getElementById('heartRate').value,
                power: document.getElementById('power').value,
                cadence: document.getElementById('cadence').value,
                speedKmh: document.getElementById('speed').value
            });
            fetch('/api/set?' + params, {method: 'POST'})
                .then(() => refreshState());
        }
        
        function triggerNotifications() {
            fetch('/api/trigger-notification', {method: 'POST'})
                .then(() => refreshState());
        }
        
        function refreshWrites() {
            fetch('/api/writes')
                .then(r => r.json())
                .then(data => {
                    const html = data.map(w => 
                        '<div class="write-entry">' +
                        '<span class="write-time">' + new Date(w.timestamp).toLocaleTimeString() + '</span> ' +
                        '<span class="write-desc">' + w.description + '</span><br>' +
                        'Data: ' + w.dataHex +
                        '</div>'
                    ).reverse().join('');
                    document.getElementById('writes').innerHTML = html || 'No writes yet';
                });
        }
        
        refreshState();
        refreshWrites();
        setInterval(refreshWrites, 2000);
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (m *MockBTDevice) handleGetState(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	state := MockDeviceState{
		HeartRate: m.heartRate,
		Power:     m.power,
		Cadence:   m.cadence / 2, // Convert from 0.5 rpm to rpm
		SpeedKmh:  float64(m.speed) * 0.01,
		Connected: m.state == bt.Connected,
		Address:   m.address,
		LocalName: m.localName,
	}
	m.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (m *MockBTDevice) handleSetValues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m.mu.Lock()
	if hr := r.URL.Query().Get("heartRate"); hr != "" {
		var val int
		fmt.Sscanf(hr, "%d", &val)
		m.heartRate = uint8(val)
	}
	if p := r.URL.Query().Get("power"); p != "" {
		var val int
		fmt.Sscanf(p, "%d", &val)
		m.power = int16(val)
	}
	if c := r.URL.Query().Get("cadence"); c != "" {
		var val int
		fmt.Sscanf(c, "%d", &val)
		m.cadence = uint16(val * 2) // Store in 0.5 rpm resolution
	}
	if s := r.URL.Query().Get("speedKmh"); s != "" {
		var val float64
		fmt.Sscanf(s, "%f", &val)
		m.speed = uint16(val * 100) // Store in 0.01 km/h resolution
	}
	m.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (m *MockBTDevice) handleGetWrites(w http.ResponseWriter, r *http.Request) {
	m.writtenValuesMu.RLock()
	writes := make([]WrittenValue, len(m.writtenValues))
	copy(writes, m.writtenValues)
	m.writtenValuesMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(writes)
}

func (m *MockBTDevice) handleTriggerNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m.TriggerAllNotifications()
	w.WriteHeader(http.StatusOK)
}

// --- MockBTManager ---

// MockBTManager is a mock implementation of bt.BTManagerInterface for testing
type MockBTManager struct {
	logger                *log.Logger
	mockDevices           []*MockBTDevice
	scanning              bool
	notificationsRunning  bool
	scanDeviceListEvent   *events.ChannelEvent[[]bt.BTDevice]
	connectedDevicesEvent *events.ChannelEvent[[]bt.BTDevice]
	ctx                   context.Context
	cancel                context.CancelFunc
	notifyCancel          context.CancelFunc // Cancel for notification goroutine
	wg                    sync.WaitGroup
	mu                    sync.RWMutex
}

// Verify MockBTManager implements bt.BTManagerInterface
var _ bt.BTManagerInterface = (*MockBTManager)(nil)

// NewMockBTManager creates a new mock Bluetooth manager with multiple devices
func NewMockBTManager(logger *log.Logger) *MockBTManager {
	if logger == nil {
		panic("MockBTManager: logger cannot be nil")
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create separate mock devices for each device type
	mockDevices := []*MockBTDevice{
		// Heart Rate Monitor - only HR service
		NewMockBTDevice(logger, MockBTDeviceConfig{
			Address:    "00:11:22:33:44:01",
			LocalName:  "Mock HR Strap",
			ServerPort: 9901,
			ServiceUUIDs: []string{
				ServiceUUIDHeartRate,
			},
		}),
		// Smart Trainer - FTMS (indoor bike data + control) and Cycling Power
		NewMockBTDevice(logger, MockBTDeviceConfig{
			Address:    "00:11:22:33:44:02",
			LocalName:  "Mock Smart Trainer",
			ServerPort: 9902,
			ServiceUUIDs: []string{
				ServiceUUIDFTMS,
				ServiceUUIDCyclingPower,
			},
		}),
		// Cadence Sensor - only CSC service
		NewMockBTDevice(logger, MockBTDeviceConfig{
			Address:    "00:11:22:33:44:03",
			LocalName:  "Mock Cadence Sensor",
			ServerPort: 9903,
			ServiceUUIDs: []string{
				ServiceUUIDCyclingSpeedCadence,
			},
		}),
	}

	mgr := &MockBTManager{
		logger:                logger,
		mockDevices:           mockDevices,
		scanDeviceListEvent:   events.NewChannelEvent[[]bt.BTDevice](true),
		connectedDevicesEvent: events.NewChannelEvent[[]bt.BTDevice](true),
		ctx:                   ctx,
		cancel:                cancel,
	}

	return mgr
}

// Enable initializes the mock BT manager (devices start disconnected)
func (m *MockBTManager) Enable() error {
	m.logger.Println("MockBTManager: Enabling (mock devices will appear when scanning)")

	// Start the web server for each device but keep them disconnected
	for _, device := range m.mockDevices {
		if err := device.Start(); err != nil {
			return err
		}
		m.logger.Printf("MockBTManager: %s web UI at http://localhost:%d", device.localName, device.serverPort)
	}

	// Emit empty connected devices list (nothing connected yet)
	m.connectedDevicesEvent.Notify([]bt.BTDevice{})

	m.logger.Println("MockBTManager: Press 's' to scan and find mock devices, then connect via UI")
	return nil
}

// GetBTDeviceByAddressString returns a BTDevice by its address string
func (m *MockBTManager) GetBTDeviceByAddressString(addressString string) bt.BTDevice {
	for _, device := range m.mockDevices {
		if device.address == addressString {
			return device
		}
	}
	return nil
}

// StartScan starts "scanning" for devices (returns all mock devices)
func (m *MockBTManager) StartScan(serviceUuidFilter []string) {
	m.logger.Println("MockBTManager: Starting scan")
	m.mu.Lock()
	m.scanning = true
	m.mu.Unlock()

	// Emit mock devices as scan results (simulating device discovery)
	// Use a goroutine to simulate async discovery
	m.wg.Add(1)
	go_func_utils.SafeGo(m.logger, func() {
		defer m.wg.Done()

		// Keep emitting the devices while scanning
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		// Build device list
		devices := make([]bt.BTDevice, len(m.mockDevices))
		for i, dev := range m.mockDevices {
			devices[i] = dev
		}

		// Emit immediately
		m.scanDeviceListEvent.Notify(devices)
		for _, dev := range m.mockDevices {
			m.logger.Printf("MockBTManager: Found device: %s (%s)", dev.localName, dev.address)
		}

		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.mu.RLock()
				scanning := m.scanning
				m.mu.RUnlock()
				if !scanning {
					return
				}
				// Keep devices visible
				for _, dev := range m.mockDevices {
					dev.SetScanLastSeen(time.Now())
				}
				m.scanDeviceListEvent.Notify(devices)
			}
		}
	})
}

// StopScan stops scanning
func (m *MockBTManager) StopScan() error {
	m.logger.Println("MockBTManager: Stopping scan")
	m.mu.Lock()
	m.scanning = false
	m.mu.Unlock()
	return nil
}

// IsScanning returns whether currently scanning
func (m *MockBTManager) IsScanning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanning
}

// Connect connects to a device
func (m *MockBTManager) Connect(device bt.BTDevice) error {
	m.logger.Printf("MockBTManager: Connecting to %s", device.GetAddressString())

	// Find the mock device by address
	var mockDev *MockBTDevice
	for _, dev := range m.mockDevices {
		if dev.address == device.GetAddressString() {
			mockDev = dev
			break
		}
	}
	if mockDev == nil {
		return fmt.Errorf("unknown device: %s", device.GetAddressString())
	}

	// Set device state to connected
	mockDev.SetConnected(true)

	// Start periodic notification sender (if not already running)
	m.startNotifications()

	// Emit connected devices
	m.connectedDevicesEvent.Notify(m.GetConnectedDevices())

	m.logger.Printf("MockBTManager: Connected to %s", device.GetAddressString())
	return nil
}

// Disconnect disconnects from a device
func (m *MockBTManager) Disconnect(device bt.BTDevice) error {
	m.logger.Printf("MockBTManager: Disconnecting from %s", device.GetAddressString())

	// Find the mock device by address
	for _, dev := range m.mockDevices {
		if dev.address == device.GetAddressString() {
			// Set device state to disconnected
			dev.SetConnected(false)
			break
		}
	}

	// Emit updated connected devices list
	connectedDevices := m.GetConnectedDevices()
	m.connectedDevicesEvent.Notify(connectedDevices)

	// Stop notifications if no devices are connected
	if len(connectedDevices) == 0 {
		m.stopNotifications()
	}

	return nil
}

// startNotifications starts the periodic notification sender
func (m *MockBTManager) startNotifications() {
	m.mu.Lock()
	if m.notificationsRunning {
		m.mu.Unlock()
		return
	}
	m.notificationsRunning = true

	notifyCtx, notifyCancel := context.WithCancel(m.ctx)
	m.notifyCancel = notifyCancel
	m.mu.Unlock()

	m.wg.Add(1)
	go_func_utils.SafeGo(m.logger, func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			m.notificationsRunning = false
			m.mu.Unlock()
		}()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		m.logger.Println("MockBTManager: Started sending notifications")

		for {
			select {
			case <-notifyCtx.Done():
				m.logger.Println("MockBTManager: Stopped sending notifications")
				return
			case <-ticker.C:
				// Send notifications for all connected devices
				for _, dev := range m.mockDevices {
					if dev.IsConnected() {
						dev.TriggerAllNotifications()
					}
				}
			}
		}
	})
}

// stopNotifications stops the periodic notification sender
func (m *MockBTManager) stopNotifications() {
	m.mu.Lock()
	if m.notifyCancel != nil {
		m.notifyCancel()
		m.notifyCancel = nil
	}
	m.mu.Unlock()
}

// GetConnectedDevices returns connected devices
func (m *MockBTManager) GetConnectedDevices() []bt.BTDevice {
	var connected []bt.BTDevice
	for _, dev := range m.mockDevices {
		if dev.IsConnected() {
			connected = append(connected, dev)
		}
	}
	return connected
}

// GetScanDevices returns scanned devices
func (m *MockBTManager) GetScanDevices() []bt.BTDevice {
	m.mu.RLock()
	scanning := m.scanning
	m.mu.RUnlock()

	if scanning {
		devices := make([]bt.BTDevice, len(m.mockDevices))
		for i, dev := range m.mockDevices {
			devices[i] = dev
		}
		return devices
	}
	return []bt.BTDevice{}
}

// ListenToDeviceList registers a channel to receive device list changes
func (m *MockBTManager) ListenToDeviceList(ch chan<- []bt.BTDevice) func() {
	return m.scanDeviceListEvent.Listen(ch)
}

// ListenToConnectedDevices registers a channel to receive connected devices list changes
func (m *MockBTManager) ListenToConnectedDevices(ch chan<- []bt.BTDevice) func() {
	return m.connectedDevicesEvent.Listen(ch)
}

// Shutdown stops the mock manager
func (m *MockBTManager) Shutdown() {
	m.logger.Println("MockBTManager: Shutting down")
	m.stopNotifications()
	m.cancel()
	m.wg.Wait()
	for _, dev := range m.mockDevices {
		dev.Shutdown()
	}
	m.logger.Println("MockBTManager: Shutdown complete")
}

// GetMockDevices returns all mock devices for direct access
func (m *MockBTManager) GetMockDevices() []*MockBTDevice {
	return m.mockDevices
}
