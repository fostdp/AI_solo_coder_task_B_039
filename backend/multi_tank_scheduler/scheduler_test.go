package multi_tank_scheduler

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/messages"
	"lng-monitoring/testutils"
	"math"
	"strings"
	"testing"
	"time"
)

func TestOptimizationSolverSpeed(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	testCases := []struct {
		name        string
		nTanks      int
		riskPattern string
		maxTimeMs   int64
	}{
		{"2_tanks", 2, "mixed", 50},
		{"4_tanks", 4, "mixed", 100},
		{"6_tanks", 6, "mixed", 200},
		{"8_tanks", 8, "all_high", 300},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			states := testutils.GenerateTankStatesForScheduler(tc.nTanks, tc.riskPattern)

			startTime := time.Now()
			result := scheduler.optimizeSchedule(context.Background(), states)
			elapsed := time.Since(startTime).Milliseconds()

			if result.ErrorMessage != "" {
				t.Fatalf("Optimization failed: %s", result.ErrorMessage)
			}

			t.Logf("%d tanks optimization took %d ms (max: %d ms)", tc.nTanks, elapsed, tc.maxTimeMs)

			if elapsed > tc.maxTimeMs {
				t.Errorf("Optimization too slow: %d ms > %d ms", elapsed, tc.maxTimeMs)
			}

			if len(result.CompressorLoads) == 0 && tc.nTanks > 0 {
				t.Error("No compressor loads returned")
			}
		})
	}
}

func TestGlobalOptimality(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	baselineStates := testutils.GenerateTankStatesForScheduler(4, "all_low")
	baselineResult := scheduler.optimizeSchedule(context.Background(), baselineStates)
	baselineObjective := scheduler.calculateObjective(
		baselineResult.EvaporationLoss,
		baselineResult.CompressorLoads,
		baselineResult.PumpOperations,
	)

	t.Logf("Baseline (all low risk) total cost: %.2f CNY", baselineObjective.TotalCost)

	highRiskStates := testutils.GenerateTankStatesForScheduler(4, "single_high")
	highRiskResult := scheduler.optimizeSchedule(context.Background(), highRiskStates)
	highRiskObjective := scheduler.calculateObjective(
		highRiskResult.EvaporationLoss,
		highRiskResult.CompressorLoads,
		highRiskResult.PumpOperations,
	)

	t.Logf("High risk scenario total cost: %.2f CNY", highRiskObjective.TotalCost)

	if highRiskObjective.TotalCost < baselineObjective.TotalCost {
		t.Error("High risk scenario should have higher cost than baseline")
	}

	costIncrease := (highRiskObjective.TotalCost - baselineObjective.TotalCost) / baselineObjective.TotalCost
	t.Logf("Cost increase for high risk: %.1f%%", costIncrease*100)

	if costIncrease < 0.1 {
		t.Error("Cost increase should be at least 10% for high risk scenario")
	}

	mixedStates := testutils.GenerateTankStatesForScheduler(4, "mixed")
	mixedResult := scheduler.optimizeSchedule(context.Background(), mixedStates)
	mixedObjective := scheduler.calculateObjective(
		mixedResult.EvaporationLoss,
		mixedResult.CompressorLoads,
		mixedResult.PumpOperations,
	)

	t.Logf("Mixed risk scenario total cost: %.2f CNY", mixedObjective.TotalCost)

	if mixedObjective.TotalCost <= baselineObjective.TotalCost {
		t.Error("Mixed risk scenario should have higher cost than baseline")
	}
	if mixedObjective.TotalCost >= highRiskObjective.TotalCost {
		t.Error("Mixed risk scenario should have lower cost than high risk")
	}

	allHighStates := testutils.GenerateTankStatesForScheduler(4, "all_high")
	allHighResult := scheduler.optimizeSchedule(context.Background(), allHighStates)
	allHighObjective := scheduler.calculateObjective(
		allHighResult.EvaporationLoss,
		allHighResult.CompressorLoads,
		allHighResult.PumpOperations,
	)

	t.Logf("All high risk scenario total cost: %.2f CNY", allHighObjective.TotalCost)

	if allHighObjective.TotalCost <= highRiskObjective.TotalCost {
		t.Error("All high risk should have highest cost")
	}
}

func TestEvaporationLossReduction(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(4, "all_high")
	for i := range states {
		states[i].Pressure = 0.25
		states[i].AvgTemp = -155.0
	}

	noOptimizationLoss := calculateBaselineEvaporation(states, cfg)
	t.Logf("Baseline evaporation loss (no optimization): %.4f m³/day", noOptimizationLoss)

	result := scheduler.optimizeSchedule(context.Background(), states)
	optimizedLoss := result.EvaporationLoss
	t.Logf("Optimized evaporation loss: %.4f m³/day", optimizedLoss)

	if optimizedLoss >= noOptimizationLoss {
		t.Errorf("Optimization should reduce evaporation loss: %.4f >= %.4f",
			optimizedLoss, noOptimizationLoss)
	}

	reductionRate := (noOptimizationLoss - optimizedLoss) / noOptimizationLoss
	t.Logf("Evaporation loss reduction rate: %.1f%%", reductionRate*100)

	minReductionRate := 0.30
	if reductionRate < minReductionRate {
		t.Errorf("Evaporation loss reduction rate %.1f%% below minimum %.1f%%",
			reductionRate*100, minReductionRate*100)
	}

	lowPressureStates := testutils.GenerateTankStatesForScheduler(4, "all_low")
	for i := range lowPressureStates {
		lowPressureStates[i].Pressure = 0.12
		lowPressureStates[i].AvgTemp = -162.0
	}

	lowPressureBaseline := calculateBaselineEvaporation(lowPressureStates, cfg)
	lowPressureResult := scheduler.optimizeSchedule(context.Background(), lowPressureStates)
	lowPressureOptimized := lowPressureResult.EvaporationLoss

	t.Logf("Low pressure - baseline: %.4f, optimized: %.4f",
		lowPressureBaseline, lowPressureOptimized)

	lowPressureReduction := (lowPressureBaseline - lowPressureOptimized) / lowPressureBaseline
	t.Logf("Low pressure reduction rate: %.1f%%", lowPressureReduction*100)

	if lowPressureOptimized >= lowPressureBaseline {
		t.Error("Low pressure scenario should also reduce evaporation")
	}
}

func calculateBaselineEvaporation(states []messages.TankStateForScheduler, cfg *config.Config) float64 {
	totalEvaporation := 0.0
	lngBoilOffRate := 0.00005

	for _, state := range states {
		tankCapacity := cfg.ModelParams.TankSpecs.CapacityCubicMeters
		baseEvaporation := tankCapacity * state.Level * lngBoilOffRate * 24.0

		pressureFactor := 1.0
		if state.Pressure > 0.20 {
			pressureFactor = 1.0 + (state.Pressure-0.20)*10
		}

		tempFactor := 1.0
		if state.AvgTemp > -160 {
			tempFactor = 1.0 + (state.AvgTemp+160)*0.1
		}

		compressorCapacity := 0.3 * 0.75
		netEvaporation := baseEvaporation * pressureFactor * tempFactor * (1.0 - compressorCapacity)
		totalEvaporation += math.Max(0, netEvaporation)
	}

	return totalEvaporation
}

func TestConstraintSatisfaction(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	patterns := []string{"all_low", "all_high", "mixed", "single_high"}

	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			states := testutils.GenerateTankStatesForScheduler(4, pattern)
			result := scheduler.optimizeSchedule(context.Background(), states)

			if result.ErrorMessage != "" {
				t.Fatalf("Optimization failed: %s", result.ErrorMessage)
			}

			for key, load := range result.CompressorLoads {
				if load < 0 {
					t.Errorf("Compressor %s load negative: %.2f", key, load)
				}
				maxLoad := cfg.ModelParams.Scheduler.MaxLoadPctPerCompressor[key]
				if load > maxLoad+0.01 {
					t.Errorf("Compressor %s load %.2f exceeds max %.2f",
						key, load, maxLoad)
				}
			}

			for _, op := range result.PumpOperations {
				if op.TankID < 1 || op.TankID > len(states) {
					t.Errorf("Invalid tank ID in pump operation: %d", op.TankID)
				}
				if op.PumpID < 1 || op.PumpID > 3 {
					t.Errorf("Invalid pump ID: %d", op.PumpID)
				}
				if op.StartTime < 0 {
					t.Errorf("Negative start time: %.2f", op.StartTime)
				}
				if op.Duration < 0 {
					t.Errorf("Negative duration: %.2f", op.Duration)
				}
				validActions := map[string]bool{"start": true, "prepare": true, "monitor": true}
				if !validActions[op.Action] {
					t.Errorf("Invalid action: %s", op.Action)
				}
			}

			if result.EvaporationLoss < 0 {
				t.Errorf("Negative evaporation loss: %.4f", result.EvaporationLoss)
			}

			objective := scheduler.calculateObjective(
				result.EvaporationLoss,
				result.CompressorLoads,
				result.PumpOperations,
			)

			if objective.TotalCost < 0 {
				t.Errorf("Negative total cost: %.2f", objective.TotalCost)
			}
			if objective.EvaporationLossCost < 0 {
				t.Errorf("Negative evaporation cost: %.2f", objective.EvaporationLossCost)
			}
			if objective.ElectricityCost < 0 {
				t.Errorf("Negative electricity cost: %.2f", objective.ElectricityCost)
			}

			expectedTotal := objective.EvaporationLossCost + objective.ElectricityCost
			if math.Abs(objective.TotalCost-expectedTotal) > 0.01 {
				t.Errorf("Cost breakdown mismatch: total=%.2f, sum=%.2f",
					objective.TotalCost, expectedTotal)
			}
		})
	}
}

func TestCompressorLoadAllocation(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(4, "mixed")

	result := scheduler.optimizeSchedule(context.Background(), states)

	highRiskTank := 0
	lowRiskTank := 0
	for i, state := range states {
		if state.RiskIndex > 0.6 {
			highRiskTank = i + 1
		}
		if state.RiskIndex < 0.2 {
			lowRiskTank = i + 1
		}
	}

	if highRiskTank > 0 && lowRiskTank > 0 {
		highKey1 := scheduler.compressorKey(highRiskTank, 1)
		lowKey1 := scheduler.compressorKey(lowRiskTank, 1)

		highLoad := result.CompressorLoads[highKey1]
		lowLoad := result.CompressorLoads[lowKey1]

		t.Logf("High risk tank %d load: %.1f%%, Low risk tank %d load: %.1f%%",
			highRiskTank, highLoad, lowRiskTank, lowLoad)

		if highLoad <= lowLoad {
			t.Errorf("High risk tank should have higher load: %.1f <= %.1f",
				highLoad, lowLoad)
		}
	}

	for key, load := range result.CompressorLoads {
		if strings.HasSuffix(key, "_C1") {
			tankID := 0
			_, err := fmt.Sscanf(key, "T%d_C1", &tankID)
			if err == nil {
				c2Key := scheduler.compressorKey(tankID, 2)
				c2Load, exists := result.CompressorLoads[c2Key]
				if exists {
					t.Logf("Tank %d: C1=%.1f%%, C2=%.1f%%", tankID, load, c2Load)
					if c2Load > load {
						t.Logf("Note: C2 load may be lower as per design (0.8 factor)")
					}
				}
			}
		}
	}
}

func TestPumpOperationLogic(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	testCases := []struct {
		name          string
		riskIndex     float64
		level         float64
		expectedAction string
		minDuration   float64
	}{
		{"critical_risk", 0.9, 0.5, "start", 2.0},
		{"high_risk", 0.7, 0.5, "start", 1.0},
		{"medium_risk", 0.5, 0.5, "prepare", 0.5},
		{"low_risk", 0.2, 0.5, "monitor", 0.0},
		{"critical_high_level", 0.9, 0.85, "start", 2.0},
		{"critical_low_level", 0.9, 0.3, "start", 2.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state := messages.TankStateForScheduler{
				TankID:      1,
				Level:       tc.level,
				AvgTemp:     -160.0,
				RiskIndex:   tc.riskIndex,
				Pressure:    0.15,
				HasBOGComp1: true,
				HasBOGComp2: true,
			}

			result := scheduler.optimizeSchedule(context.Background(),
				[]messages.TankStateForScheduler{state})

			if tc.expectedAction == "monitor" {
				if len(result.PumpOperations) > 0 {
					t.Errorf("Expected no pump operations for low risk, got %d",
						len(result.PumpOperations))
				}
				return
			}

			if len(result.PumpOperations) == 0 {
				t.Fatalf("Expected pump operation for risk %.2f, got none", tc.riskIndex)
			}

			op := result.PumpOperations[0]
			t.Logf("Risk %.2f -> action=%s, duration=%.1fh, start=%.1fh, pump=%d",
				tc.riskIndex, op.Action, op.Duration, op.StartTime, op.PumpID)

			if op.Action != tc.expectedAction {
				t.Errorf("Expected action '%s', got '%s'", tc.expectedAction, op.Action)
			}

			if op.Duration < tc.minDuration-0.01 {
				t.Errorf("Duration %.1f below minimum %.1f", op.Duration, tc.minDuration)
			}

			expectedPump := 1
			if tc.level > 0.6 {
				expectedPump = 2
			}
			if tc.level > 0.8 {
				expectedPump = 3
			}
			if op.PumpID != expectedPump {
				t.Errorf("Expected pump %d for level %.2f, got %d",
					expectedPump, tc.level, op.PumpID)
			}
		})
	}
}



func TestNormalConditions(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(4, "all_low")
	for i := range states {
		states[i].Pressure = 0.15
		states[i].AvgTemp = -162.0
		states[i].Level = 0.5
	}

	result := scheduler.optimizeSchedule(context.Background(), states)

	if result.ErrorMessage != "" {
		t.Fatalf("Optimization failed: %s", result.ErrorMessage)
	}

	t.Logf("Normal conditions - Compressor loads: %d, Pump ops: %d, Evap loss: %.4f m³/day",
		len(result.CompressorLoads), len(result.PumpOperations), result.EvaporationLoss)

	if len(result.CompressorLoads) != 8 {
		t.Errorf("Expected 8 compressor loads (2 per tank × 4 tanks), got %d",
			len(result.CompressorLoads))
	}

	for _, load := range result.CompressorLoads {
		if load < 30 || load > 70 {
			t.Logf("Note: Load %.1f%% outside typical normal range 30-70%%", load)
		}
	}

	if len(result.PumpOperations) != 0 {
		t.Errorf("Expected no pump operations for normal conditions, got %d",
			len(result.PumpOperations))
	}

	if result.EvaporationLoss < 10 || result.EvaporationLoss > 100 {
		t.Errorf("Evaporation loss %.4f outside expected range 10-100 m³/day",
			result.EvaporationLoss)
	}
}

func TestBoundaryConditions(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	t.Run("empty_states", func(t *testing.T) {
		result := scheduler.optimizeSchedule(context.Background(), []messages.TankStateForScheduler{})
		if result.ErrorMessage == "" {
			t.Error("Expected error for empty states")
		}
		if !strings.Contains(result.ErrorMessage, "no tank states") {
			t.Errorf("Unexpected error message: %s", result.ErrorMessage)
		}
	})

	t.Run("single_tank", func(t *testing.T) {
		states := testutils.GenerateTankStatesForScheduler(1, "single_high")
		result := scheduler.optimizeSchedule(context.Background(), states)
		if result.ErrorMessage != "" {
			t.Errorf("Single tank optimization failed: %s", result.ErrorMessage)
		}
		if len(result.CompressorLoads) != 2 {
			t.Errorf("Expected 2 compressor loads for single tank, got %d",
				len(result.CompressorLoads))
		}
	})

	t.Run("no_compressors", func(t *testing.T) {
		states := testutils.GenerateTankStatesForScheduler(2, "all_high")
		for i := range states {
			states[i].HasBOGComp1 = false
			states[i].HasBOGComp2 = false
		}
		result := scheduler.optimizeSchedule(context.Background(), states)
		if len(result.CompressorLoads) != 0 {
			t.Errorf("Expected no compressor loads when no compressors available, got %d",
				len(result.CompressorLoads))
		}
		t.Logf("No compressors - evaporation loss: %.4f m³/day", result.EvaporationLoss)
	})

	t.Run("extreme_pressure", func(t *testing.T) {
		states := testutils.GenerateTankStatesForScheduler(2, "all_high")
		states[0].Pressure = 0.30
		states[1].Pressure = 0.05

		result := scheduler.optimizeSchedule(context.Background(), states)
		if result.ErrorMessage != "" {
			t.Errorf("Extreme pressure optimization failed: %s", result.ErrorMessage)
		}

		highKey := scheduler.compressorKey(1, 1)
		lowKey := scheduler.compressorKey(2, 1)

		t.Logf("Extreme pressure - High pressure (%.2f) load: %.1f%%, Low pressure (%.2f) load: %.1f%%",
			states[0].Pressure, result.CompressorLoads[highKey],
			states[1].Pressure, result.CompressorLoads[lowKey])

		if result.CompressorLoads[highKey] <= result.CompressorLoads[lowKey] {
			t.Error("High pressure tank should have higher compressor load")
		}
	})

	t.Run("extreme_temperature", func(t *testing.T) {
		states := testutils.GenerateTankStatesForScheduler(2, "mixed")
		states[0].AvgTemp = -150.0
		states[1].AvgTemp = -165.0

		result := scheduler.optimizeSchedule(context.Background(), states)

		t.Logf("Extreme temp - Warm tank evap: baseline vs optimized check")
		if result.EvaporationLoss <= 0 {
			t.Error("Evaporation loss should be positive")
		}
	})
}

func TestAbnormalConditions(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	t.Run("all_tanks_critical", func(t *testing.T) {
		states := testutils.GenerateTankStatesForScheduler(4, "all_high")
		for i := range states {
			states[i].RiskIndex = 0.95
			states[i].Pressure = 0.28
			states[i].AvgTemp = -152.0
		}

		startTime := time.Now()
		result := scheduler.optimizeSchedule(context.Background(), states)
		elapsed := time.Since(startTime)

		t.Logf("All critical - optimized in %v", elapsed)
		t.Logf("Compressor loads: %d, Pump ops: %d",
			len(result.CompressorLoads), len(result.PumpOperations))

		for _, load := range result.CompressorLoads {
			if load < 70 {
				t.Errorf("Expected high load (>70%%) for critical conditions, got %.1f%%", load)
			}
		}

		if len(result.PumpOperations) < 2 {
			t.Errorf("Expected at least 2 pump operations for all critical, got %d",
				len(result.PumpOperations))
		}
	})

	t.Run("rapid_risk_escalation", func(t *testing.T) {
		baseStates := testutils.GenerateTankStatesForScheduler(4, "all_low")
		lowResult := scheduler.optimizeSchedule(context.Background(), baseStates)

		highStates := testutils.GenerateTankStatesForScheduler(4, "all_high")
		highResult := scheduler.optimizeSchedule(context.Background(), highStates)

		t.Logf("Risk escalation - Low risk cost: %.2f, High risk cost: %.2f",
			scheduler.calculateObjective(lowResult.EvaporationLoss, lowResult.CompressorLoads, lowResult.PumpOperations).TotalCost,
			scheduler.calculateObjective(highResult.EvaporationLoss, highResult.CompressorLoads, highResult.PumpOperations).TotalCost)

		loadIncreaseCount := 0
		for key, lowLoad := range lowResult.CompressorLoads {
			highLoad := highResult.CompressorLoads[key]
			if highLoad > lowLoad+5 {
				loadIncreaseCount++
			}
		}
		t.Logf("Compressors with load increase >5%%: %d/%d",
			loadIncreaseCount, len(lowResult.CompressorLoads))

		if loadIncreaseCount < len(lowResult.CompressorLoads)/2 {
			t.Error("Most compressors should increase load for risk escalation")
		}
	})

	t.Run("unbalanced_tank_levels", func(t *testing.T) {
		states := testutils.GenerateTankStatesForScheduler(4, "mixed")
		states[0].Level = 0.95
		states[1].Level = 0.05
		states[2].Level = 0.90
		states[3].Level = 0.10

		result := scheduler.optimizeSchedule(context.Background(), states)

		for _, op := range result.PumpOperations {
			tank := states[op.TankID-1]
			t.Logf("Tank %d (level=%.2f, risk=%.2f) -> pump %d, action=%s",
				op.TankID, tank.Level, tank.RiskIndex, op.PumpID, op.Action)
		}

		highLevelPumps := 0
		lowLevelPumps := 0
		for _, op := range result.PumpOperations {
			if states[op.TankID-1].Level > 0.8 {
				highLevelPumps++
			}
			if states[op.TankID-1].Level < 0.2 {
				lowLevelPumps++
			}
		}

		t.Logf("High level tanks with pumps: %d, Low level tanks with pumps: %d",
			highLevelPumps, lowLevelPumps)
	})
}

func TestCostBreakdown(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(4, "mixed")
	result := scheduler.optimizeSchedule(context.Background(), states)

	breakdown := scheduler.GetCostBreakdown(context.Background(), &result)

	requiredKeys := []string{
		"evaporation_loss_ton",
		"evaporation_loss_cost",
		"electricity_cost",
		"total_operational_cost",
		"currency",
		"optimization_interval_h",
	}

	for _, key := range requiredKeys {
		if _, exists := breakdown[key]; !exists {
			t.Errorf("Missing key in cost breakdown: %s", key)
		}
	}

	if breakdown["currency"] != "CNY" {
		t.Errorf("Expected currency CNY, got %v", breakdown["currency"])
	}

	evapTon := breakdown["evaporation_loss_ton"].(float64)
	expectedEvapTon := result.EvaporationLoss * 0.425
	if math.Abs(evapTon-expectedEvapTon) > 0.01 {
		t.Errorf("Evaporation ton mismatch: %.4f vs %.4f", evapTon, expectedEvapTon)
	}

	totalCost := breakdown["total_operational_cost"].(float64)
	evapCost := breakdown["evaporation_loss_cost"].(float64)
	elecCost := breakdown["electricity_cost"].(float64)

	if math.Abs(totalCost-(evapCost+elecCost)) > 0.01 {
		t.Errorf("Cost breakdown sum mismatch: total=%.2f, sum=%.2f",
			totalCost, evapCost+elecCost)
	}

	t.Logf("Cost breakdown - Evap: %.2f CNY, Elec: %.2f CNY, Total: %.2f CNY",
		evapCost, elecCost, totalCost)
	t.Logf("Evaporation loss: %.4f m³/day = %.2f tons/day",
		result.EvaporationLoss, evapTon)
}

func TestSchedulerResultFields(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(4, "mixed")
	result := scheduler.optimizeSchedule(context.Background(), states)

	if result.CompressorLoads == nil {
		t.Error("CompressorLoads should not be nil")
	}
	if result.PumpOperations == nil {
		t.Error("PumpOperations should not be nil")
	}
	if result.OptimizedAt.IsZero() {
		t.Error("OptimizedAt should not be zero")
	}

	for key, load := range result.CompressorLoads {
		if !strings.HasPrefix(key, "T") || !strings.Contains(key, "_C") {
			t.Errorf("Invalid compressor key format: %s", key)
		}
		if load < 0 || load > 100 {
			t.Errorf("Invalid load value for %s: %.2f", key, load)
		}
	}

	for i, op := range result.PumpOperations {
		if op.TankID < 1 {
			t.Errorf("Operation %d: invalid TankID %d", i, op.TankID)
		}
		if op.PumpID < 1 || op.PumpID > 3 {
			t.Errorf("Operation %d: invalid PumpID %d", i, op.PumpID)
		}
		if op.Duration < 0 {
			t.Errorf("Operation %d: negative duration %.2f", i, op.Duration)
		}
		if op.Action == "" {
			t.Errorf("Operation %d: empty action", i, op.Action)
		}
	}

	t.Logf("Result fields verified: %d compressors, %d pump operations",
		len(result.CompressorLoads), len(result.PumpOperations))
}

func TestEndToEndOptimization(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	scenarios := []struct {
		name          string
		nTanks        int
		riskPattern   string
		expectedPumps int
	}{
		{"low_risk_4_tanks", 4, "all_low", 0},
		{"mixed_risk_4_tanks", 4, "mixed", 2},
		{"high_risk_4_tanks", 4, "all_high", 4},
		{"single_high_2_tanks", 2, "single_high", 1},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			states := testutils.GenerateTankStatesForScheduler(scenario.nTanks, scenario.riskPattern)

			result := scheduler.optimizeSchedule(context.Background(), states)

			if result.ErrorMessage != "" {
				t.Fatalf("Optimization failed: %s", result.ErrorMessage)
			}

			objective := scheduler.calculateObjective(
				result.EvaporationLoss,
				result.CompressorLoads,
				result.PumpOperations,
			)

			breakdown := scheduler.GetCostBreakdown(context.Background(), &result)

			t.Logf("Scenario %s: total cost=%.2f CNY, evap=%.4f m³/day, pumps=%d",
				scenario.name, objective.TotalCost, result.EvaporationLoss, len(result.PumpOperations))
			t.Logf("  Evap cost: %.2f CNY, Elec cost: %.2f CNY",
				breakdown["evaporation_loss_cost"].(float64),
				breakdown["electricity_cost"].(float64))

			if len(result.CompressorLoads) != scenario.nTanks*2 {
				t.Errorf("Expected %d compressor loads, got %d",
					scenario.nTanks*2, len(result.CompressorLoads))
			}

			if len(result.PumpOperations) < scenario.expectedPumps-1 ||
				len(result.PumpOperations) > scenario.expectedPumps+2 {
				t.Logf("Note: Pump operations %d outside expected range [%d, %d]",
					len(result.PumpOperations), scenario.expectedPumps-1, scenario.expectedPumps+2)
			}

			if result.EvaporationLoss < 0 {
				t.Error("Negative evaporation loss")
			}

			if objective.TotalCost <= 0 {
				t.Error("Non-positive total cost")
			}
		})
	}
}

func TestPerformanceBenchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping benchmark in short mode")
	}

	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(4, "mixed")

	nIterations := 1000
	totalTime := time.Duration(0)
	minTime := time.Duration(math.MaxInt64)
	maxTime := time.Duration(0)

	for i := 0; i < nIterations; i++ {
		start := time.Now()
		_ = scheduler.optimizeSchedule(context.Background(), states)
		elapsed := time.Since(start)

		totalTime += elapsed
		if elapsed < minTime {
			minTime = elapsed
		}
		if elapsed > maxTime {
			maxTime = elapsed
		}
	}

	avgTime := totalTime / time.Duration(nIterations)

	t.Logf("Performance benchmark (%d iterations):", nIterations)
	t.Logf("  Average: %v", avgTime)
	t.Logf("  Min: %v", minTime)
	t.Logf("  Max: %v", maxTime)
	t.Logf("  Throughput: %.1f ops/sec", float64(nIterations)/totalTime.Seconds())

	maxAllowed := 10 * time.Millisecond
	if avgTime > maxAllowed {
		t.Errorf("Average time %v exceeds maximum %v", avgTime, maxAllowed)
	}

	if maxTime > 50*time.Millisecond {
		t.Errorf("Max time %v exceeds 50ms threshold", maxTime)
	}
}

func TestBalanceCompressorLoads(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	testCases := []struct {
		name        string
		avgPressure float64
		initialLoad float64
		expectChange bool
	}{
		{"low_pressure", 0.10, 40.0, false},
		{"medium_pressure", 0.18, 40.0, false},
		{"high_pressure", 0.25, 40.0, true},
		{"very_high_pressure", 0.30, 50.0, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			loads := map[string]float64{
				"T1_C1": tc.initialLoad,
				"T1_C2": tc.initialLoad * 0.8,
				"T2_C1": tc.initialLoad,
				"T2_C2": tc.initialLoad * 0.8,
			}

			initialTotal := 0.0
			for _, load := range loads {
				initialTotal += load
			}

			scheduler.balanceCompressorLoads(loads, tc.avgPressure)

			finalTotal := 0.0
			for _, load := range loads {
				finalTotal += load
			}

			targetTotal := tc.avgPressure * 400.0
			t.Logf("Pressure %.2f: initial total=%.1f, target=%.1f, final total=%.1f",
				tc.avgPressure, initialTotal, targetTotal, finalTotal)

			if tc.expectChange {
				if finalTotal <= initialTotal {
					t.Error("Expected load increase for high pressure")
				}
			} else {
				if finalTotal > initialTotal+0.01 {
					t.Error("Expected no load change for low pressure")
				}
			}

			for key, load := range loads {
				maxLoad := cfg.ModelParams.Scheduler.MaxLoadPctPerCompressor[key]
				if load > maxLoad+0.01 {
					t.Errorf("Load %.2f exceeds max %.2f for %s", load, maxLoad, key)
				}
			}
		})
	}
}

func TestCompressorKey(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	testCases := []struct {
		tankID int
		compID int
		expected string
	}{
		{1, 1, "T1_C1"},
		{1, 2, "T1_C2"},
		{4, 1, "T4_C1"},
		{4, 2, "T4_C2"},
		{10, 1, "T10_C1"},
	}

	for _, tc := range testCases {
		result := scheduler.compressorKey(tc.tankID, tc.compID)
		if result != tc.expected {
			t.Errorf("compressorKey(%d, %d) = %s, expected %s",
				tc.tankID, tc.compID, result, tc.expected)
		}
	}
}

func TestRootCause_DecompositionScalability(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	smallTankCount := 4
	largeTankCount := 20

	smallStates := testutils.GenerateTankStatesForScheduler(smallTankCount, "random")
	largeStates := testutils.GenerateTankStatesForScheduler(largeTankCount, "random")

	scheduler.modelParams.DecompositionOn = false
	startSmallNoDecomp := time.Now()
	resultSmallNoDecomp := scheduler.optimizeSchedule(nil, smallStates)
	durationSmallNoDecomp := time.Since(startSmallNoDecomp)

	startLargeNoDecomp := time.Now()
	resultLargeNoDecomp := scheduler.optimizeSchedule(nil, largeStates)
	durationLargeNoDecomp := time.Since(startLargeNoDecomp)

	scheduler.modelParams.DecompositionOn = true
	startSmallWithDecomp := time.Now()
	resultSmallWithDecomp := scheduler.optimizeSchedule(nil, smallStates)
	durationSmallWithDecomp := time.Since(startSmallWithDecomp)

	startLargeWithDecomp := time.Now()
	resultLargeWithDecomp := scheduler.optimizeSchedule(nil, largeStates)
	durationLargeWithDecomp := time.Since(startLargeWithDecomp)

	t.Logf("Small problem (%d tanks):", smallTankCount)
	t.Logf("  Without decomposition: %v, decomposed: %v", durationSmallNoDecomp, resultSmallNoDecomp.Decomposed)
	t.Logf("  With decomposition: %v, decomposed: %v, subproblems: %d", durationSmallWithDecomp, resultSmallWithDecomp.Decomposed, resultSmallWithDecomp.SubproblemCount)

	t.Logf("Large problem (%d tanks):", largeTankCount)
	t.Logf("  Without decomposition: %v, decomposed: %v", durationLargeNoDecomp, resultLargeNoDecomp.Decomposed)
	t.Logf("  With decomposition: %v, decomposed: %v, subproblems: %d", durationLargeWithDecomp, resultLargeWithDecomp.Decomposed, resultLargeWithDecomp.SubproblemCount)

	if resultSmallWithDecomp.Decomposed {
		t.Error("Small problem should not use decomposition")
	}

	if !resultLargeWithDecomp.Decomposed {
		t.Error("Large problem should use decomposition")
	}

	if resultLargeWithDecomp.SubproblemCount < 2 {
		t.Errorf("Expected at least 2 subproblems for large problem, got %d", resultLargeWithDecomp.SubproblemCount)
	}

	scalabilityRatioNoDecomp := float64(durationLargeNoDecomp) / float64(durationSmallNoDecomp)
	scalabilityRatioWithDecomp := float64(durationLargeWithDecomp) / float64(durationSmallWithDecomp)
	tankCountRatio := float64(largeTankCount) / float64(smallTankCount)

	t.Logf("Scalability ratio (large/small time):")
	t.Logf("  Without decomposition: %.2fx (tank ratio: %.2fx)", scalabilityRatioNoDecomp, tankCountRatio)
	t.Logf("  With decomposition: %.2fx (tank ratio: %.2fx)", scalabilityRatioWithDecomp, tankCountRatio)

	if scalabilityRatioWithDecomp >= scalabilityRatioNoDecomp {
		t.Errorf("Decomposition should improve scalability. Without: %.2fx, With: %.2fx", scalabilityRatioNoDecomp, scalabilityRatioWithDecomp)
	}

	expectedEvapNoDecomp := resultLargeNoDecomp.EvaporationLoss
	expectedEvapWithDecomp := resultLargeWithDecomp.EvaporationLoss
	evapDiff := math.Abs(expectedEvapNoDecomp - expectedEvapWithDecomp) / expectedEvapNoDecomp

	t.Logf("Evaporation loss: without decomp=%.2f, with decomp=%.2f, diff=%.2f%%",
		expectedEvapNoDecomp, expectedEvapWithDecomp, evapDiff*100)

	if evapDiff > 0.25 {
		t.Errorf("Decomposition result differs too much from centralized. Diff: %.2f%%, expected < 25%%", evapDiff*100)
	}
}

func TestRootCause_RiskGrouping(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	states := testutils.GenerateTankStatesForScheduler(12, "risk_bands")

	groups := scheduler.groupTanksByRisk(states)

	t.Logf("Group count: %d", len(groups))
	for i, group := range groups {
		t.Logf("  Group %d: %d tanks, thresholds >= %.2f", i, len(group), scheduler.modelParams.RiskGroupThresholds[i])
	}

	if len(groups) != len(scheduler.modelParams.RiskGroupThresholds) {
		t.Errorf("Expected %d groups, got %d", len(scheduler.modelParams.RiskGroupThresholds), len(groups))
	}

	for i, group := range groups {
		threshold := scheduler.modelParams.RiskGroupThresholds[i]
		for _, state := range group {
			if i > 0 {
				prevThreshold := scheduler.modelParams.RiskGroupThresholds[i-1]
				if state.RiskIndex >= prevThreshold {
					t.Errorf("Tank %d with risk %.2f in wrong group %d (should be in group %d)",
						state.TankID, state.RiskIndex, i, i-1)
				}
			}
			if state.RiskIndex < threshold {
				t.Errorf("Tank %d with risk %.2f in group %d but threshold is %.2f",
					state.TankID, state.RiskIndex, i, threshold)
			}
		}
	}
}

func TestRootCause_SubproblemDecomposition(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	maxTanks := scheduler.modelParams.MaxTanksPerSubproblem
	testCases := []int{4, 8, 15, 20}

	for _, nTanks := range testCases {
		states := testutils.GenerateTankStatesForScheduler(nTanks, "random")
		subproblems := scheduler.decomposeIntoSubproblems(states)

		t.Logf("%d tanks -> %d subproblems", nTanks, len(subproblems))

		totalTanks := 0
		for i, sp := range subproblems {
			totalTanks += len(sp)
			t.Logf("  Subproblem %d: %d tanks", i, len(sp))
			if len(sp) > maxTanks {
				t.Errorf("Subproblem %d has %d tanks, exceeds max %d", i, len(sp), maxTanks)
			}
		}

		if totalTanks != nTanks {
			t.Errorf("Total tanks in subproblems %d != original %d", totalTanks, nTanks)
		}

		if nTanks > maxTanks && len(subproblems) < 2 {
			t.Errorf("Expected multiple subproblems for %d tanks (max %d per subproblem)", nTanks, maxTanks)
		}
	}
}

func TestRootCause_DynamicCompressorConfig(t *testing.T) {
	cfg := testutils.NewTestConfig()
	scheduler := NewMultiTankScheduler(cfg, nil, nil, nil)

	testTankCounts := []int{4, 10, 20}
	compressorsPerTank := 2

	for _, nTanks := range testTankCounts {
		scheduler.ensureCompressorConfig(nTanks, compressorsPerTank)

		expectedConfigs := nTanks * compressorsPerTank
		actualConfigs := len(scheduler.modelParams.MaxLoadPctPerCompressor)

		t.Logf("%d tanks -> %d compressor configs (expected %d)", nTanks, actualConfigs, expectedConfigs)

		if actualConfigs < expectedConfigs {
			t.Errorf("Expected at least %d compressor configs for %d tanks, got %d",
				expectedConfigs, nTanks, actualConfigs)
		}

		for tankID := 1; tankID <= nTanks; tankID++ {
			for compID := 1; compID <= compressorsPerTank; compID++ {
				key := scheduler.compressorKey(tankID, compID)
				maxLoad, exists := scheduler.modelParams.MaxLoadPctPerCompressor[key]
				if !exists {
					t.Errorf("Missing config for compressor %s", key)
				}
				if maxLoad != scheduler.modelParams.DefaultMaxLoadPct {
					t.Errorf("Compressor %s has max load %.2f, expected %.2f",
						key, maxLoad, scheduler.modelParams.DefaultMaxLoadPct)
				}
			}
		}
	}
}
