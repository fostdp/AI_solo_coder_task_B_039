package prediction

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/models"
	"math"
	"time"
)

type RolloverPredictor struct {
	cfg *config.PredictionConfig
	db  *database.DB
}

type GridData struct {
	Heights     []float64
	Temperatures []float64
	Densities   []float64
	Velocities  []float64
}

func NewPredictor(cfg *config.PredictionConfig, db *database.DB) *RolloverPredictor {
	return &RolloverPredictor{
		cfg: cfg,
		db:  db,
	}
}

func (p *RolloverPredictor) Start(ctx context.Context, tankCount int) {
	ticker := time.NewTicker(time.Duration(p.cfg.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for tankID := 1; tankID <= tankCount; tankID++ {
				if err := p.PredictAndStore(ctx, tankID); err != nil {
					fmt.Printf("Prediction error for tank %d: %v\n", tankID, err)
				}
			}
		}
	}
}

func (p *RolloverPredictor) PredictAndStore(ctx context.Context, tankID int) error {
	layerTemps, err := p.db.GetLayerAvgTemps(ctx, tankID)
	if err != nil {
		return fmt.Errorf("get layer temps: %w", err)
	}

	densityData, err := p.db.GetLatestDensityData(ctx, tankID)
	if err != nil {
		return fmt.Errorf("get density data: %w", err)
	}

	tank, err := p.getTankInfo(ctx, tankID)
	if err != nil {
		return fmt.Errorf("get tank info: %w", err)
	}

	gridData := p.interpolateToGrid(layerTemps, densityData, tank.Height)

	maxTempDiff := calculateMaxGradient(gridData.Temperatures, gridData.Heights)
	maxDensityDiff := calculateMaxGradient(gridData.Densities, gridData.Heights)

	stability := p.calculateStratificationStability(gridData)
	convectionVel := p.calculateConvectionVelocity(gridData, tank.Height)

	criticalTime := p.solveConvectionEquation(gridData, tank.Height, tank.Diameter)

	riskIndex := p.calculateRiskIndex(maxTempDiff, maxDensityDiff, stability, criticalTime)

	recommendation := p.generateRecommendation(riskIndex, maxTempDiff, maxDensityDiff)

	prediction := models.RolloverPrediction{
		Time:                    time.Now(),
		TankID:                  tankID,
		RiskIndex:               riskIndex,
		MaxTempDiff:             maxTempDiff,
		MaxDensityDiff:          maxDensityDiff,
		CriticalTimeHours:       criticalTime,
		StratificationStability: stability,
		ConvectionVelocity:      convectionVel,
		Recommendation:          recommendation,
		ModelVersion:            p.cfg.ModelVersion,
	}

	return p.db.InsertRolloverPrediction(ctx, prediction)
}

func (p *RolloverPredictor) getTankInfo(ctx context.Context, tankID int) (*models.Tank, error) {
	tanks, err := p.db.GetTanks(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tanks {
		if t.TankID == tankID {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("tank %d not found", tankID)
}

func (p *RolloverPredictor) interpolateToGrid(layerTemps []float64, densityData []models.DensityData, tankHeight float64) GridData {
	n := p.cfg.GridPoints
	heights := make([]float64, n)
	temps := make([]float64, n)
	densities := make([]float64, n)
	velocities := make([]float64, n)

	layerHeights := []float64{4.0, 14.0, 24.0, 34.0, 44.0}
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
	g := 9.81
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

	stability := 1.0 - math.Exp(-avgN2*100)
	return math.Max(0, math.Min(1, stability))
}

func (p *RolloverPredictor) calculateConvectionVelocity(grid GridData, tankHeight float64) float64 {
	g := 9.81
	alpha := 0.001
	nu := 1.0e-6

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

	Ra := g * alpha * maxTempGrad * math.Pow(tankHeight, 4) / (nu * 1.0e-7)
	if Ra < 1e3 {
		return 0
	}

	Pr := nu / 1.0e-7
	Nu := 0.1 * math.Pow(Ra, 1.0/3) * math.Pow(Pr, 0.074)

	k := 0.14
	h := Nu * k / tankHeight
	deltaT := maxTempGrad * tankHeight

	velocity := h * deltaT / (422.5 * 1000)
	return velocity
}

func (p *RolloverPredictor) solveConvectionEquation(grid GridData, tankHeight, tankDiameter float64) float64 {
	n := p.cfg.GridPoints
	dz := tankHeight / float64(n-1)
	dt := 0.1

	rho := make([]float64, n)
	copy(rho, grid.Densities)

	rhoNew := make([]float64, n)
	u := make([]float64, n)
	copy(u, grid.Velocities)

	g := 9.81
	nu := 1.0e-6
	alphaT := 1.0e-7

	var criticalTime float64 = -1
	maxTimeSteps := p.cfg.TimeSteps

	for step := 0; step < maxTimeSteps; step++ {
		for i := 1; i < n-1; i++ {
			if rho[i] == 0 {
				continue
			}
			drho_dz := (rho[i+1] - rho[i-1]) / (2 * dz)
			buoyancy := -g * drho_dz / rho[i]

			du_dz2 := (u[i+1] - 2*u[i] + u[i-1]) / (dz * dz)
			u[i] += dt * (buoyancy + nu*du_dz2)

			CFL := math.Abs(u[i]) * dt / dz
			if CFL > 0.5 {
				dt = 0.5 * dz / math.Max(math.Abs(u[i]), 1e-10)
			}
		}

		u[0] = 0
		u[n-1] = 0

		for i := 1; i < n-1; i++ {
			drho_dz := (rho[i+1] - rho[i-1]) / (2 * dz)
			d2rho_dz2 := (rho[i+1] - 2*rho[i] + rho[i-1]) / (dz * dz)

			advection := -u[i] * drho_dz
			diffusion := alphaT * d2rho_dz2

			rhoNew[i] = rho[i] + dt*(advection+diffusion)
		}

		rhoNew[0] = rho[0]
		rhoNew[n-1] = rho[n-1]

		copy(rho, rhoNew)

		interfaceHeight, gradient := p.findDensityInterface(rho, grid.Heights)
		if interfaceHeight > 0 && gradient > 50 && criticalTime < 0 {
			criticalTime = float64(step) * dt / 3600.0
			break
		}

		if math.Abs(u[n/2]) > 0.01 && criticalTime < 0 {
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
	tempScore := math.Min(1, tempDiff/10.0)
	densityScore := math.Min(1, densityDiff/5.0)
	stabilityScore := 1.0 - stability
	timeScore := 1.0
	if criticalTime > 0 {
		timeScore = math.Min(1, 24.0/criticalTime)
	}

	riskIndex := 0.35*tempScore + 0.25*densityScore + 0.25*stabilityScore + 0.15*timeScore
	return math.Max(0, math.Min(1, riskIndex))
}

func (p *RolloverPredictor) generateRecommendation(riskIndex, tempDiff, densityDiff float64) string {
	switch {
	case riskIndex >= 0.8:
		return fmt.Sprintf("高风险！立即开启低压泵进行循环混合，监测温度差%.2f℃，密度差%.2fkg/m³", tempDiff, densityDiff)
	case riskIndex >= 0.6:
		return fmt.Sprintf("较高风险，建议准备开启低压泵循环，当前温度差%.2f℃，密度差%.2fkg/m³", tempDiff, densityDiff)
	case riskIndex >= 0.4:
		return fmt.Sprintf("中等风险，密切监测分层变化，温度差%.2f℃，密度差%.2fkg/m³", tempDiff, densityDiff)
	case riskIndex >= 0.2:
		return "低风险，正常监测"
	default:
		return "安全，分层稳定"
	}
}

func (p *RolloverPredictor) GetPrediction(ctx context.Context, tankID int) (*models.RolloverPrediction, error) {
	return p.db.GetLatestRolloverPrediction(ctx, tankID)
}
