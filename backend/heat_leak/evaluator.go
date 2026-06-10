package heat_leak

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"math"
	"sync"
	"time"
)

type HeatLeakEvaluator struct {
	cfg         *config.Config
	db          *database.DB
	requestChan <-chan messages.HeatLeakRequest
	resultChan  chan<- messages.HeatLeakResult
	mu          sync.RWMutex
	modelParams *config.HeatLeakParams
	calibration map[int]float64
}

type InverseSolver struct {
	lambda       float64
	maxIter      int
	tolerance    float64
	referenceK   float64
	insulationThickness float64
}

func NewHeatLeakEvaluator(
	cfg *config.Config,
	db *database.DB,
	requestChan <-chan messages.HeatLeakRequest,
	resultChan  chan<- messages.HeatLeakResult,
) *HeatLeakEvaluator {
	params := &config.HeatLeakParams{
		ReferenceConductivity:    0.025,
		InsulationThickness:      0.8,
		WarningThresholdPct:      cfg.HeatLeak.WarningThresholdPct,
		EvaluationIntervalHours:  1,
		HistoryWindowHours:       cfg.HeatLeak.HistoryWindowHours,
		SurfaceAreaSqM:           25000.0,
		MaxHeatLoadKW:            150.0,
		CalibrationIntervalDays:  90,
		SlidingWindowSize:        6,
		AmbientTempSmoothAlpha:   0.3,
		BaseRegularizationLambda: 0.01,
		AdaptiveRegularizationOn: true,
		TempChangeRateThreshold:  0.5,
		MaxRegularizationLambda:  0.1,
	}
	if cfg.ModelParams != nil {
		params = &cfg.ModelParams.HeatLeak
	}

	return &HeatLeakEvaluator{
		cfg:         cfg,
		db:          db,
		requestChan: requestChan,
		resultChan:  resultChan,
		modelParams: params,
		calibration: make(map[int]float64),
	}
}

func (e *HeatLeakEvaluator) Start(ctx context.Context) {
	go e.processLoop(ctx)
	if e.cfg.HeatLeak.AutoEvaluate {
		go e.scheduledEvaluations(ctx)
	}
}

func (e *HeatLeakEvaluator) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-e.requestChan:
			go e.processRequest(ctx, req)
		}
	}
}

func (e *HeatLeakEvaluator) scheduledEvaluations(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(e.cfg.HeatLeak.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.evaluateAllTanks(ctx)
		}
	}
}

func (e *HeatLeakEvaluator) processRequest(ctx context.Context, req messages.HeatLeakRequest) {
	result := e.evaluateHeatLeak(ctx, req.TankID, req.TemperatureHistory, req.AmbientTemperature)
	select {
	case <-ctx.Done():
		return
	case e.resultChan <- result:
	}

	go e.saveAssessment(ctx, result)
}

func (e *HeatLeakEvaluator) evaluateAllTanks(ctx context.Context) {
	ambientTemp, _ := e.db.GetAmbientTemperature(ctx, 1*time.Hour)
	if ambientTemp == 0 {
		ambientTemp = e.cfg.HeatLeak.DefaultAmbientTemp
	}

	for tankID := 1; tankID <= e.cfg.Modbus.TankCount; tankID++ {
		history, err := e.db.GetLayerSummaryHistory(ctx, tankID, e.modelParams.HistoryWindowHours)
		if err != nil || len(history) == 0 {
			continue
		}

		result := e.evaluateHeatLeak(ctx, tankID, history, ambientTemp)
		select {
		case <-ctx.Done():
			return
		case e.resultChan <- result:
		}

		go e.saveAssessment(ctx, result)
	}
}

func (e *HeatLeakEvaluator) evaluateHeatLeak(
	ctx context.Context,
	tankID int,
	tempHistory []models.LayerSummary,
	ambientTemp float64,
) messages.HeatLeakResult {
	if len(tempHistory) < 10 {
		return messages.HeatLeakResult{
			TankID:      tankID,
			EvaluatedAt: time.Now(),
			ErrorMessage: fmt.Sprintf("insufficient data: %d points, need at least 10", len(tempHistory)),
		}
	}

	ambientHistory, _ := e.db.GetAmbientTempHistory(ctx, e.modelParams.SlidingWindowSize)
	ambientChangeRate := e.calculateAmbientChangeRate(ambientHistory)
	smoothedAmbient := e.smoothAmbientTemperature(ambientTemp, ambientHistory)

	smoothedHistory := e.slidingWindowSmooth(tempHistory)
	layerData := e.organizeByLayer(smoothedHistory)
	innerTemp := e.calculateInnerAverageTemp(layerData)
	layerHeatRates := e.calculateLayerHeatRates(layerData)

	adaptiveLambda := e.calculateAdaptiveLambda(ambientChangeRate)

	referenceK := e.getCalibratedK(tankID)
	equivalentK, leakRegions := e.solveInverseProblem(
		layerHeatRates, layerData, innerTemp, smoothedAmbient, referenceK, adaptiveLambda,
	)

	insulationPerformance := e.calculateInsulationPerformance(equivalentK, referenceK)
	heatLeakRate := e.calculateTotalHeatLeakRate(equivalentK, innerTemp, smoothedAmbient)
	totalHeatLoad := heatLeakRate / 3600.0

	warningThreshold := (100.0 - e.modelParams.WarningThresholdPct) / 100.0
	isWarning := insulationPerformance < warningThreshold

	return messages.HeatLeakResult{
		TankID:                 tankID,
		EquivalentConductivity: equivalentK,
		InsulationPerformance:  insulationPerformance,
		HeatLeakRate:           heatLeakRate,
		LeakRegion:             leakRegions,
		IsWarning:              isWarning,
		TotalHeatLoadKW:        totalHeatLoad,
		LastCalibratedAt:       e.getLastCalibrationTime(tankID),
		EvaluatedAt:            time.Now(),
	}
}

func (e *HeatLeakEvaluator) organizeByLayer(history []models.LayerSummary) map[int][]models.LayerSummary {
	layerData := make(map[int][]models.LayerSummary)
	for _, d := range history {
		layerData[d.Layer] = append(layerData[d.Layer], d)
	}
	return layerData
}

func (e *HeatLeakEvaluator) calculateInnerAverageTemp(layerData map[int][]models.LayerSummary) float64 {
	var totalTemp float64
	var count int
	for _, data := range layerData {
		if len(data) > 0 {
			latest := data[len(data)-1]
			totalTemp += latest.AvgTemp
			count++
		}
	}
	if count == 0 {
		return -162.0
	}
	return totalTemp / float64(count)
}

func (e *HeatLeakEvaluator) calculateLayerHeatRates(layerData map[int][]models.LayerSummary) map[int]float64 {
	heatRates := make(map[int]float64)
	lngDensity := 425.0
	specificHeat := 2200.0
	layerHeight := e.modelParams.InsulationThickness / float64(len(layerData))

	for layer, data := range layerData {
		if len(data) < 5 {
			heatRates[layer] = 0
			continue
		}

		tempTrend := e.calculateTemperatureTrend(data)

		tankRadius := e.cfg.ModelParams.TankSpecs.DiameterMeters / 2.0
		layerVolume := math.Pi * tankRadius * tankRadius * layerHeight
		layerMass := layerVolume * lngDensity

		heatRate := layerMass * specificHeat * tempTrend
		heatRates[layer] = heatRate
	}

	return heatRates
}

func (e *HeatLeakEvaluator) smoothAmbientTemperature(
	ambientTemp float64,
	ambientHistory []float64,
) float64 {
	if len(ambientHistory) == 0 {
		return ambientTemp
	}

	alpha := e.modelParams.AmbientTempSmoothAlpha
	smoothed := ambientTemp
	for i := len(ambientHistory) - 1; i >= 0; i-- {
		smoothed = alpha*smoothed + (1-alpha)*ambientHistory[i]
	}

	return smoothed
}

func (e *HeatLeakEvaluator) slidingWindowSmooth(
	data []models.LayerSummary,
) []models.LayerSummary {
	windowSize := e.modelParams.SlidingWindowSize
	if windowSize <= 1 || len(data) < windowSize {
		return data
	}

	smoothed := make([]models.LayerSummary, len(data))
	for i := range data {
		start := i - windowSize/2
		if start < 0 {
			start = 0
		}
		end := start + windowSize
		if end > len(data) {
			end = len(data)
			start = end - windowSize
			if start < 0 {
				start = 0
			}
		}

		var sumTemp, sumDens float64
		count := 0
		for j := start; j < end; j++ {
			sumTemp += data[j].AvgTemp
			sumDens += data[j].AvgDensity
			count++
		}

		smoothed[i] = data[i]
		if count > 0 {
			smoothed[i].AvgTemp = sumTemp / float64(count)
			smoothed[i].AvgDensity = sumDens / float64(count)
		}
	}

	return smoothed
}

func (e *HeatLeakEvaluator) calculateAmbientChangeRate(
	ambientHistory []float64,
) float64 {
	if len(ambientHistory) < 2 {
		return 0
	}

	n := len(ambientHistory)
	var sumX, sumY, sumXY, sumX2 float64
	for i, t := range ambientHistory {
		x := float64(i)
		y := t
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	slope := (float64(n)*sumXY - sumX*sumY) / (float64(n)*sumX2 - sumX*sumX)
	return math.Abs(slope)
}

func (e *HeatLeakEvaluator) calculateAdaptiveLambda(
	ambientChangeRate float64,
) float64 {
	if !e.modelParams.AdaptiveRegularizationOn {
		return e.modelParams.BaseRegularizationLambda
	}

	lambda := e.modelParams.BaseRegularizationLambda
	if ambientChangeRate > e.modelParams.TempChangeRateThreshold {
		factor := ambientChangeRate / e.modelParams.TempChangeRateThreshold
		lambda = e.modelParams.BaseRegularizationLambda * math.Min(factor, 10.0)
	}

	return math.Min(lambda, e.modelParams.MaxRegularizationLambda)
}

func (e *HeatLeakEvaluator) calculateTemperatureTrend(data []models.LayerSummary) float64 {
	n := len(data)
	if n < 2 {
		return 0
	}

	smoothedData := e.slidingWindowSmooth(data)

	var sumX, sumY, sumXY, sumX2 float64
	for i, d := range smoothedData {
		x := float64(i)
		y := d.AvgTemp
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	slope := (float64(n)*sumXY - sumX*sumY) / (float64(n)*sumX2 - sumX*sumX)

	if len(smoothedData) >= 2 {
		timeDiff := smoothedData[len(smoothedData)-1].Time.Sub(smoothedData[0].Time).Seconds()
		if timeDiff > 0 {
			slope = slope / timeDiff * 30.0
		}
	}

	return slope
}

func (e *HeatLeakEvaluator) solveInverseProblem(
	layerHeatRates map[int]float64,
	layerData map[int][]models.LayerSummary,
	innerTemp, ambientTemp, referenceK float64,
	adaptiveLambda float64,
) (float64, []int) {
	solver := &InverseSolver{
		lambda:              adaptiveLambda,
		maxIter:             100,
		tolerance:           1e-6,
		referenceK:          referenceK,
		insulationThickness: e.modelParams.InsulationThickness,
	}

	tankDiameter := e.cfg.ModelParams.TankSpecs.DiameterMeters
	tankHeight := e.cfg.ModelParams.TankSpecs.HeightMeters
	totalArea := math.Pi * tankDiameter * tankHeight

	deltaT := ambientTemp - innerTemp
	if deltaT <= 0 {
		deltaT = 50.0
	}

	totalHeatRate := 0.0
	for _, hr := range layerHeatRates {
		totalHeatRate += math.Abs(hr)
	}

	initialK := referenceK
	equivalentK := solver.leastSquaresSolve(totalHeatRate, totalArea, deltaT, initialK)

	var leakRegions []int
	layerK := make(map[int]float64)
	for layer, hr := range layerHeatRates {
		layerArea := totalArea / float64(len(layerData))
		layerK[layer] = solver.leastSquaresSolve(math.Abs(hr), layerArea, deltaT, referenceK)

		ratio := layerK[layer] / referenceK
		if ratio > 1.2 {
			leakRegions = append(leakRegions, layer)
		}
	}

	if len(leakRegions) == 0 {
		anomalousLayers := e.detectAnomalousLayers(layerHeatRates, layerData)
		if len(anomalousLayers) > 0 {
			leakRegions = anomalousLayers
		}
	}

	return equivalentK, leakRegions
}

func (s *InverseSolver) leastSquaresSolve(heatRate, area, deltaT, initialK float64) float64 {
	if deltaT <= 0 || area <= 0 {
		return s.referenceK
	}

	k := initialK
	for i := 0; i < s.maxIter; i++ {
		predictedHeatRate := k * area * deltaT / s.insulationThickness
		residual := predictedHeatRate - heatRate

		if math.Abs(residual)/heatRate < s.tolerance {
			break
		}

		gradient := area * deltaT / s.insulationThickness
		if math.Abs(gradient) < 1e-10 {
			break
		}

		step := residual / (gradient + s.lambda*k)
		k = k - step

		k = math.Max(0.01, math.Min(0.5, k))
	}

	return k
}

func (e *HeatLeakEvaluator) detectAnomalousLayers(
	layerHeatRates map[int]float64,
	layerData map[int][]models.LayerSummary,
) []int {
	var rates []float64
	for _, hr := range layerHeatRates {
		rates = append(rates, math.Abs(hr))
	}
	if len(rates) == 0 {
		return nil
	}

	mean, std := e.statistics(rates)
	threshold := mean + 2.0*std

	var anomalous []int
	for layer, hr := range layerHeatRates {
		if math.Abs(hr) > threshold {
			anomalous = append(anomalous, layer)
		}
	}

	return anomalous
}

func (e *HeatLeakEvaluator) statistics(data []float64) (float64, float64) {
	if len(data) == 0 {
		return 0, 0
	}

	var sum float64
	for _, v := range data {
		sum += v
	}
	mean := sum / float64(len(data))

	var variance float64
	for _, v := range data {
		variance += math.Pow(v-mean, 2)
	}
	variance /= float64(len(data) - 1)

	return mean, math.Sqrt(variance)
}

func (e *HeatLeakEvaluator) calculateInsulationPerformance(equivalentK, referenceK float64) float64 {
	if referenceK <= 0 {
		return 1.0
	}
	performance := referenceK / equivalentK
	return math.Max(0, math.Min(1.0, performance))
}

func (e *HeatLeakEvaluator) calculateTotalHeatLeakRate(equivalentK, innerTemp, ambientTemp float64) float64 {
	area := e.modelParams.SurfaceAreaSqM
	thickness := e.modelParams.InsulationThickness
	deltaT := ambientTemp - innerTemp

	if deltaT <= 0 {
		deltaT = 50.0
	}

	heatLeakRate := equivalentK * area * deltaT / thickness
	return math.Min(heatLeakRate, e.modelParams.MaxHeatLoadKW*3600.0)
}

func (e *HeatLeakEvaluator) getCalibratedK(tankID int) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if k, ok := e.calibration[tankID]; ok {
		return k
	}
	return e.modelParams.ReferenceConductivity
}

func (e *HeatLeakEvaluator) getLastCalibrationTime(tankID int) time.Time {
	return time.Now().AddDate(0, 0, -30)
}

func (e *HeatLeakEvaluator) saveAssessment(ctx context.Context, result messages.HeatLeakResult) {
	ambientTemp, _ := e.db.GetAmbientTemperature(ctx, 1*time.Hour)
	if ambientTemp == 0 {
		ambientTemp = e.cfg.HeatLeak.DefaultAmbientTemp
	}

	assessment := &models.HeatLeakAssessment{
		Time:                   result.EvaluatedAt,
		TankID:                 result.TankID,
		EquivalentConductivity: result.EquivalentConductivity,
		InsulationPerformance:  result.InsulationPerformance,
		HeatLeakRate:           result.HeatLeakRate,
		LeakRegions:            result.LeakRegion,
		IsWarning:              result.IsWarning,
		TotalHeatLoadKW:        result.TotalHeatLoadKW,
		AmbientTemp:            ambientTemp,
		InnerTemp:              -162.0,
		ModelVersion:           e.cfg.HeatLeak.ModelVersion,
	}

	if err := e.db.InsertHeatLeakAssessment(ctx, assessment); err != nil {
		fmt.Printf("Error saving heat leak assessment: %v\n", err)
	}
}

func (e *HeatLeakEvaluator) RunManualEvaluation(
	ctx context.Context,
	tankID int,
	ambientTemp float64,
	historyHours int,
) (*messages.HeatLeakResult, error) {
	history, err := e.db.GetLayerSummaryHistory(ctx, tankID, historyHours)
	if err != nil {
		return nil, fmt.Errorf("get layer summary history: %w", err)
	}
	if len(history) < 10 {
		return nil, fmt.Errorf("insufficient data: %d points, need at least 10", len(history))
	}

	if ambientTemp <= -100 {
		ambientTemp, _ = e.db.GetAmbientTemperature(ctx, 1*time.Hour)
		if ambientTemp == 0 {
			ambientTemp = e.cfg.HeatLeak.DefaultAmbientTemp
		}
	}

	result := e.evaluateHeatLeak(ctx, tankID, history, ambientTemp)
	go e.saveAssessment(ctx, result)

	return &result, nil
}

func (e *HeatLeakEvaluator) Calibrate(tankID int, referenceK float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if referenceK > 0 {
		e.calibration[tankID] = referenceK
	}
}
