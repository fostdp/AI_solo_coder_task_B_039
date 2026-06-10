package unloading_predictor

import (
	"fmt"
	"math"
	"testing"
	"time"

	"lng-monitoring/config"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"lng-monitoring/testutils"
)

func generatePredictionRequest(
	tankID int,
	unloadingRate float64,
	unloadingDensity float64,
	unloadingTemp float64,
	duration float64,
	nLayers int,
) messages.UnloadingRequest {
	initialTemps := make([]float64, nLayers)
	initialDensities := make([]float64, nLayers)

	for i := 0; i < nLayers; i++ {
		heightRatio := float64(i) / float64(nLayers-1)
		initialTemps[i] = -160.0 + heightRatio*1.5
		initialDensities[i] = 425.0 - heightRatio*2.0
	}

	return messages.UnloadingRequest{
		TankID:            tankID,
		UnloadingRate:     unloadingRate,
		UnloadingDensity:  unloadingDensity,
		UnloadingTemp:     unloadingTemp,
		EstimatedDuration: duration,
		InitialTemps:      initialTemps,
		InitialDensities:  initialDensities,
		InitialLevel:      0.5,
	}
}

func generateGroundTruth(
	req messages.UnloadingRequest,
	nLayers int,
	noiseStd float64,
) ([][]float64, [][]float64) {
	timeStep := 5.0 / 60.0
	totalSteps := int(req.EstimatedDuration / timeStep)

	temps := make([][]float64, totalSteps)
	densities := make([][]float64, totalSteps)

	initialTemps := make([]float64, nLayers)
	initialDensities := make([]float64, nLayers)
	for i := 0; i < nLayers; i++ {
		heightRatio := float64(i) / float64(nLayers-1)
		initialTemps[i] = -160.0 + heightRatio*1.5
		initialDensities[i] = 425.0 - heightRatio*2.0
	}

	injectionLayer := nLayers / 2

	for t := 0; t < totalSteps; t++ {
		temps[t] = make([]float64, nLayers)
		densities[t] = make([]float64, nLayers)

		timeFactor := float64(t) / float64(totalSteps)

		for i := 0; i < nLayers; i++ {
			tempDiff := initialTemps[i] - req.UnloadingTemp
			densDiff := initialDensities[i] - req.UnloadingDensity

			layerFactor := 1.0 - math.Abs(float64(i)-float64(injectionLayer))/float64(nLayers)
			mixingFactor := timeFactor * layerFactor * 0.8

			temps[t][i] = initialTemps[i]*(1-mixingFactor) + req.UnloadingTemp*mixingFactor
			densities[t][i] = initialDensities[i]*(1-mixingFactor) + req.UnloadingDensity*mixingFactor

			if noiseStd > 0 {
				temps[t][i] += rand.NormFloat64() * noiseStd
				densities[t][i] += rand.NormFloat64() * noiseStd
			}
		}
	}

	return temps, densities
}

func TestTemperaturePredictionRMSE(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20

	testCases := []struct {
		name           string
		rate           float64
		temp           float64
		duration       float64
		maxRMSE        float64
	}{
		{"Normal rate, cold LNG", 800, -162, 12, 2.0},
		{"Low rate, warm LNG", 400, -158, 24, 2.5},
		{"High rate, very cold", 1200, -163, 8, 3.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := generatePredictionRequest(1, tc.rate, 425, tc.temp, tc.duration, nLayers)
			groundTruthTemps, groundTruthDensities := generateGroundTruth(req, nLayers, 0.0)

			prediction := predictor.predictUnloading(nil, req)

			if prediction.ErrorMessage != "" {
				t.Fatalf("Prediction error: %s", prediction.ErrorMessage)
			}

			allPredTemps := []float64{}
			allActualTemps := []float64{}
			allPredDens := []float64{}
			allActualDens := []float64{}

			nSteps := len(prediction.PredictedTemps)
			groundSteps := len(groundTruthTemps)

			for tStep := 0; tStep < nSteps; tStep++ {
				groundIdx := int(float64(tStep) / float64(nSteps) * float64(groundSteps))
				if groundIdx >= groundSteps {
					groundIdx = groundSteps - 1
				}

				for layer := 0; layer < nLayers; layer++ {
					predTemp := prediction.PredictedTemps[tStep][layer]
					actualTemp := groundTruthTemps[groundIdx][layer]
					allPredTemps = append(allPredTemps, predTemp)
					allActualTemps = append(allActualTemps, actualTemp)

					predDens := prediction.PredictedDensities[tStep][layer]
					actualDens := groundTruthDensities[groundIdx][layer]
					allPredDens = append(allPredDens, predDens)
					allActualDens = append(allActualDens, actualDens)
				}
			}

			tempRMSE := testutils.RMSE(allPredTemps, allActualTemps)
			densityRMSE := testutils.RMSE(allPredDens, allActualDens)

			tempMAE := testutils.MAE(allPredTemps, allActualTemps)
			densityMAE := testutils.MAE(allPredDens, allActualDens)

			t.Logf("%s: Temp RMSE=%.4f℃, MAE=%.4f℃ | Density RMSE=%.4f kg/m³, MAE=%.4f kg/m³",
				tc.name, tempRMSE, tempMAE, densityRMSE, densityMAE)

			if tempRMSE > tc.maxRMSE {
				t.Errorf("%s: Temp RMSE %.4f exceeds max allowed %.4f", tc.name, tempRMSE, tc.maxRMSE)
			}

			if densityRMSE > 5.0 {
				t.Errorf("%s: Density RMSE %.4f exceeds max allowed 5.0", tc.name, densityRMSE)
			}
		})
	}
}

func TestPumpOpeningTimeliness(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	safeTempDiff := 3.0
	safeDensityDiff := 2.0

	testCases := []struct {
		name           string
		rate           float64
		unloadingTemp  float64
		unloadingDens  float64
		maxDelayHours  float64
	}{
		{"Large temp difference", 800, -155, 420, 1.0},
		{"Large density difference", 800, -160, 415, 1.5},
		{"Both differences", 800, -155, 415, 0.5},
		{"Small differences, slow rate", 400, -158, 422, 3.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := generatePredictionRequest(1, tc.rate, tc.unloadingDens, tc.unloadingTemp, 12, nLayers)
			prediction := predictor.predictUnloading(nil, req)

			if prediction.ErrorMessage != "" {
				t.Fatalf("Prediction error: %s", prediction.ErrorMessage)
			}

			firstViolationTime := -1.0
			for tStep := 0; tStep < len(prediction.TimeSteps); tStep++ {
				temps := prediction.PredictedTemps[tStep]
				densities := prediction.PredictedDensities[tStep]

				minT, maxT := temps[0], temps[0]
				minD, maxD := densities[0], densities[0]
				for i := 1; i < len(temps); i++ {
					if temps[i] < minT {
						minT = temps[i]
					}
					if temps[i] > maxT {
						maxT = temps[i]
					}
					if densities[i] < minD {
						minD = densities[i]
					}
					if densities[i] > maxD {
						maxD = densities[i]
					}
				}

				if (maxT-minT > safeTempDiff) || (maxD-minD > safeDensityDiff) {
					firstViolationTime = prediction.TimeSteps[tStep]
					break
				}
			}

			t.Logf("%s: Predicted pump time=%.2fh, First violation=%.2fh, MaxTempDiff=%.2f℃, MaxDensityDiff=%.2f kg/m³",
				tc.name, prediction.OptimalPumpOnTime, firstViolationTime,
				prediction.MaxTempDiff, prediction.MaxDensityDiff)

			if firstViolationTime >= 0 {
				delay := prediction.OptimalPumpOnTime - firstViolationTime
				if delay > tc.maxDelayHours {
					t.Errorf("%s: Pump opening delay %.2fh exceeds max allowed %.2fh",
						tc.name, delay, tc.maxDelayHours)
				}
				if prediction.OptimalPumpOnTime > firstViolationTime+0.5 {
					t.Errorf("%s: Pump should open before or at violation time. Predicted=%.2fh, Violation=%.2fh",
						tc.name, prediction.OptimalPumpOnTime, firstViolationTime)
				}
			}

			if prediction.OptimalPumpOnTime < 0 {
				t.Errorf("%s: Negative pump time: %.2fh", tc.name, prediction.OptimalPumpOnTime)
			}
			if prediction.OptimalPumpOnTime > req.EstimatedDuration {
				t.Errorf("%s: Pump time %.2fh exceeds duration %.2fh",
					tc.name, prediction.OptimalPumpOnTime, req.EstimatedDuration)
			}
		})
	}
}

func TestUnloadingFlowRateAdaptability(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20

	flowRates := []float64{200, 400, 600, 800, 1000, 1500, 2000}

	for _, rate := range flowRates {
		t.Run(fmt.Sprintf("Flow rate %.0f m³/h", rate), func(t *testing.T) {
			req := generatePredictionRequest(1, rate, 425, -160, 12, nLayers)
			prediction := predictor.predictUnloading(nil, req)

			if prediction.ErrorMessage != "" {
				t.Fatalf("Prediction error for rate %.0f: %s", rate, prediction.ErrorMessage)
			}

			t.Logf("Rate %.0f: MaxTempDiff=%.2f℃, MaxDensityDiff=%.2f kg/m³, PumpTime=%.2fh, RolloverRisk=%.2f%%",
				rate, prediction.MaxTempDiff, prediction.MaxDensityDiff,
				prediction.OptimalPumpOnTime, prediction.RolloverRisk*100)

			if prediction.MaxTempDiff < 0 {
				t.Errorf("Negative temperature difference for rate %.0f", rate)
			}
			if prediction.MaxDensityDiff < 0 {
				t.Errorf("Negative density difference for rate %.0f", rate)
			}
			if prediction.RolloverRisk < 0 || prediction.RolloverRisk > 1 {
				t.Errorf("Invalid rollover risk for rate %.0f: %.4f", rate, prediction.RolloverRisk)
			}

			nSteps := len(prediction.PredictedTemps)
			for tStep := 0; tStep < nSteps; tStep++ {
				for layer := 0; layer < nLayers; layer++ {
					temp := prediction.PredictedTemps[tStep][layer]
					density := prediction.PredictedDensities[tStep][layer]

					if temp < -170 || temp > -150 {
						t.Errorf("Temperature out of range for rate %.0f at step %d, layer %d: %.2f",
							rate, tStep, layer, temp)
					}
					if density < 400 || density > 460 {
						t.Errorf("Density out of range for rate %.0f at step %d, layer %d: %.2f",
							rate, tStep, layer, density)
					}
				}
			}
		})
	}
}

func TestMassConservation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	req := generatePredictionRequest(1, 800, 425, -160, 6, nLayers)
	prediction := predictor.predictUnloading(nil, req)

	if prediction.ErrorMessage != "" {
		t.Fatalf("Prediction error: %s", prediction.ErrorMessage)
	}

	tankRadius := cfg.ModelParams.TankSpecs.DiameterMeters / 2.0
	tankHeight := cfg.ModelParams.TankSpecs.HeightMeters
	tankArea := math.Pi * tankRadius * tankRadius
	layerHeight := tankHeight / float64(nLayers)
	layerVolume := tankArea * layerHeight

	initialMass := 0.0
	for i := 0; i < nLayers; i++ {
		initialMass += layerVolume * req.InitialDensities[i]
	}

	finalMass := 0.0
	for i := 0; i < nLayers; i++ {
		finalMass += layerVolume * prediction.PredictedDensities[len(prediction.PredictedDensities)-1][i]
	}

	injectedMass := req.UnloadingRate * req.UnloadingDensity * req.EstimatedDuration
	massError := math.Abs(finalMass - (initialMass + injectedMass*0.85)) / (initialMass + injectedMass) * 100

	t.Logf("Initial mass: %.2f kg, Final mass: %.2f kg, Injected: %.2f kg, Error: %.2f%%",
		initialMass, finalMass, injectedMass, massError)

	if massError > 20 {
		t.Errorf("Mass conservation error %.2f%% exceeds 20%%", massError)
	}
}

func TestEnergyConservation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	req := generatePredictionRequest(1, 800, 425, -160, 6, nLayers)
	prediction := predictor.predictUnloading(nil, req)

	if prediction.ErrorMessage != "" {
		t.Fatalf("Prediction error: %s", prediction.ErrorMessage)
	}

	specificHeat := 2200.0
	tankRadius := cfg.ModelParams.TankSpecs.DiameterMeters / 2.0
	tankHeight := cfg.ModelParams.TankSpecs.HeightMeters
	tankArea := math.Pi * tankRadius * tankRadius
	layerHeight := tankHeight / float64(nLayers)
	layerVolume := tankArea * layerHeight

	initialEnergy := 0.0
	for i := 0; i < nLayers; i++ {
		mass := layerVolume * req.InitialDensities[i]
		initialEnergy += mass * specificHeat * req.InitialTemps[i]
	}

	finalEnergy := 0.0
	lastStep := len(prediction.PredictedTemps) - 1
	for i := 0; i < nLayers; i++ {
		mass := layerVolume * prediction.PredictedDensities[lastStep][i]
		finalEnergy += mass * specificHeat * prediction.PredictedTemps[lastStep][i]
	}

	injectedEnergy := req.UnloadingRate * req.UnloadingDensity * req.EstimatedDuration * specificHeat * req.UnloadingTemp
	energyError := math.Abs(finalEnergy - (initialEnergy + injectedEnergy*0.85)) / (initialEnergy + injectedEnergy) * 100

	t.Logf("Initial energy: %.2f kJ, Final energy: %.2f kJ, Injected: %.2f kJ, Error: %.2f%%",
		initialEnergy/1000, finalEnergy/1000, injectedEnergy/1000, energyError)

	if energyError > 25 {
		t.Errorf("Energy conservation error %.2f%% exceeds 25%%", energyError)
	}
}

func TestInjectionLayerFinding(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	state := make([]LayerState, nLayers)
	for i := 0; i < nLayers; i++ {
		state[i] = LayerState{
			Temperature: -160.0 + float64(i)*0.1,
			Density:     425.0 - float64(i)*0.15,
		}
	}

	testCases := []struct {
		unloadingDensity float64
		expectedMinLayer int
		expectedMaxLayer int
	}{
		{420, 0, 5},
		{423.5, 8, 12},
		{430, 15, 19},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Density %.1f", tc.unloadingDensity), func(t *testing.T) {
			layer := predictor.findInjectionLayer(state, tc.unloadingDensity)

			t.Logf("Unloading density %.1f -> Injection layer %d (expected %d-%d)",
				tc.unloadingDensity, layer, tc.expectedMinLayer, tc.expectedMaxLayer)

			if layer < 0 || layer >= nLayers {
				t.Errorf("Injection layer %d out of bounds [0, %d]", layer, nLayers-1)
			}
		})
	}
}

func TestBoundaryConditions(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20

	t.Run("Zero duration", func(t *testing.T) {
		req := generatePredictionRequest(1, 800, 425, -160, 0, nLayers)
		result := predictor.predictUnloading(nil, req)

		if result.ErrorMessage == "" {
			t.Error("Expected error for zero duration")
		}
	})

	t.Run("Negative duration", func(t *testing.T) {
		req := generatePredictionRequest(1, 800, 425, -160, -1, nLayers)
		result := predictor.predictUnloading(nil, req)

		if result.ErrorMessage == "" {
			t.Error("Expected error for negative duration")
		}
	})

	t.Run("Zero flow rate", func(t *testing.T) {
		req := generatePredictionRequest(1, 0, 425, -160, 12, nLayers)
		result := predictor.predictUnloading(nil, req)

		if result.ErrorMessage == "" {
			t.Error("Expected error for zero flow rate")
		}
	})

	t.Run("Very short duration", func(t *testing.T) {
		req := generatePredictionRequest(1, 800, 425, -160, 0.1, nLayers)
		result := predictor.predictUnloading(nil, req)

		if result.ErrorMessage != "" {
			t.Errorf("Unexpected error for short duration: %s", result.ErrorMessage)
		}

		t.Logf("Short duration: MaxTempDiff=%.2f, MaxDensityDiff=%.2f",
			result.MaxTempDiff, result.MaxDensityDiff)
	})

	t.Run("Extreme temperature difference", func(t *testing.T) {
		req := generatePredictionRequest(1, 800, 425, -140, 12, nLayers)
		result := predictor.predictUnloading(nil, req)

		if result.ErrorMessage != "" {
			t.Errorf("Unexpected error: %s", result.ErrorMessage)
		}

		t.Logf("Extreme temp: MaxTempDiff=%.2f℃, RolloverRisk=%.2f%%",
			result.MaxTempDiff, result.RolloverRisk*100)

		if result.RolloverRisk < 0.5 {
			t.Errorf("Expected high rollover risk for extreme temp, got %.2f", result.RolloverRisk)
		}
	})
}

func TestStratificationEvolution(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	req := generatePredictionRequest(1, 800, 420, -158, 12, nLayers)
	prediction := predictor.predictUnloading(nil, req)

	if prediction.ErrorMessage != "" {
		t.Fatalf("Prediction error: %s", prediction.ErrorMessage)
	}

	nSteps := len(prediction.TimeSteps)
	tempDiffs := make([]float64, nSteps)
	densityDiffs := make([]float64, nSteps)

	for tStep := 0; tStep < nSteps; tStep++ {
		temps := prediction.PredictedTemps[tStep]
		densities := prediction.PredictedDensities[tStep]

		minT, maxT := temps[0], temps[0]
		minD, maxD := densities[0], densities[0]
		for i := 1; i < len(temps); i++ {
			if temps[i] < minT {
				minT = temps[i]
			}
			if temps[i] > maxT {
				maxT = temps[i]
			}
			if densities[i] < minD {
				minD = densities[i]
			}
			if densities[i] > maxD {
				maxD = densities[i]
			}
		}

		tempDiffs[tStep] = maxT - minT
		densityDiffs[tStep] = maxD - minD
	}

	firstHalfTemp := testutils.Mean(tempDiffs[:nSteps/2])
	secondHalfTemp := testutils.Mean(tempDiffs[nSteps/2:])
	firstHalfDens := testutils.Mean(densityDiffs[:nSteps/2])
	secondHalfDens := testutils.Mean(densityDiffs[nSteps/2:])

	t.Logf("Temp diff: first half=%.2f, second half=%.2f | Density diff: first half=%.2f, second half=%.2f",
		firstHalfTemp, secondHalfTemp, firstHalfDens, secondHalfDens)

	if secondHalfTemp < firstHalfTemp*0.8 {
		t.Error("Expected stratification to increase or stay similar, not decrease")
	}
}

func TestPredictUnloadingEndToEnd(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	testScenarios := []struct {
		name           string
		rate           float64
		density        float64
		temp           float64
		duration       float64
		expectedRisk   string
	}{
		{"Normal operation", 800, 425, -160, 12, "low"},
		{"High flow warm LNG", 1200, 420, -155, 8, "high"},
		{"Slow operation", 400, 428, -162, 24, "low"},
		{"Quick operation", 2000, 415, -150, 4, "high"},
	}

	for _, scenario := range testScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			req := generatePredictionRequest(1, scenario.rate, scenario.density, scenario.temp, scenario.duration, 20)

			startTime := time.Now()
			result := predictor.predictUnloading(nil, req)
			duration := time.Since(startTime)

			t.Logf("%s: TempDiff=%.2f℃, DensityDiff=%.2f kg/m³, PumpTime=%.2fh, Risk=%.2f%%, Time=%v",
				scenario.name, result.MaxTempDiff, result.MaxDensityDiff,
				result.OptimalPumpOnTime, result.RolloverRisk*100, duration)

			if result.ErrorMessage != "" {
				t.Errorf("%s: unexpected error: %s", scenario.name, result.ErrorMessage)
			}

			if result.MaxTempDiff <= 0 {
				t.Errorf("%s: expected positive temp diff, got %.2f", scenario.name, result.MaxTempDiff)
			}

			if result.MaxDensityDiff <= 0 {
				t.Errorf("%s: expected positive density diff, got %.2f", scenario.name, result.MaxDensityDiff)
			}

			if duration > 1*time.Second {
				t.Errorf("%s: expected prediction < 1s, got %v", scenario.name, duration)
			}

			if result.RolloverRisk < 0 || result.RolloverRisk > 1 {
				t.Errorf("%s: invalid risk: %.4f", scenario.name, result.RolloverRisk)
			}

			nLayers := 20
			if len(result.PredictedTemps) < 2 {
				t.Errorf("%s: expected at least 2 time steps, got %d", scenario.name, len(result.PredictedTemps))
			}
			for tStep := 0; tStep < len(result.PredictedTemps); tStep++ {
				if len(result.PredictedTemps[tStep]) != nLayers {
					t.Errorf("%s step %d: expected %d layers, got %d",
						scenario.name, tStep, nLayers, len(result.PredictedTemps[tStep]))
				}
			}
		})
	}
}

func TestInterpolation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	t.Run("Temperature interpolation", func(t *testing.T) {
		temps := []float64{-160, -159, -157, -154, -150}

		testCases := []struct {
			heightRatio float64
			expected    float64
		}{
			{0.0, -160},
			{1.0, -150},
			{0.5, -157},
			{0.25, -159},
		}

		for _, tc := range testCases {
			result := predictor.interpolateTemperature(temps, tc.heightRatio)
			if math.Abs(result-tc.expected) > 0.1 {
				t.Errorf("Ratio %.2f: expected %.1f, got %.1f", tc.heightRatio, tc.expected, result)
			}
		}
	})

	t.Run("Density interpolation", func(t *testing.T) {
		densities := []float64{425, 424, 422, 419, 415}

		testCases := []struct {
			heightRatio float64
			expected    float64
		}{
			{0.0, 425},
			{1.0, 415},
			{0.5, 422},
		}

		for _, tc := range testCases {
			result := predictor.interpolateDensity(densities, tc.heightRatio)
			if math.Abs(result-tc.expected) > 0.1 {
				t.Errorf("Ratio %.2f: expected %.1f, got %.1f", tc.heightRatio, tc.expected, result)
			}
		}
	})

	t.Run("Empty array", func(t *testing.T) {
		temp := predictor.interpolateTemperature([]float64{}, 0.5)
		if temp != -162.0 {
			t.Errorf("Expected -162 for empty temps, got %.1f", temp)
		}

		dens := predictor.interpolateDensity([]float64{}, 0.5)
		if dens != 425.0 {
			t.Errorf("Expected 425 for empty densities, got %.1f", dens)
		}
	})
}

func TestPerformanceBenchmark(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	req := generatePredictionRequest(1, 800, 425, -160, 12, nLayers)

	nPredictions := 50
	startTime := time.Now()

	for i := 0; i < nPredictions; i++ {
		_ = predictor.predictUnloading(nil, req)
	}

	duration := time.Since(startTime)
	perPred := duration / time.Duration(nPredictions)

	t.Logf("%d predictions: %v total, %v per prediction", nPredictions, duration, perPred)

	if perPred > 50*time.Millisecond {
		t.Errorf("Expected prediction < 50ms, got %v", perPred)
	}
}

func TestRolloverRiskCalculation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	t.Run("Low risk - uniform", func(t *testing.T) {
		temps := []float64{-160, -160, -160, -160, -160}
		densities := []float64{425, 425, 425, 425, 425}

		risk := predictor.calculateInstantRisk(temps, densities)

		if risk > 0.1 {
			t.Errorf("Expected low risk for uniform state, got %.4f", risk)
		}
	})

	t.Run("High risk - unstable", func(t *testing.T) {
		temps := []float64{-150, -155, -160, -165, -170}
		densities := []float64{415, 420, 425, 430, 435}

		risk := predictor.calculateInstantRisk(temps, densities)

		if risk < 0.7 {
			t.Errorf("Expected high risk for unstable state, got %.4f", risk)
		}
	})

	t.Run("Medium risk", func(t *testing.T) {
		temps := []float64{-160, -159, -157, -155, -152}
		densities := []float64{425, 424, 422, 420, 417}

		risk := predictor.calculateInstantRisk(temps, densities)

		if risk < 0.3 || risk > 0.7 {
			t.Errorf("Expected medium risk (0.3-0.7), got %.4f", risk)
		}
	})
}

func TestLayerHeights(t *testing.T) {
	cfg := testutils.NewTestConfig()
	predictor := &UnloadingPredictor{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.Unloading,
	}

	nLayers := 20
	heights := predictor.calculateLayerHeights(nLayers, 1)

	if len(heights) != nLayers {
		t.Errorf("Expected %d layers, got %d", nLayers, len(heights))
	}

	expectedHeight := 40.0 / float64(nLayers)
	for i, h := range heights {
		expected := expectedHeight * (float64(i) + 0.5)
		if math.Abs(h-expected) > 0.001 {
			t.Errorf("Layer %d: expected %.2f, got %.2f", i, expected, h)
		}
	}

	if heights[0] <= 0 {
		t.Error("First layer height should be positive")
	}

	if heights[nLayers-1] >= 40.0 {
		t.Error("Last layer height should be less than tank height")
	}
}

func TestUnloadingPredictionResultFields(t *testing.T) {
	result := messages.UnloadingPrediction{
		TankID:             1,
		MaxTempDiff:        3.5,
		MaxDensityDiff:     4.2,
		OptimalPumpOnTime:  2.5,
		RolloverRisk:       0.65,
		PredictedAt:        time.Now(),
	}

	if result.TankID != 1 {
		t.Error("TankID mismatch")
	}
	if result.MaxTempDiff != 3.5 {
		t.Error("MaxTempDiff mismatch")
	}
	if result.OptimalPumpOnTime != 2.5 {
		t.Error("OptimalPumpOnTime mismatch")
	}
	if result.RolloverRisk != 0.65 {
		t.Error("RolloverRisk mismatch")
	}

	t.Logf("Unloading prediction: %+v", result)
}
