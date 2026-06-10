package bog_diagnostic

import (
	"math"
	"testing"
	"time"

	"lng-monitoring/config"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"lng-monitoring/testutils"
)

func TestIsolationForestBasic(t *testing.T) {
	iforest := NewIsolationForest(50, 100)

	nNormal := 200
	normalData := make([][]float64, nNormal)
	for i := 0; i < nNormal; i++ {
		normalData[i] = []float64{
			1.5 + rand.NormFloat64()*0.2,
			30.0 + rand.NormFloat64()*2.0,
		}
	}

	iforest.Fit(normalData)

	normalScores := make([]float64, nNormal)
	for i, x := range normalData {
		normalScores[i] = iforest.AnomalyScore(x)
	}

	anomalies := [][]float64{
		{5.0, 60.0},
		{4.5, 55.0},
		{5.5, 65.0},
		{0.5, 10.0},
	}

	anomalyScores := make([]float64, len(anomalies))
	for i, x := range anomalies {
		anomalyScores[i] = iforest.AnomalyScore(x)
	}

	normalMean := testutils.Mean(normalScores)
	anomalyMean := testutils.Mean(anomalyScores)

	if anomalyMean < normalMean {
		t.Errorf("Expected anomaly scores to be higher than normal scores. Got normal mean: %.4f, anomaly mean: %.4f", normalMean, anomalyMean)
	}

	threshold := 0.6
	metrics := &testutils.TestMetrics{}
	for _, s := range normalScores {
		if s > threshold {
			metrics.FalsePositives++
		} else {
			metrics.TrueNegatives++
		}
	}
	for _, s := range anomalyScores {
		if s > threshold {
			metrics.TruePositives++
		} else {
			metrics.FalseNegatives++
		}
	}

	accuracy := metrics.Accuracy()
	recall := metrics.Recall()

	if accuracy < 0.8 {
		t.Errorf("Expected accuracy > 0.8, got: %.4f", accuracy)
	}

	t.Logf("Accuracy: %.4f, Recall: %.4f, Precision: %.4f, F1: %.4f",
		accuracy, recall, metrics.Precision(), metrics.F1Score())
}

func TestIsolationForestAccuracyAndRecall(t *testing.T) {
	nTrials := 5
	avgAccuracy := 0.0
	avgRecall := 0.0
	avgF1 := 0.0

	for trial := 0; trial < nTrials; trial++ {
		iforest := NewIsolationForest(100, 256)

		nNormal := 500
		nAnomalies := 50
		normalData := make([][]float64, nNormal)

		for i := 0; i < nNormal; i++ {
			normalData[i] = []float64{
				1.5 + rand.NormFloat64()*0.3,
				30.0 + rand.NormFloat64()*3.0,
				0.15 + rand.NormFloat64()*0.02,
				85.0 + rand.NormFloat64()*5.0,
			}
		}

		iforest.Fit(normalData)

		threshold := 0.7
		metrics := &testutils.TestMetrics{}

		for i := 0; i < nNormal; i++ {
			score := iforest.AnomalyScore(normalData[i])
			if score > threshold {
				metrics.FalsePositives++
			} else {
				metrics.TrueNegatives++
			}
		}

		for i := 0; i < nAnomalies; i++ {
			anomaly := []float64{
				4.0 + rand.NormFloat64()*1.0,
				55.0 + rand.NormFloat64()*5.0,
				0.12 + rand.NormFloat64()*0.03,
				100.0 + rand.NormFloat64()*8.0,
			}
			score := iforest.AnomalyScore(anomaly)
			if score > threshold {
				metrics.TruePositives++
			} else {
				metrics.FalseNegatives++
			}
		}

		avgAccuracy += metrics.Accuracy()
		avgRecall += metrics.Recall()
		avgF1 += metrics.F1Score()
	}

	avgAccuracy /= float64(nTrials)
	avgRecall /= float64(nTrials)
	avgF1 /= float64(nTrials)

	if avgAccuracy < 0.85 {
		t.Errorf("Expected average accuracy > 0.85, got: %.4f", avgAccuracy)
	}

	if avgRecall < 0.80 {
		t.Errorf("Expected average recall > 0.80, got: %.4f", avgRecall)
	}

	t.Logf("Average over %d trials - Accuracy: %.4f, Recall: %.4f, F1: %.4f",
		nTrials, avgAccuracy, avgRecall, avgF1)
}

func TestBearingFaultVsPistonRingWear(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
		iforest:     NewIsolationForest(100, 256),
	}

	nBearing := 30
	nPiston := 30
	nNormal := 30

	trainingData := make([][]float64, nNormal*2)
	for i := 0; i < nNormal; i++ {
		data := testutils.GenerateNormalBOGData(100, 1)
		trainingData[i] = service.extractFeatures(data[0], data)
	}
	for i := 0; i < nNormal; i++ {
		data := testutils.GenerateNormalBOGData(100, 2)
		trainingData[nNormal+i] = service.extractFeatures(data[0], data)
	}
	service.iforest.Fit(trainingData)

	bearingClassified := make(map[string]int)
	pistonClassified := make(map[string]int)

	for i := 0; i < nBearing; i++ {
		data := testutils.GenerateBearingFaultData(100, 1, 0.5)
		latest := data[len(data)-1]
		features := service.extractFeatures(latest, data)
		score := service.iforest.AnomalyScore(features)
		vibTrend := service.calculateTrend(data, "vibration")
		currTrend := service.calculateTrend(data, "current")
		faultType := service.classifyFaultType(features, score, vibTrend, currTrend)
		bearingClassified[faultType]++
	}

	for i := 0; i < nPiston; i++ {
		data := testutils.GeneratePistonRingWearData(100, 2, 0.5)
		latest := data[len(data)-1]
		features := service.extractFeatures(latest, data)
		score := service.iforest.AnomalyScore(features)
		vibTrend := service.calculateTrend(data, "vibration")
		currTrend := service.calculateTrend(data, "current")
		faultType := service.classifyFaultType(features, score, vibTrend, currTrend)
		pistonClassified[faultType]++
	}

	bearingCorrect := bearingClassified["bearing_fault"]
	pistonCorrect := pistonClassified["piston_ring_wear"]

	bearingAccuracy := float64(bearingCorrect) / float64(nBearing)
	pistonAccuracy := float64(pistonCorrect) / float64(nPiston)

	t.Logf("Bearing fault classification: %+v", bearingClassified)
	t.Logf("Piston ring wear classification: %+v", pistonClassified)

	if bearingAccuracy < 0.7 {
		t.Errorf("Expected bearing fault accuracy > 0.7, got: %.4f", bearingAccuracy)
	}

	if pistonAccuracy < 0.7 {
		t.Errorf("Expected piston ring wear accuracy > 0.7, got: %.4f", pistonAccuracy)
	}
}

func TestBearingVsPistonDiscrimination(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
		iforest:     NewIsolationForest(100, 256),
	}

	bearingData := testutils.GenerateBearingFaultData(100, 1, 0.6)
	pistonData := testutils.GeneratePistonRingWearData(100, 2, 0.6)

	bearingLatest := bearingData[len(bearingData)-1]
	pistonLatest := pistonData[len(pistonData)-1]

	bearingFeatures := service.extractFeatures(bearingLatest, bearingData)
	pistonFeatures := service.extractFeatures(pistonLatest, pistonData)

	bearingVibNorm := bearingFeatures[3]
	bearingCurrNorm := bearingFeatures[4]
	pistonVibNorm := pistonFeatures[3]
	pistonCurrNorm := pistonFeatures[4]

	t.Logf("Bearing - vibNorm: %.4f, currNorm: %.4f", bearingVibNorm, bearingCurrNorm)
	t.Logf("Piston - vibNorm: %.4f, currNorm: %.4f", pistonVibNorm, pistonCurrNorm)

	if bearingVibNorm < 0.5 {
		t.Errorf("Expected bearing fault to have high vibration norm, got: %.4f", bearingVibNorm)
	}

	if pistonCurrNorm < 0.5 {
		t.Errorf("Expected piston ring wear to have high current norm, got: %.4f", pistonCurrNorm)
	}

	if bearingVibNorm < pistonVibNorm {
		t.Errorf("Expected bearing fault to have higher vibration norm than piston wear")
	}

	if pistonCurrNorm < bearingCurrNorm {
		t.Errorf("Expected piston ring wear to have higher current norm than bearing fault")
	}
}

func TestRecommendationTimeliness(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
	}

	testCases := []struct {
		name           string
		isAnomaly      bool
		anomalyType    string
		anomalyScore   float64
		remainingHours float64
		expectKeywords []string
		notExpectKW    []string
	}{
		{
			name:           "Normal operation",
			isAnomaly:      false,
			anomalyType:    "normal",
			anomalyScore:   0.3,
			remainingHours: math.Inf(1),
			expectKeywords: []string{"正常", "定期"},
		},
		{
			name:           "Early bearing fault",
			isAnomaly:      true,
			anomalyType:    "bearing_fault",
			anomalyScore:   0.75,
			remainingHours: 500,
			expectKeywords: []string{"轴承", "检查", "计划"},
			notExpectKW:    []string{"立即", "紧急"},
		},
		{
			name:           "Critical bearing fault",
			isAnomaly:      true,
			anomalyType:    "bearing_fault",
			anomalyScore:   0.95,
			remainingHours: 24,
			expectKeywords: []string{"轴承", "立即", "停机", "紧急"},
		},
		{
			name:           "Piston ring wear early",
			isAnomaly:      true,
			anomalyType:    "piston_ring_wear",
			anomalyScore:   0.7,
			remainingHours: 300,
			expectKeywords: []string{"活塞环", "磨损", "监测"},
		},
		{
			name:           "Critical piston ring wear",
			isAnomaly:      true,
			anomalyType:    "piston_ring_wear",
			anomalyScore:   0.9,
			remainingHours: 48,
			expectKeywords: []string{"活塞环", "更换", "紧急"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := service.generateRecommendation(tc.isAnomaly, tc.anomalyType, tc.anomalyScore, tc.remainingHours)

			t.Logf("Recommendation for %s: %s", tc.name, rec)

			for _, kw := range tc.expectKeywords {
				found := false
				for runeIdx, r := range rec {
					if runeIdx+len(kw) <= len(rec) {
						if rec[runeIdx:runeIdx+len(kw)] == kw {
							found = true
							break
						}
					}
				}
				if !found {
					t.Errorf("Expected keyword '%s' in recommendation, got: %s", kw, rec)
				}
			}

			for _, kw := range tc.notExpectKW {
				found := false
				for runeIdx, r := range rec {
					if runeIdx+len(kw) <= len(rec) {
						if rec[runeIdx:runeIdx+len(kw)] == kw {
							found = true
							break
						}
					}
				}
				if found {
					t.Errorf("Unexpected keyword '%s' in recommendation, got: %s", kw, rec)
				}
			}
		})
	}
}

func TestRemainingLifeEstimation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
	}

	testCases := []struct {
		name           string
		anomalyScore   float64
		vibTrend       float64
		currTrend      float64
		minHours       float64
		maxHours       float64
	}{
		{
			name:         "Normal",
			anomalyScore: 0.4,
			vibTrend:     0,
			currTrend:    0,
			maxHours:     math.Inf(1),
		},
		{
			name:         "Early warning",
			anomalyScore: 0.55,
			vibTrend:     0.005,
			currTrend:    0.002,
			minHours:     100,
			maxHours:     1000,
		},
		{
			name:         "Clear anomaly",
			anomalyScore: 0.8,
			vibTrend:     0.02,
			currTrend:    0.01,
			minHours:     10,
			maxHours:     200,
		},
		{
			name:         "Critical",
			anomalyScore: 0.95,
			vibTrend:     0.05,
			currTrend:    0.03,
			minHours:     0,
			maxHours:     48,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			remaining := service.estimateRemainingLife(tc.anomalyScore, tc.vibTrend, tc.currTrend)

			t.Logf("%s: remaining hours = %.2f", tc.name, remaining)

			if remaining < tc.minHours {
				t.Errorf("Expected remaining >= %.2f, got: %.2f", tc.minHours, remaining)
			}
			if !math.IsInf(tc.maxHours, 1) && remaining > tc.maxHours {
				t.Errorf("Expected remaining <= %.2f, got: %.2f", tc.maxHours, remaining)
			}
		})
	}
}

func TestBoundaryConditions(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
		iforest:     NewIsolationForest(50, 100),
	}

	boundaries := testutils.GenerateBoundaryTestCases()

	normalData := testutils.GenerateNormalBOGData(50, 1)
	trainingFeatures := make([][]float64, 50)
	for i, d := range normalData {
		trainingFeatures[i] = service.extractFeatures(d, normalData)
	}
	service.iforest.Fit(trainingFeatures)

	testCases := []struct {
		name        string
		data        models.BOGCompressorData
		expectAnomaly bool
	}{
		{
			name: "Normal vibration low",
			data: models.BOGCompressorData{
				Vibration:    boundaries["vibration"]["min_normal"],
				Current:      30,
				Temperature:  80,
				Pressure:     0.15,
				FlowRate:     500,
				RunningStatus: 1,
			},
			expectAnomaly: false,
		},
		{
			name: "Normal vibration high",
			data: models.BOGCompressorData{
				Vibration:    boundaries["vibration"]["max_normal"],
				Current:      30,
				Temperature:  80,
				Pressure:     0.15,
				FlowRate:     500,
				RunningStatus: 1,
			},
			expectAnomaly: false,
		},
		{
			name: "Warning vibration boundary",
			data: models.BOGCompressorData{
				Vibration:    boundaries["vibration"]["warning_start"],
				Current:      30,
				Temperature:  80,
				Pressure:     0.15,
				FlowRate:     500,
				RunningStatus: 1,
			},
			expectAnomaly: true,
		},
		{
			name: "Critical vibration",
			data: models.BOGCompressorData{
				Vibration:    boundaries["vibration"]["critical_start"],
				Current:      30,
				Temperature:  80,
				Pressure:     0.15,
				FlowRate:     500,
				RunningStatus: 1,
			},
			expectAnomaly: true,
		},
		{
			name: "Normal current boundary",
			data: models.BOGCompressorData{
				Vibration:    1.5,
				Current:      boundaries["current"]["max_normal"],
				Temperature:  80,
				Pressure:     0.15,
				FlowRate:     500,
				RunningStatus: 1,
			},
			expectAnomaly: false,
		},
		{
			name: "High current anomaly",
			data: models.BOGCompressorData{
				Vibration:    1.5,
				Current:      boundaries["current"]["critical_start"],
				Temperature:  80,
				Pressure:     0.15,
				FlowRate:     500,
				RunningStatus: 1,
			},
			expectAnomaly: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			history := testutils.GenerateNormalBOGData(50, 1)
			history[len(history)-1] = tc.data

			features := service.extractFeatures(tc.data, history)
			score := service.iforest.AnomalyScore(features)
			isAnomaly := score > service.modelParams.AnomalyThreshold

			t.Logf("%s: score=%.4f, isAnomaly=%v", tc.name, score, isAnomaly)

			if tc.expectAnomaly && !isAnomaly {
				t.Errorf("Expected anomaly for %s, got score %.4f", tc.name, score)
			}
		})
	}
}

func TestDiagnoseCompressorEndToEnd(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
		iforest:     NewIsolationForest(100, 256),
	}

	nTraining := 200
	normalData := make([][]float64, nTraining)
	for i := 0; i < nTraining; i++ {
		data := testutils.GenerateNormalBOGData(100, 1)
		normalData[i] = service.extractFeatures(data[0], data)
	}
	service.iforest.Fit(normalData)

	testScenarios := []struct {
		name        string
		genData     func() []models.BOGCompressorData
		expectAnomaly   bool
		expectedType  string
	}{
		{
			name: "Normal operation",
			genData: func() []models.BOGCompressorData {
				return testutils.GenerateNormalBOGData(100, 1)
			},
			expectAnomaly: false,
			expectedType:  "normal",
		},
		{
			name: "Bearing fault mild",
			genData: func() []models.BOGCompressorData {
				return testutils.GenerateBearingFaultData(100, 1, 0.3)
			},
			expectAnomaly: true,
			expectedType:  "bearing_fault",
		},
		{
			name: "Bearing fault severe",
			genData: func() []models.BOGCompressorData {
				return testutils.GenerateBearingFaultData(100, 1, 0.8)
			},
			expectAnomaly: true,
			expectedType:  "bearing_fault",
		},
		{
			name: "Piston ring wear mild",
			genData: func() []models.BOGCompressorData {
				return testutils.GeneratePistonRingWearData(100, 2, 0.3)
			},
			expectAnomaly: true,
			expectedType:  "piston_ring_wear",
		},
		{
			name: "Imbalance",
			genData: func() []models.BOGCompressorData {
				return testutils.GenerateImbalanceData(100, 1, 0.5)
			},
			expectAnomaly: true,
		},
	}

	for _, scenario := range testScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			data := scenario.genData()
			latest := data[len(data)-1]

			startTime := time.Now()
			result := service.diagnoseCompressor(nil, 1, latest, data)
			duration := time.Since(startTime)

			t.Logf("%s: anomaly=%.4f, type=%s, remaining=%.2fh, time=%v",
				scenario.name, result.AnomalyScore, result.AnomalyType, result.RemainingHours, duration)

			if result.IsAnomaly != scenario.expectAnomaly {
				t.Errorf("%s: expected anomaly=%v, got %v (score=%.4f)",
					scenario.name, scenario.expectAnomaly, result.IsAnomaly, result.AnomalyScore)
			}

			if scenario.expectAnomaly && result.Recommendation == "" {
				t.Errorf("%s: expected non-empty recommendation for anomaly", scenario.name)
			}

			if duration > 100*time.Millisecond {
				t.Errorf("%s: expected diagnosis < 100ms, got %v", scenario.name, duration)
			}

			if result.Confidence < 0 || result.Confidence > 1 {
				t.Errorf("%s: expected confidence in [0,1], got %.4f", scenario.name, result.Confidence)
			}

			if result.RemainingHours < 0 || result.RemainingHours > 8760 {
				t.Errorf("%s: expected remaining hours in [0, 8760], got %.2f", scenario.name, result.RemainingHours)
			}
		})
	}
}

func TestExtractFeatures(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
	}

	normalData := testutils.GenerateNormalBOGData(100, 1)
	latest := normalData[len(normalData)-1]

	features := service.extractFeatures(latest, normalData)

	if len(features) != 8 {
		t.Errorf("Expected 8 features, got %d", len(features))
	}

	for i, f := range features {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			t.Errorf("Feature %d is NaN or Inf: %v", i, f)
		}
		if f < -10 || f > 10 {
			t.Errorf("Feature %d out of expected range: %v", i, f)
		}
	}

	t.Logf("Features: %v", features)
}

func TestCalculateTrend(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
	}

	nPoints := 50
	data := make([]models.BOGCompressorData, nPoints)
	for i := 0; i < nPoints; i++ {
		data[i] = models.BOGCompressorData{
			Vibration: 1.0 + float64(i)*0.1,
			Current:   30.0,
		}
	}

	trend := service.calculateTrend(data, "vibration")

	t.Logf("Vibration trend: %.6f", trend)

	if trend < 0.09 || trend > 0.11 {
		t.Errorf("Expected trend ~0.1, got %.6f", trend)
	}

	constantData := make([]models.BOGCompressorData, 50)
	for i := range constantData {
		constantData[i] = models.BOGCompressorData{
			Vibration: 1.5,
			Current:   30.0,
		}
	}

	flatTrend := service.calculateTrend(constantData, "vibration")

	t.Logf("Flat trend: %.6f", flatTrend)

	if math.Abs(flatTrend) > 0.001 {
		t.Errorf("Expected flat trend ~0, got %.6f", flatTrend)
	}
}

func TestConfidenceCalculation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
	}

	normalFeatures := []float64{0.3, 0.3, 0.5, 0.3, 0.3, 0.5, 0.5, 0.3}
	anomalyFeatures := []float64{0.9, 0.9, 0.2, 0.9, 0.9, 0.9, 0.9, 0.9}

	normalConf := service.calculateConfidence(0.3, normalFeatures)
	anomalyConf := service.calculateConfidence(0.9, anomalyFeatures)

	t.Logf("Normal confidence: %.4f", normalConf)
	t.Logf("Anomaly confidence: %.4f", anomalyConf)

	if normalConf < 0 || normalConf > 1 {
		t.Errorf("Normal confidence out of range: %.4f", normalConf)
	}

	if anomalyConf < 0 || anomalyConf > 1 {
		t.Errorf("Anomaly confidence out of range: %.4f", anomalyConf)
	}

	if anomalyConf <= normalConf {
		t.Errorf("Expected anomaly confidence > normal confidence")
	}

	if anomalyConf < 0.7 {
		t.Errorf("Expected high anomaly confidence > 0.7, got %.4f", anomalyConf)
	}
}

func TestHarmonicNumber(t *testing.T) {
	f := &IsolationForest{}

	testCases := []struct {
		n        float64
		expected float64
	}{
		{1, math.Log(1) + 0.5772156649},
		{2, math.Log(2) + 0.5772156649},
		{3, math.Log(3) + 0.5772156649},
		{10, math.Log(10) + 0.5772156649},
	}

	for _, tc := range testCases {
		result := f.harmonicNumber(tc.n)
		diff := math.Abs(result - tc.expected)
		if diff > 0.001 {
			t.Errorf("Harmonic(%v): expected %.4f, got %.4f", tc.n, tc.expected, result)
		}
	}
}

func TestNormalize(t *testing.T) {
	s := &BOGDiagnosticService{}

	testCases := []struct {
		value    float64
		minVal   float64
		maxVal   float64
		expected float64
	}{
		{1.5, 0.5, 3.0, 0.4},
		{0.5, 0.5, 3.0, 0.0},
		{3.0, 0.5, 3.0, 1.0},
		{1.75, 0.5, 3.0, 0.5},
	}

	for _, tc := range testCases {
		result := s.normalize(tc.value, tc.minVal, tc.maxVal)
		diff := math.Abs(result - tc.expected)
		if diff > 0.001 {
			t.Errorf("Normalize(%.1f, %.1f, %.1f): expected %.2f, got %.2f",
				tc.value, tc.minVal, tc.maxVal, tc.expected, result)
		}
	}

	result := s.normalize(5.0, 5.0, 5.0)
	if result != 0.5 {
		t.Errorf("Normalize with zero range: expected 0.5, got %.2f", result)
	}
}

func TestAllSame(t *testing.T) {
	f := &IsolationForest{}

	allSame := [][]float64{
		{1.0, 2.0, 3.0},
		{1.0, 2.0, 3.0},
		{1.0, 2.0, 3.0},
	}

	if !f.allSame(allSame) {
		t.Error("Expected allSame to return true for identical samples")
	}

	notSame := [][]float64{
		{1.0, 2.0, 3.0},
		{1.1, 2.0, 3.0},
		{1.0, 2.0, 3.0},
	}

	if f.allSame(notSame) {
		t.Error("Expected allSame to return false for different samples")
	}
}

func TestFeatureRange(t *testing.T) {
	f := &IsolationForest{}

	X := [][]float64{
		{1.0, 5.0, 10.0},
		{2.0, 3.0, 15.0},
		{0.5, 7.0, 8.0},
	}

	minVal, maxVal := f.featureRange(X, 0)
	if minVal != 0.5 || maxVal != 2.0 {
		t.Errorf("Feature 0: expected min=0.5, max=2.0, got min=%.1f, max=%.1f", minVal, maxVal)
	}

	minVal, maxVal = f.featureRange(X, 2)
	if minVal != 8.0 || maxVal != 15.0 {
		t.Errorf("Feature 2: expected min=8.0, max=15.0, got min=%.1f, max=%.1f", minVal, maxVal)
	}
}

func TestPathLength(t *testing.T) {
	tree := &TreeNode{
		splitFeature: 0,
		splitValue:   1.5,
		left: &TreeNode{
			isLeaf: true,
			size:   5,
		},
		right: &TreeNode{
			isLeaf: true,
			size:   3,
		},
	}

	f := &IsolationForest{}

	lengthLeft := f.pathLength([]float64{1.0, 2.0}, tree, 0)
	expectedLeft := 1.0 + f.harmonicNumber(4)
	if math.Abs(lengthLeft-expectedLeft) > 0.001 {
		t.Errorf("Left path length: expected %.4f, got %.4f", expectedLeft, lengthLeft)
	}

	lengthRight := f.pathLength([]float64{2.0, 2.0}, tree, 0)
	expectedRight := 1.0 + f.harmonicNumber(2)
	if math.Abs(lengthRight-expectedRight) > 0.001 {
		t.Errorf("Right path length: expected %.4f, got %.4f", expectedRight, lengthRight)
	}

	singleLeaf := &TreeNode{
		isLeaf: true,
		size:   1,
	}

	lengthSingle := f.pathLength([]float64{1.0}, singleLeaf, 5)
	if lengthSingle != 5.0 {
		t.Errorf("Single leaf path length: expected 5.0, got %.4f", lengthSingle)
	}
}

func TestDiagnosticResultFields(t *testing.T) {
	result := messages.BOGDiagnosticResult{
		TankID:         1,
		CompressorID:   2,
		AnomalyScore:   0.85,
		IsAnomaly:      true,
		AnomalyType:    "bearing_fault",
		Confidence:     0.9,
		RemainingHours: 100,
		Recommendation: "Test recommendation",
		DiagnosedAt:    time.Now(),
	}

	if result.TankID != 1 {
		t.Error("TankID mismatch")
	}
	if result.CompressorID != 2 {
		t.Error("CompressorID mismatch")
	}
	if result.AnomalyScore != 0.85 {
		t.Error("AnomalyScore mismatch")
	}
	if !result.IsAnomaly {
		t.Error("IsAnomaly should be true")
	}
	if result.AnomalyType != "bearing_fault" {
		t.Error("AnomalyType mismatch")
	}

	t.Logf("Diagnostic result: %+v", result)
}

func TestPerformanceBenchmark(t *testing.T) {
	cfg := testutils.NewTestConfig()
	service := &BOGDiagnosticService{
		cfg:         cfg,
		modelParams: &cfg.ModelParams.BOGDiagnostic,
		iforest:     NewIsolationForest(100, 256),
	}

	nTraining := 500
	trainingData := make([][]float64, nTraining)
	for i := 0; i < nTraining; i++ {
		data := testutils.GenerateNormalBOGData(100, 1)
		trainingData[i] = service.extractFeatures(data[0], data)
	}

	startFit := time.Now()
	service.iforest.Fit(trainingData)
	fitDuration := time.Since(startFit)

	t.Logf("Fit %d samples, %d trees: %v", nTraining, service.iforest.nTrees, fitDuration)

	if fitDuration > 2*time.Second {
		t.Errorf("Fit too slow: %v", fitDuration)
	}

	nPredictions := 1000
	startPredict := time.Now()
	for i := 0; i < nPredictions; i++ {
		data := testutils.GenerateNormalBOGData(1, 1)
		features := service.extractFeatures(data[0], data)
		_ = service.iforest.AnomalyScore(features)
	}
	predictDuration := time.Since(startPredict)
	predictPerOp := predictDuration / time.Duration(nPredictions)

	t.Logf("Predict %d times: %v total, %v per op", nPredictions, predictDuration, predictPerOp)

	if predictPerOp > 1*time.Millisecond {
		t.Errorf("Prediction too slow: %v per op", predictPerOp)
	}
}
