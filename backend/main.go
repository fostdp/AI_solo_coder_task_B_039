package main

import (
	"context"
	"fmt"
	"lng-monitoring/alarm"
	"lng-monitoring/alarm_forwarder"
	"lng-monitoring/api"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"lng-monitoring/modbus_poller"
	"lng-monitoring/models"
	"lng-monitoring/rollover_predictor"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type tankDataBuffer struct {
	temperatures []models.TemperatureData
	densities    []models.DensityData
	pressure     float64
	hasTemp      bool
	hasDensity   bool
	hasPressure  bool
	lastUpdate   time.Time
}

func main() {
	modelParamsPath := "config/model_params.json"
	cfg := config.LoadWithModelParams(modelParamsPath)

	if cfg.ModelParams == nil {
		fmt.Println("Warning: using default model parameters")
	}

	db, err := database.New(&cfg.Database)
	if err != nil {
		fmt.Printf("Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("Database connected successfully")

	pollResultChan := make(chan modbus_poller.PollResult, 100)
	predictionRequestChan := make(chan messages.PredictionRequest, 10)
	predictionResultChan := make(chan messages.PredictionResult, 10)
	controlCommandChan := make(chan messages.ControlCommand, 10)
	forwardResultChan := make(chan messages.ForwardResult, 10)

	poller, err := modbus_poller.NewPoller(&cfg.Modbus, pollResultChan)
	if err != nil {
		fmt.Printf("Failed to create modbus poller: %v\n", err)
		os.Exit(1)
	}
	defer poller.Close()

	if err := poller.Connect(); err != nil {
		fmt.Printf("Failed to connect to modbus: %v\n", err)
		fmt.Println("Continuing without modbus connection (will retry)")
	} else {
		fmt.Println("Modbus connected successfully")
	}

	modelParams := cfg.ModelParams
	if modelParams == nil {
		modelParams = &config.ModelParams{}
	}

	predictor := rollover_predictor.NewPredictor(
		modelParams,
		db,
		predictionRequestChan,
		predictionResultChan,
	)
	fmt.Println("Rollover predictor initialized")

	forwarder := alarm_forwarder.NewForwarder(
		cfg,
		modelParams,
		db,
		poller,
		predictionResultChan,
		controlCommandChan,
		forwardResultChan,
	)
	fmt.Println("Alarm forwarder initialized")

	server := api.NewServer(cfg, db, nil, nil)
	fmt.Println("API server initialized")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	tankBuffers := make(map[int]*tankDataBuffer)
	for tankID := 1; tankID <= cfg.Modbus.TankCount; tankID++ {
		tankBuffers[tankID] = &tankDataBuffer{}
	}
	var bufferMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting Modbus data collection...")
		poller.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting data aggregation service...")
		for {
			select {
			case <-ctx.Done():
				return
			case result := <-pollResultChan:
				if result.Error != nil {
					fmt.Printf("Poll error for %s (tank %d): %v\n", result.TaskID, result.TankID, result.Error)
					continue
				}

				bufferMu.Lock()
				buf := tankBuffers[result.TankID]
				if buf == nil {
					buf = &tankDataBuffer{}
					tankBuffers[result.TankID] = buf
				}

				switch result.DataType {
				case "temperature":
					if temps, ok := result.Data.([]models.TemperatureData); ok {
						buf.temperatures = temps
						buf.hasTemp = true
						go db.InsertTemperatureData(ctx, temps)
						summaries := poller.CalculateLayerSummary(temps, result.TankID, result.CollectedAt)
						go db.InsertLayerSummary(ctx, summaries)
					}
				case "density":
					if densities, ok := result.Data.([]models.DensityData); ok {
						buf.densities = densities
						buf.hasDensity = true
						go db.InsertDensityData(ctx, densities)
					}
				case "pressure":
					if pressures, ok := result.Data.([]models.PressureData); ok && len(pressures) > 0 {
						buf.pressure = pressures[0].Pressure
						buf.hasPressure = true
						go db.InsertPressureData(ctx, pressures)
					}
				case "bog":
					if bogData, ok := result.Data.([]models.BOGCompressorData); ok {
						go db.InsertBOGCompressorData(ctx, bogData)
					}
				}

				buf.lastUpdate = result.CollectedAt

				if buf.hasTemp && buf.hasDensity && buf.hasPressure {
					req := messages.PredictionRequest{
						TankID:       result.TankID,
						Temperatures: buf.temperatures,
						Densities:    buf.densities,
						Pressure:     buf.pressure,
						CollectedAt:  buf.lastUpdate,
					}

					select {
					case predictionRequestChan <- req:
						buf.hasTemp = false
						buf.hasDensity = false
						buf.hasPressure = false
					case <-ctx.Done():
						bufferMu.Unlock()
						return
					}
				}
				bufferMu.Unlock()
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting rollover prediction service...")
		predictor.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting alarm forwarding service...")
		forwarder.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case result := <-forwardResultChan:
				if result.Success {
					fmt.Printf("Command succeeded: %s for tank %d\n", result.Command.CommandType, result.Command.TankID)
				} else {
					fmt.Printf("Command failed: %s for tank %d: %s\n", result.Command.CommandType, result.Command.TankID, result.Error)
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Printf("Starting API server on %s:%d...\n", cfg.Server.Host, cfg.Server.Port)
		if err := server.Start(); err != nil {
			fmt.Printf("API server error: %v\n", err)
			cancel()
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nReceived shutdown signal, stopping services...")
	cancel()

	wg.Wait()
	fmt.Println("All services stopped gracefully")
}
