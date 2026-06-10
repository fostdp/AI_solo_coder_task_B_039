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

const (
	DefaultWorkerPoolSize = 3
	MaxPendingTasks      = 10
)

type UnloadingPredictor struct {
	cfg         *config.Config
	db          *database.DB
	requestChan <-chan messages.UnloadingRequest
	resultChan  chan<- messages.UnloadingPrediction
	mu          sync.RWMutex
	modelParams *config.UnloadingParams
	workerPool  *MixingModelWorkerPool
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

type MixingModelTask struct {
	ctx       context.Context
	req       messages.UnloadingRequest
	model     *OneDMixerModel
	initState []LayerState
	timeStep  float64
	totalSteps int
	resultChan chan *MixingModelResult
}

type MixingModelResult struct {
	PredictedTemps     [][]float64
	PredictedDensities [][]float64
	TimeSteps          []float64
	Error              error
}

type MixingModelWorkerPool struct {
	taskChan    chan *MixingModelTask
	workerCount int
	wg          sync.WaitGroup
	mu          sync.RWMutex
	running     bool
}

func NewUnloadingPredictor(
	cfg *config.Config,
	db *database.DB,
	requestChan <-chan messages.UnloadingRequest,
	resultChan chan<- messages.UnloadingPrediction,
) *UnloadingPredictor {
	params := &config.UnloadingParams{
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
	}
	if cfg.ModelParams != nil {
		params = &cfg.ModelParams.Unloading
	}

	workerPool := NewMixingModelWorkerPool(DefaultWorkerPoolSize)

	return &UnloadingPredictor{
		cfg:         cfg,
		db:          db,
		requestChan: requestChan,
		resultChan:  resultChan,
		modelParams: params,
		workerPool:  workerPool,
	}
}

func NewMixingModelWorkerPool(workerCount int) *MixingModelWorkerPool {
	if workerCount <= 0 {
		workerCount = DefaultWorkerPoolSize
	}
	return &MixingModelWorkerPool{
		taskChan:    make(chan *MixingModelTask, MaxPendingTasks),
		workerCount: workerCount,
		running:     false,
	}
}

func (pool *MixingModelWorkerPool) Start(ctx context.Context) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	if pool.running {
		return
	}
	pool.running = true

	for i := 0; i < pool.workerCount; i++ {
		pool.wg.Add(1)
		go pool.worker(ctx, i)
	}
}

func (pool *MixingModelWorkerPool) Stop() {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	if !pool.running {
		return
	}
	pool.running = false
	close(pool.taskChan)
	pool.wg.Wait()
}

func (pool *MixingModelWorkerPool) Submit(task *MixingModelTask) bool {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	if !pool.running {
		return false
	}

	select {
	case pool.taskChan <- task:
		return true
	default:
		return false
	}
}

func (pool *MixingModelWorkerPool) worker(ctx context.Context, workerID int) {
	defer pool.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-pool.taskChan:
			if !ok {
				return
			}
			result := pool.executeMixingModel(task)
			select {
			case task.resultChan <- result:
			case <-task.ctx.Done():
			case <-ctx.Done():
			}
		}
	}
}

func (pool *MixingModelWorkerPool) executeMixingModel(task *MixingModelTask) *MixingModelResult {
	select {
	case <-task.ctx.Done():
		return &MixingModelResult{Error: task.ctx.Err()}
	default:
	}

	nLayers := task.model.nLayers
	totalSteps := task.totalSteps
	timeStep := task.timeStep

	predictedTemps := make([][]float64, totalSteps)
	predictedDensities := make([][]float64, totalSteps)
	timeSteps := make([]float64, totalSteps)

	currentState := make([]LayerState, nLayers)
	copy(currentState, task.initState)

	responseSteps := 3
	flowHistory := make([]float64, 0, responseSteps)

	for t := 0; t < totalSteps; t++ {
		select {
		case <-task.ctx.Done():
			return &MixingModelResult{Error: task.ctx.Err()}
		default:
		}

		timeSteps[t] = float64(t) * timeStep

		currentTemps := make([]float64, nLayers)
		currentDensities := make([]float64, nLayers)
		for i := 0; i < nLayers; i++ {
			currentTemps[i] = currentState[i].Temperature
			currentDensities[i] = currentState[i].Density
		}
		predictedTemps[t] = currentTemps
		predictedDensities[t] = currentDensities

		if t < totalSteps-1 {
			actualFlow := task.req.UnloadingRate
			if t > 0 && task.req.FlowRateChanges != nil && len(task.req.FlowRateChanges) > 0 {
				elapsedHours := float64(t) * timeStep
				for _, change := range task.req.FlowRateChanges {
					if math.Abs(elapsedHours-change.ChangeTimeHours) < timeStep*0.5 {
						actualFlow = change.NewFlowRate
						break
					}
				}
			}

			flowHistory = append(flowHistory, actualFlow)
			if len(flowHistory) > responseSteps {
				flowHistory = flowHistory[1:]
			}

			adaptiveReq := task.req
			adaptiveReq.UnloadingRate = actualFlow

			newState := make([]LayerState, nLayers)
			copy(newState, currentState)

			task.executeStep(newState, adaptiveReq, timeStep, t)

			currentState = newState
		}
	}

	return &MixingModelResult{
		PredictedTemps:     predictedTemps,
		PredictedDensities: predictedDensities,
		TimeSteps:          timeSteps,
	}
}

func (task *MixingModelTask) executeStep(state []LayerState, req messages.UnloadingRequest, timeStep float64, stepIndex int) {
	nLayers := task.model.nLayers
	unloadingMassFlow := req.UnloadingRate * req.UnloadingDensity / 3600.0
	tankRadius := 20.0
	tankArea := math.Pi * tankRadius * tankRadius

	elapsedTime := float64(stepIndex) * timeStep
	if elapsedTime >= req.EstimatedDuration {
		return
	}

	newState := make([]LayerState, nLayers)
	for i := range state {
		newState[i] = state[i]
	}

	injectionLayer := 0
	bestDiff := math.Inf(1)
	for i := 0; i < nLayers; i++ {
		diff := math.Abs(newState[i].Density - req.UnloadingDensity)
		if diff < bestDiff {
			bestDiff = diff
			injectionLayer = i
		}
	}

	if injectionLayer >= 0 && injectionLayer < nLayers {
		massAdded := unloadingMassFlow * timeStep * task.model.mixingEfficiency
		newState[injectionLayer].Mass += massAdded

		oldTemp := newState[injectionLayer].Temperature
		oldMass := newState[injectionLayer].Mass
		newTemp := (oldTemp*(oldMass-massAdded) + req.UnloadingTemp*massAdded) / oldMass
		newState[injectionLayer].Temperature = newTemp

		newDensity := oldMass / (oldMass/state[injectionLayer].Density + massAdded/req.UnloadingDensity)
		newState[injectionLayer].Density = newDensity
	}

	for i := 1; i < nLayers-1; i++ {
		dispersionFluxT := task.model.axialDispersion * tankArea *
			(newState[i+1].Temperature - 2*newState[i].Temperature + newState[i-1].Temperature) /
			(task.model.layerHeights[1] - task.model.layerHeights[0])

		dispersionFluxD := task.model.axialDispersion * tankArea *
			(newState[i+1].Density - 2*newState[i].Density + newState[i-1].Density) /
			(task.model.layerHeights[1] - task.model.layerHeights[0])

		layerVolume := tankArea * (task.model.layerHeights[1] - task.model.layerHeights[0])
		layerMass := newState[i].Mass

		newState[i].Temperature += dispersionFluxT * timeStep / layerMass * 425.0 * 2200.0
		newState[i].Density += dispersionFluxD * timeStep / layerVolume
	}

	for i := range state {
		state[i] = newState[i]
	}
}

func (p *UnloadingPredictor) Start(ctx context.Context) {
	p.workerPool.Start(ctx)
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

	responseSteps := p.modelParams.ResponseTimeSteps
	flowHistory := make([]float64, 0, responseSteps)

	actualFlow := req.UnloadingRate
	smoothedFlow := p.smoothFlowRate(actualFlow, flowHistory)
	flowChangeRate := p.calculateFlowChangeRate(actualFlow, flowHistory)
	adaptiveMixing, adaptiveDispersion := p.calculateAdaptiveParameters(flowChangeRate)
	model.mixingEfficiency = adaptiveMixing
	model.axialDispersion = adaptiveDispersion

	adaptiveReq := req
	adaptiveReq.UnloadingRate = smoothedFlow

	resultChan := make(chan *MixingModelResult, 1)

	task := &MixingModelTask{
		ctx:        ctx,
		req:        adaptiveReq,
		model:      model,
		initState:  initialState,
		timeStep:   timeStep,
		totalSteps: totalSteps,
		resultChan: resultChan,
	}

	if !p.workerPool.Submit(task) {
		return p.predictUnloadingFallback(ctx, req, model, initialState, timeStep, totalSteps)
	}

	select {
	case <-ctx.Done():
		return messages.UnloadingPrediction{
			TankID:        req.TankID,
			PredictedAt:   time.Now(),
			ErrorMessage:  "context cancelled",
		}
	case result := <-resultChan:
		if result.Error != nil {
			return messages.UnloadingPrediction{
				TankID:        req.TankID,
				PredictedAt:   time.Now(),
				ErrorMessage:  result.Error.Error(),
			}
		}

		maxTempDiff, maxDensityDiff := p.calculateMaxDifferences(result.PredictedTemps, result.PredictedDensities)
		optimalPumpOnTime := p.calculateOptimalPumpTime(result.PredictedTemps, result.PredictedDensities, result.TimeSteps, req)
		rolloverRisk := p.calculateRolloverRisk(result.PredictedTemps, result.PredictedDensities)

		return messages.UnloadingPrediction{
			TankID:             req.TankID,
			PredictedTemps:     result.PredictedTemps,
			PredictedDensities: result.PredictedDensities,
			TimeSteps:          result.TimeSteps,
			MaxTempDiff:        maxTempDiff,
			MaxDensityDiff:     maxDensityDiff,
			OptimalPumpOnTime:  optimalPumpOnTime,
			RolloverRisk:       rolloverRisk,
			PredictedAt:        time.Now(),
			AsyncComputed:      true,
		}
	}
}

func (p *UnloadingPredictor) predictUnloadingFallback(
	ctx context.Context,
	req messages.UnloadingRequest,
	model *OneDMixerModel,
	initialState []LayerState,
	timeStep float64,
	totalSteps int,
) messages.UnloadingPrediction {
	nLayers := model.nLayers

	predictedTemps := make([][]float64, totalSteps)
	predictedDensities := make([][]float64, totalSteps)
	timeSteps := make([]float64, totalSteps)

	currentState := make([]LayerState, nLayers)
	copy(currentState, initialState)

	for t := 0; t < totalSteps; t++ {
		timeSteps[t] = float64(t) * timeStep

		currentTemps := make([]float64, nLayers)
		currentDensities := make([]float64, nLayers)
		for i := 0; i < nLayers; i++ {
			currentTemps[i] = currentState[i].Temperature
			currentDensities[i] = currentState[i].Density
		}
		predictedTemps[t] = currentTemps
		predictedDensities[t] = currentDensities

		if t < totalSteps-1 {
			newState := make([]LayerState, nLayers)
			copy(newState, currentState)
			p.advanceTimeStep(model, newState, req, timeStep, t)
			currentState = newState
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
		AsyncComputed:      false,
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

func (p *UnloadingPredictor) smoothFlowRate(
	currentFlow float64,
	flowHistory []float64,
) float64 {
	if !p.modelParams.AdaptiveFilteringOn || len(flowHistory) == 0 {
		return currentFlow
	}

	alpha := p.modelParams.FlowSmoothingAlpha
	smoothed := currentFlow
	for i := len(flowHistory) - 1; i >= 0; i-- {
		smoothed = alpha*smoothed + (1-alpha)*flowHistory[i]
	}

	return smoothed
}

func (p *UnloadingPredictor) calculateFlowChangeRate(
	currentFlow float64,
	flowHistory []float64,
) float64 {
	if len(flowHistory) < 2 {
		return 0
	}

	n := len(flowHistory)
	avgFlow := 0.0
	for _, f := range flowHistory {
		avgFlow += f
	}
	avgFlow /= float64(n)

	if avgFlow < 0.01 {
		return 0
	}

	return math.Abs(currentFlow-avgFlow) / avgFlow
}

func (p *UnloadingPredictor) calculateAdaptiveParameters(
	flowChangeRate float64,
) (float64, float64) {
	if !p.modelParams.AdaptiveFilteringOn {
		return p.modelParams.MixingEfficiency, p.modelParams.AxialDispersionCoeff
	}

	threshold := p.modelParams.FlowRateChangeThreshold
	if flowChangeRate < threshold {
		return p.modelParams.MixingEfficiency, p.modelParams.AxialDispersionCoeff
	}

	excessRate := (flowChangeRate - threshold) / threshold
	boostFactor := math.Min(1.0, excessRate)

	adaptiveMixingEfficiency := p.modelParams.MixingEfficiency *
		(1.0 + boostFactor*p.modelParams.MaxMixingEfficiencyBoost)

	dispersionBoost := p.modelParams.MinAxialDispersionBoost +
		boostFactor*(p.modelParams.MaxAxialDispersionBoost-p.modelParams.MinAxialDispersionBoost)
	adaptiveDispersion := p.modelParams.AxialDispersionCoeff * dispersionBoost

	return math.Min(1.0, adaptiveMixingEfficiency), adaptiveDispersion
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
