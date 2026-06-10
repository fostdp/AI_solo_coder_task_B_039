package unloading_predictor

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

type UnloadingPredictor struct {
	cfg         *config.Config
	db          *database.DB
	requestChan <-chan messages.UnloadingRequest
	resultChan  chan<- messages.UnloadingPrediction
	mu          sync.RWMutex
	modelParams *config.UnloadingParams
}

type OneDMixerModel struct {
	nLayers          int
	layerHeights     []float64
	mixingEfficiency float64
	axialDispersion  float64
	densityDiffusion float64
}

type LayerState struct {
	Temperature float64
	Density     float64
	Height      float64
	Mass        float64
}

func NewUnloadingPredictor(
	cfg *config.Config,
	db *database.DB,
	requestChan <-chan messages.UnloadingRequest,
	resultChan chan<- messages.UnloadingPrediction,
) *UnloadingPredictor {
	params := &config.UnloadingParams{
		MixingEfficiency:      0.85,
		PumpFlowRateM3H:       800.0,
		MinPumpDurationHours:  0.5,
		MaxStratificationSafe: 3.0,
		PredictionTimeStepMin: 5,
		NumVerticalLayers:     20,
		AxialDispersionCoeff:  0.05,
		DensityDiffusionCoeff: 1.0e-8,
	}
	if cfg.ModelParams != nil {
		params = &cfg.ModelParams.Unloading
	}

	return &UnloadingPredictor{
		cfg:         cfg,
		db:          db,
		requestChan: requestChan,
		resultChan:  resultChan,
		modelParams: params,
	}
}

func (p *UnloadingPredictor) Start(ctx context.Context) {
	go p.processLoop(ctx)
}

func (p *UnloadingPredictor) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-p.requestChan:
			go p.processRequest(ctx, req)
		}
	}
}

func (p *UnloadingPredictor) processRequest(ctx context.Context, req messages.UnloadingRequest) {
	result := p.predictUnloading(ctx, req)
	select {
	case <-ctx.Done():
		return
	case p.resultChan <- result:
	}

	go p.savePrediction(ctx, req, result)
}

func (p *UnloadingPredictor) predictUnloading(
	ctx context.Context,
	req messages.UnloadingRequest,
) messages.UnloadingPrediction {
	if req.EstimatedDuration <= 0 {
		return messages.UnloadingPrediction{
			TankID:        req.TankID,
			PredictedAt:   time.Now(),
			ErrorMessage:  "invalid estimated duration",
		}
	}

	nLayers := p.modelParams.NumVerticalLayers
	timeStep := float64(p.modelParams.PredictionTimeStepMin) / 60.0
	totalSteps := int(req.EstimatedDuration / timeStep)
	if totalSteps < 2 {
		totalSteps = 2
	}

	model := &OneDMixerModel{
		nLayers:          nLayers,
		mixingEfficiency: p.modelParams.MixingEfficiency,
		axialDispersion:  p.modelParams.AxialDispersionCoeff,
		densityDiffusion: p.modelParams.DensityDiffusionCoeff,
	}

	model.layerHeights = p.calculateLayerHeights(nLayers, req.TankID)

	initialState := p.initializeLayers(req, nLayers)

	predictedTemps := make([][]float64, totalSteps)
	predictedDensities := make([][]float64, totalSteps)
	timeSteps := make([]float64, totalSteps)

	for t := 0; t < totalSteps; t++ {
		timeSteps[t] = float64(t) * timeStep

		currentTemps := make([]float64, nLayers)
		currentDensities := make([]float64, nLayers)
		for i := 0; i < nLayers; i++ {
			currentTemps[i] = initialState[i].Temperature
			currentDensities[i] = initialState[i].Density
		}
		predictedTemps[t] = currentTemps
		predictedDensities[t] = currentDensities

		if t < totalSteps-1 {
			p.advanceTimeStep(model, initialState, req, timeStep, t)
		}
	}

	maxTempDiff, maxDensityDiff := p.calculateMaxDifferences(predictedTemps, predictedDensities)
	optimalPumpOnTime := p.calculateOptimalPumpTime(predictedTemps, predictedDensities, timeSteps, req)
	rolloverRisk := p.calculateRolloverRisk(predictedTemps, predictedDensities)

	return messages.UnloadingPrediction{
		TankID:             req.TankID,
		PredictedTemps:     predictedTemps,
		PredictedDensities: predictedDensities,
		TimeSteps:          timeSteps,
		MaxTempDiff:        maxTempDiff,
		MaxDensityDiff:     maxDensityDiff,
		OptimalPumpOnTime:  optimalPumpOnTime,
		RolloverRisk:       rolloverRisk,
		PredictedAt:        time.Now(),
	}
}

func (p *UnloadingPredictor) calculateLayerHeights(nLayers, tankID int) []float64 {
	heights := make([]float64, nLayers)
	tankHeight := p.cfg.ModelParams.TankSpecs.HeightMeters
	layerHeight := tankHeight / float64(nLayers)
	for i := 0; i < nLayers; i++ {
		heights[i] = layerHeight * (float64(i) + 0.5)
	}
	return heights
}

func (p *UnloadingPredictor) initializeLayers(req messages.UnloadingRequest, nLayers int) []LayerState {
	states := make([]LayerState, nLayers)
	tankHeight := p.cfg.ModelParams.TankSpecs.HeightMeters
	tankRadius := p.cfg.ModelParams.TankSpecs.DiameterMeters / 2.0
	totalVolume := math.Pi * tankRadius * tankRadius * tankHeight
	baseDensity := p.cfg.ModelParams.PhysicalProperties.BaseDensity

	layerHeight := tankHeight / float64(nLayers)
	layerVolume := totalVolume / float64(nLayers)

	for i := 0; i < nLayers; i++ {
		heightRatio := float64(i) / float64(nLayers-1)
		interpolatedTemp := p.interpolateTemperature(req.InitialTemps, heightRatio)
		interpolatedDensity := p.interpolateDensity(req.InitialDensities, heightRatio)

		if interpolatedDensity <= 0 {
			interpolatedDensity = baseDensity
		}

		states[i] = LayerState{
			Temperature: interpolatedTemp,
			Density:     interpolatedDensity,
			Height:      layerHeight * (float64(i) + 0.5),
			Mass:        layerVolume * interpolatedDensity,
		}
	}

	return states
}

func (p *UnloadingPredictor) interpolateTemperature(temps []float64, heightRatio float64) float64 {
	if len(temps) == 0 {
		return -162.0
	}
	if len(temps) == 1 {
		return temps[0]
	}

	pos := heightRatio * float64(len(temps)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))

	if lower == upper {
		return temps[lower]
	}

	frac := pos - float64(lower)
	if lower < 0 {
		lower = 0
	}
	if upper >= len(temps) {
		upper = len(temps) - 1
	}

	return temps[lower]*(1-frac) + temps[upper]*frac
}

func (p *UnloadingPredictor) interpolateDensity(densities []float64, heightRatio float64) float64 {
	if len(densities) == 0 {
		return 425.0
	}
	if len(densities) == 1 {
		return densities[0]
	}

	pos := heightRatio * float64(len(densities)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))

	if lower == upper {
		return densities[lower]
	}

	frac := pos - float64(lower)
	if lower < 0 {
		lower = 0
	}
	if upper >= len(densities) {
		upper = len(densities) - 1
	}

	return densities[lower]*(1-frac) + densities[upper]*frac
}

func (p *UnloadingPredictor) advanceTimeStep(
	model *OneDMixerModel,
	state []LayerState,
	req messages.UnloadingRequest,
	timeStep float64,
	stepIndex int,
) {
	nLayers := model.nLayers
	unloadingMassFlow := req.UnloadingRate * req.UnloadingDensity / 3600.0
	tankRadius := p.cfg.ModelParams.TankSpecs.DiameterMeters / 2.0
	tankArea := math.Pi * tankRadius * tankRadius

	elapsedTime := float64(stepIndex) * timeStep
	if elapsedTime >= req.EstimatedDuration {
		return
	}

	newState := make([]LayerState, nLayers)
	for i := range state {
		newState[i] = state[i]
	}

	injectionLayer := p.findInjectionLayer(state, req.UnloadingDensity)
	if injectionLayer >= 0 && injectionLayer < nLayers {
		massAdded := unloadingMassFlow * timeStep * model.mixingEfficiency
		newState[injectionLayer].Mass += massAdded

		oldTemp := newState[injectionLayer].Temperature
		oldMass := newState[injectionLayer].Mass
		newTemp := (oldTemp*(oldMass-massAdded) + req.UnloadingTemp*massAdded) / oldMass
		newState[injectionLayer].Temperature = newTemp

		newDensity := oldMass / (oldMass/state[injectionLayer].Density + massAdded/req.UnloadingDensity)
		newState[injectionLayer].Density = newDensity
	}

	for i := 1; i < nLayers-1; i++ {
		dispersionFluxT := model.axialDispersion * tankArea *
			(newState[i+1].Temperature - 2*newState[i].Temperature + newState[i-1].Temperature) /
			(model.layerHeights[1] - model.layerHeights[0])

		dispersionFluxD := model.axialDispersion * tankArea *
			(newState[i+1].Density - 2*newState[i].Density + newState[i-1].Density) /
			(model.layerHeights[1] - model.layerHeights[0])

		layerVolume := tankArea * (model.layerHeights[1] - model.layerHeights[0])
		layerMass := newState[i].Mass

		newState[i].Temperature += dispersionFluxT * timeStep / layerMass * 425.0 * 2200.0
		newState[i].Density += dispersionFluxD * timeStep / layerVolume

		densityGradient := (newState[i+1].Density - newState[i-1].Density) /
			(model.layerHeights[i+1] - model.layerHeights[i-1])

		if densityGradient > 0 && newState[i].Density < newState[i+1].Density {
			mixingAmount := densityGradient * model.densityDiffusion * timeStep

			tempExchange := mixingAmount * (newState[i+1].Temperature - newState[i].Temperature)
			densExchange := mixingAmount * (newState[i+1].Density - newState[i].Density)

			newState[i].Temperature += tempExchange
			newState[i+1].Temperature -= tempExchange
			newState[i].Density += densExchange
			newState[i+1].Density -= densExchange
		}
	}

	for i := 0; i < nLayers-1; i++ {
		if newState[i].Density > newState[i+1].Density {
			overturnRatio := 0.1
			tempSwap := (newState[i].Temperature + newState[i+1].Temperature) / 2
			densSwap := (newState[i].Density + newState[i+1].Density) / 2

			newState[i].Temperature = newState[i].Temperature*(1-overturnRatio) + tempSwap*overturnRatio
			newState[i+1].Temperature = newState[i+1].Temperature*(1-overturnRatio) + tempSwap*overturnRatio
			newState[i].Density = newState[i].Density*(1-overturnRatio) + densSwap*overturnRatio
			newState[i+1].Density = newState[i+1].Density*(1-overturnRatio) + densSwap*overturnRatio
		}
	}

	for i := range state {
		state[i] = newState[i]
	}
}

func (p *UnloadingPredictor) findInjectionLayer(state []LayerState, unloadingDensity float64) int {
	for i := 0; i < len(state); i++ {
		if state[i].Density > unloadingDensity {
			if i == 0 {
				return 0
			}
			if math.Abs(state[i].Density-unloadingDensity) < math.Abs(state[i-1].Density-unloadingDensity) {
				return i
			}
			return i - 1
		}
	}
	return len(state) - 1
}

func (p *UnloadingPredictor) calculateMaxDifferences(
	temps [][]float64,
	densities [][]float64,
) (float64, float64) {
	maxTempDiff := 0.0
	maxDensityDiff := 0.0

	for t := 0; t < len(temps); t++ {
		minT, maxT := temps[t][0], temps[t][0]
		minD, maxD := densities[t][0], densities[t][0]

		for i := 1; i < len(temps[t]); i++ {
			if temps[t][i] < minT {
				minT = temps[t][i]
			}
			if temps[t][i] > maxT {
				maxT = temps[t][i]
			}
			if densities[t][i] < minD {
				minD = densities[t][i]
			}
			if densities[t][i] > maxD {
				maxD = densities[t][i]
			}
		}

		tempDiff := maxT - minT
		densityDiff := maxD - minD

		if tempDiff > maxTempDiff {
			maxTempDiff = tempDiff
		}
		if densityDiff > maxDensityDiff {
			maxDensityDiff = densityDiff
		}
	}

	return maxTempDiff, maxDensityDiff
}

func (p *UnloadingPredictor) calculateOptimalPumpTime(
	temps [][]float64,
	densities [][]float64,
	timeSteps []float64,
	req messages.UnloadingRequest,
) float64 {
	safeTempDiff := p.modelParams.MaxStratificationSafe
	safeDensityDiff := 2.0

	for t := 0; t < len(timeSteps); t++ {
		minT, maxT := temps[t][0], temps[t][0]
		minD, maxD := densities[t][0], densities[t][0]

		for i := 1; i < len(temps[t]); i++ {
			if temps[t][i] < minT {
				minT = temps[t][i]
			}
			if temps[t][i] > maxT {
				maxT = temps[t][i]
			}
			if densities[t][i] < minD {
				minD = densities[t][i]
			}
			if densities[t][i] > maxD {
				maxD = densities[t][i]
			}
		}

		if (maxT-minT > safeTempDiff) || (maxD-minD > safeDensityDiff) {
			pumpTime := timeSteps[t]
			if pumpTime < p.modelParams.MinPumpDurationHours {
				return p.modelParams.MinPumpDurationHours
			}
			return pumpTime
		}
	}

	return req.EstimatedDuration * 0.5
}

func (p *UnloadingPredictor) calculateRolloverRisk(
	temps [][]float64,
	densities [][]float64,
) float64 {
	maxRisk := 0.0

	for t := 0; t < len(temps); t++ {
		risk := p.calculateInstantRisk(temps[t], densities[t])
		if risk > maxRisk {
			maxRisk = risk
		}
	}

	return maxRisk
}

func (p *UnloadingPredictor) calculateInstantRisk(temps, densities []float64) float64 {
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

	tempRisk := math.Min(1.0, (maxT-minT)/10.0)
	densityRisk := math.Min(1.0, (maxD-minD)/5.0)

	instability := 0.0
	for i := 0; i < len(densities)-1; i++ {
		if densities[i] < densities[i+1] {
			instability += (densities[i+1] - densities[i]) / 5.0
		}
	}
	instability = math.Min(1.0, instability)

	risk := 0.35*tempRisk + 0.25*densityRisk + 0.4*instability
	return math.Max(0.0, math.Min(1.0, risk))
}

func (p *UnloadingPredictor) savePrediction(
	ctx context.Context,
	req messages.UnloadingRequest,
	result messages.UnloadingPrediction,
) {
	pred := &models.UnloadingPredictionModel{
		Time:               result.PredictedAt,
		TankID:             req.TankID,
		UnloadingRate:      req.UnloadingRate,
		UnloadingDensity:   req.UnloadingDensity,
		UnloadingTemp:      req.UnloadingTemp,
		EstimatedDuration:  req.EstimatedDuration,
		MaxTempDiff:        result.MaxTempDiff,
		MaxDensityDiff:     result.MaxDensityDiff,
		OptimalPumpOnTime:  result.OptimalPumpOnTime,
		RolloverRisk:       result.RolloverRisk,
		TimeSteps:          result.TimeSteps,
		PredictedTemps:     result.PredictedTemps,
		PredictedDensities: result.PredictedDensities,
		ModelVersion:       p.cfg.Unloading.ModelVersion,
	}

	if err := p.db.InsertUnloadingPrediction(ctx, pred); err != nil {
		fmt.Printf("Error saving unloading prediction: %v\n", err)
	}
}

func (p *UnloadingPredictor) RunManualPrediction(
	ctx context.Context,
	req models.UnloadingManualRequest,
) (*messages.UnloadingPrediction, error) {
	temps, err := p.db.GetLayerAvgTemps(ctx, req.TankID)
	if err != nil {
		return nil, fmt.Errorf("get layer temps: %w", err)
	}

	densities, err := p.db.GetLatestDensityData(ctx, req.TankID)
	if err != nil {
		return nil, fmt.Errorf("get density data: %w", err)
	}

	densityValues := make([]float64, len(densities))
	for i, d := range densities {
		densityValues[i] = d.Density
	}

	messageReq := messages.UnloadingRequest{
		TankID:            req.TankID,
		UnloadingRate:     req.UnloadingRate,
		UnloadingDensity:  req.UnloadingDensity,
		UnloadingTemp:     req.UnloadingTemp,
		InitialTemps:      temps,
		InitialDensities:  densityValues,
		EstimatedDuration: req.EstimatedDuration,
		RequestedAt:       time.Now(),
	}

	result := p.predictUnloading(ctx, messageReq)
	go p.savePrediction(ctx, messageReq, result)

	return &result, nil
}
