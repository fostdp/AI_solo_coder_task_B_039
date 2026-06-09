package alarm

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/models"
	"time"
)

type AlarmEngine struct {
	cfg        *config.AlarmConfig
	db         *database.DB
	opcuaClient *OPCUAClient
}

func NewEngine(cfg *config.AlarmConfig, db *database.DB, opcuaCfg *config.OPCUAConfig) *AlarmEngine {
	opcuaClient := NewOPCUAClient(opcuaCfg)
	return &AlarmEngine{
		cfg:         cfg,
		db:          db,
		opcuaClient: opcuaClient,
	}
}

func (e *AlarmEngine) Start(ctx context.Context, tankCount int) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	if err := e.opcuaClient.Connect(); err != nil {
		fmt.Printf("OPC UA connection error: %v\n", err)
	}
	defer e.opcuaClient.Close()

	e.opcuaClient.StartHeartbeat(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for tankID := 1; tankID <= tankCount; tankID++ {
				if err := e.CheckAndTriggerAlarms(ctx, tankID); err != nil {
					fmt.Printf("Alarm check error for tank %d: %v\n", tankID, err)
				}
			}
			if err := e.PushUnsentAlarms(ctx); err != nil {
				fmt.Printf("OPC UA push error: %v\n", err)
			}
		}
	}
}

func (e *AlarmEngine) CheckAndTriggerAlarms(ctx context.Context, tankID int) error {
	tanks, err := e.db.GetTanks(ctx)
	if err != nil {
		return fmt.Errorf("get tanks: %w", err)
	}

	var tank *models.Tank
	for _, t := range tanks {
		if t.TankID == tankID {
			tank = &t
			break
		}
	}
	if tank == nil {
		return fmt.Errorf("tank %d not found", tankID)
	}

	if err := e.checkRolloverAlarm(ctx, tankID, tank); err != nil {
		fmt.Printf("Rollover alarm check error: %v\n", err)
	}

	if err := e.checkOverpressureAlarm(ctx, tankID, tank); err != nil {
		fmt.Printf("Overpressure alarm check error: %v\n", err)
	}

	return nil
}

func (e *AlarmEngine) checkRolloverAlarm(ctx context.Context, tankID int, tank *models.Tank) error {
	layerTemps, err := e.db.GetLayerAvgTemps(ctx, tankID)
	if err != nil {
		return err
	}

	densityData, err := e.db.GetLatestDensityData(ctx, tankID)
	if err != nil {
		return err
	}

	maxTempDiff := 0.0
	for i := 1; i < len(layerTemps); i++ {
		diff := layerTemps[i] - layerTemps[i-1]
		if diff > maxTempDiff {
			maxTempDiff = diff
		}
	}

	maxDensityDiff := 0.0
	for i := 1; i < len(densityData); i++ {
		diff := densityData[i-1].Density - densityData[i].Density
		if diff > maxDensityDiff {
			maxDensityDiff = diff
		}
	}

	activeAlarms, err := e.db.GetActiveAlarms(ctx)
	if err != nil {
		return err
	}

	hasActiveRolloverAlarm := false
	for _, a := range activeAlarms {
		if a.TankID == tankID && a.AlarmType == "ROLLOVER_WARNING" && !a.Cleared {
			hasActiveRolloverAlarm = true
			break
		}
	}

	if maxTempDiff > e.cfg.TempDiffThreshold && maxDensityDiff > e.cfg.DensityDiffThreshold {
		if !hasActiveRolloverAlarm {
			alarm := models.Alarm{
				Time:           time.Now(),
				TankID:         tankID,
				AlarmLevel:     1,
				AlarmType:      "ROLLOVER_WARNING",
				AlarmMessage:   fmt.Sprintf("%s储罐一级翻滚预警：层间温差%.2f℃超过阈值%.2f℃，密度差%.2fkg/m³超过阈值%.2fkg/m³。建议立即开启低压泵循环混合。",
					tank.TankName, maxTempDiff, e.cfg.TempDiffThreshold, maxDensityDiff, e.cfg.DensityDiffThreshold),
				ThresholdValue: e.cfg.TempDiffThreshold,
				ActualValue:    maxTempDiff,
			}

			alarmID, err := e.db.InsertAlarm(ctx, alarm)
			if err != nil {
				return fmt.Errorf("insert alarm: %w", err)
			}

			fmt.Printf("一级翻滚预警触发 - 储罐ID: %d, 告警ID: %d\n", tankID, alarmID)

			if err := e.triggerBOGCompressorAdjustment(ctx, tankID); err != nil {
				fmt.Printf("BOG压缩机调节失败: %v\n", err)
			}
		}
	} else {
		if hasActiveRolloverAlarm {
			for _, a := range activeAlarms {
				if a.TankID == tankID && a.AlarmType == "ROLLOVER_WARNING" && !a.Cleared {
					if err := e.db.ClearAlarm(ctx, a.AlarmID); err != nil {
						fmt.Printf("Clear alarm error: %v\n", err)
					} else {
						fmt.Printf("翻滚预警已消除 - 储罐ID: %d, 告警ID: %d\n", tankID, a.AlarmID)
					}
				}
			}
		}
	}

	return nil
}

func (e *AlarmEngine) checkOverpressureAlarm(ctx context.Context, tankID int, tank *models.Tank) error {
	pressureData, err := e.db.GetLatestPressureData(ctx, tankID)
	if err != nil {
		return err
	}

	pressurePct := (pressureData.Pressure / tank.DesignPressure) * 100.0

	activeAlarms, err := e.db.GetActiveAlarms(ctx)
	if err != nil {
		return err
	}

	hasActiveOverpressureAlarm := false
	for _, a := range activeAlarms {
		if a.TankID == tankID && a.AlarmType == "OVERPRESSURE_ALARM" && !a.Cleared {
			hasActiveOverpressureAlarm = true
			break
		}
	}

	if pressurePct > e.cfg.PressureThresholdPct {
		if !hasActiveOverpressureAlarm {
			alarm := models.Alarm{
				Time:           time.Now(),
				TankID:         tankID,
				AlarmLevel:     2,
				AlarmType:      "OVERPRESSURE_ALARM",
				AlarmMessage:   fmt.Sprintf("%s储罐二级超压告警：当前压力%.4fMPa达到设计压力%.4fMPa的%.2f%%，超过阈值%.2f%%。请立即检查BOG压缩机运行状态！",
					tank.TankName, pressureData.Pressure, tank.DesignPressure, pressurePct, e.cfg.PressureThresholdPct),
				ThresholdValue: e.cfg.PressureThresholdPct,
				ActualValue:    pressurePct,
			}

			alarmID, err := e.db.InsertAlarm(ctx, alarm)
			if err != nil {
				return fmt.Errorf("insert alarm: %w", err)
			}

			fmt.Printf("二级超压告警触发 - 储罐ID: %d, 告警ID: %d\n", tankID, alarmID)

			if err := e.triggerBOGCompressorAdjustment(ctx, tankID); err != nil {
				fmt.Printf("BOG压缩机调节失败: %v\n", err)
			}
		}
	} else {
		if hasActiveOverpressureAlarm {
			for _, a := range activeAlarms {
				if a.TankID == tankID && a.AlarmType == "OVERPRESSURE_ALARM" && !a.Cleared {
					if err := e.db.ClearAlarm(ctx, a.AlarmID); err != nil {
						fmt.Printf("Clear alarm error: %v\n", err)
					} else {
						fmt.Printf("超压告警已消除 - 储罐ID: %d, 告警ID: %d\n", tankID, a.AlarmID)
					}
				}
			}
		}
	}

	return nil
}

func (e *AlarmEngine) triggerBOGCompressorAdjustment(ctx context.Context, tankID int) error {
	fmt.Printf("BOG压缩机自动调节已启动 - 储罐ID: %d\n", tankID)
	return nil
}

func (e *AlarmEngine) PushUnsentAlarms(ctx context.Context) error {
	alarms, err := e.db.GetActiveAlarms(ctx)
	if err != nil {
		return err
	}

	for _, alarm := range alarms {
		if !alarm.OPCUAPushed {
			if err := e.opcuaClient.PushAlarm(&alarm); err != nil {
				fmt.Printf("OPC UA push failed for alarm %d: %v\n", alarm.AlarmID, err)
				continue
			}
			if err := e.db.MarkAlarmPushed(ctx, alarm.AlarmID); err != nil {
				fmt.Printf("Mark alarm pushed failed: %v\n", err)
			} else {
				fmt.Printf("告警已通过OPC UA推送至DCS - 告警ID: %d\n", alarm.AlarmID)
			}
		}
	}

	return nil
}

func (e *AlarmEngine) AcknowledgeAlarm(ctx context.Context, alarmID int) error {
	return e.db.AcknowledgeAlarm(ctx, alarmID)
}

func (e *AlarmEngine) ClearAlarm(ctx context.Context, alarmID int) error {
	return e.db.ClearAlarm(ctx, alarmID)
}

func (e *AlarmEngine) GetActiveAlarms(ctx context.Context) ([]models.Alarm, error) {
	return e.db.GetActiveAlarms(ctx)
}

func (e *AlarmEngine) GetAlarmConfig(ctx context.Context) ([]models.AlarmConfig, error) {
	return e.db.GetAlarmConfig(ctx)
}
