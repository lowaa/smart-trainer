package trainer

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/go_func_utils"
)

// AutoInitService listens for scanned devices and automatically connects to
// preferred devices (from persistence) when they appear and are not yet connected.
type AutoInitService struct {
	model         *UIModel
	deviceHandler *DeviceHandler
	logger        *log.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

func NewAutoInitService(model *UIModel, deviceHandler *DeviceHandler, logger *log.Logger) *AutoInitService {
	if model == nil {
		panic("AutoInitService: model cannot be nil")
	}
	if deviceHandler == nil {
		panic("AutoInitService: deviceHandler cannot be nil")
	}
	if logger == nil {
		panic("AutoInitService: logger cannot be nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &AutoInitService{
		model:         model,
		deviceHandler: deviceHandler,
		logger:        logger,
		ctx:           ctx,
		cancel:        cancel,
	}

	s.wg.Add(2)
	go_func_utils.SafeGo(logger, func() { s.listenToScanDevices() })
	go_func_utils.SafeGo(logger, func() { s.listenToUIState() })

	return s
}

func (s *AutoInitService) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

func (s *AutoInitService) listenToScanDevices() {
	defer s.wg.Done()

	// Buffer matches the number of device types we might auto-connect
	ch := make(chan UIDeviceModelByDeviceType, 1)
	unregister := s.model.ListenToScanDevices(ch)
	defer unregister()

	// Tracks which device types we've already requested auto-connect for this session
	autoConnectRequested := make(map[DeviceTypeID]bool)

	for {
		select {
		case <-s.ctx.Done():
			return
		case devices, ok := <-ch:
			if !ok {
				return
			}
			s.handleScanUpdate(devices, autoConnectRequested)
		}
	}
}

func (s *AutoInitService) listenToUIState() {
	defer s.wg.Done()

	ch := make(chan UIState, 1)
	unregister := s.model.ListenToUIState(ch)
	defer unregister()

	for {
		select {
		case <-s.ctx.Done():
			return
		case state, ok := <-ch:
			if !ok {
				return
			}
			if state.Mode == UIModeDeviceManagement {
				s.deviceHandler.StartScan()
			} else {
				if err := s.deviceHandler.StopScan(); err != nil {
					s.logger.Printf("AutoInitService: StopScan error: %v", err)
				}
			}
		}
	}
}

func (s *AutoInitService) handleScanUpdate(devices UIDeviceModelByDeviceType, autoConnectRequested map[DeviceTypeID]bool) {
	for deviceTypeID, uiDevices := range devices {
		if autoConnectRequested[deviceTypeID] || s.deviceHandler.IsDeviceTypeSubscribed(deviceTypeID) {
			continue
		}

		preferredAddr := s.model.GetPreferredDevice(deviceTypeID)
		if preferredAddr == "" {
			continue
		}

		for _, uiDevice := range uiDevices {
			if uiDevice.Address != preferredAddr {
				continue
			}

			autoConnectRequested[deviceTypeID] = true
			s.logger.Printf("Auto-connecting %s (%s) from persistence", uiDevice.Address, deviceTypeID)

			if err := s.deviceHandler.ConnectAndSubscribe(deviceTypeID, uiDevice.Address); err != nil {
				s.logger.Printf("Auto-connect failed for %s: %v", deviceTypeID, err)
				break
			}

			s.model.SetConnectedDeviceForDeviceType(deviceTypeID, uiDevice)
			// Rate-limit successive connections. There's some kind of race going on here but
			// i haven't spent time working it out
			time.Sleep(1 * time.Second)
			break
		}
	}
}
