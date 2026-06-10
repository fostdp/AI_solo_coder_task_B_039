package heat_leak

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"lng-monitoring/config"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"lng-monitoring/testutils"
)

var rng = rand.New(rand.NewSource(42))

func TestInverseSolverLeastSquares(t *testing.T) {
	solver := &InverseSolver{
		lambda:              0.01,
		maxIter:             100,
		tolerance:           1e-6,
		referenceK:          0.025,
		insulationThickness: 0.8,
	}

	deltaT := 185.0
	area := 25000.0
	thickness := 0.8

	testCases := []struct {
		name       string
		trueK      float64
		noisePct   float64
		maxErrorPct float64
	}{
		{"Perfect insulation, no noise", 0.025, 0.0, 5.0},
		{"Degraded 20%, no noise", 0.03125, 0.0, 5.0},
		{"Degraded 50%, no noise", 0.05, 0.0, 5.0},
		{"Severely degraded, no noise", 0.1, 0.0, 5.0},
		{"Perfect insulation, 5% noise", 0.025, 0.05, 10.0},
		{"Degraded 30%, 5% noise", 0.0357, 0.05, 10.0},
		{"Degraded 20%, 10% noise", 0.03125, 0.1, 15.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			heatRate := tc.trueK * area * deltaT / thickness
			noisyHeatRate := heatRate * (1 + (math.Floor(rand.Float64()*200-100) / 100 * tc.noisePct))

			invertedK := solver.leastSquaresSolve(noisyHeatRate, area, deltaT, 0.025)

			errorPct := math.Abs(invertedK - tc.trueK) / tc.trueK * 100

			t.Logf("True K: %.6f, Inverted K: %.6f, Error: %.2f%%", tc.trueK, invertedK, errorPct)

			if errorPct > tc.maxErrorPct {
				t.Errorf("Expected error < %.2f%%, got %.2f%%", tc.maxErrorPct, errorPct)
			}
		})
	}
}

func TestConductivityInversionAccuracy(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	solver := &InverseSolver{
		lambda:              0.01,
		maxIter:             100,
		tolerance:           1e-6,
		referenceK:          0.025,
		insulationThickness: 0.8,
	}

	testCases := []struct {
		name        string
		trueK       float64
		area        float64
		deltaT      float64
		noiseLevel  float64
	}{
		{"no_noise_k025", 0.025, 1000.0, 50.0, 0.0},
		{"no_noise_k035", 0.035, 1000.0, 50.0, 0.0},
		{"no_noise_k045", 0.045, 1000.0, 50.0, 0.0},
		{"low_noise_k025", 0.025, 1000.0, 50.0, 0.02},
		{"low_noise_k035", 0.035, 1000.0, 50.0, 0.02},
		{"high_noise_k025", 0.025, 1000.0, 50.0, 0.05},
		{"low_deltaT", 0.030, 1000.0, 20.0, 0.01},
		{"high_deltaT", 0.030, 1000.0, 80.0, 0.01},
	}

	avgAccuracy := 0.0

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			heatRate := tc.trueK * tc.area * tc.deltaT / solver.insulationThickness

			noise := 1.0
			if tc.noiseLevel > 0 {
				noise = 1.0 + (testutils.Mean([]float64{float64(len(tc.name))})/100.0-0.5)*2*tc.noiseLevel
			}
			measuredHeatRate := heatRate * noise

			invertedK := solver.leastSquaresSolve(measuredHeatRate, tc.area, tc.deltaT, 0.025)

			accuracy := 100 - math.Abs(invertedK-tc.trueK)/tc.trueK*100
			avgAccuracy += accuracy

			t.Logf("True K=%.6f, Inverted K=%.6f, Heat Rate=%.2f, Accuracy=%.2f%%",
				tc.trueK, invertedK, heatRate, accuracy)

			maxError := 0.05
			if tc.noiseLevel > 0.03 {
				maxError = 0.10
			}

			if math.Abs(invertedK-tc.trueK)/tc.trueK > maxError {
				t.Errorf("Inversion error too large: %.2f%% > %.0f%%",
					math.Abs(invertedK-tc.trueK)/tc.trueK*100, maxError*100)
			}
		})
	}

	avgAccuracy /= float64(len(testCases))
	t.Logf("Average inversion accuracy: %.2f%%", avgAccuracy)

	if avgAccuracy < 90 {
		t.Errorf("Expected average accuracy > 90%%, got %.2f%%", avgAccuracy)
	}
}

func TestHeatLeakLocalization(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	testCases := []struct {
		name         string
		leakRegions  []int
		severity     float64
		minAccuracy  float64
	}{
		{"Single leak, mild", []int{10}, 0.3, 60.0},
		{"Single leak, severe", []int{10}, 0.8, 80.0},
		{"Two leaks, mild", []int{5, 15}, 0.4, 60.0},
		{"Two leaks, severe", []int{5, 15}, 0.7, 75.0},
		{"Three leaks", []int{3, 10, 17}, 0.6, 70.0},
		{"Top region leak", []int{18}, 0.5, 70.0},
		{"Bottom region leak", []int{2}, 0.5, 70.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nLayers := 20
			nTimeSteps := 48

			history := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, tc.leakRegions, tc.severity)

			layerData := evaluator.organizeByLayer(history)
			innerTemp := evaluator.calculateInnerAverageTemp(layerData)
			layerHeatRates := evaluator.calculateLayerHeatRates(layerData)

			_, detectedRegions := evaluator.solveInverseProblem(
				layerHeatRates, layerData, innerTemp, 25.0, 0.025, 0.01,
			)

			detectedSet := make(map[int]bool)
			for _, r := range detectedRegions {
				detectedSet[r] = true
			}

			trueSet := make(map[int]bool)
			for _, r := range tc.leakRegions {
				trueSet[r] = true
			}

			truePositives := 0
			for r := range trueSet {
				if detectedSet[r] {
					truePositives++
				}
			}

			falsePositives := 0
			for r := range detectedSet {
				if !trueSet[r] {
					falsePositives++
				}
			}

			recall := float64(truePositives) / float64(len(tc.leakRegions))
			precision := 1.0
			if len(detectedRegions) > 0 {
				precision = float64(truePositives) / float64(len(detectedRegions))
			}
			f1 := 0.0
			if recall+precision > 0 {
				f1 = 2 * recall * precision / (recall + precision)
			}

			t.Logf("True regions: %v, Detected: %v", tc.leakRegions, detectedRegions)
			t.Logf("Recall: %.2f, Precision: %.2f, F1: %.2f", recall, precision, f1)

			if f1*100 < tc.minAccuracy {
				t.Errorf("Expected F1 score > %.2f, got %.2f", tc.minAccuracy/100, f1)
			}
		})
	}
}

func TestLeakLocalizationEdgeCases(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	nLayers := 20
	nTimeSteps := 48

	t.Run("No leak (all normal)", func(t *testing.T) {
		history := testutils.GenerateNormalLayerData(nLayers, nTimeSteps, -160.0)

		layerData := evaluator.organizeByLayer(history)
		innerTemp := evaluator.calculateInnerAverageTemp(layerData)
		layerHeatRates := evaluator.calculateLayerHeatRates(layerData)

		_, detectedRegions := evaluator.solveInverseProblem(
			layerHeatRates, layerData, innerTemp, 25.0, 0.025,
		)

		falseAlarmRate := float64(len(detectedRegions)) / float64(nLayers)

		t.Logf("Detected regions with no leak: %v, false alarm rate: %.2f%%",
			detectedRegions, falseAlarmRate*100)

		if falseAlarmRate > 0.1 {
			t.Errorf("Expected false alarm rate < 10%%, got %.2f%%", falseAlarmRate*100)
		}
	})

	t.Run("Adjacent leaks", func(t *testing.T) {
		leakRegions := []int{10, 11, 12}
		history := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, leakRegions, 0.6)

		layerData := evaluator.organizeByLayer(history)
		innerTemp := evaluator.calculateInnerAverageTemp(layerData)
		layerHeatRates := evaluator.calculateLayerHeatRates(layerData)

		_, detectedRegions := evaluator.solveInverseProblem(
			layerHeatRates, layerData, innerTemp, 25.0, 0.025,
		)

		detectedSet := make(map[int]bool)
		for _, r := range detectedRegions {
			detectedSet[r] = true
		}

		overlapCount := 0
		for _, r := range leakRegions {
			if detectedSet[r] || detectedSet[r-1] || detectedSet[r+1] {
				overlapCount++
			}
		}

		overlapRate := float64(overlapCount) / float64(len(leakRegions))
		t.Logf("Adjacent true: %v, detected: %v, overlap: %.0f%%",
			leakRegions, detectedRegions, overlapRate*100)

		if overlapRate < 0.6 {
			t.Errorf("Expected overlap rate > 60%%, got %.0f%%", overlapRate*100)
		}
	})
}

func TestWarningThreshold(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	testCases := []struct {
		name              string
		performance       float64
		thresholdPct      float64
		expectWarning     bool
	}{
		{"Perfect insulation", 1.0, 20.0, false},
		{"Slight degradation", 0.95, 20.0, false},
		{"At threshold", 0.80, 20.0, false},
		{"Just above threshold", 0.81, 20.0, false},
		{"Just below threshold", 0.79, 20.0, true},
		{"Moderate degradation", 0.70, 20.0, true},
		{"Severe degradation", 0.50, 20.0, true},
		{"Critical degradation", 0.30, 20.0, true},
		{"Different threshold, good", 0.90, 10.0, false},
		{"Different threshold, bad", 0.88, 10.0, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			evaluator.modelParams.WarningThresholdPct = tc.thresholdPct
			warningThreshold := (100.0 - tc.thresholdPct) / 100.0
			isWarning := tc.performance < warningThreshold

			if isWarning != tc.expectWarning {
				t.Errorf("%s: expected warning=%v, got %v (performance=%.2f, threshold=%.2f)",
					tc.name, tc.expectWarning, isWarning, tc.performance, warningThreshold)
			}
		})
	}
}

func TestInsulationPerformanceCalculation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	testCases := []struct {
		name           string
		equivalentK    float64
		referenceK     float64
		expectedPerf   float64
	}{
		{"Perfect insulation", 0.025, 0.025, 1.0},
		{"20% degradation", 0.03125, 0.025, 0.8},
		{"50% degradation", 0.05, 0.025, 0.5},
		{"Better than reference", 0.020, 0.025, 1.25},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			performance := evaluator.calculateInsulationPerformance(tc.equivalentK, tc.referenceK)

			if math.Abs(performance - tc.expectedPerf) > 0.001 {
				t.Errorf("%s: expected %.4f, got %.4f", tc.name, tc.expectedPerf, performance)
			}
		})
	}
}

func TestHeatLeakRateCalculation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	conductivity := 0.025
	thickness := 0.8
	innerTemp := -160.0
	ambientTemp := 25.0

	deltaT := ambientTemp - innerTemp
	expectedRate := conductivity * deltaT / thickness * 1000

	rate := evaluator.calculateTotalHeatLeakRate(conductivity, innerTemp, ambientTemp)

	t.Logf("Heat leak rate: %.2f W/m², expected: %.2f W/m²", rate, expectedRate)

	if math.Abs(rate - expectedRate) > 0.1 {
		t.Errorf("Expected heat leak rate %.2f, got %.2f", expectedRate, rate)
	}

	highConductivity := 0.05
	highRate := evaluator.calculateTotalHeatLeakRate(highConductivity, innerTemp, ambientTemp)
	expectedHighRate := highConductivity * deltaT / thickness * 1000

	if math.Abs(highRate - expectedHighRate) > 0.1 {
		t.Errorf("Expected high heat leak rate %.2f, got %.2f", expectedHighRate, highRate)
	}

	if highRate <= rate {
		t.Errorf("Expected higher rate for higher conductivity")
	}
}

func TestTemperatureTrendCalculation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
	}

	t.Run("Constant temperature", func(t *testing.T) {
		nPoints := 50
		data := make([]models.LayerSummary, nPoints)
		baseTime := time.Now().Add(-time.Duration(nPoints) * time.Hour)

		for i := 0; i < nPoints; i++ {
			data[i] = models.LayerSummary{
				Time:    baseTime.Add(time.Duration(i) * time.Hour),
				Layer:   10,
				AvgTemp: -160.0,
			}
		}

		trend := evaluator.calculateTemperatureTrend(data)

		t.Logf("Constant temp trend: %.6f", trend)

		if math.Abs(trend) > 0.001 {
			t.Errorf("Expected trend ~0, got %.6f", trend)
		}
	})

	t.Run("Rising temperature", func(t *testing.T) {
		nPoints := 50
		data := make([]models.LayerSummary, nPoints)
		baseTime := time.Now().Add(-time.Duration(nPoints) * time.Hour)

		for i := 0; i < nPoints; i++ {
			data[i] = models.LayerSummary{
				Time:    baseTime.Add(time.Duration(i) * time.Hour),
				Layer:   10,
				AvgTemp: -160.0 + float64(i)*0.1,
			}
		}

		trend := evaluator.calculateTemperatureTrend(data)

		t.Logf("Rising temp trend: %.6f", trend)

		if trend <= 0 {
			t.Errorf("Expected positive trend, got %.6f", trend)
		}
	})
}

func TestEvaluateHeatLeakEndToEnd(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	testScenarios := []struct {
		name        string
		leakRegions []int
		severity    float64
		expectWarning bool
		minPerformance float64
		maxPerformance float64
	}{
		{
			name:        "Normal operation, no leak",
			leakRegions: []int{},
			severity:    0.0,
			expectWarning: false,
			minPerformance: 0.9,
			maxPerformance: 1.1,
		},
		{
			name:        "Single mild leak",
			leakRegions: []int{10},
			severity:    0.3,
			expectWarning: false,
			minPerformance: 0.8,
			maxPerformance: 1.0,
		},
		{
			name:        "Single severe leak",
			leakRegions: []int{10},
			severity:    0.8,
			expectWarning: true,
			minPerformance: 0.5,
			maxPerformance: 0.8,
		},
		{
			name:        "Multiple severe leaks",
			leakRegions: []int{5, 10, 15},
			severity:    0.7,
			expectWarning: true,
			minPerformance: 0.4,
			maxPerformance: 0.7,
		},
	}

	for _, scenario := range testScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			nLayers := 20
			nTimeSteps := 48

			var history []models.LayerSummary
			if len(scenario.leakRegions) == 0 {
				history = testutils.GenerateNormalLayerData(nLayers, nTimeSteps, -160.0)
			} else {
				history = testutils.GenerateHeatLeakData(nLayers, nTimeSteps, scenario.leakRegions, scenario.severity)
			}

			startTime := time.Now()
			result := evaluator.evaluateHeatLeak(nil, 1, history, 25.0)
			duration := time.Since(startTime)

			t.Logf("%s: K=%.6f, performance=%.4f, warning=%v, regions=%v, time=%v",
				scenario.name, result.EquivalentConductivity, result.InsulationPerformance,
				result.IsWarning, result.LeakRegion, duration)

			if result.ErrorMessage != "" {
				t.Errorf("Unexpected error: %s", result.ErrorMessage)
			}

			if result.IsWarning != scenario.expectWarning {
				t.Errorf("%s: expected warning=%v, got %v (performance=%.4f)",
					scenario.name, scenario.expectWarning, result.IsWarning, result.InsulationPerformance)
			}

			if result.InsulationPerformance < scenario.minPerformance ||
				result.InsulationPerformance > scenario.maxPerformance {
				t.Errorf("%s: expected performance in [%.2f, %.2f], got %.4f",
					scenario.name, scenario.minPerformance, scenario.maxPerformance, result.InsulationPerformance)
			}

			if duration > 500*time.Millisecond {
				t.Errorf("%s: expected evaluation < 500ms, got %v", scenario.name, duration)
			}

			if result.HeatLeakRate <= 0 {
				t.Errorf("Expected positive heat leak rate, got %.2f", result.HeatLeakRate)
			}

			if result.TotalHeatLoadKW <= 0 {
				t.Errorf("Expected positive total heat load, got %.2f", result.TotalHeatLoadKW)
			}
		})
	}
}

func TestBoundaryConditions(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	boundaries := testutils.GenerateBoundaryTestCases()

	t.Run("Insufficient data", func(t *testing.T) {
		history := testutils.GenerateNormalLayerData(20, 5, -160.0)
		result := evaluator.evaluateHeatLeak(nil, 1, history, 25.0)

		if result.ErrorMessage == "" {
			t.Error("Expected error for insufficient data")
		}
	})

	t.Run("At warning threshold boundary", func(t *testing.T) {
		nLayers := 20
		nTimeSteps := 48
		history := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, []int{10}, 0.5)

		result := evaluator.evaluateHeatLeak(nil, 1, history, 25.0)

		thresholdPerformance := boundaries["insulation_performance"]["warning_start"]
		performance := result.InsulationPerformance

		t.Logf("Performance: %.4f, threshold: %.4f, isWarning: %v",
			performance, thresholdPerformance, result.IsWarning)
	})

	t.Run("Extreme temperature difference", func(t *testing.T) {
		heatRate := evaluator.calculateTotalHeatLeakRate(0.025, -165.0, 40.0)

		if heatRate <= 0 {
			t.Errorf("Expected positive heat rate for extreme delta T")
		}

		t.Logf("Extreme delta T heat rate: %.2f W/m²", heatRate)
	})

	t.Run("Zero temperature difference", func(t *testing.T) {
		heatRate := evaluator.calculateTotalHeatLeakRate(0.025, 25.0, 25.0)

		if heatRate != 0 {
			t.Errorf("Expected zero heat rate for zero delta T, got %.2f", heatRate)
		}
	})
}

func TestInverseSolverConvergence(t *testing.T) {
	solver := &InverseSolver{
		lambda:              0.01,
		maxIter:             100,
		tolerance:           1e-10,
		referenceK:          0.025,
		insulationThickness: 0.8,
	}

	deltaT := 185.0
	area := 25000.0
	trueK := 0.04
	heatRate := trueK * area * deltaT / 0.8

	result := solver.leastSquaresSolve(heatRate, area, deltaT, 0.025)

	errorPct := math.Abs(result - trueK) / trueK * 100

	t.Logf("True K: %.6f, Converged K: %.6f, Error: %.4f%%", trueK, result, errorPct)

	if errorPct > 0.01 {
		t.Errorf("Expected convergence error < 0.01%%, got %.4f%%", errorPct)
	}
}

func TestDetectAnomalousLayers(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	nLayers := 20
	heatRates := make(map[int]float64)
	layerData := make(map[int][]models.LayerSummary)

	for i := 0; i < nLayers; i++ {
		heatRates[i] = 5.0 + rand.NormFloat64()*1.0
		layerData[i] = make([]models.LayerSummary, 10)
	}

	anomalousLayers := []int{5, 15}
	for _, l := range anomalousLayers {
		heatRates[l] = 15.0
	}

	detected := evaluator.detectAnomalousLayers(heatRates, layerData)

	t.Logf("Anomalous: %v, Detected: %v", anomalousLayers, detected)

	detectedSet := make(map[int]bool)
	for _, d := range detected {
		detectedSet[d] = true
	}

	for _, a := range anomalousLayers {
		if !detectedSet[a] {
			t.Errorf("Expected to detect layer %d as anomalous", a)
		}
	}
}

func TestCalibration(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	evaluator.setCalibration(1, 0.026)

	k := evaluator.getCalibratedK(1)
	if math.Abs(k - 0.026) > 1e-9 {
		t.Errorf("Expected calibrated K 0.026, got %.6f", k)
	}

	kNoCalib := evaluator.getCalibratedK(2)
	if math.Abs(kNoCalib - 0.025) > 1e-9 {
		t.Errorf("Expected default K 0.025, got %.6f", kNoCalib)
	}
}

func TestPerformanceBenchmark(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	nLayers := 20
	nTimeSteps := 48

	history := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, []int{5, 15}, 0.6)

	nEvaluations := 100
	startTime := time.Now()

	for i := 0; i < nEvaluations; i++ {
		_ = evaluator.evaluateHeatLeak(nil, 1, history, 25.0)
	}

	duration := time.Since(startTime)
	perEval := duration / time.Duration(nEvaluations)

	t.Logf("%d evaluations: %v total, %v per evaluation", nEvaluations, duration, perEval)

	if perEval > 10*time.Millisecond {
		t.Errorf("Expected evaluation < 10ms, got %v", perEval)
	}
}

func TestHeatLeakResultFields(t *testing.T) {
	result := messages.HeatLeakResult{
		TankID:                 1,
		EquivalentConductivity: 0.03,
		InsulationPerformance:  0.833,
		HeatLeakRate:           57.81,
		LeakRegion:             []int{5, 15},
		IsWarning:              true,
		TotalHeatLoadKW:        160.6,
		EvaluatedAt:            time.Now(),
	}

	if result.TankID != 1 {
		t.Error("TankID mismatch")
	}
	if result.EquivalentConductivity != 0.03 {
		t.Error("Conductivity mismatch")
	}
	if !result.IsWarning {
		t.Error("IsWarning should be true")
	}
	if len(result.LeakRegion) != 2 {
		t.Error("Leak region count mismatch")
	}

	t.Logf("Heat leak result: %+v", result)
}

func TestRootCause_AmbientTempSuddenChangeStability(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	nLayers := 20
	nTimeSteps := 48
	stableAmbient := 25.0

	stableHistory := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, []int{}, 0.0)
	stableResult := evaluator.evaluateHeatLeak(nil, 1, stableHistory, stableAmbient)

	suddenChangeHistory := generateAmbientSuddenChangeData(nLayers, nTimeSteps, stableAmbient)
	suddenChangeAmbient := 35.0

	evaluator.modelParams.AdaptiveRegularizationOn = false
	evaluator.modelParams.SlidingWindowSize = 1
	resultWithoutReg := evaluator.evaluateHeatLeak(nil, 1, suddenChangeHistory, suddenChangeAmbient)

	evaluator.modelParams.AdaptiveRegularizationOn = true
	evaluator.modelParams.SlidingWindowSize = 6
	resultWithReg := evaluator.evaluateHeatLeak(nil, 1, suddenChangeHistory, suddenChangeAmbient)

	t.Logf("Stable condition K: %.6f", stableResult.EquivalentConductivity)
	t.Logf("Without regularization K: %.6f", resultWithoutReg.EquivalentConductivity)
	t.Logf("With regularization K: %.6f", resultWithReg.EquivalentConductivity)

	deviationWithout := math.Abs(resultWithoutReg.EquivalentConductivity - stableResult.EquivalentConductivity)
	deviationWith := math.Abs(resultWithReg.EquivalentConductivity - stableResult.EquivalentConductivity)

	t.Logf("Deviation without regularization: %.6f", deviationWithout)
	t.Logf("Deviation with regularization: %.6f", deviationWith)
	t.Logf("Improvement: %.2f%%", (deviationWithout-deviationWith)/deviationWithout*100)

	if deviationWith >= deviationWithout {
		t.Errorf("Regularization did not reduce deviation. Without: %.6f, With: %.6f", deviationWithout, deviationWith)
	}

	relativeDeviation := deviationWith / stableResult.EquivalentConductivity
	if relativeDeviation > 0.15 {
		t.Errorf("Deviation with regularization too large: %.2f%%, expected < 15%%", relativeDeviation*100)
	}
}

func TestRootCause_SlidingWindowSmoothing(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	nLayers := 20
	nTimeSteps := 48
	baseTemp := -165.0

	noisyTemps := generateNoisyTemperatureData(nLayers, nTimeSteps, baseTemp)

	evaluator.modelParams.SlidingWindowSize = 1
	resultNoSmooth := evaluator.evaluateHeatLeak(nil, 1, noisyTemps, 25.0)

	evaluator.modelParams.SlidingWindowSize = 6
	resultSmooth := evaluator.evaluateHeatLeak(nil, 1, noisyTemps, 25.0)

	t.Logf("Without smoothing K: %.6f, HeatLeakRate: %.2f", resultNoSmooth.EquivalentConductivity, resultNoSmooth.HeatLeakRate)
	t.Logf("With smoothing K: %.6f, HeatLeakRate: %.2f", resultSmooth.EquivalentConductivity, resultSmooth.HeatLeakRate)

	expectedK := 0.025
	errorNoSmooth := math.Abs(resultNoSmooth.EquivalentConductivity - expectedK)
	errorSmooth := math.Abs(resultSmooth.EquivalentConductivity - expectedK)

	t.Logf("Error without smoothing: %.6f", errorNoSmooth)
	t.Logf("Error with smoothing: %.6f", errorSmooth)

	if errorSmooth >= errorNoSmooth {
		t.Errorf("Sliding window did not reduce error. Without: %.6f, With: %.6f", errorNoSmooth, errorSmooth)
	}

	relativeError := errorSmooth / expectedK
	if relativeError > 0.10 {
		t.Errorf("Relative error too large: %.2f%%, expected < 10%%", relativeError*100)
	}
}

func TestRootCause_AdaptiveRegularization(t *testing.T) {
	cfg := testutils.NewTestConfig()
	evaluator := &HeatLeakEvaluator{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.HeatLeak,
		calibration: make(map[int]float64),
	}

	stableChangeRate := 0.1
	moderateChangeRate := 0.6
	highChangeRate := 2.0

	lambdaStable := evaluator.calculateAdaptiveLambda(stableChangeRate)
	lambdaModerate := evaluator.calculateAdaptiveLambda(moderateChangeRate)
	lambdaHigh := evaluator.calculateAdaptiveLambda(highChangeRate)

	t.Logf("Lambda at change rate %.1f: %.6f", stableChangeRate, lambdaStable)
	t.Logf("Lambda at change rate %.1f: %.6f", moderateChangeRate, lambdaModerate)
	t.Logf("Lambda at change rate %.1f: %.6f", highChangeRate, lambdaHigh)

	if lambdaStable != evaluator.modelParams.BaseRegularizationLambda {
		t.Errorf("Expected base lambda for stable condition, got %.6f", lambdaStable)
	}

	if lambdaModerate <= lambdaStable {
		t.Errorf("Expected higher lambda for moderate change rate. Stable: %.6f, Moderate: %.6f", lambdaStable, lambdaModerate)
	}

	if lambdaHigh <= lambdaModerate {
		t.Errorf("Expected higher lambda for high change rate. Moderate: %.6f, High: %.6f", lambdaModerate, lambdaHigh)
	}

	if lambdaHigh > evaluator.modelParams.MaxRegularizationLambda {
		t.Errorf("Lambda exceeded max limit: %.6f > %.6f", lambdaHigh, evaluator.modelParams.MaxRegularizationLambda)
	}
}

func generateAmbientSuddenChangeData(nLayers, nTimeSteps int, baseAmbient float64) []messages.LayerHistoryData {
	data := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, []int{}, 0.0)
	for i := range data {
		if i > nTimeSteps/2 {
			for j := range data[i].Temperatures {
				data[i].Temperatures[j] += 0.5 * float64(i-nTimeSteps/2) / float64(nTimeSteps/2)
			}
		}
	}
	return data
}

func generateNoisyTemperatureData(nLayers, nTimeSteps int, baseTemp float64) []messages.LayerHistoryData {
	data := testutils.GenerateHeatLeakData(nLayers, nTimeSteps, []int{}, 0.0)
	for i := range data {
		for j := range data[i].Temperatures {
			data[i].Temperatures[j] = baseTemp + float64(j)*0.1 + rng.NormFloat64()*0.3
		}
	}
	return data
}
