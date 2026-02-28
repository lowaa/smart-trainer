package trainer

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type uiModelPersistenceData struct {
	PreferredDeviceByDeviceType map[DeviceTypeID]string `json:"preferred_device_by_device_type"`
}

type uiModelPersistence struct {
	filePath string
	data     uiModelPersistenceData
	logger   *log.Logger
}

func newUIModelPersistence(logger *log.Logger) *uiModelPersistence {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	p := &uiModelPersistence{
		filePath: filepath.Join(homeDir, ".smart-trainer", "ui_state.json"),
		logger:   logger,
	}
	p.load()
	return p
}

func (p *uiModelPersistence) getPreferredDevice(deviceTypeID DeviceTypeID) string {
	addr := p.data.PreferredDeviceByDeviceType[deviceTypeID]
	p.logger.Printf("UIModelPersistence: getPreferredDevice %s -> %q", deviceTypeID, addr)
	return addr
}

func (p *uiModelPersistence) setPreferredDevice(deviceTypeID DeviceTypeID, address string) {
	p.logger.Printf("UIModelPersistence: setPreferredDevice %s -> %q", deviceTypeID, address)
	p.data.PreferredDeviceByDeviceType[deviceTypeID] = address
	p.save()
}

func (p *uiModelPersistence) load() {
	p.data = uiModelPersistenceData{
		PreferredDeviceByDeviceType: make(map[DeviceTypeID]string),
	}
	raw, err := os.ReadFile(p.filePath)
	if err != nil {
		p.logger.Printf("UIModelPersistence: load %s (no existing file)", p.filePath)
		return
	}
	if err := json.Unmarshal(raw, &p.data); err != nil {
		p.logger.Printf("UIModelPersistence: load %s failed to parse: %v", p.filePath, err)
		return
	}
	if p.data.PreferredDeviceByDeviceType == nil {
		p.data.PreferredDeviceByDeviceType = make(map[DeviceTypeID]string)
	}
	p.logger.Printf("UIModelPersistence: load %s -> %v", p.filePath, p.data.PreferredDeviceByDeviceType)
}

func (p *uiModelPersistence) save() {
	if err := os.MkdirAll(filepath.Dir(p.filePath), 0755); err != nil {
		p.logger.Printf("UIModelPersistence: save mkdir failed: %v", err)
		return
	}
	raw, err := json.MarshalIndent(p.data, "", "  ")
	if err != nil {
		p.logger.Printf("UIModelPersistence: save marshal failed: %v", err)
		return
	}
	if err := os.WriteFile(p.filePath, raw, 0644); err != nil {
		p.logger.Printf("UIModelPersistence: save %s failed: %v", p.filePath, err)
		return
	}
	p.logger.Printf("UIModelPersistence: save %s -> %v", p.filePath, p.data.PreferredDeviceByDeviceType)
}
