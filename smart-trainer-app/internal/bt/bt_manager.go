package bt

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/events"
	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"

	"tinygo.org/x/bluetooth"
)

// BTManagerInterface defines the interface for Bluetooth manager implementations
type BTManagerInterface interface {
	Enable() error
	GetBTDeviceByAddressString(addressString string) BTDevice
	StartScan(serviceUuidFilter []string)
	StopScan() error
	IsScanning() bool
	Connect(device BTDevice) error
	Disconnect(device BTDevice) error
	GetConnectedDevices() []BTDevice
	GetScanDevices() []BTDevice
	ListenToDeviceList(ch chan<- []BTDevice) func()
	ListenToConnectedDevices(ch chan<- []BTDevice) func()
	Shutdown()
}

// Verify BTManager implements BTManagerInterface
var _ BTManagerInterface = (*BTManager)(nil)

type BTManager struct {
	adapter               *bluetooth.Adapter
	devicesByAddress      map[string]*btDeviceImpl
	mu                    sync.RWMutex
	scanning              bool
	scanTimeout           time.Duration
	scanDeviceListEvent   *events.ChannelEvent[[]BTDevice]
	scanContext           context.Context
	scanContextCancel     context.CancelFunc
	connectedDevicesEvent *events.ChannelEvent[[]BTDevice]
	ctx                   context.Context
	cancel                context.CancelFunc
	wg                    sync.WaitGroup
	logger                *log.Logger
}

func NewBTManager(adapter *bluetooth.Adapter, logger *log.Logger, scanTimeout ...time.Duration) *BTManager {
	if logger == nil {
		panic("BTManager: logger cannot be nil")
	}
	timeout := 10 * time.Second
	if len(scanTimeout) > 0 && scanTimeout[0] > 0 {
		timeout = scanTimeout[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &BTManager{
		adapter:               adapter,
		devicesByAddress:      make(map[string]*btDeviceImpl),
		scanTimeout:           timeout,
		scanDeviceListEvent:   events.NewChannelEvent[[]BTDevice](true),
		connectedDevicesEvent: events.NewChannelEvent[[]BTDevice](true),
		ctx:                   ctx,
		cancel:                cancel,
		logger:                logger,
	}
}

// GetBTDeviceByAddressString returns a BTDevice by its address string, or nil if not found
func (m *BTManager) GetBTDeviceByAddressString(addressString string) BTDevice {
	device, ok := m.devicesByAddress[addressString]
	if ok {
		return device
	}
	return nil
}

func (m *BTManager) getBTDeviceImpl(address bluetooth.Address) (*btDeviceImpl, bool) {
	addressStr := address.String()

	result, ok := m.devicesByAddress[addressStr]

	newObj := false

	if !ok {
		newObj = true
		result = newBtDeviceImpl(m.logger, address, m.scanTimeout)
		m.devicesByAddress[addressStr] = result
	}
	return result, newObj
}

func (m *BTManager) Enable() error {
	// Set up connection handler to track connections and disconnections
	m.adapter.SetConnectHandler(func(device bluetooth.Device, connected bool) {
		addressStr := device.Address.String()

		if connected {
			m.logger.Printf("Device connected: %s", addressStr)
			d, _ := m.getBTDeviceImpl(device.Address)
			d.setConnectedDevice(&device)
			d.setState(Connected)
		} else {
			m.logger.Printf("Device disconnected: %s", addressStr)
			d, _ := m.getBTDeviceImpl(device.Address)
			d.setConnectedDevice(nil)
			d.setState(Disconnected)
		}

		// Emit connected devices change event
		m.emitConnectedDevicesChange()
	})

	return m.adapter.Enable()
}

func (m *BTManager) StartScan(serviceUuidFilter []string) {
	// some appearance codes for later...
	// Smartwatch appearance codes: 0x00C0 - 0x00C3
	// Fitness Machine Service (0x1826) - modern smart trainers
	// Cycling Power Service (0x1818) - power measurement
	// Cycling Speed and Cadence Service (0x1816) - basic trainers

	m.logger.Println("Starting scan")
	m.mu.Lock()
	defer m.mu.Unlock()

	// Most memory efficient implementation
	filterSet := make(map[string]struct{})

	if serviceUuidFilter != nil {
		for _, filter := range serviceUuidFilter {
			filterSet[filter] = struct{}{}
		}
	}

	m.logger.Printf("Scan filter set is: %v", filterSet)

	// Stop any existing cleanup goroutine
	if m.scanning && m.scanContextCancel != nil {
		m.logger.Printf("A scan is already running. Stop the old scan and make a new context...")
		m.scanContextCancel()
	}

	m.scanning = true
	m.scanContext, m.scanContextCancel = context.WithCancel(m.ctx)

	// Start cleanup goroutine to remove stale devices
	m.wg.Add(1)
	go_func_utils.SafeGo(m.logger, func() {
		m.cleanupStaleDevices(m.scanContext)
	})

	// Start scanning
	m.wg.Add(1)
	go_func_utils.SafeGo(m.logger, func() {
		defer m.wg.Done()
		defer m.logger.Printf("exiting scan handling loop")

		err := m.adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
			select {
			case <-m.scanContext.Done():
				// ignore the result - still need to StopScan on the adapter
				return
			default:
			}
			addressStr := device.Address.String()
			now := time.Now()

			// m.logger.Printf("Device: %v, Service uuids detected: %v", device.LocalName(), device.ServiceUUIDs())

			if serviceUuidFilter != nil {
				found := false
				for _, uuid := range device.ServiceUUIDs() {
					_, ok := filterSet[uuid.String()]
					if ok {
						found = true
						break
					}
				}
				if !found {
					// m.logger.Printf("Scanned device %v (%v) does not meet service filter", device.LocalName(), device.Address.String())
					return
				}
			}

			// Update or create device entry
			d, newObj := m.getBTDeviceImpl(device.Address)
			d.setScanResult(&device)
			d.SetScanLastSeen(now)
			name := device.LocalName()
			if name == "" {
				name = "Unknown"
			}
			if newObj {
				d.setServiceUUIDs(device.ServiceUUIDs())
				m.logger.Printf("Found device: %s (%s) [RSSI: %d]", name, addressStr, device.RSSI)
			}
		})
		if err != nil {
			m.logger.Printf("Scan error: %v", err)
		}
	})

	// Emit current scan results every 1 second
	m.wg.Add(1)
	go_func_utils.SafeGo(m.logger, func() {
		defer m.wg.Done()
		defer m.logger.Printf("exiting scan emit event ticker loop")

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-m.scanContext.Done():
				return
			default:
			case <-ticker.C:
				scanDevices := m.GetScanDevices()
				m.scanDeviceListEvent.Notify(scanDevices)
			}
		}
	})
}

// Shutdown stops all goroutines and waits for them to finish
func (m *BTManager) Shutdown() {
	m.logger.Println("BTManager: Shutting down")
	connectedDevices := m.GetConnectedDevices()
	m.logger.Printf("Number of connected devices %v", len(connectedDevices))
	for _, dev := range connectedDevices {
		err := m.Disconnect(dev)
		if err != nil {
			m.logger.Printf("Error disconnecting from %v: %v", dev.GetAddressString(), err)
		} else {
			m.logger.Printf("Disconnected from %v", dev.GetAddressString())
		}
	}
	if err := m.StopScan(); err != nil {
		m.logger.Printf("BTManager: Error stopping scan: %v", err)
	}
	m.cancel()
	m.wg.Wait()
	m.logger.Println("BTManager: Shutdown complete")
}

func (m *BTManager) cleanupStaleDevices(ctx context.Context) {
	defer m.wg.Done()
	defer m.logger.Printf("exiting cleanup stale devices loop")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			var removed []string
			for mac, btDevice := range m.devicesByAddress {
				if now.Sub(btDevice.GetScanLastSeen()) > m.scanTimeout {
					delete(m.devicesByAddress, mac)
					removed = append(removed, mac)
				}
			}
			m.mu.Unlock()

			for _, mac := range removed {
				m.logger.Printf("Device timeout: %s (not seen for %v)", mac, m.scanTimeout)
			}
		}
	}
}

func (m *BTManager) StopScan() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanning = false
	if m.scanContextCancel != nil {
		m.scanContextCancel()
		m.scanContextCancel = nil
	}
	return m.adapter.StopScan()
}

// IsScanning returns whether the BTManager is currently scanning
func (m *BTManager) IsScanning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanning
}

// Connect connects to a Bluetooth device using the adapter
// Note: The actual connection success/failure will be reported via SetConnectHandler
func (m *BTManager) Connect(device BTDevice) error {
	addressStr := device.GetAddressString()
	m.logger.Printf("BTManager: Attempting to connect to device: %s", addressStr)

	btDeviceImpl, ok := m.devicesByAddress[addressStr]
	if !ok || btDeviceImpl == nil {
		return errors.New(fmt.Sprintf("Could not find btDeviceImpl for %s", addressStr))
	}

	// Use default connection parameters
	params := bluetooth.ConnectionParams{}

	_, err := m.adapter.Connect(btDeviceImpl.getAddress(), params)
	if err != nil {
		m.logger.Printf("BTManager: Connection error: %v", err)
		return err
	}

	btDeviceImpl.setState(Connecting)

	// Note: The SetConnectHandler will be called asynchronously when the connection
	// is actually established, so we don't log success here.
	m.logger.Printf("BTManager: Connection initiated to device: %s", addressStr)
	return nil
}

func (m *BTManager) Disconnect(device BTDevice) error {
	addressStr := device.GetAddressString()
	m.logger.Printf("BTManager: Attempting to disconnect from device: %s", addressStr)

	// Look up the internal btDeviceImpl by address string
	btDeviceImpl, ok := m.devicesByAddress[addressStr]
	if !ok || btDeviceImpl == nil {
		return errors.New(fmt.Sprintf("Could not find btDeviceImpl for %s", addressStr))
	}
	if btDeviceImpl.GetState() == Disconnected {
		m.logger.Printf("BTDevice in disconnected state")
		return nil
	}
	innerDevice := btDeviceImpl.getConnectedDevice()
	if innerDevice == nil {
		m.logger.Printf("Tried to disconnect but device was nil")
		return nil
	}
	// Can we call this in the disconnecting state? shrug
	return innerDevice.Disconnect()
}

// GetConnectedDevices returns a map of all currently connected devices
func (m *BTManager) GetConnectedDevices() []BTDevice {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getConnectedDevices()
}

func (m *BTManager) getConnectedDevices() []BTDevice {
	result := make([]BTDevice, 0)
	for _, btDevice := range m.devicesByAddress {
		if btDevice.IsConnected() {
			result = append(result, btDevice)
		}
	}
	return result
}

func (m *BTManager) GetScanDevices() []BTDevice {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]BTDevice, 0)
	for _, btDevice := range m.devicesByAddress {
		if btDevice.IsRecentlyScanned() {
			result = append(result, btDevice)
		}
	}
	return result
}

// ListenToDeviceList registers a channel to receive device list changes
// Events are debounced to at most once per second.
// Returns a deregistration function that can be called to remove the listener
func (m *BTManager) ListenToDeviceList(ch chan<- []BTDevice) func() {
	return m.scanDeviceListEvent.Listen(ch)
}

// ListenToConnectedDevices registers a channel to receive connected devices list changes
// Returns a deregistration function that can be called to remove the listener
func (m *BTManager) ListenToConnectedDevices(ch chan<- []BTDevice) func() {
	return m.connectedDevicesEvent.Listen(ch)
}

func (m *BTManager) emitConnectedDevicesChange() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a copy of the connected devices map
	devicesToEmit := make([]BTDevice, 0)
	for _, device := range m.devicesByAddress {
		if device.IsConnected() {
			devicesToEmit = append(devicesToEmit, device)
		}
	}

	// Notify listeners
	m.connectedDevicesEvent.Notify(devicesToEmit)
}
