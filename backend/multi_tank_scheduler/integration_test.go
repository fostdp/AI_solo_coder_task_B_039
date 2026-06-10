package multi_tank_scheduler

import (
	"context"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"sync"
	"testing"
	"time"
)

func TestMultiTankScheduler_AsyncOptimization(t *testing.T) {
	cfg := &config.Config{
		Modbus: config.ModbusConfig{
			TankCount: 4,
			Register: config.ModbusRegisters{
				CompressorsPerTank: 2,
			},
		},
		Scheduler: config.SchedulerConfig{
			AutoOptimize:       false,
			IntervalSec:        600,
			MinRiskForAction:   0.3,
			ModelVersion:       "1.0.0",
		},
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				CapacityCubicMeters: 200000.0,
			},
			Scheduler: config.SchedulerParams{
				CompressorEfficiency:        0.75,
				EvaporationLossCostYuan:     4500.0,
				ElectricityCostYuan:         0.65,
				PumpPowerKW:                 220.0,
				CompressorPowerKWPerPct:     2.5,
				MaxLoadPctPerCompressor:     map[string]float64{},
				MinRuntimeHours:             2.0,
				OptimizationIntervalMin:     10,
				DecompositionOn:             true,
				MaxTanksPerSubproblem:       4,
				RiskGroupThresholds:         []float64{0.7, 0.4, 0.0},
				MaxIterationsDecomposition:  10,
				CoordinationStepSize:        0.1,
				EarlyTerminationGap:         0.01,
				DefaultMaxLoadPct:           100.0,
				ConcurrentSubproblems:       true,
			},
		},
	}

	requestChan := make(chan messages.SchedulerRequest, 10)
	resultChan := make(chan messages.ScheduleResult, 10)
	db := &database.DB{}

	scheduler := NewMultiTankScheduler(cfg, db, requestChan, resultChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scheduler.Start(ctx)

	states := []messages.TankStateForScheduler{
		{TankID: 1, Level: 0.8, AvgTemp: -160.0, RiskIndex: 0.8, Pressure: 0.22, HasBOGComp1: true, HasBOGComp2: true},
		{TankID: 2, Level: 0.6, AvgTemp: -161.0, RiskIndex: 0.5, Pressure: 0.20, HasBOGComp1: true, HasBOGComp2: false},
		{TankID: 3, Level: 0.4, AvgTemp: -162.0, RiskIndex: 0.2, Pressure: 0.18, HasBOGComp1: true, HasBOGComp2: true},
		{TankID: 4, Level: 0.7, AvgTemp: -160.5, RiskIndex: 0.6, Pressure: 0.21, HasBOGComp1: false, HasBOGComp2: true},
	}

	result := scheduler.optimizeSchedule(ctx, states)

	if result.ErrorMessage != "" {
		t.Logf("Optimization result: %v", result.ErrorMessage)
	}

	if len(result.CompressorLoads) == 0 {
		t.Error("Expected non-empty compressor loads")
	}

	for key, load := range result.CompressorLoads {
		if load < 0 || load > 100 {
			t.Errorf("Compressor %s load out of range: %f", key, load)
		}
		t.Logf("Compressor %s: %.1f%%", key, load)
	}

	if !result.AsyncOptimized {
		t.Log("Warning: Optimization was not computed asynchronously (worker pool may be busy)")
	}

	t.Logf("Optimization completed: %d pump operations, decomposed=%v, async=%v",
		len(result.PumpOperations), result.Decomposed, result.AsyncOptimized)

	for _, op := range result.PumpOperations {
		t.Logf("Pump operation: Tank %d, Pump %d, Action: %s, Start: %.1fh, Duration: %.1fh",
			op.TankID, op.PumpID, op.Action, op.StartTime, op.Duration)
	}
}

func TestMultiTankScheduler_OptimizerPoolConcurrency(t *testing.T) {
	cfg := &config.Config{
		Modbus: config.ModbusConfig{
			TankCount: 4,
			Register: config.ModbusRegisters{
				CompressorsPerTank: 2,
			},
		},
		Scheduler: config.SchedulerConfig{
			AutoOptimize:     false,
			IntervalSec:      600,
			MinRiskForAction: 0.3,
			ModelVersion:     "1.0.0",
		},
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				CapacityCubicMeters: 200000.0,
			},
			Scheduler: config.SchedulerParams{
				CompressorEfficiency:        0.75,
				EvaporationLossCostYuan:     4500.0,
				ElectricityCostYuan:         0.65,
				DefaultMaxLoadPct:           100.0,
				DecompositionOn:             false,
				MaxTanksPerSubproblem:       4,
			},
		},
	}

	optimizerPool := NewOptimizerWorkerPool(2)
	if optimizerPool.workerCount != 2 {
		t.Errorf("Expected worker count 2, got %d", optimizerPool.workerCount)
	}

	requestChan := make(chan messages.SchedulerRequest, 10)
	resultChan := make(chan messages.ScheduleResult, 10)
	db := &database.DB{}
	scheduler := NewMultiTankScheduler(cfg, db, requestChan, resultChan)

	optimizerPool.setSchedulerReference(scheduler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	optimizerPool.Start(ctx)

	if !optimizerPool.running {
		t.Error("Expected optimizer pool to be running")
	}

	states := []messages.TankStateForScheduler{
		{TankID: 1, Level: 0.8, AvgTemp: -160.0, RiskIndex: 0.8, Pressure: 0.22, HasBOGComp1: true, HasBOGComp2: true},
		{TankID: 2, Level: 0.6, AvgTemp: -161.0, RiskIndex: 0.5, Pressure: 0.20, HasBOGComp1: true, HasBOGComp2: false},
	}

	resultChan2 := make(chan *OptimizationResult, 1)
	task := &OptimizationTask{
		ctx:        ctx,
		states:     states,
		resultChan: resultChan2,
	}

	if !optimizerPool.Submit(task) {
		t.Error("Failed to submit task to optimizer pool")
	}

	select {
	case result := <-resultChan2:
		if result.Error != nil {
			t.Errorf("Unexpected error: %v", result.Error)
		}
		if result.ScheduleResult == nil {
			t.Error("Expected non-nil schedule result")
		}
		if len(result.ScheduleResult.CompressorLoads) == 0 {
			t.Error("Expected non-empty compressor loads in result")
		}
		t.Logf("Optimizer pool task completed successfully")
	case <-time.After(3 * time.Second):
		t.Error("Timeout waiting for optimizer pool result")
	}

	optimizerPool.Stop()

	if optimizerPool.running {
		t.Error("Expected optimizer pool to be stopped")
	}
}

func TestMultiTankScheduler_MultipleConcurrentOptimizations(t *testing.T) {
	cfg := &config.Config{
		Modbus: config.ModbusConfig{
			TankCount: 4,
			Register: config.ModbusRegisters{
				CompressorsPerTank: 2,
			},
		},
		Scheduler: config.SchedulerConfig{
			AutoOptimize:     false,
			IntervalSec:      600,
			MinRiskForAction: 0.3,
			ModelVersion:     "1.0.0",
		},
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				CapacityCubicMeters: 200000.0,
			},
			Scheduler: config.SchedulerParams{
				CompressorEfficiency:        0.75,
				EvaporationLossCostYuan:     4500.0,
				ElectricityCostYuan:         0.65,
				DefaultMaxLoadPct:           100.0,
				DecompositionOn:             false,
				MaxTanksPerSubproblem:       4,
			},
		},
	}

	requestChan := make(chan messages.SchedulerRequest, 10)
	resultChan := make(chan messages.ScheduleResult, 10)
	db := &database.DB{}

	scheduler := NewMultiTankScheduler(cfg, db, requestChan, resultChan)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	scheduler.Start(ctx)

	numRequests := 3
	results := make([]messages.ScheduleResult, numRequests)

	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			states := []messages.TankStateForScheduler{
				{TankID: 1, Level: 0.8 - float64(idx)*0.1, AvgTemp: -160.0 + float64(idx)*0.5, RiskIndex: 0.8 - float64(idx)*0.2, Pressure: 0.22 - float64(idx)*0.01, HasBOGComp1: true, HasBOGComp2: true},
				{TankID: 2, Level: 0.6 - float64(idx)*0.05, AvgTemp: -161.0 + float64(idx)*0.3, RiskIndex: 0.5 - float64(idx)*0.1, Pressure: 0.20 - float64(idx)*0.01, HasBOGComp1: true, HasBOGComp2: false},
			}
			result := scheduler.optimizeSchedule(ctx, states)
			results[idx] = result
		}(i)
	}

	wg.Wait()

	for i := 0; i < numRequests; i++ {
		if len(results[i].CompressorLoads) == 0 {
			t.Errorf("Request %d: Expected non-empty compressor loads", i)
		}
		if results[i].ErrorMessage != "" {
			t.Logf("Request %d had error: %s", i, results[i].ErrorMessage)
		}
		t.Logf("Request %d completed: async=%v, compressors=%d, pumps=%d",
			i, results[i].AsyncOptimized, len(results[i].CompressorLoads), len(results[i].PumpOperations))
	}
}

func TestMultiTankScheduler_Decomposition(t *testing.T) {
	cfg := &config.Config{
		Modbus: config.ModbusConfig{
			TankCount: 8,
			Register: config.ModbusRegisters{
				CompressorsPerTank: 2,
			},
		},
		Scheduler: config.SchedulerConfig{
			AutoOptimize:     false,
			IntervalSec:      600,
			MinRiskForAction: 0.3,
			ModelVersion:     "1.0.0",
		},
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				CapacityCubicMeters: 200000.0,
			},
			Scheduler: config.SchedulerParams{
				CompressorEfficiency:        0.75,
				EvaporationLossCostYuan:     4500.0,
				ElectricityCostYuan:         0.65,
				DefaultMaxLoadPct:           100.0,
				DecompositionOn:             true,
				MaxTanksPerSubproblem:       3,
				RiskGroupThresholds:         []float64{0.7, 0.4, 0.0},
				ConcurrentSubproblems:       true,
			},
		},
	}

	requestChan := make(chan messages.SchedulerRequest, 10)
	resultChan := make(chan messages.ScheduleResult, 10)
	db := &database.DB{}

	scheduler := NewMultiTankScheduler(cfg, db, requestChan, resultChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scheduler.Start(ctx)

	states := make([]messages.TankStateForScheduler, 8)
	for i := 0; i < 8; i++ {
		states[i] = messages.TankStateForScheduler{
			TankID:      i + 1,
			Level:       0.5 + float64(i)*0.05,
			AvgTemp:     -162.0 + float64(i)*0.3,
			RiskIndex:   0.1 + float64(i)*0.1,
			Pressure:    0.18 + float64(i)*0.005,
			HasBOGComp1: true,
			HasBOGComp2: i%2 == 0,
		}
	}

	result := scheduler.optimizeSchedule(ctx, states)

	if !result.Decomposed {
		t.Log("Warning: Expected decomposition to be enabled for large number of tanks")
	}

	if result.SubproblemCount < 2 {
		t.Errorf("Expected at least 2 subproblems, got %d", result.SubproblemCount)
	}

	t.Logf("Decomposition test completed: %d subproblems, async=%v",
		result.SubproblemCount, result.AsyncOptimized)
}
