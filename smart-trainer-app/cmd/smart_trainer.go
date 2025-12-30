package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"tinygo.org/x/bluetooth"
)

var adapter = bluetooth.DefaultAdapter

type BTManager struct {
	adapter  *bluetooth.Adapter
	devices  map[string]bluetooth.ScanResult
	mu       sync.RWMutex
	scanning bool
}

func NewBTManager(adapter *bluetooth.Adapter) *BTManager {
	return &BTManager{
		adapter: adapter,
		devices: make(map[string]bluetooth.ScanResult),
	}
}

func (m *BTManager) Enable() error {
	return m.adapter.Enable()
}

func (m *BTManager) StartScan(logCallback func(string, ...interface{})) {
	m.scanning = true
	go func() {
		err := m.adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
			m.mu.Lock()
			defer m.mu.Unlock()
			mac := device.Address.String()
			m.devices[mac] = device
			if logCallback != nil {
				name := device.LocalName()
				if name == "" {
					name = "Unknown"
				}
				logCallback("Found device: %s (%s) [RSSI: %d]", name, mac, device.RSSI)
			}
		})
		if err != nil && logCallback != nil {
			logCallback("Scan error: %v", err)
		}
	}()
}

func (m *BTManager) StopScan() error {
	m.scanning = false
	return m.adapter.StopScan()
}

func (m *BTManager) GetDevices() map[string]bluetooth.ScanResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]bluetooth.ScanResult)
	for k, v := range m.devices {
		result[k] = v
	}
	return result
}

func (m *BTManager) GetDeviceList() []bluetooth.ScanResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]bluetooth.ScanResult, 0, len(m.devices))
	for _, v := range m.devices {
		result = append(result, v)
	}
	return result
}

func formatDeviceName(device bluetooth.ScanResult) string {
	mac := device.Address.String()
	name := device.LocalName()
	if name == "" {
		name = "Unknown"
	}
	return fmt.Sprintf("%s (%s) [RSSI: %d]", name, mac, device.RSSI)
}

func main() {
	manager := NewBTManager(adapter)
	must("enable BLE stack", manager.Enable())

	app := tview.NewApplication()

	// Create log text view (right half)
	logView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	logView.SetBorder(true).SetTitle(" Logs ")

	// Helper function to append logs (thread-safe via tview's internal handling)
	logMessage := func(format string, args ...interface{}) {
		message := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
		fmt.Fprint(logView, message)
	}

	// Create device list (left half)
	deviceList := tview.NewList().
		ShowSecondaryText(false).
		SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
			devices := manager.GetDeviceList()
			if index < len(devices) {
				selected := devices[index]
				logMessage("Selected device: %s", formatDeviceName(selected))
				// TODO: Connect to selected device
			}
		})
	deviceList.SetBorder(true).SetTitle(" Scan Results (Press Enter to Select) ")

	// Update device list periodically
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			devices := manager.GetDeviceList()
			currentItems := deviceList.GetItemCount()

			// Update list if device count changed
			if len(devices) != currentItems {
				deviceList.Clear()
				for _, device := range devices {
					deviceList.AddItem(formatDeviceName(device), "", 0, nil)
				}
				app.Draw()
			}
		}
	}()

	// Create flex layout: left half for devices, right half for logs
	flex := tview.NewFlex().
		AddItem(deviceList, 0, 1, true). // Left half, focusable
		AddItem(logView, 0, 1, false)    // Right half

	// Set up keyboard handlers
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Tab to switch focus between panes
		if event.Key() == tcell.KeyTab {
			if deviceList.HasFocus() {
				app.SetFocus(logView)
			} else {
				app.SetFocus(deviceList)
			}
			return nil
		}
		// Escape to quit
		if event.Key() == tcell.KeyEscape {
			manager.StopScan()
			app.Stop()
			return nil
		}
		return event
	})

	logMessage("Starting BLE scan...")
	manager.StartScan(logMessage)

	// Start UI
	if err := app.SetRoot(flex, true).SetFocus(deviceList).Run(); err != nil {
		panic(err)
	}

	must("stop scan", manager.StopScan())
}

func must(action string, err error) {
	if err != nil {
		panic("failed to " + action + ": " + err.Error())
	}
}
