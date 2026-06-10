package testutils

import (
	"math"
	"math/rand"
	"time"

	"lng-monitoring/config"
	"lng-monitoring/messages"
	"lng-monitoring/models"
)

var rng = rand.New(rand.NewSource(42))

func init() {
}

type TestMetrics struct {
	TruePositives  int
	TrueNegatives  int
	FalsePositives int
	FalseNegatives int
}

func (m *TestMetrics) Accuracy() float64 {
	total := m.TruePositives + m.TrueNegatives + m.FalsePositives + m.FalseNegatives
	if total == 0 {
		return 0
	}
	return float64(m.TruePositives+m.TrueNegatives) / float64(total)
}

func (m *TestMetrics) Precision() float64 {
	denom := m.TruePositives + m.FalsePositives
	if denom == 0 {
		return 0
	}
	return float64(m.TruePositives) / float64(denom)
}

func (m *TestMetrics) Recall() float64 {
	denom := m.TruePositives + m.FalseNegatives
	if denom == 0 {
		return 0
	}
	return float64(m.TruePositives) / float64(denom)
}

func (m *TestMetrics) F1Score() float64 {
	p := m.Precision()
	r := m.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

func RMSE(predictions, actuals []float64) float64 {
	if len(predictions) != len(actuals) || len(predictions) == 0 {
		return math.Inf(1)
	}
	var sumSq float64
	for i := range predictions {
		diff := predictions[i] - actuals[i]
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(predictions)))
}

func MAE(predictions, actuals []float64) float64 {
	if len(predictions) != len(actuals) || len(predictions) == 0 {
		return math.Inf(1)
	}
	var sumAbs float64
	for i := range predictions {
		sumAbs += math.Abs(predictions[i] - actuals[i])
	}
	return sumAbs / float64(len(predictions))
}

func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func StdDev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := Mean(values)
	var sumSq float64
	for _, v := range values {
		diff := v - mean
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(values)-1))
}

func GenerateNormalBOGData(nSamples int, compressorID int) []models.BOGCompressorData {
	data := make([]models.BOGCompressorData, nSamples)
	baseTime := time.Now().Add(-time.Duration(nSamples) * time.Minute)

	for i := 0; i < nSamples; i++ {
		data[i] = models.BOGCompressorData{
			Time:              baseTime.Add(time.Duration(i) * time.Minute),
			TankID:            1,
			CompressorID:      compressorID,
			Vibration:         1.5 + rng.NormFloat64()*0.3,
			Current:           30.0 + rng.NormFloat64()*3.0,
			Temperature:       85.0 + rng.NormFloat64()*5.0,
			Pressure:          0.15 + rng.NormFloat64()*0.02,
			FlowRate:          500.0 + rng.NormFloat64()*50.0,
			RuntimeHours:      float64(i) / 60.0,
			OilTemperature:    65.0 + rng.NormFloat64()*3.0,
			DischargePressure: 1.2 + rng.NormFloat64()*0.1,
			SuctionPressure:   0.12 + rng.NormFloat64()*0.01,
		}
	}
	return data
}

func GenerateBearingFaultData(nSamples int, compressorID int, severity float64) []models.BOGCompressorData {
	data := GenerateNormalBOGData(nSamples, compressorID)
	for i := range data {
		faultFactor := 1.0 + severity*float64(i)/float64(nSamples)
		data[i].Vibration = 3.0 + rng.NormFloat64()*0.8*faultFactor
		data[i].Temperature = 95.0 + rng.NormFloat64()*8.0*faultFactor
		data[i].OilTemperature = 75.0 + rng.NormFloat64()*5.0*faultFactor
	}
	return data
}

func GeneratePistonRingWearData(nSamples int, compressorID int, severity float64) []models.BOGCompressorData {
	data := GenerateNormalBOGData(nSamples, compressorID)
	for i := range data {
		faultFactor := 1.0 + severity*float64(i)/float64(nSamples)
		data[i].Current = 45.0 + rng.NormFloat64()*5.0*faultFactor
		data[i].Pressure = 0.12 + rng.NormFloat64()*0.03*faultFactor
		data[i].FlowRate = 400.0 + rng.NormFloat64()*60.0*faultFactor
		data[i].DischargePressure = 1.0 + rng.NormFloat64()*0.15*faultFactor
	}
	return data
}

func GenerateImbalanceData(nSamples int, compressorID int, severity float64) []models.BOGCompressorData {
	data := GenerateNormalBOGData(nSamples, compressorID)
	for i := range data {
		faultFactor := 1.0 + severity*float64(i)/float64(nSamples)
		data[i].Vibration = 2.5 + rng.NormFloat64()*0.6*faultFactor
		data[i].Current = 35.0 + rng.NormFloat64()*4.0*faultFactor
	}
	return data
}

func GenerateNormalLayerData(nLayers, nTimeSteps int, baseTemp float64) []models.LayerSummary {
	data := make([]models.LayerSummary, nTimeSteps)
	baseTime := time.Now().Add(-time.Duration(nTimeSteps) * time.Hour)

	for t := 0; t < nTimeSteps; t++ {
		layers := make([]models.LayerData, nLayers)
		for i := 0; i < nLayers; i++ {
			heightFactor := float64(i) / float64(nLayers)
			layers[i] = models.LayerData{
				LayerIndex:  i,
				AvgTemp:     baseTemp + heightFactor*2.0 + rng.NormFloat64()*0.1,
				AvgDensity:  425.0 - heightFactor*3.0 + rng.NormFloat64()*0.3,
				Height:      heightFactor * 40.0,
				Pressure:    0.15 + heightFactor*0.01,
			}
		}
		data[t] = models.LayerSummary{
			Time:       baseTime.Add(time.Duration(t) * time.Hour),
			TankID:     1,
			Layers:     layers,
			MaxTempDiff: 2.0 + rng.NormFloat64()*0.2,
			MaxDensityDiff: 3.0 + rng.NormFloat64()*0.3,
		}
	}
	return data
}

func GenerateHeatLeakData(nLayers, nTimeSteps int, leakRegions []int, leakSeverity float64) []models.LayerSummary {
	data := GenerateNormalLayerData(nLayers, nTimeSteps, -160.0)

	for t := range data {
		timeFactor := 1.0 + 0.5*float64(t)/float64(nTimeSteps)
		for _, layerIdx := range leakRegions {
			if layerIdx < len(data[t].Layers) {
				data[t].Layers[layerIdx].AvgTemp += leakSeverity * timeFactor
				data[t].Layers[layerIdx].AvgDensity -= leakSeverity * timeFactor * 0.5
			}
		}

		var maxTemp, minTemp float64 = math.Inf(-1), math.Inf(1)
		var maxDensity, minDensity float64 = math.Inf(-1), math.Inf(1)
		for _, layer := range data[t].Layers {
			if layer.AvgTemp > maxTemp {
				maxTemp = layer.AvgTemp
			}
			if layer.AvgTemp < minTemp {
				minTemp = layer.AvgTemp
			}
			if layer.AvgDensity > maxDensity {
				maxDensity = layer.AvgDensity
			}
			if layer.AvgDensity < minDensity {
				minDensity = layer.AvgDensity
			}
		}
		data[t].MaxTempDiff = maxTemp - minTemp
		data[t].MaxDensityDiff = maxDensity - minDensity
	}

	return data
}

func GenerateInitialTankState(nLayers int) []models.LayerData {
	layers := make([]models.LayerData, nLayers)
	for i := 0; i < nLayers; i++ {
		heightFactor := float64(i) / float64(nLayers)
		layers[i] = models.LayerData{
			LayerIndex:  i,
			AvgTemp:     -160.0 + heightFactor*1.5,
			AvgDensity:  425.0 - heightFactor*2.0,
			Height:      heightFactor * 40.0,
			Mass:        100000.0 / float64(nLayers),
			Pressure:    0.15 + heightFactor*0.01,
		}
	}
	return layers
}

func GenerateTankStatesForScheduler(nTanks int, riskPattern string) []messages.TankStateForScheduler {
	states := make([]messages.TankStateForScheduler, nTanks)
	for i := 0; i < nTanks; i++ {
		var risk float64
		switch riskPattern {
		case "all_low":
			risk = 0.1 + rng.Float64()*0.2
		case "all_high":
			risk = 0.7 + rng.Float64()*0.3
		case "mixed":
			risk = float64(i) / float64(nTanks-1) * 0.8
		case "single_high":
			if i == 0 {
				risk = 0.9
			} else {
				risk = 0.1 + rng.Float64()*0.2
			}
		default:
			risk = rng.Float64()
		}

		states[i] = messages.TankStateForScheduler{
			TankID:      i + 1,
			Level:       0.4 + rng.Float64()*0.5,
			AvgTemp:     -160.0 + rng.Float64()*5.0,
			RiskIndex:   risk,
			Pressure:    0.1 + rng.Float64()*0.1,
			HasBOGComp1: true,
			HasBOGComp2: true,
		}
	}
	return states
}

func NewTestConfig() *config.Config {
	return &config.Config{
		BOGDiagnostic: config.BOGDiagnosticConfig{
			AnomalyThreshold:    0.7,
			WarningThreshold:    0.5,
			HistoryWindowHours:  24,
			AutoDiagnose:        true,
			DiagnosticInterval:  30,
		},
		HeatLeak: config.HeatLeakConfig{
			WarningThresholdPct:   20.0,
			HistoryWindowHours:    24,
			AutoEvaluate:          true,
			EvaluationIntervalHours: 60,
		},
		Unloading: config.UnloadingConfig{
			AutoPredict: true,
		},
		Scheduler: config.SchedulerConfig{
			AutoOptimize:            true,
			OptimizationIntervalMin: 10,
			MinRiskForAction:        0.4,
			IntervalSec:             600,
			ModelVersion:            "1.0",
		},
		ModelParams: &config.ModelParams{
			TankSpecs: config.TankSpecs{
				HeightMeters:        40.0,
				DiameterMeters:      80.0,
				CapacityCubicMeters: 200000.0,
				Layers:              20,
				ThermometersPerLayer: 8,
				DensityMeters:        8,
				DensitySensorHeights: []float64{5.0, 10.0, 15.0, 20.0, 25.0, 30.0, 35.0, 38.0},
				LayerHeights:        []float64{2.0, 4.0, 6.0, 8.0, 10.0, 12.0, 14.0, 16.0, 18.0, 20.0, 22.0, 24.0, 26.0, 28.0, 30.0, 32.0, 34.0, 36.0, 38.0, 40.0},
			},
			BOGDiagnostic: config.BOGDiagnosticParams{
				ContaminationRate:     0.1,
				NormalVibrationRange:  [2]float64{0.5, 3.0},
				NormalCurrentRange:    [2]float64{15.0, 45.0},
				AnomalyThreshold:      0.7,
				WarningThreshold:      0.5,
				HistoryWindowHours:    24,
				TrendWindowPoints:     50,
				IForestNTrees:         100,
				IForestSampleSize:     256,
				FaultTypeThresholds: map[string]float64{
					"bearing_fault":      0.65,
					"piston_ring_wear":   0.60,
					"imbalance":          0.55,
					"motor_fault":        0.70,
				},
			},
			HeatLeak: config.HeatLeakParams{
				ReferenceConductivity:   0.025,
				InsulationThickness:     0.8,
				WarningThresholdPct:     20.0,
				EvaluationIntervalHours: 1,
				HistoryWindowHours:      24,
				SurfaceAreaSqM:          25000.0,
				MaxHeatLoadKW:           150.0,
				CalibrationIntervalDays: 90,
			},
			Unloading: config.UnloadingParams{
				MixingEfficiency:      0.85,
				PumpFlowRateM3H:       800.0,
				MinPumpDurationHours:  0.5,
				MaxStratificationSafe: 3.0,
				PredictionTimeStepMin: 5,
				NumVerticalLayers:     20,
				AxialDispersionCoeff:  0.05,
				DensityDiffusionCoeff: 1.0e-8,
			},
			Scheduler: config.SchedulerParams{
				CompressorEfficiency:    0.75,
				EvaporationLossCostYuan: 4500.0,
				ElectricityCostYuan:     0.65,
				PumpPowerKW:             220.0,
				CompressorPowerKWPerPct: 2.5,
				MaxLoadPctPerCompressor: map[string]float64{
					"T1_C1": 100.0, "T1_C2": 100.0,
					"T2_C1": 100.0, "T2_C2": 100.0,
					"T3_C1": 100.0, "T3_C2": 100.0,
					"T4_C1": 100.0, "T4_C2": 100.0,
				},
				MinRuntimeHours:         2.0,
				OptimizationIntervalMin: 10,
			},
		},
	}
}

func GenerateBoundaryTestCases() map[string]map[string]float64 {
	return map[string]map[string]float64{
		"vibration": {
			"min_normal":     0.5,
			"max_normal":     3.0,
			"warning_start":  3.0,
			"critical_start": 5.0,
			"max_safe":       8.0,
		},
		"compressor_temperature": {
			"min_normal":     60.0,
			"max_normal":     85.0,
			"warning_start":  90.0,
			"critical_start": 100.0,
			"max_safe":       120.0,
		},
		"tank_temperature": {
			"min_safe":     -165.0,
			"max_safe":     -150.0,
			"warning_start": -155.0,
			"critical_start": -152.0,
			"optimal":      -160.0,
		},
		"current": {
			"min_normal":     15.0,
			"max_normal":     45.0,
			"warning_start":  50.0,
			"critical_start": 60.0,
			"max_safe":       80.0,
		},
		"insulation_performance": {
			"min_safe":       0.8,
			"optimal":        1.0,
			"warning_start":  0.8,
			"critical_start": 0.6,
			"max_possible":   1.2,
		},
	}
}
