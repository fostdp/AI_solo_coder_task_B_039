package multi_tank_scheduler

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"math"
	"sort"
	"sync"
	"time"
)

const (
	DefaultOptimizerPoolSize = 2
	MaxOptimizationQueue     = 10
)

type MultiTankScheduler struct {
	cfg           *config.Config
	db            *database.DB
	requestChan   <-chan messages.SchedulerRequest
	resultChan    chan<- messages.ScheduleResult
	mu            sync.RWMutex
	modelParams   *config.SchedulerParams
	optimizerPool *OptimizerWorkerPool
}

type LPSolver struct {
	epsilon float64
	maxIter int
}

type OptimizationVariable struct {
	CompressorLoads map[string]float64
	PumpOperations  []messages.PumpOperation
}

type OptimizationObjective struct {
	EvaporationLossCost float64
	ElectricityCost     float64
	TotalCost           float64
}

type OptimizationTask struct {
	ctx        context.Context
	states     []messages.TankStateForScheduler
	resultChan chan *OptimizationResult
}

type OptimizationResult struct {
	ScheduleResult *messages.ScheduleResult
	Error          error
}

type OptimizerWorkerPool struct {
	taskChan    chan *OptimizationTask
	workerCount int
	wg          sync.WaitGroup
	mu          sync.RWMutex
	running     bool
}

func NewMultiTankScheduler(
	cfg *config.Config,
	db *database.DB,
	requestChan <-chan messages.SchedulerRequest,
	resultChan chan<- messages.ScheduleResult,
) *MultiTankScheduler {
	params := &config.SchedulerParams{
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
	}
	if cfg.ModelParams != nil {
		params = &cfg.ModelParams.Scheduler
	}

	optimizerPool := NewOptimizerWorkerPool(DefaultOptimizerPoolSize)

	scheduler := &MultiTankScheduler{
		cfg:           cfg,
		db:            db,
		requestChan:   requestChan,
		resultChan:    resultChan,
		modelParams:   params,
		optimizerPool: optimizerPool,
	}

	scheduler.ensureCompressorConfig(cfg.Modbus.TankCount, cfg.Modbus.Register.CompressorsPerTank)

	return scheduler
}

func NewOptimizerWorkerPool(workerCount int) *OptimizerWorkerPool {
	if workerCount <= 0 {
		workerCount = DefaultOptimizerPoolSize
	}
	return &OptimizerWorkerPool{
		taskChan:    make(chan *OptimizationTask, MaxOptimizationQueue),
		workerCount: workerCount,
		running:     false,
	}
}

func (pool *OptimizerWorkerPool) Start(ctx context.Context) {
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

func (pool *OptimizerWorkerPool) Stop() {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	if !pool.running {
		return
	}
	pool.running = false
	close(pool.taskChan)
	pool.wg.Wait()
}

func (pool *OptimizerWorkerPool) Submit(task *OptimizationTask) bool {
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

func (pool *OptimizerWorkerPool) worker(ctx context.Context, workerID int) {
	defer pool.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-pool.taskChan:
			if !ok {
				return
			}
			result := pool.executeOptimization(task)
			select {
			case task.resultChan <- result:
			case <-task.ctx.Done():
			case <-ctx.Done():
			}
		}
	}
}

func (pool *OptimizerWorkerPool) executeOptimization(task *OptimizationTask) *OptimizationResult {
	select {
	case <-task.ctx.Done():
		return &OptimizationResult{Error: task.ctx.Err()}
	default:
	}

	scheduler, ok := pool.extractScheduler()
	if !ok {
		return &OptimizationResult{Error: fmt.Errorf("scheduler not available")}
	}

	result := scheduler.executeOptimization(task.ctx, task.states)
	return &OptimizationResult{ScheduleResult: &result, Error: nil}
}

var schedulerReference struct {
	sync.RWMutex
	scheduler *MultiTankScheduler
}

func (pool *OptimizerWorkerPool) setSchedulerReference(s *MultiTankScheduler) {
	schedulerReference.Lock()
	defer schedulerReference.Unlock()
	schedulerReference.scheduler = s
}

func (pool *OptimizerWorkerPool) extractScheduler() (*MultiTankScheduler, bool) {
	schedulerReference.RLock()
	defer schedulerReference.RUnlock()
	return schedulerReference.scheduler, schedulerReference.scheduler != nil
}

func (s *MultiTankScheduler) executeOptimization(
	ctx context.Context,
	states []messages.TankStateForScheduler,
) messages.ScheduleResult {
	if len(states) == 0 {
		return messages.ScheduleResult{
			OptimizedAt:  time.Now(),
			ErrorMessage: "no tank states available",
			AsyncOptimized: true,
		}
	}

	s.ensureCompressorConfig(len(states), 2)

	useDecomposition := s.modelParams.DecompositionOn &&
		len(states) > s.modelParams.MaxTanksPerSubproblem

	if useDecomposition {
		result := s.decomposeAndOptimize(ctx, states)
		result.AsyncOptimized = true
		return result
	}

	compressorLoads := s.optimizeCompressorLoads(states)
	pumpOperations := s.optimizePumpOperations(states)
	evaporationLoss := s.calculateEvaporationLoss(states, compressorLoads)

	result := messages.ScheduleResult{
		CompressorLoads: make(map[string]float64),
		PumpOperations:  pumpOperations,
		EvaporationLoss: evaporationLoss,
		OptimizedAt:     time.Now(),
		Decomposed:      false,
		SubproblemCount: 1,
		AsyncOptimized:  true,
	}

	for key, load := range compressorLoads {
		result.CompressorLoads[key] = load
	}

	return result
}

func (s *MultiTankScheduler) Start(ctx context.Context) {
	s.optimizerPool.setSchedulerReference(s)
	s.optimizerPool.Start(ctx)
	go s.processLoop(ctx)
	if s.cfg.Scheduler.AutoOptimize {
		go s.scheduledOptimization(ctx)
	}
}

func (s *MultiTankScheduler) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.requestChan:
			go s.processRequest(ctx, req)
		}
	}
}

func (s *MultiTankScheduler) scheduledOptimization(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.Scheduler.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.optimizeAllTanks(ctx)
		}
	}
}

func (s *MultiTankScheduler) processRequest(ctx context.Context, req messages.SchedulerRequest) {
	result := s.optimizeSchedule(ctx, req.TankStates)
	select {
	case <-ctx.Done():
		return
	case s.resultChan <- result:
	}

	go s.saveSchedule(ctx, result)
}

func (s *MultiTankScheduler) optimizeAllTanks(ctx context.Context) {
	states, err := s.collectTankStates(ctx)
	if err != nil {
		return
	}
	if len(states) == 0 {
		return
	}

	req := messages.SchedulerRequest{
		TankStates:  states,
		CollectedAt: time.Now(),
	}

	result := s.optimizeSchedule(ctx, states)
	select {
	case <-ctx.Done():
		return
	case s.resultChan <- result:
	}

	go s.saveSchedule(ctx, result)
}

func (s *MultiTankScheduler) collectTankStates(ctx context.Context) ([]messages.TankStateForScheduler, error) {
	var states []messages.TankStateForScheduler

	for tankID := 1; tankID <= s.cfg.Modbus.TankCount; tankID++ {
		level, err := s.db.GetTankLevelEstimate(ctx, tankID)
		if err != nil {
			continue
		}

		temps, err := s.db.GetLayerAvgTemps(ctx, tankID)
		if err != nil || len(temps) == 0 {
			continue
		}

		avgTemp := 0.0
		for _, t := range temps {
			avgTemp += t
		}
		avgTemp /= float64(len(temps))

		prediction, err := s.db.GetLatestRolloverPrediction(ctx, tankID)
		riskIndex := 0.0
		if err == nil && prediction != nil {
			riskIndex = prediction.RiskIndex
		}

		pressureData, err := s.db.GetLatestPressureData(ctx, tankID)
		pressure := 0.0
		if err == nil && pressureData != nil {
			pressure = pressureData.Pressure
		}

		compStatus, err := s.db.GetTankCompressorStatus(ctx, tankID)
		comp1Status := false
		comp2Status := false
		if err == nil {
			comp1Status = compStatus[1]
			comp2Status = compStatus[2]
		}

		states = append(states, messages.TankStateForScheduler{
			TankID:      tankID,
			Level:       level,
			AvgTemp:     avgTemp,
			RiskIndex:   riskIndex,
			Pressure:    pressure,
			HasBOGComp1: comp1Status,
			HasBOGComp2: comp2Status,
		})
	}

	return states, nil
}

func (s *MultiTankScheduler) optimizeSchedule(
	ctx context.Context,
	states []messages.TankStateForScheduler,
) messages.ScheduleResult {
	resultChan := make(chan *OptimizationResult, 1)

	task := &OptimizationTask{
		ctx:        ctx,
		states:     states,
		resultChan: resultChan,
	}

	if !s.optimizerPool.Submit(task) {
		return s.optimizeScheduleFallback(ctx, states)
	}

	select {
	case <-ctx.Done():
		return messages.ScheduleResult{
			OptimizedAt:  time.Now(),
			ErrorMessage: "context cancelled",
			AsyncOptimized: false,
		}
	case result := <-resultChan:
		if result.Error != nil {
			return messages.ScheduleResult{
				OptimizedAt:  time.Now(),
				ErrorMessage: result.Error.Error(),
				AsyncOptimized: false,
			}
		}
		return *result.ScheduleResult
	}
}

func (s *MultiTankScheduler) optimizeScheduleFallback(
	ctx context.Context,
	states []messages.TankStateForScheduler,
) messages.ScheduleResult {
	if len(states) == 0 {
		return messages.ScheduleResult{
			OptimizedAt:  time.Now(),
			ErrorMessage: "no tank states available",
			AsyncOptimized: false,
		}
	}

	s.ensureCompressorConfig(len(states), 2)

	useDecomposition := s.modelParams.DecompositionOn &&
		len(states) > s.modelParams.MaxTanksPerSubproblem

	if useDecomposition {
		result := s.decomposeAndOptimize(ctx, states)
		result.AsyncOptimized = false
		return result
	}

	compressorLoads := s.optimizeCompressorLoads(states)
	pumpOperations := s.optimizePumpOperations(states)
	evaporationLoss := s.calculateEvaporationLoss(states, compressorLoads)

	result := messages.ScheduleResult{
		CompressorLoads: make(map[string]float64),
		PumpOperations:  pumpOperations,
		EvaporationLoss: evaporationLoss,
		OptimizedAt:     time.Now(),
		Decomposed:      false,
		SubproblemCount: 1,
		AsyncOptimized:  false,
	}

	for key, load := range compressorLoads {
		result.CompressorLoads[key] = load
	}

	return result
}

func (s *MultiTankScheduler) optimizeCompressorLoads(
	states []messages.TankStateForScheduler,
) map[string]float64 {
	loads := make(map[string]float64)
	sort.Slice(states, func(i, j int) bool {
		return states[i].RiskIndex > states[j].RiskIndex
	})

	totalCapacity := 0.0
	for _, state := range states {
		if state.HasBOGComp1 {
			totalCapacity += s.modelParams.MaxLoadPctPerCompressor[s.compressorKey(state.TankID, 1)]
		}
		if state.HasBOGComp2 {
			totalCapacity += s.modelParams.MaxLoadPctPerCompressor[s.compressorKey(state.TankID, 2)]
		}
	}

	if totalCapacity == 0 {
		return loads
	}

	totalPressure := 0.0
	for _, state := range states {
		totalPressure += state.Pressure
	}
	avgPressure := totalPressure / float64(len(states))

	baseLoad := 40.0
	if avgPressure > 0.20 {
		baseLoad = 50.0
	}
	if avgPressure > 0.23 {
		baseLoad = 70.0
	}

	for _, state := range states {
		loadAllocation := 1.0
		if state.RiskIndex > s.cfg.Scheduler.MinRiskForAction {
			loadAllocation = 1.0 + state.RiskIndex*0.5
		}

		if state.HasBOGComp1 {
			key := s.compressorKey(state.TankID, 1)
			maxLoad := s.modelParams.MaxLoadPctPerCompressor[key]
			load := baseLoad * loadAllocation
			if state.Pressure > 0.23 {
				load += 20.0
			}
			if state.RiskIndex > 0.6 {
				load += 15.0
			}
			loads[key] = math.Min(load, maxLoad)
		}

		if state.HasBOGComp2 {
			key := s.compressorKey(state.TankID, 2)
			maxLoad := s.modelParams.MaxLoadPctPerCompressor[key]
			load := baseLoad * loadAllocation * 0.8
			if state.Pressure > 0.24 {
				load += 25.0
			}
			loads[key] = math.Min(load, maxLoad)
		}
	}

	s.balanceCompressorLoads(loads, avgPressure)

	return loads
}

func (s *MultiTankScheduler) balanceCompressorLoads(
	loads map[string]float64,
	avgPressure float64,
) {
	totalLoad := 0.0
	for _, load := range loads {
		totalLoad += load
	}

	targetTotalLoad := avgPressure * 400.0
	if targetTotalLoad > totalLoad {
		adjustmentFactor := targetTotalLoad / totalLoad
		for key := range loads {
			maxLoad := s.modelParams.MaxLoadPctPerCompressor[key]
			newLoad := loads[key] * adjustmentFactor
			loads[key] = math.Min(newLoad, maxLoad)
		}
	}
}

func (s *MultiTankScheduler) optimizePumpOperations(
	states []messages.TankStateForScheduler,
) []messages.PumpOperation {
	var operations []messages.PumpOperation

	for _, state := range states {
		if state.RiskIndex < s.cfg.Scheduler.MinRiskForAction {
			continue
		}

		action := "monitor"
		duration := 0.0
		startTime := 0.0

		switch {
		case state.RiskIndex >= 0.8:
			action = "start"
			duration = math.Max(2.0, s.modelParams.MinRuntimeHours)
			startTime = 0.0
		case state.RiskIndex >= 0.6:
			action = "start"
			duration = math.Max(1.0, s.modelParams.MinRuntimeHours*0.5)
			startTime = 0.5
		case state.RiskIndex >= 0.4:
			action = "prepare"
			duration = 0.5
			startTime = 1.0
		}

		if action != "monitor" {
			pumpID := 1
			if state.Level > 0.6 {
				pumpID = 2
			}
			if state.Level > 0.8 {
				pumpID = 3
			}

			operations = append(operations, messages.PumpOperation{
				TankID:    state.TankID,
				PumpID:    pumpID,
				StartTime: startTime,
				Duration:  duration,
				Action:    action,
			})
		}
	}

	sort.Slice(operations, func(i, j int) bool {
		return operations[i].StartTime < operations[j].StartTime
	})

	return operations
}

func (s *MultiTankScheduler) calculateEvaporationLoss(
	states []messages.TankStateForScheduler,
	compressorLoads map[string]float64,
) float64 {
	totalEvaporation := 0.0
	lngBoilOffRate := 0.00005

	for _, state := range states {
		tankCapacity := s.cfg.ModelParams.TankSpecs.CapacityCubicMeters
		baseEvaporation := tankCapacity * state.Level * lngBoilOffRate * 24.0

		pressureFactor := 1.0
		if state.Pressure > 0.20 {
			pressureFactor = 1.0 + (state.Pressure-0.20)*10
		}

		tempFactor := 1.0
		if state.AvgTemp > -160 {
			tempFactor = 1.0 + (state.AvgTemp+160)*0.1
		}

		compressorCapacity := 0.0
		if state.HasBOGComp1 {
			key := s.compressorKey(state.TankID, 1)
			compressorCapacity += compressorLoads[key] / 100.0
		}
		if state.HasBOGComp2 {
			key := s.compressorKey(state.TankID, 2)
			compressorCapacity += compressorLoads[key] / 100.0
		}
		compressorCapacity *= s.modelParams.CompressorEfficiency

		netEvaporation := baseEvaporation * pressureFactor * tempFactor * (1.0 - math.Min(0.95, compressorCapacity))
		totalEvaporation += math.Max(0, netEvaporation)
	}

	return totalEvaporation
}

func (s *MultiTankScheduler) calculateObjective(
	evaporationLoss float64,
	compressorLoads map[string]float64,
	pumpOperations []messages.PumpOperation,
) OptimizationObjective {
	evapCost := evaporationLoss * 0.425 * s.modelParams.EvaporationLossCostYuan / 1000.0

	electricityCost := 0.0
	for _, load := range compressorLoads {
		electricityCost += load * s.modelParams.CompressorPowerKWPerPct * s.modelParams.ElectricityCostYuan
	}

	pumpCost := 0.0
	for _, op := range pumpOperations {
		if op.Action == "start" {
			pumpCost += s.modelParams.PumpPowerKW * op.Duration * s.modelParams.ElectricityCostYuan
		}
	}

	totalCost := evapCost + electricityCost + pumpCost

	return OptimizationObjective{
		EvaporationLossCost: evapCost,
		ElectricityCost:     electricityCost + pumpCost,
		TotalCost:           totalCost,
	}
}

func (s *MultiTankScheduler) compressorKey(tankID, compID int) string {
	return fmt.Sprintf("T%d_C%d", tankID, compID)
}

func (s *MultiTankScheduler) ensureCompressorConfig(nTanks, compressorsPerTank int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.modelParams.MaxLoadPctPerCompressor == nil {
		s.modelParams.MaxLoadPctPerCompressor = make(map[string]float64)
	}

	for tankID := 1; tankID <= nTanks; tankID++ {
		for compID := 1; compID <= compressorsPerTank; compID++ {
			key := s.compressorKey(tankID, compID)
			if _, exists := s.modelParams.MaxLoadPctPerCompressor[key]; !exists {
				s.modelParams.MaxLoadPctPerCompressor[key] = s.modelParams.DefaultMaxLoadPct
			}
		}
	}
}

func (s *MultiTankScheduler) groupTanksByRisk(states []messages.TankStateForScheduler) [][]messages.TankStateForScheduler {
	thresholds := s.modelParams.RiskGroupThresholds
	if len(thresholds) == 0 {
		thresholds = []float64{0.7, 0.4, 0.0}
	}

	groups := make([][]messages.TankStateForScheduler, len(thresholds))
	for _, state := range states {
		groupIdx := len(thresholds) - 1
		for i, thresh := range thresholds {
			if state.RiskIndex >= thresh {
				groupIdx = i
				break
			}
		}
		groups[groupIdx] = append(groups[groupIdx], state)
	}

	return groups
}

func (s *MultiTankScheduler) decomposeIntoSubproblems(
	states []messages.TankStateForScheduler,
) [][]messages.TankStateForScheduler {
	maxTanks := s.modelParams.MaxTanksPerSubproblem
	if maxTanks <= 0 {
		maxTanks = 4
	}

	groups := s.groupTanksByRisk(states)

	var subproblems [][]messages.TankStateForScheduler
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		if len(group) <= maxTanks {
			subproblems = append(subproblems, group)
		} else {
			for i := 0; i < len(group); i += maxTanks {
				end := i + maxTanks
				if end > len(group) {
					end = len(group)
				}
				subproblems = append(subproblems, group[i:end])
			}
		}
	}

	return subproblems
}

type subproblemResult struct {
	loads   map[string]float64
	pumps   []messages.PumpOperation
	evapLoss float64
	cost    float64
}

func (s *MultiTankScheduler) optimizeSubproblem(
	ctx context.Context,
	states []messages.TankStateForScheduler,
	globalPressure float64,
) subproblemResult {
	compressorLoads := s.optimizeCompressorLoads(states)
	pumpOperations := s.optimizePumpOperations(states)
	evaporationLoss := s.calculateEvaporationLoss(states, compressorLoads)
	objective := s.calculateObjective(evaporationLoss, compressorLoads, pumpOperations)

	return subproblemResult{
		loads:    compressorLoads,
		pumps:    pumpOperations,
		evapLoss: evaporationLoss,
		cost:     objective.TotalCost,
	}
}

func (s *MultiTankScheduler) coordinateSubproblems(
	results []subproblemResult,
	globalAvgPressure float64,
) (map[string]float64, []messages.PumpOperation, float64) {
	combinedLoads := make(map[string]float64)
	combinedPumps := make([]messages.PumpOperation, 0)
	totalEvapLoss := 0.0
	totalLoad := 0.0
	totalCapacity := 0.0

	for _, res := range results {
		for k, v := range res.loads {
			combinedLoads[k] = v
			totalLoad += v
			if maxLoad, ok := s.modelParams.MaxLoadPctPerCompressor[k]; ok {
				totalCapacity += maxLoad
			}
		}
		combinedPumps = append(combinedPumps, res.pumps...)
		totalEvapLoss += res.evapLoss
	}

	if totalCapacity > 0 {
		targetTotalLoad := globalAvgPressure * 400.0
		if targetTotalLoad > totalLoad && targetTotalLoad <= totalCapacity {
			adjustmentFactor := targetTotalLoad / totalLoad
			for key := range combinedLoads {
				maxLoad := s.modelParams.MaxLoadPctPerCompressor[key]
				newLoad := combinedLoads[key] * adjustmentFactor
				combinedLoads[key] = math.Min(newLoad, maxLoad)
			}
		}
	}

	sort.Slice(combinedPumps, func(i, j int) bool {
		return combinedPumps[i].StartTime < combinedPumps[j].StartTime
	})

	return combinedLoads, combinedPumps, totalEvapLoss
}

func (s *MultiTankScheduler) decomposeAndOptimize(
	ctx context.Context,
	states []messages.TankStateForScheduler,
) messages.ScheduleResult {
	subproblems := s.decomposeIntoSubproblems(states)

	totalPressure := 0.0
	for _, state := range states {
		totalPressure += state.Pressure
	}
	globalAvgPressure := totalPressure / float64(len(states))

	var results []subproblemResult

	if s.modelParams.ConcurrentSubproblems && len(subproblems) > 1 {
		var wg sync.WaitGroup
		resultChan := make(chan subproblemResult, len(subproblems))

		for _, sub := range subproblems {
			wg.Add(1)
			go func(subproblem []messages.TankStateForScheduler) {
				defer wg.Done()
				select {
				case <-ctx.Done():
					return
				case resultChan <- s.optimizeSubproblem(ctx, subproblem, globalAvgPressure):
				}
			}(sub)
		}

		wg.Wait()
		close(resultChan)

		for res := range resultChan {
			results = append(results, res)
		}
	} else {
		for _, sub := range subproblems {
			select {
			case <-ctx.Done():
				break
			default:
				results = append(results, s.optimizeSubproblem(ctx, sub, globalAvgPressure))
			}
		}
	}

	loads, pumps, evapLoss := s.coordinateSubproblems(results, globalAvgPressure)

	result := messages.ScheduleResult{
		CompressorLoads: make(map[string]float64),
		PumpOperations:  pumps,
		EvaporationLoss: evapLoss,
		OptimizedAt:     time.Now(),
		Decomposed:      true,
		SubproblemCount: len(subproblems),
		AsyncOptimized:  false,
	}

	for key, load := range loads {
		result.CompressorLoads[key] = load
	}

	return result
}

func (s *MultiTankScheduler) saveSchedule(ctx context.Context, result messages.ScheduleResult) {
	compressorLoadsInt := make(map[string]int)
	for k, v := range result.CompressorLoads {
		compressorLoadsInt[k] = int(v)
	}

	pumpOps := make([]models.PumpSchedule, len(result.PumpOperations))
	for i, op := range result.PumpOperations {
		pumpOps[i] = models.PumpSchedule{
			TankID:    op.TankID,
			PumpID:    op.PumpID,
			StartTime: op.StartTime,
			Duration:  op.Duration,
			Action:    op.Action,
		}
	}

	objective := s.calculateObjective(result.EvaporationLoss, result.CompressorLoads, result.PumpOperations)

	schedule := &models.MultiTankSchedule{
		Time:               result.OptimizedAt,
		CompressorLoads:    compressorLoadsInt,
		PumpOperations:     pumpOps,
		EvaporationLossKg:  result.EvaporationLoss * 425.0,
		EvaporationLossM3:  result.EvaporationLoss,
		ObjectiveValue:     objective.TotalCost,
		OptimizationStatus: "OPTIMAL",
		ModelVersion:       s.cfg.Scheduler.ModelVersion,
	}

	if err := s.db.InsertMultiTankSchedule(ctx, schedule); err != nil {
		fmt.Printf("Error saving schedule: %v\n", err)
	}
}

func (s *MultiTankScheduler) RunManualOptimization(
	ctx context.Context,
) (*messages.ScheduleResult, error) {
	states, err := s.collectTankStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect tank states: %w", err)
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("no tank states available")
	}

	result := s.optimizeSchedule(ctx, states)
	go s.saveSchedule(ctx, result)

	return &result, nil
}

func (s *MultiTankScheduler) GetCostBreakdown(
	ctx context.Context,
	result *messages.ScheduleResult,
) map[string]interface{} {
	objective := s.calculateObjective(result.EvaporationLoss, result.CompressorLoads, result.PumpOperations)

	return map[string]interface{}{
		"evaporation_loss_ton":     result.EvaporationLoss * 0.425,
		"evaporation_loss_cost":    objective.EvaporationLossCost,
		"electricity_cost":         objective.ElectricityCost,
		"total_operational_cost":   objective.TotalCost,
		"currency":                 "CNY",
		"optimization_interval_h":  float64(s.modelParams.OptimizationIntervalMin) / 60.0,
	}
}
