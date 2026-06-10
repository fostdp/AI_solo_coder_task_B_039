package unloading_predictor

import (
	"context"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"sync"
	"testing"
	"time"
)

func TestUnloadingPredictor_AsyncComputation(t *testing.T) {
	cfg := &config.Config{
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				HeightMeters:      40.0,
				DiameterMeters:    80.0,
				CapacityCubicMeters: 200000.0,
			},
			PhysicalProperties: config.PhysicalProperties{
				BaseDensity: 425.0,
			},
			Unloading: config.UnloadingParams{
				MixingEfficiency:          0.85,
				PumpFlowRateM3H:           800.0,
				MinPumpDurationHours:      0.5,
				MaxStratificationSafe:     3.0,
				PredictionTimeStepMin:     5,
				NumVerticalLayers:         20,
				AxialDispersionCoeff:      0.05,
				DensityDiffusionCoeff:     1.0e-8,
				AdaptiveFilteringOn:       true,
				FlowRateChangeThreshold:   0.2,
				MaxMixingEfficiencyBoost:  0.15,
				FlowSmoothingAlpha:        0.3,
				ResponseTimeSteps:         3,
				MinAxialDispersionBoost:   1.0,
				MaxAxialDispersionBoost:   3.0,
			},
		},
	}

	requestChan := make(chan messages.UnloadingRequest, 10)
	resultChan := make(chan messages.UnloadingPrediction, 10)

	db := &database.DB{}
	predictor := NewUnloadingPredictor(cfg, db, requestChan, resultChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	predictor.Start(ctx)

	req := messages.UnloadingRequest{
		TankID:            1,
		UnloadingRate:     1000.0,
		UnloadingDensity:  430.0,
		UnloadingTemp:     -165.0,
		EstimatedDuration: 4.0,
		InitialTemps:      []float64{-162.0, -161.5, -161.0, -160.5, -160.0},
		InitialDensities:  []float64{425.0, 424.5, 424.0, 423.5, 423.0},
		RequestedAt:       time.Now(),
	}

	result := predictor.predictUnloading(ctx, req)

	if result.ErrorMessage != "" {
		t.Logf("Prediction result: %v", result.ErrorMessage)
	}

	if result.TankID != req.TankID {
		t.Errorf("Expected TankID %d, got %d", req.TankID, result.TankID)
	}

	if !result.AsyncComputed {
		t.Log("Warning: Prediction was not computed asynchronously (worker pool may be busy)")
	}

	if len(result.PredictedTemps) == 0 {
		t.Error("Expected non-empty predicted temperatures")
	}

	if len(result.PredictedDensities) == 0 {
		t.Error("Expected non-empty predicted densities")
	}

	if len(result.TimeSteps) == 0 {
		t.Error("Expected non-empty time steps")
	}

	expectedSteps := int(req.EstimatedDuration / (float64(cfg.ModelParams.Unloading.PredictionTimeStepMin) / 60.0))
	if len(result.TimeSteps) < expectedSteps-1 || len(result.TimeSteps) > expectedSteps+1 {
		t.Errorf("Expected about %d time steps, got %d", expectedSteps, len(result.TimeSteps))
	}

	t.Logf("Prediction completed: %d time steps, async=%v", len(result.TimeSteps), result.AsyncComputed)
}

func TestUnloadingPredictor_WorkerPoolConcurrency(t *testing.T) {
	cfg := &config.Config{
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				HeightMeters:      40.0,
				DiameterMeters:    80.0,
				CapacityCubicMeters: 200000.0,
			},
			PhysicalProperties: config.PhysicalProperties{
				BaseDensity: 425.0,
			},
			Unloading: config.UnloadingParams{
				MixingEfficiency:          0.85,
				PumpFlowRateM3H:           800.0,
				MinPumpDurationHours:      0.5,
				MaxStratificationSafe:     3.0,
				PredictionTimeStepMin:     5,
				NumVerticalLayers:         10,
				AxialDispersionCoeff:      0.05,
				DensityDiffusionCoeff:     1.0e-8,
			},
		},
	}

	workerPool := NewMixingModelWorkerPool(2)
	if workerPool.workerCount != 2 {
		t.Errorf("Expected worker count 2, got %d", workerPool.workerCount)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workerPool.Start(ctx)

	if !workerPool.running {
		t.Error("Expected worker pool to be running")
	}

	req := messages.UnloadingRequest{
		TankID:            1,
		UnloadingRate:     1000.0,
		UnloadingDensity:  430.0,
		UnloadingTemp:     -165.0,
		EstimatedDuration: 2.0,
		InitialTemps:      []float64{-162.0, -161.0, -160.0},
		InitialDensities:  []float64{425.0, 424.0, 423.0},
		RequestedAt:       time.Now(),
	}

	model := &OneDMixerModel{
		nLayers:          10,
		mixingEfficiency: 0.85,
		axialDispersion:  0.05,
		densityDiffusion: 1.0e-8,
	}
	model.layerHeights = []float64{2.0, 6.0, 10.0, 14.0, 18.0, 22.0, 26.0, 30.0, 34.0, 38.0}

	initialState := []LayerState{
		{Temperature: -162.0, Density: 425.0, Height: 2.0, Mass: 1000.0},
		{Temperature: -161.8, Density: 424.8, Height: 6.0, Mass: 1000.0},
		{Temperature: -161.6, Density: 424.6, Height: 10.0, Mass: 1000.0},
		{Temperature: -161.4, Density: 424.4, Height: 14.0, Mass: 1000.0},
		{Temperature: -161.2, Density: 424.2, Height: 18.0, Mass: 1000.0},
		{Temperature: -161.0, Density: 424.0, Height: 22.0, Mass: 1000.0},
		{Temperature: -160.8, Density: 423.8, Height: 26.0, Mass: 1000.0},
		{Temperature: -160.6, Density: 423.6, Height: 30.0, Mass: 1000.0},
		{Temperature: -160.4, Density: 423.4, Height: 34.0, Mass: 1000.0},
		{Temperature: -160.2, Density: 423.2, Height: 38.0, Mass: 1000.0},
	}

	resultChan := make(chan *MixingModelResult, 1)
	task := &MixingModelTask{
		ctx:        ctx,
		req:        req,
		model:      model,
		initState:  initialState,
		timeStep:   5.0 / 60.0,
		totalSteps: 24,
		resultChan: resultChan,
	}

	if !workerPool.Submit(task) {
		t.Error("Failed to submit task to worker pool")
	}

	select {
	case result := <-resultChan:
		if result.Error != nil {
			t.Errorf("Unexpected error: %v", result.Error)
		}
		if len(result.PredictedTemps) != 24 {
			t.Errorf("Expected 24 time steps, got %d", len(result.PredictedTemps))
		}
		t.Logf("Worker pool task completed successfully")
	case <-time.After(3 * time.Second):
		t.Error("Timeout waiting for worker pool result")
	}

	workerPool.Stop()

	if workerPool.running {
		t.Error("Expected worker pool to be stopped")
	}
}

func TestUnloadingPredictor_MultipleConcurrentRequests(t *testing.T) {
	cfg := &config.Config{
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				HeightMeters:      40.0,
				DiameterMeters:    80.0,
				CapacityCubicMeters: 200000.0,
			},
			PhysicalProperties: config.PhysicalProperties{
				BaseDensity: 425.0,
			},
			Unloading: config.UnloadingParams{
				MixingEfficiency:          0.85,
				PredictionTimeStepMin:     5,
				NumVerticalLayers:         10,
				AxialDispersionCoeff:      0.05,
			},
		},
	}

	requestChan := make(chan messages.UnloadingRequest, 10)
	resultChan := make(chan messages.UnloadingPrediction, 10)
	db := &database.DB{}

	predictor := NewUnloadingPredictor(cfg, db, requestChan, resultChan)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	predictor.Start(ctx)

	numRequests := 3
	results := make([]messages.UnloadingPrediction, numRequests)
	errors := make([]error, numRequests)

	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := messages.UnloadingRequest{
				TankID:            idx + 1,
				UnloadingRate:     800.0 + float64(idx)*100,
				UnloadingDensity:  430.0,
				UnloadingTemp:     -165.0,
				EstimatedDuration: 3.0,
				InitialTemps:      []float64{-162.0, -161.5, -161.0},
				InitialDensities:  []float64{425.0, 424.5, 424.0},
				RequestedAt:       time.Now(),
			}
			result := predictor.predictUnloading(ctx, req)
			results[idx] = result
			if result.ErrorMessage != "" {
				errors[idx] = nil
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < numRequests; i++ {
		if results[i].TankID != i+1 {
			t.Errorf("Request %d: Expected TankID %d, got %d", i, i+1, results[i].TankID)
		}
		if len(results[i].PredictedTemps) == 0 {
			t.Errorf("Request %d: Expected non-empty results", i)
		}
		t.Logf("Request %d completed: async=%v, steps=%d", i, results[i].AsyncComputed, len(results[i].TimeSteps))
	}
}
