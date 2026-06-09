package rollover_predictor

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"math"
	"time"
)

type RolloverPredictor struct {
	cfg         *config.ModelParams
	db          *database.DB
	requestChan <-chan messages.PredictionRequest
	resultChan  chan<- messages.PredictionResult
}

type GridData struct {
	Heights      []float64
	Temperatures []float64
	Densities    []float64
	Velocities   []float64
}

func NewPredictor(
	cfg *config.ModelParams,
	db *database.DB,
	requestChan <-chan messages.PredictionRequest,
	resultChan chan<- messages.PredictionResult,
) *RolloverPredictor {
	return &RolloverPredictor{
		cfg:         cfg,
		db:          db,
		requestChan: requestChan,
		resultChan:  resultChan,
	}
}

func (p *RolloverPredictor) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-p.requestChan:
			result := p.Predict(req)
			select {
			case p.resultChan <- result:
			case <-ctx.Done():
				return
			}
			if result.ErrorMessage == "" {
				p.storeResult(ctx, result, req)
			}
		}
	}
}

func (p *RolloverPredictor) Predict(req messages.PredictionRequest) messages.PredictionResult {
	now := time.Now()

	if len(req.Temperatures) == 0 || len(req.Densities) == 0 {
		return messages.PredictionResult{
			TankID:       req.TankID,
			RiskIndex:    0,
			RiskLevel:    "UNKNOWN",
			PredictedAt:  now,
			ErrorMessage: "insufficient data",
		}
	}

	layerTemps := p.calculateLayerTemps(req.Temperatures)
	tankHeight := p.cfg.TankSpecs.HeightMeters

	gridData := p.interpolateToGrid(layerTemps, req.Densities, tankHeight)

	maxTempDiff := calculateMaxGradient(gridData.Temperatures, gridData.Heights)
	maxDensityDiff := calculateMaxGradient(gridData.Densities, gridData.Heights)

	stability := p.calculateStratificationStability(gridData)
	buoyancyFreq := p.calculateBuoyancyFrequency(gridData)
	interfaceHeight, _ := p.findDensityInterface(gridData.Densities, gridData.Heights)

	criticalTime := p.solveConvectionEquation(gridData, tankHeight, p.cfg.TankSpecs.DiameterMeters)

	riskIndex := p.calculateRiskIndex(maxTempDiff, maxDensityDiff, stability, criticalTime)
	riskLevel := p.determineRiskLevel(riskIndex)

	return messages.PredictionResult{
		TankID:          req.TankID,
		RiskIndex:       riskIndex,
		RiskLevel:       riskLevel,
		CriticalTime:    criticalTime,
		MaxTempDiff:     maxTempDiff,
		MaxDensityDiff:  maxDensityDiff,
		BuoyancyFreq:    buoyancyFreq,
		InterfaceHeight: interfaceHeight,
		PredictedAt:     now,
	}
}

func (p *RolloverPredictor) calculateLayerTemps(tempData []models.TemperatureData) []float64 {
	layerMap := make(map[int][]float64)
	for _, d := range tempData {
		layerMap[d.Layer] = append(layerMap[d.Layer], d.Temperature)
	}

	layers := len(p.cfg.TankSpecs.LayerHeights)
	result := make([]float64, layers)
	for i := 0; i < layers; i++ {
		layer := i + 1
		if temps, ok := layerMap[layer]; ok && len(temps) > 0 {
			sum := 0.0
			for _, t := range temps {
				sum += t
			}
			result[i] = sum / float64(len(temps))
		}
	}
	return result
}

func (p *RolloverPredictor) interpolateToGrid(layerTemps []float64, densityData []models.DensityData, tankHeight float64) GridData {
	n := p.cfg.NumericalMethod.GridPoints
	heights := make([]float64, n)
	temps := make([]float64, n)
	densities := make([]float64, n)
	velocities := make([]float64, n)

	layerHeights := p.cfg.TankSpecs.LayerHeights
	for i := 0; i < n; i++ {
		heights[i] = float64(i) * tankHeight / float64(n-1)
		temps[i] = linearInterpolate(layerHeights, layerTemps, heights[i])
	}

	densityHeights := make([]float64, len(densityData))
	densityValues := make([]float64, len(densityData))
	for i, d := range densityData {
		densityHeights[i] = d.HeightPosition
		densityValues[i] = d.Density
	}

	for i := 0; i < n; i++ {
		densities[i] = linearInterpolate(densityHeights, densityValues, heights[i])
		velocities[i] = 0.0
	}

	return GridData{
		Heights:      heights,
		Temperatures: temps,
		Densities:    densities,
		Velocities:   velocities,
	}
}

func linearInterpolate(x, y []float64, xi float64) float64 {
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	if len(x) != len(y) {
		return 0
	}

	if xi <= x[0] {
		return y[0]
	}
	if xi >= x[len(x)-1] {
		return y[len(y)-1]
	}

	for i := 0; i < len(x)-1; i++ {
		if xi >= x[i] && xi <= x[i+1] {
			t := (xi - x[i]) / (x[i+1] - x[i])
			return y[i] + t*(y[i+1]-y[i])
		}
	}
	return y[len(y)-1]
}

func calculateMaxGradient(values, heights []float64) float64 {
	maxDiff := 0.0
	for i := 1; i < len(values); i++ {
		if heights[i] != heights[i-1] {
			diff := math.Abs(values[i] - values[i-1])
			if diff > maxDiff {
				maxDiff = diff
			}
		}
	}
	return maxDiff
}

func (p *RolloverPredictor) calculateStratificationStability(grid GridData) float64 {
	phys := p.cfg.PhysicalProperties
	g := phys.Gravity
	rhoRef := grid.Densities[0]
	if rhoRef == 0 {
		return 0
	}

	var avgN2 float64
	count := 0
	for i := 1; i < len(grid.Densities); i++ {
		drho := grid.Densities[i] - grid.Densities[i-1]
		dz := grid.Heights[i] - grid.Heights[i-1]
		if dz > 0 {
			N2 := -g * drho / (rhoRef * dz)
			if N2 > 0 {
				avgN2 += N2
				count++
			}
		}
	}

	if count == 0 {
		return 0
	}
	avgN2 /= float64(count)

	stability := 1.0 - math.Exp(-avgN2*p.cfg.StabilityAnalysis.BuoyancyFrequencyScale)
	return math.Max(0, math.Min(1, stability))
}

func (p *RolloverPredictor) calculateBuoyancyFrequency(grid GridData) float64 {
	phys := p.cfg.PhysicalProperties
	g := phys.Gravity
	rhoRef := grid.Densities[0]
	if rhoRef == 0 {
		return 0
	}

	var maxN float64
	for i := 1; i < len(grid.Densities); i++ {
		drho := grid.Densities[i] - grid.Densities[i-1]
		dz := grid.Heights[i] - grid.Heights[i-1]
		if dz > 0 {
			N2 := -g * drho / (rhoRef * dz)
			if N2 > 0 {
				N := math.Sqrt(N2)
				if N > maxN {
					maxN = N
				}
			}
		}
	}
	return maxN
}

func (p *RolloverPredictor) calculateConvectionVelocity(grid GridData, tankHeight float64) float64 {
	phys := p.cfg.PhysicalProperties
	g := phys.Gravity
	alpha := phys.ThermalExpansionCoeff
	nu := phys.KinematicViscosity
	alphaT := phys.ThermalDiffusivity

	maxTempGrad := 0.0
	for i := 1; i < len(grid.Temperatures); i++ {
		dz := grid.Heights[i] - grid.Heights[i-1]
		if dz > 0 {
			grad := math.Abs(grid.Temperatures[i] - grid.Temperatures[i-1]) / dz
			if grad > maxTempGrad {
				maxTempGrad = grad
			}
		}
	}

	Ra := g * alpha * maxTempGrad * math.Pow(tankHeight, 4) / (nu * alphaT)
	RaCritical := p.cfg.StabilityAnalysis.RayleighNumberCritical
	if Ra < RaCritical {
		return 0
	}

	Pr := nu / alphaT
	Nu := 0.1 * math.Pow(Ra, 1.0/3) * math.Pow(Pr, 0.074)

	k := 0.14
	h := Nu * k / tankHeight
	deltaT := maxTempGrad * tankHeight

	rhoRef := phys.BaseDensity
	cp := 422.5
	velocity := h * deltaT / (rhoRef * cp)
	return velocity
}

func (p *RolloverPredictor) solveConvectionEquation(grid GridData, tankHeight, tankDiameter float64) float64 {
	num := p.cfg.NumericalMethod
	phys := p.cfg.PhysicalProperties

	n := num.GridPoints
	dz := tankHeight / float64(n-1)
	dt := num.InitialTimeStep
	dtMin := num.MinTimeStep
	dtMax := num.MaxTimeStep

	underRelaxation := num.InitialUnderRelaxation
	minRelaxation := num.MinUnderRelaxation
	maxRelaxation := num.MaxUnderRelaxation

	rho := make([]float64, n)
	copy(rho, grid.Densities)

	rhoNew := make([]float64, n)
	copy(rhoNew, grid.Densities)

	rhoPrev := make([]float64, n)
	copy(rhoPrev, grid.Densities)

	u := make([]float64, n)
	copy(u, grid.Velocities)

	uPrev := make([]float64, n)

	g := phys.Gravity
	nu := phys.KinematicViscosity
	alphaT := phys.ThermalDiffusivity

	var criticalTime float64 = -1
	maxTimeSteps := num.MaxTimeSteps

	var maxResidual float64
	consecutiveDivergence := 0
	boundaryChangeCount := 0

	interfaceGradThreshold := p.cfg.StabilityAnalysis.InterfaceGradientThreshold
	convectionVelThreshold := p.cfg.StabilityAnalysis.ConvectionVelocityThreshold

	for step := 0; step < maxTimeSteps; step++ {
		copy(uPrev, u)
		copy(rhoPrev, rho)

		for i := 1; i < n-1; i++ {
			if rho[i] == 0 {
				continue
			}
			drho_dz := (rho[i+1] - rho[i-1]) / (2 * dz)
			buoyancy := -g * drho_dz / rho[i]

			du_dz2 := (u[i+1] - 2*u[i] + u[i-1]) / (dz * dz)
			du_dt := buoyancy + nu*du_dz2

			u[i] = uPrev[i] + underRelaxation*dt*du_dt

			CFL := math.Abs(u[i]) * dt / dz
			if CFL > num.CFLLimit {
				dt = 0.8 * dz / math.Max(math.Abs(u[i]), 1e-10)
				dt = math.Max(dtMin, math.Min(dtMax, dt))
			}
		}

		u[0] = 0
		u[n-1] = 0

		maxRhoChange := 0.0
		for i := 1; i < n-1; i++ {
			drho_dz := (rho[i+1] - rho[i-1]) / (2 * dz)
			d2rho_dz2 := (rho[i+1] - 2*rho[i] + rho[i-1]) / (dz * dz)

			advection := -u[i] * drho_dz
			diffusion := alphaT * d2rho_dz2

			drho_dt := advection + diffusion
			rhoNew[i] = rho[i] + underRelaxation*dt*drho_dt

			change := math.Abs(rhoNew[i] - rho[i])
			if change > maxRhoChange {
				maxRhoChange = change
			}
		}

		rhoNew[0] = rho[0]
		rhoNew[n-1] = rho[n-1]

		boundaryChange := math.Abs(rho[0]-rhoPrev[0]) + math.Abs(rho[n-1]-rhoPrev[n-1])
		if boundaryChange > num.BoundaryChangeThreshold {
			boundaryChangeCount++
			if boundaryChangeCount > 2 {
				underRelaxation = math.Max(minRelaxation, underRelaxation*0.7)
				dt = math.Max(dtMin, dt*0.5)
				boundaryChangeCount = 0
			}
		} else {
			boundaryChangeCount = 0
		}

		residual := 0.0
		for i := 1; i < n-1; i++ {
			contRes := (rhoNew[i]-rho[i])/dt + (u[i+1]*rhoNew[i+1]-u[i-1]*rhoNew[i-1])/(2*dz)
			residual += contRes * contRes
		}
		residual = math.Sqrt(residual / float64(n-2))

		if step > 0 {
			if residual > maxResidual*num.DivergenceThreshold && maxResidual > 1e-10 {
				consecutiveDivergence++
				if consecutiveDivergence >= num.MaxConsecutiveDivergence {
					underRelaxation = math.Max(minRelaxation, underRelaxation*0.6)
					dt = math.Max(dtMin, dt*0.5)
					copy(rho, rhoPrev)
					copy(u, uPrev)
					consecutiveDivergence = 0
					continue
				}
			} else {
				consecutiveDivergence = 0
				if residual < maxResidual*num.ConvergenceThreshold && maxResidual > 0 {
					underRelaxation = math.Min(maxRelaxation, underRelaxation*1.05)
					dt = math.Min(dtMax, dt*1.1)
				}
			}
		}
		maxResidual = math.Max(maxResidual, residual)

		copy(rho, rhoNew)

		interfaceHeight, gradient := p.findDensityInterface(rho, grid.Heights)
		if interfaceHeight > 0 && gradient > interfaceGradThreshold && criticalTime < 0 {
			criticalTime = float64(step) * dt / 3600.0
			break
		}

		if math.Abs(u[n/2]) > convectionVelThreshold && criticalTime < 0 {
			criticalTime = float64(step) * dt / 3600.0
			break
		}
	}

	if criticalTime < 0 {
		criticalTime = float64(maxTimeSteps) * dt / 3600.0
	}

	return criticalTime
}

func (p *RolloverPredictor) findDensityInterface(rho, heights []float64) (float64, float64) {
	maxGradient := 0.0
	interfaceHeight := 0.0

	for i := 1; i < len(rho); i++ {
		dz := heights[i] - heights[i-1]
		if dz > 0 {
			grad := math.Abs(rho[i] - rho[i-1]) / dz
			if grad > maxGradient {
				maxGradient = grad
				interfaceHeight = (heights[i] + heights[i-1]) / 2
			}
		}
	}

	return interfaceHeight, maxGradient
}

func (p *RolloverPredictor) calculateRiskIndex(tempDiff, densityDiff, stability, criticalTime float64) float64 {
	risk := p.cfg.RiskCalculation

	tempScore := math.Min(1, tempDiff/risk.MaxTempDiffReference)
	densityScore := math.Min(1, densityDiff/risk.MaxDensityDiffReference)
	stabilityScore := 1.0 - stability
	timeScore := 1.0
	if criticalTime > 0 {
		timeScore = math.Min(1, risk.CriticalTimeReference/criticalTime)
	}

	riskIndex := risk.TempDiffWeight*tempScore +
		risk.DensityDiffWeight*densityScore +
		risk.InstabilityWeight*stabilityScore +
		risk.TimeWeight*timeScore

	return math.Max(0, math.Min(1, riskIndex))
}

func (p *RolloverPredictor) determineRiskLevel(riskIndex float64) string {
	thresholds := p.cfg.RiskCalculation.RiskThresholds

	if riskIndex >= thresholds["high"] {
		return "CRITICAL"
	} else if riskIndex >= thresholds["medium_high"] {
		return "HIGH"
	} else if riskIndex >= thresholds["medium"] {
		return "MEDIUM"
	} else if riskIndex >= thresholds["low"] {
		return "LOW"
	}
	return "SAFE"
}

func (p *RolloverPredictor) storeResult(ctx context.Context, result messages.PredictionResult, req messages.PredictionRequest) {
	prediction := models.RolloverPrediction{
		Time:                    result.PredictedAt,
		TankID:                  result.TankID,
		RiskIndex:               result.RiskIndex,
		MaxTempDiff:             result.MaxTempDiff,
		MaxDensityDiff:          result.MaxDensityDiff,
		CriticalTimeHours:       result.CriticalTime,
		StratificationStability: 1 - (result.BuoyancyFreq / 10.0),
		ConvectionVelocity:      p.calculateConvectionVelocity(GridData{
			Heights:      nil,
			Temperatures: nil,
			Densities:    nil,
			Velocities:   nil,
		}, p.cfg.TankSpecs.HeightMeters),
		Recommendation: p.generateRecommendation(result.RiskIndex, result.MaxTempDiff, result.MaxDensityDiff),
		ModelVersion:   "2.0-refactored",
	}

	if err := p.db.InsertRolloverPrediction(ctx, prediction); err != nil {
		fmt.Printf("Store prediction error: %v\n", err)
	}
}

func (p *RolloverPredictor) generateRecommendation(riskIndex, tempDiff, densityDiff float64) string {
	thresholds := p.cfg.RiskCalculation.RiskThresholds

	switch {
	case riskIndex >= thresholds["high"]:
		return fmt.Sprintf("高风险！立即开启低压泵进行循环混合，监测温度差%.2f℃，密度差%.2fkg/m³", tempDiff, densityDiff)
	case riskIndex >= thresholds["medium_high"]:
		return fmt.Sprintf("较高风险，建议准备开启低压泵循环，当前温度差%.2f℃，密度差%.2fkg/m³", tempDiff, densityDiff)
	case riskIndex >= thresholds["medium"]:
		return fmt.Sprintf("中等风险，密切监测分层变化，温度差%.2f℃，密度差%.2fkg/m³", tempDiff, densityDiff)
	case riskIndex >= thresholds["low"]:
		return "低风险，正常监测"
	default:
		return "安全，分层稳定"
	}
}
