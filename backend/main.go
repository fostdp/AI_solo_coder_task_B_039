package main

import (
	"context"
	"fmt"
	"lng-monitoring/alarm_forwarder"
	"lng-monitoring/api"
	"lng-monitoring/bog_diagnoser"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/insulation_monitor"
	"lng-monitoring/messages"
	"lng-monitoring/modbus_poller"
	"lng-monitoring/models"
	"lng-monitoring/multi_tank_scheduler"
	"lng-monitoring/rollover_predictor"
	"lng-monitoring/unloading_predictor"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	modbusPollCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "modbus_poll_total",
			Help: "Total number of Modbus poll operations",
		},
		[]string{"tank_id", "data_type"},
	)

	predictionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "prediction_duration_seconds",
			Help:    "Prediction calculation duration",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tank_id"},
	)

	alarmCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alarm_total",
			Help: "Total number of alarms generated",
		},
		[]string{"tank_id", "alarm_level"},
	)

	activeConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "active_connections",
			Help: "Number of active connections",
		},
		[]string{"module"},
	)

	bogDiagnosticCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bog_diagnostic_total",
			Help: "Total number of BOG diagnostic operations",
		},
		[]string{"tank_id", "compressor_id", "is_anomaly"},
	)

	heatLeakEvaluationCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "heat_leak_evaluation_total",
			Help: "Total number of heat leak evaluations",
		},
		[]string{"tank_id", "is_warning"},
	)

	evaporationLossMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "evaporation_loss_ton_per_hour",
			Help: "Predicted evaporation loss in tons per hour",
		},
		[]string{"optimization_run"},
	)
)

type tankDataBuffer struct {
	temperatures []models.TemperatureData
	densities    []models.DensityData
	pressure     float64
	compressors  []models.BOGCompressorData
	hasTemp      bool
	hasDensity   bool
	hasPressure  bool
	hasCompressor bool
	collectedAt  time.Time
}

func main() {
	go func() {
		fmt.Println("pprof endpoint available at :6060/debug/pprof/")
		http.ListenAndServe(":6060", nil)
	}()

	http.Handle("/metrics", promhttp.Handler())

	cfg := config.LoadWithModelParams("./config/model_params.json")
	if cfg == nil {
		fmt.Println("Warning: Using default config")
		os.Exit(1)
	}

	modelParams, err := config.LoadModelParams("./config/model_params.json")
	if err != nil {
		fmt.Printf("Warning: Failed to load model params: %v\n", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.New(&cfg.Database)
	if err != nil {
		fmt.Printf("Database init failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("Database connected")

	pollResultChan := make(chan modbus_poller.PollResult, 100)
	predictionRequestChan := make(chan messages.PredictionRequest, 10)
	predictionResultChan := make(chan messages.PredictionResult, 10)
	controlCommandChan := make(chan messages.ControlCommand, 10)
	forwardResultChan := make(chan messages.ForwardResult, 10)

	bogBatchChan := make(chan messages.BOGBatch, 10)
	bogDiagnosticResultChan := make(chan messages.BOGDiagnosticResult, 10)
	heatLeakRequestChan := make(chan messages.HeatLeakRequest, 10)
	heatLeakResultChan := make(chan messages.HeatLeakResult, 10)
	unloadingRequestChan := make(chan messages.UnloadingRequest, 10)
	unloadingResultChan := make(chan messages.UnloadingPrediction, 10)
	schedulerRequestChan := make(chan messages.SchedulerRequest, 10)
	scheduleResultChan := make(chan messages.ScheduleResult, 10)

	poller, err := modbus_poller.NewPoller(&cfg.Modbus, pollResultChan)
	if err != nil {
		fmt.Printf("Modbus poller init failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Modbus poller initialized")

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

	bogDiagnostic := bog_diagnoser.NewBOGDiagnoserService(
		cfg,
		db,
		bogBatchChan,
		bogDiagnosticResultChan,
	)
	fmt.Println("BOG diagnostic service initialized")

	heatLeakEvaluator := insulation_monitor.NewInsulationMonitorService(
		cfg,
		db,
		heatLeakRequestChan,
		heatLeakResultChan,
	)
	fmt.Println("Heat leak evaluator initialized")

	unloadingPredictor := unloading_predictor.NewUnloadingPredictor(
		cfg,
		db,
		unloadingRequestChan,
		unloadingResultChan,
	)
	fmt.Println("Unloading predictor initialized")

	multiTankScheduler := multi_tank_scheduler.NewMultiTankScheduler(
		cfg,
		db,
		schedulerRequestChan,
		scheduleResultChan,
	)
	fmt.Println("Multi-tank scheduler initialized")

	server := api.NewServer(
		cfg,
		db,
		nil,
		nil,
		bogDiagnostic,
		heatLeakEvaluator,
		unloadingPredictor,
		multiTankScheduler,
	)
	fmt.Println("API server initialized")

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		poller.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		predictor.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		forwarder.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		bogDiagnostic.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		heatLeakEvaluator.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		unloadingPredictor.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		multiTankScheduler.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := server.Start(); err != nil {
			fmt.Printf("API server error: %v\n", err)
		}
	}()

	tankBuffers := make(map[int]*tankDataBuffer)
	for i := 1; i <= 4; i++ {
		tankBuffers[i] = &tankDataBuffer{}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case result := <-pollResultChan:
				modbusPollCount.WithLabelValues(
					fmt.Sprintf("%d", result.TankID),
					result.DataType,
				).Inc()

				buf, exists := tankBuffers[result.TankID]
				if !exists {
					buf = &tankDataBuffer{}
					tankBuffers[result.TankID] = buf
				}
				buf.collectedAt = result.CollectedAt

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
					if pressureData, ok := result.Data.([]models.PressureData); ok {
						if len(pressureData) > 0 {
							buf.pressure = pressureData[0].Pressure
							buf.hasPressure = true
							go db.InsertPressureData(ctx, pressureData)
						}
					}
				case "compressor":
					if compData, ok := result.Data.([]models.BOGCompressorData); ok {
						buf.compressors = compData
						buf.hasCompressor = true
						go db.InsertBOGCompressorData(ctx, compData)

						bogBatch := messages.BOGBatch{
							TankID:      result.TankID,
							Data:        compData,
							CollectedAt: result.CollectedAt,
						}
						select {
						case bogBatchChan <- bogBatch:
						default:
							fmt.Printf("BOG batch channel full, skipping tank %d\n", result.TankID)
						}
					}
				}

				if buf.hasTemp && buf.hasDensity && buf.hasPressure {
					req := messages.PredictionRequest{
						TankID:       result.TankID,
						Temperatures: buf.temperatures,
						Densities:    buf.densities,
						Pressure:     buf.pressure,
						CollectedAt:  buf.collectedAt,
					}

					select {
					case predictionRequestChan <- req:
						buf.hasTemp = false
						buf.hasDensity = false
						buf.hasPressure = false
					default:
						fmt.Printf("Prediction channel full, skipping tank %d\n", result.TankID)
					}
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case result := <-predictionResultChan:
				if result.ErrorMessage == "" {
					predictionDuration.WithLabelValues(
						fmt.Sprintf("%d", result.TankID),
					).Observe(time.Since(result.PredictedAt).Seconds())
				}
			case result := <-forwardResultChan:
				if result.Success {
					alarmCount.WithLabelValues(
						fmt.Sprintf("%d", result.TankID),
						result.AlarmLevel,
					).Inc()
				}
			case result := <-bogDiagnosticResultChan:
				if result.ErrorMessage == "" {
					isAnomalyStr := "false"
					if result.IsAnomaly {
						isAnomalyStr = "true"
					}
					bogDiagnosticCount.WithLabelValues(
						fmt.Sprintf("%d", result.TankID),
						fmt.Sprintf("%d", result.CompressorID),
						isAnomalyStr,
					).Inc()

					diag := &models.BOGDiagnostic{
						Time:           result.DiagnosedAt,
						TankID:         result.TankID,
						CompressorID:   result.CompressorID,
						AnomalyScore:   result.AnomalyScore,
						IsAnomaly:      result.IsAnomaly,
						AnomalyType:    result.AnomalyType,
						Confidence:     result.Confidence,
						RemainingHours: result.RemainingHours,
						Recommendation: result.Recommendation,
						ModelVersion:   "1.0.0",
					}
					go db.InsertBOGDiagnostic(ctx, diag)
				}
			case result := <-heatLeakResultChan:
				if result.ErrorMessage == "" {
					isWarningStr := "false"
					if result.IsWarning {
						isWarningStr = "true"
					}
					heatLeakEvaluationCount.WithLabelValues(
						fmt.Sprintf("%d", result.TankID),
						isWarningStr,
					).Inc()

					assessment := &models.HeatLeakAssessment{
						Time:                   result.EvaluatedAt,
						TankID:                 result.TankID,
						EquivalentConductivity: result.EquivalentConductivity,
						InsulationPerformance:  result.InsulationPerformance,
						HeatLeakRate:           result.HeatLeakRate,
						LeakRegions:            result.LeakRegion,
						IsWarning:              result.IsWarning,
						TotalHeatLoadKW:        result.TotalHeatLoadKW,
						ModelVersion:           "1.0.0",
					}
					go db.InsertHeatLeakAssessment(ctx, assessment)
				}
			case result := <-unloadingResultChan:
				if result.ErrorMessage == "" {
					pred := &models.UnloadingPredictionModel{
						Time:              result.PredictedAt,
						TankID:            result.TankID,
						MaxTempDiff:       result.MaxTempDiff,
						MaxDensityDiff:    result.MaxDensityDiff,
						OptimalPumpOnTime: result.OptimalPumpOnTime,
						RolloverRisk:      result.RolloverRisk,
						TimeSteps:         result.TimeSteps,
						PredictedTemps:    result.PredictedTemps,
						PredictedDensities: result.PredictedDensities,
						ModelVersion:      "1.0.0",
					}
					go db.InsertUnloadingPrediction(ctx, pred)
				}
			case result := <-scheduleResultChan:
				if result.ErrorMessage == "" {
					evaporationLossMetric.WithLabelValues("latest").Set(result.EvaporationLoss)

					compressorLoadsInt := make(map[string]int)
					for k, v := range result.CompressorLoads {
						compressorLoadsInt[k] = int(v)
					}

					pumpSchedules := make([]models.PumpSchedule, len(result.PumpOperations))
					for i, op := range result.PumpOperations {
						pumpSchedules[i] = models.PumpSchedule{
							TankID:    op.TankID,
							PumpID:    op.PumpID,
							StartTime: op.StartTime,
							Duration:  op.Duration,
							Action:    op.Action,
						}
					}

					schedule := &models.MultiTankSchedule{
						Time:               result.OptimizedAt,
						CompressorLoads:    compressorLoadsInt,
						PumpOperations:     pumpSchedules,
						EvaporationLossKg:  result.EvaporationLoss * 1000,
						EvaporationLossM3:  result.EvaporationLoss / 0.425,
						OptimizationStatus: "success",
						ModelVersion:       "1.0.0",
					}
					go db.InsertMultiTankSchedule(ctx, schedule)
				}
			case <-controlCommandChan:
			}
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\n=== LNG储罐翻滚预测系统已启动 ===")
	fmt.Println("API: http://localhost:8080")
	fmt.Println("Metrics: http://localhost:8080/metrics")
	fmt.Println("pprof: http://localhost:6060/debug/pprof/")
	fmt.Println("Press Ctrl+C to stop\n")

	<-sigChan
	fmt.Println("\nShutting down...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		fmt.Printf("API server shutdown error: %v\n", err)
	}

	wg.Wait()
	fmt.Println("All services stopped")
}
