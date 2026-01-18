package main

import (
	"bufio"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/bt"
	"github.com/lowaak/smart-trainer/smart-trainer-app/internal/trainer"
	"github.com/rivo/tview"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
	tinybluetooth "tinygo.org/x/bluetooth"
)

var adapter = tinybluetooth.DefaultAdapter

func init() {
	// Define command line flags
	pflag.Bool("test", false, "Run in test mode with mock Bluetooth device")
	pflag.Parse()

	// Bind flags to viper
	viper.BindPFlags(pflag.CommandLine)
}

// createLogger creates a logger that writes to both a file (via lumberjack) and a channel for UI display.
// Returns the logger and a channel that receives log lines for the UI.
// IMPORTANT: The channel reader goroutine is started immediately to prevent io.Pipe from blocking.
func createLogger() (*log.Logger, <-chan string, error) {
	// Create logs directory if it doesn't exist
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, nil, err
	}

	// Create log file path
	logFile := filepath.Join(logDir, "smart-trainer.log")

	// Create lumberjack logger for automatic rotation at 1MB
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    1,    // megabytes
		MaxBackups: 3,    // keep 3 backup files
		MaxAge:     28,   // days
		Compress:   true, // compress old log files
	}

	// Create a pipe for sending log output to the UI
	pipeReader, pipeWriter := io.Pipe()

	// Create a buffered channel for log lines
	// Buffer allows writes to proceed even if UIModel hasn't started consuming yet
	logChan := make(chan string, 100)

	// Start reading from the pipe immediately to prevent blocking
	// This goroutine reads lines and sends them to the channel
	go func() {
		scanner := bufio.NewScanner(pipeReader)
		for scanner.Scan() {
			line := scanner.Text() + "\n"
			// Non-blocking send - drop lines if channel is full (shouldn't happen with buffer)
			select {
			case logChan <- line:
			default:
				// Channel full, drop the line to prevent blocking
			}
		}
		close(logChan)
	}()

	// Use MultiWriter to write to both the file and the UI pipe
	multiWriter := io.MultiWriter(lumberjackLogger, pipeWriter)

	// Create logger with timestamp prefix (no file location for cleaner UI display)
	logger := log.New(multiWriter, "", log.Ldate|log.Ltime)
	return logger, logChan, nil
}

func main() {
	// Create file logger with UI log channel
	logger, uiLogChan, err := createLogger()
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	// Note: lumberjack handles file closing automatically, no need to defer close

	defer func() {
		if r := recover(); r != nil {
			logger.Printf("Panic: %v\n%s", r, debug.Stack())
			panic(r)
		}
	}()

	// Check for test mode
	testMode := viper.GetBool("test")

	if testMode {
		logger.Println("Starting smart trainer application in TEST MODE")
		runTestMode(logger, uiLogChan)
	} else {
		logger.Println("Starting smart trainer application")
		runNormalMode(logger, uiLogChan)
	}
}

func runNormalMode(logger *log.Logger, uiLogChan <-chan string) {
	manager := bt.NewBTManager(adapter, logger)
	must("enable BLE stack", manager.Enable())

	// Pass the UI log channel to UIModel for displaying logs in the UI
	model := trainer.NewUIModel(manager, logger, uiLogChan)

	// Create device handler for BT device management
	deviceHandler := trainer.NewDeviceHandler(model, manager, logger)

	// Create workout manager for workout execution
	workoutManager := trainer.NewWorkoutManager(model, deviceHandler, logger)

	controller := trainer.NewUIController(model, deviceHandler, workoutManager, logger)
	app := tview.NewApplication()
	uiImpl := trainer.NewCursesUIView(logger, app, model)

	ui := trainer.NewBaseUIView(trainer.NewBaseUIViewArg{
		UIViewImpl:   uiImpl,
		UIModel:      model,
		UIController: controller,
		Logger:       logger,
	})

	// Populate workouts after UI is initialized
	uiImpl.SetWorkoutList(trainer.AllWorkouts)

	// Start UI
	if err := ui.Run(); err != nil {
		panic(err)
	}

	controller.Shutdown()
	manager.Shutdown()
	workoutManager.Shutdown()
	ui.Shutdown()
	model.Shutdown()
}

func runTestMode(logger *log.Logger, uiLogChan <-chan string) {
	// Use mock BT manager instead of real Bluetooth
	mockManager := trainer.NewMockBTManager(logger)
	must("enable mock BT", mockManager.Enable())

	logger.Println("===========================================")
	logger.Println("TEST MODE: Mock device web UI available at:")
	logger.Println("  http://localhost:9999")
	logger.Println("===========================================")

	// Pass the UI log channel to UIModel for displaying logs in the UI
	model := trainer.NewUIModel(mockManager, logger, uiLogChan)

	// Create device handler for BT device management
	deviceHandler := trainer.NewDeviceHandler(model, mockManager, logger)

	// Create workout manager for workout execution
	workoutManager := trainer.NewWorkoutManager(model, deviceHandler, logger)

	controller := trainer.NewUIController(model, deviceHandler, workoutManager, logger)
	app := tview.NewApplication()
	uiImpl := trainer.NewCursesUIView(logger, app, model)

	ui := trainer.NewBaseUIView(trainer.NewBaseUIViewArg{
		UIViewImpl:   uiImpl,
		UIModel:      model,
		UIController: controller,
		Logger:       logger,
	})

	// Populate workouts after UI is initialized
	uiImpl.SetWorkoutList(trainer.AllWorkouts)

	// Start UI
	if err := ui.Run(); err != nil {
		panic(err)
	}

	controller.Shutdown()
	mockManager.Shutdown()
	workoutManager.Shutdown()
	ui.Shutdown()
	model.Shutdown()
}

func must(action string, err error) {
	if err != nil {
		panic("failed to " + action + ": " + err.Error())
	}
}
