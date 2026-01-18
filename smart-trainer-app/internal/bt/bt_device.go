package bt

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/safe_map"
	"tinygo.org/x/bluetooth"
)

type BTDeviceState int

// Define the constants related to the type
const (
	Disconnected BTDeviceState = iota // 0
	Connecting                        // 1
	Connected                         // 2
)

type BTDevice interface {
	GetAddressString() string
	GetScanRSSI() (int16, error)
	GetScanLastSeen() time.Time
	SetScanLastSeen(time.Time)
	GetLocalName() string
	IsConnected() bool
	GetState() BTDeviceState
	GetStateDescription() string
	IsRecentlyScanned() bool
	WaitForConnection(timeout time.Duration) error
	EnableNotifications(serviceUuid string, characteristicUuid string, callbackFunc func(buf []byte)) error
	DisableNotifications(serviceUuid string, characteristicUuid string) error
	ReadCharacteristic(serviceUuid string, characteristicUuid string) ([]byte, error)
	WriteCharacteristic(serviceUuid string, characteristicUuid string, data []byte) error
	WriteCharacteristicWithoutResponse(serviceUuid string, characteristicUuid string, data []byte) error
	GetServiceUUIDs() []string
	HasServiceUUID(uuid string) bool
}

type btDeviceImpl struct {
	address                        bluetooth.Address
	scanLastSeen                   time.Time
	localName                      string
	scanResult                     *bluetooth.ScanResult
	connectedDevice                *bluetooth.Device // will be nil if not connected
	mu                             sync.RWMutex
	bleMu                          sync.Mutex // Serializes BLE characteristic operations (notifications, writes)
	scanTimeout                    time.Duration
	logger                         *log.Logger
	state                          BTDeviceState
	serviceByUuid                  *safe_map.SafeMap[string, *bluetooth.DeviceService]
	characteristicByUuid           *safe_map.SafeMap[string, *bluetooth.DeviceCharacteristic]
	serviceCharsDiscovered         *safe_map.SafeMap[string, bool] // tracks which services have had all characteristics discovered
	allServicesDiscovered          bool                            // tracks if all services have been discovered
	serviceUuids                   []bluetooth.UUID
	serviceUuidStrs                []string
}

func newBtDeviceImpl(
	logger *log.Logger,
	address bluetooth.Address,
	scanTimeout time.Duration,
) *btDeviceImpl {
	if logger == nil {
		panic("logger must be non nil")
	}
	if scanTimeout <= 0 {
		panic("scanTimeout must be > 0")
	}
	return &btDeviceImpl{
		logger:                 logger,
		address:                address,
		localName:              "Unknown",
		scanTimeout:            scanTimeout,
		scanLastSeen:           time.Unix(0, 0), // just give it a default...
		state:                  Disconnected,
		serviceByUuid:          safe_map.NewSafeMap[string, *bluetooth.DeviceService](),
		characteristicByUuid:   safe_map.NewSafeMap[string, *bluetooth.DeviceCharacteristic](),
		serviceCharsDiscovered: safe_map.NewSafeMap[string, bool](),
		serviceUuids:           make([]bluetooth.UUID, 0),
	}
}

func (b *btDeviceImpl) getAddress() bluetooth.Address {
	return b.address
}

func (b *btDeviceImpl) GetServiceUUIDs() []string {
	return b.serviceUuidStrs
}

func (b *btDeviceImpl) HasServiceUUID(uuid string) bool {
	if b.serviceUuidStrs == nil {
		return false
	}
	for _, u := range b.serviceUuidStrs {
		if u == uuid {
			return true
		}
	}
	return false
}

func (b *btDeviceImpl) setServiceUUIDs(serviceUuids []bluetooth.UUID) {
	b.serviceUuids = serviceUuids
	b.serviceUuidStrs = make([]string, 0)
	for _, uuid := range serviceUuids {
		b.serviceUuidStrs = append(b.serviceUuidStrs, uuid.String())
	}
}

func (b *btDeviceImpl) WaitForConnection(timeout time.Duration) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	timeoutChan := time.After(timeout)

	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			connected := false
			if b.connectedDevice != nil {
				connected = true
			}
			b.mu.Unlock()
			if connected {
				return nil
			}
		case <-timeoutChan:
			return errors.New(fmt.Sprintf("Timeout after %v waiting for connection", timeout))
		}
	}
}

func (b *btDeviceImpl) GetAddressString() string {
	return b.address.String()
}

func (b *btDeviceImpl) EnableNotifications(
	serviceUuidStr string,
	characteristicUuidStr string,
	callbackFunc func(buf []byte)) error {

	// Serialize BLE operations to avoid race conditions
	b.bleMu.Lock()
	defer b.bleMu.Unlock()

	b.logger.Printf("BTDevice: EnableNotifications called for service=%s char=%s", serviceUuidStr, characteristicUuidStr)

	serviceUuid, err := bluetooth.ParseUUID(serviceUuidStr)
	if err != nil {
		return fmt.Errorf("invalid service UUID %q: %w", serviceUuidStr, err)
	}

	characteristicUuid, err := bluetooth.ParseUUID(characteristicUuidStr)
	if err != nil {
		return fmt.Errorf("invalid characteristic UUID %q: %w", characteristicUuidStr, err)
	}

	b.logger.Printf("BTDevice: Discovering characteristic %s...", characteristicUuidStr)
	characteristic, err := b.getDeviceCharacteristic(serviceUuid, characteristicUuid)
	if err != nil {
		b.logger.Printf("BTDevice: Failed to get characteristic: %v", err)
		return err
	}
	b.logger.Printf("BTDevice: Got characteristic, enabling notifications...")

	err = characteristic.EnableNotifications(callbackFunc)
	if err != nil {
		b.logger.Printf("BTDevice: EnableNotifications failed: %v", err)
		return fmt.Errorf("failed to enable notifications: %w", err)
	}

	b.logger.Printf("BTDevice: Notifications enabled successfully for %s", characteristicUuidStr)
	return nil
}

func (b *btDeviceImpl) DisableNotifications(
	serviceUuidStr string,
	characteristicUuidStr string) error {

	// Serialize BLE operations to avoid race conditions
	b.bleMu.Lock()
	defer b.bleMu.Unlock()

	b.logger.Printf("BTDevice: DisableNotifications called for service=%s char=%s", serviceUuidStr, characteristicUuidStr)

	serviceUuid, err := bluetooth.ParseUUID(serviceUuidStr)
	if err != nil {
		return fmt.Errorf("invalid service UUID %q: %w", serviceUuidStr, err)
	}

	characteristicUuid, err := bluetooth.ParseUUID(characteristicUuidStr)
	if err != nil {
		return fmt.Errorf("invalid characteristic UUID %q: %w", characteristicUuidStr, err)
	}

	characteristic, err := b.getDeviceCharacteristic(serviceUuid, characteristicUuid)
	if err != nil {
		b.logger.Printf("BTDevice: Failed to get characteristic: %v", err)
		return err
	}

	// Pass nil callback to disable notifications
	err = characteristic.EnableNotifications(nil)
	if err != nil {
		b.logger.Printf("BTDevice: DisableNotifications failed: %v", err)
		return fmt.Errorf("failed to disable notifications: %w", err)
	}

	b.logger.Printf("BTDevice: Notifications disabled successfully for %s", characteristicUuidStr)
	return nil
}

func (b *btDeviceImpl) ReadCharacteristic(
	serviceUuidStr string,
	characteristicUuidStr string) ([]byte, error) {

	// Serialize BLE operations to avoid race conditions
	b.bleMu.Lock()
	defer b.bleMu.Unlock()

	serviceUuid, err := bluetooth.ParseUUID(serviceUuidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid service UUID %q: %w", serviceUuidStr, err)
	}

	characteristicUuid, err := bluetooth.ParseUUID(characteristicUuidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid characteristic UUID %q: %w", characteristicUuidStr, err)
	}

	characteristic, err := b.getDeviceCharacteristic(serviceUuid, characteristicUuid)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 512)
	n, err := characteristic.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read characteristic: %w", err)
	}

	return buf[:n], nil
}

func (b *btDeviceImpl) WriteCharacteristic(
	serviceUuidStr string,
	characteristicUuidStr string,
	data []byte) error {
	// Serialize BLE operations to avoid race conditions
	b.bleMu.Lock()
	defer b.bleMu.Unlock()
	return b.writeCharacteristic(serviceUuidStr, characteristicUuidStr, data, true)
}

func (b *btDeviceImpl) WriteCharacteristicWithoutResponse(
	serviceUuidStr string,
	characteristicUuidStr string,
	data []byte) error {
	// Serialize BLE operations to avoid race conditions
	b.bleMu.Lock()
	defer b.bleMu.Unlock()
	return b.writeCharacteristic(serviceUuidStr, characteristicUuidStr, data, false)
}

func (b *btDeviceImpl) writeCharacteristic(
	serviceUuidStr string,
	characteristicUuidStr string,
	data []byte,
	waitForResponse bool) error {

	serviceUuid, err := bluetooth.ParseUUID(serviceUuidStr)
	if err != nil {
		return fmt.Errorf("invalid service UUID %q: %w", serviceUuidStr, err)
	}

	characteristicUuid, err := bluetooth.ParseUUID(characteristicUuidStr)
	if err != nil {
		return fmt.Errorf("invalid characteristic UUID %q: %w", characteristicUuidStr, err)
	}

	characteristic, err := b.getDeviceCharacteristic(serviceUuid, characteristicUuid)
	if err != nil {
		return err
	}

	if waitForResponse {
		_, err = characteristic.Write(data)
	} else {
		_, err = characteristic.WriteWithoutResponse(data)
	}
	if err != nil {
		return fmt.Errorf("failed to write characteristic: %w", err)
	}

	return nil
}

func (b *btDeviceImpl) GetScanRSSI() (int16, error) {
	if b.scanResult == nil {
		return 0, errors.New("no rssi available")
	}
	return b.scanResult.RSSI, nil
}

func (b *btDeviceImpl) GetState() BTDeviceState {
	return b.state
}

func (b *btDeviceImpl) GetStateDescription() string {
	switch b.state {
	case Connected:
		return "Connected"
	case Disconnected:
		return "Disconnected"
	case Connecting:
		return "Connecting"
	default:
		// This shouldn't happen...
		return "Unknown"
	}
}

func (b *btDeviceImpl) GetLocalName() string {
	if b.scanResult != nil {
		scanResultLocalName := b.scanResult.LocalName()
		if scanResultLocalName != "" {
			return scanResultLocalName
		}
	}
	return b.localName
}

func (b *btDeviceImpl) GetScanLastSeen() time.Time {
	return b.scanLastSeen
}

func (b *btDeviceImpl) SetScanLastSeen(t time.Time) {
	b.scanLastSeen = t
}

func (b *btDeviceImpl) IsConnected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	//return b.state == Connected
	return b.connectedDevice != nil
}

func (b *btDeviceImpl) IsRecentlyScanned() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.scanResult == nil {
		return false
	}
	now := time.Now()
	if now.Sub(b.GetScanLastSeen()) > b.scanTimeout {
		return false
	}
	return true
}

func (b *btDeviceImpl) setScanResult(scanResult *bluetooth.ScanResult) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scanResult = scanResult
}

func (b *btDeviceImpl) getScanResult() *bluetooth.ScanResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.scanResult
}

func (b *btDeviceImpl) setConnectedDevice(device *bluetooth.Device) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connectedDevice = device
}

func (b *btDeviceImpl) getConnectedDevice() *bluetooth.Device {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.connectedDevice
}

func (b *btDeviceImpl) setState(state BTDeviceState) {
	b.state = state
}

func (b *btDeviceImpl) getDeviceService(serviceUuid bluetooth.UUID) (*bluetooth.DeviceService, error) {
	if b.connectedDevice == nil {
		return nil, errors.New("no connected device")
	}

	serviceUuidStr := serviceUuid.String()

	// Check cache first
	service, ok := b.serviceByUuid.Load(serviceUuidStr)
	if ok {
		return service, nil
	}

	// If we haven't discovered all services yet, do it now
	// Do this because discovering singular services multiple times will interrupt
	// operation of an earlier used service
	if !b.allServicesDiscovered {
		connectedDevice := b.getConnectedDevice()

		// Discover ALL services at once (nil = all)
		b.logger.Printf("BTDevice: Discovering all services for device")
		deviceServices, err := connectedDevice.DiscoverServices(nil)
		if err != nil {
			return nil, fmt.Errorf("error discovering services: %w", err)
		}

		// Cache all discovered services
		for i := range deviceServices {
			svc := &deviceServices[i]
			svcUuidStr := svc.UUID().String()
			b.serviceByUuid.Store(svcUuidStr, svc)
			b.logger.Printf("BTDevice: Cached service %s", svcUuidStr)
		}

		b.allServicesDiscovered = true
	}

	// Now retrieve the requested service from cache
	service, ok = b.serviceByUuid.Load(serviceUuidStr)
	if !ok {
		return nil, fmt.Errorf("service %v not found on device", serviceUuidStr)
	}

	return service, nil
}

func (b *btDeviceImpl) getDeviceCharacteristic(serviceUuid bluetooth.UUID, charUuid bluetooth.UUID) (*bluetooth.DeviceCharacteristic, error) {
	serviceUuidStr := serviceUuid.String()
	charUuidStr := charUuid.String()
	comboUuidStr := fmt.Sprintf("%s_%s", serviceUuidStr, charUuidStr)

	// Check if we already have this characteristic cached
	characteristic, ok := b.characteristicByUuid.Load(comboUuidStr)
	if ok {
		return characteristic, nil
	}

	// Check if we've already discovered all characteristics for this service
	if discovered, _ := b.serviceCharsDiscovered.Load(serviceUuidStr); !discovered {
		// Need to discover all characteristics for this service
		service, err := b.getDeviceService(serviceUuid)
		if err != nil {
			return nil, err
		}

		// Discover ALL characteristics for this service (nil = all)
		b.logger.Printf("BTDevice: Discovering all characteristics for service %s", serviceUuidStr)
		discoveredCharacteristics, err := service.DiscoverCharacteristics(nil)
		if err != nil {
			return nil, fmt.Errorf("could not discover characteristics for service %v: %w", serviceUuidStr, err)
		}

		// Cache all discovered characteristics
		for i := range discoveredCharacteristics {
			char := &discoveredCharacteristics[i]
			charKey := fmt.Sprintf("%s_%s", serviceUuidStr, char.UUID().String())
			b.characteristicByUuid.Store(charKey, char)
			b.logger.Printf("BTDevice: Cached characteristic %s", char.UUID().String())
		}

		// Mark this service as having all characteristics discovered
		b.serviceCharsDiscovered.Store(serviceUuidStr, true)
	}

	// Now retrieve the requested characteristic from cache
	characteristic, ok = b.characteristicByUuid.Load(comboUuidStr)
	if !ok {
		return nil, fmt.Errorf("characteristic %v not found in service %v", charUuidStr, serviceUuidStr)
	}

	return characteristic, nil
}
