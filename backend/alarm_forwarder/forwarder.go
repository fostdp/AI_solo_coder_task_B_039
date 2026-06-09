package alarm_forwarder

import (
	"context"
	"fmt"
	"lng-monitoring/alarm"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"lng-monitoring/modbus_poller"
	"lng-monitoring/models"
	"sync"
	"time"
)

type AlarmForwarder struct {
	cfg              *config.Config
	modelParams      *config.ModelParams
	db               *database.DB
	opcuaClient      *alarm.OPCUAClient
	modbusPoller     *modbus_poller.ModbusPoller
	predictionChan   <-chan messages.PredictionResult
	commandChan      <-chan messages.ControlCommand
	resultChan       chan<- messages.ForwardResult
	activeAlarms     map[string]models.Alarm
	alarmsMu         sync.Mutex
}

func NewForwarder(
	cfg *config.Config,
	modelParams *config.ModelParams,
	db *database.DB,
	modbusPoller *modbus_poller.ModbusPoller,
	predictionChan <-chan messages.PredictionResult,
	commandChan <-chan messages.ControlCommand,
	resultChan chan<- messages.ForwardResult,
) *AlarmForwarder {
	opcuaClient := alarm.NewOPCUAClient(&cfg.OPCUA)
	return &AlarmForwarder{
		cfg:            cfg,
		modelParams:    modelParams,
		db:             db,
		opcuaClient:    opcuaClient,
		modbusPoller:   modbusPoller,
		predictionChan: predictionChan,
		commandChan:    commandChan,
		resultChan:     resultChan,
		activeAlarms:   make(map[string]models.Alarm),
	}
}

func (f *AlarmForwarder) Start(ctx context.Context) {
	if err := f.opcuaClient.Connect(); err != nil {
		fmt.Printf("OPC UA connection error: %v\n", err)
	}
	defer f.opcuaClient.Close()

	f.opcuaClient.StartHeartbeat(ctx)

	go f.flushBufferedAlarmsPeriodically(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case pred := <-f.predictionChan:
			f.processPrediction(ctx, pred)
		case cmd := <-f.commandChan:
			f.processCommand(ctx, cmd)
		}
	}
}

func (f *AlarmForwarder) processPrediction(ctx context.Context, pred messages.PredictionResult) {
	if pred.ErrorMessage != "" {
		return
	}

	thresholds := f.modelParams.AlarmThresholds

	f.checkRolloverAlarm(ctx, pred, thresholds)
	f.checkOverpressureAlarm(ctx, pred, thresholds)
}

func (f *AlarmForwarder) checkRolloverAlarm(ctx context.Context, pred messages.PredictionResult, thresholds config.AlarmThresholds) {
	alarmKey := fmt.Sprintf("rollover_%d", pred.TankID)

	shouldTrigger := pred.MaxTempDiff > thresholds.TempDiffAlarm &&
		pred.MaxDensityDiff > thresholds.DensityDiffAlarm

	f.alarmsMu.Lock()
	_, hasActive := f.activeAlarms[alarmKey]
	f.alarmsMu.Unlock()

	if shouldTrigger && !hasActive {
		tank, err := f.getTankInfo(ctx, pred.TankID)
		if err != nil {
			fmt.Printf("Get tank info error: %v\n", err)
			return
		}

		alarm := models.Alarm{
			Time:           time.Now(),
			TankID:         pred.TankID,
			AlarmLevel:     1,
			AlarmType:      "ROLLOVER_WARNING",
			AlarmMessage: fmt.Sprintf("%s储罐一级翻滚预警：层间温差%.2f℃超过阈值%.2f℃，密度差%.2fkg/m³超过阈值%.2fkg/m³。建议立即开启低压泵循环混合。",
				tank.TankName, pred.MaxTempDiff, thresholds.TempDiffAlarm, pred.MaxDensityDiff, thresholds.DensityDiffAlarm),
			ThresholdValue: thresholds.TempDiffAlarm,
			ActualValue:    pred.MaxTempDiff,
		}

		alarmID, err := f.db.InsertAlarm(ctx, alarm)
		if err != nil {
			fmt.Printf("Insert alarm error: %v\n", err)
			return
		}
		alarm.AlarmID = alarmID

		f.alarmsMu.Lock()
		f.activeAlarms[alarmKey] = alarm
		f.alarmsMu.Unlock()

		fmt.Printf("一级翻滚预警触发 - 储罐ID: %d, 告警ID: %d\n", pred.TankID, alarmID)

		f.pushAlarmToOPCUA(&alarm)
		f.triggerBOGCompressorAdjustment(ctx, pred.TankID)

	} else if !shouldTrigger && hasActive {
		f.clearAlarm(ctx, alarmKey, pred.TankID)
	}
}

func (f *AlarmForwarder) checkOverpressureAlarm(ctx context.Context, pred messages.PredictionResult, thresholds config.AlarmThresholds) {
	alarmKey := fmt.Sprintf("overpressure_%d", pred.TankID)

	pressureData, err := f.db.GetLatestPressureData(ctx, pred.TankID)
	if err != nil {
		return
	}

	designPressure := thresholds.DesignPressureMPa
	pressurePct := (pressureData.Pressure / designPressure) * 100.0
	shouldTrigger := pressurePct > thresholds.PressureThresholdPct

	f.alarmsMu.Lock()
	_, hasActive := f.activeAlarms[alarmKey]
	f.alarmsMu.Unlock()

	if shouldTrigger && !hasActive {
		tank, err := f.getTankInfo(ctx, pred.TankID)
		if err != nil {
			fmt.Printf("Get tank info error: %v\n", err)
			return
		}

		alarm := models.Alarm{
			Time:           time.Now(),
			TankID:         pred.TankID,
			AlarmLevel:     2,
			AlarmType:      "OVERPRESSURE_ALARM",
			AlarmMessage: fmt.Sprintf("%s储罐二级超压告警：当前压力%.4fMPa达到设计压力%.4fMPa的%.2f%%，超过阈值%.2f%%。请立即检查BOG压缩机运行状态！",
				tank.TankName, pressureData.Pressure, designPressure, pressurePct, thresholds.PressureThresholdPct),
			ThresholdValue: thresholds.PressureThresholdPct,
			ActualValue:    pressurePct,
		}

		alarmID, err := f.db.InsertAlarm(ctx, alarm)
		if err != nil {
			fmt.Printf("Insert alarm error: %v\n", err)
			return
		}
		alarm.AlarmID = alarmID

		f.alarmsMu.Lock()
		f.activeAlarms[alarmKey] = alarm
		f.alarmsMu.Unlock()

		fmt.Printf("二级超压告警触发 - 储罐ID: %d, 告警ID: %d\n", pred.TankID, alarmID)

		f.pushAlarmToOPCUA(&alarm)
		f.triggerBOGCompressorAdjustment(ctx, pred.TankID)

	} else if !shouldTrigger && hasActive {
		f.clearAlarm(ctx, alarmKey, pred.TankID)
	}
}

func (f *AlarmForwarder) processCommand(ctx context.Context, cmd messages.ControlCommand) {
	var err error
	switch cmd.CommandType {
	case "BOG_COMPRESSOR":
		err = f.adjustBOGCompressor(ctx, cmd.TankID, cmd.TargetValue)
	case "LOW_PRESSURE_PUMP":
		err = f.startLowPressurePump(ctx, cmd.TankID)
	default:
		err = fmt.Errorf("unknown command type: %s", cmd.CommandType)
	}

	result := messages.ForwardResult{
		Success:   err == nil,
		Command:   cmd,
		Timestamp: time.Now(),
	}
	if err != nil {
		result.Error = err.Error()
	}

	select {
	case f.resultChan <- result:
	case <-ctx.Done():
	}
}

func (f *AlarmForwarder) getTankInfo(ctx context.Context, tankID int) (*models.Tank, error) {
	tanks, err := f.db.GetTanks(ctx)
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

func (f *AlarmForwarder) pushAlarmToOPCUA(alarm *models.Alarm) {
	if err := f.opcuaClient.PushAlarm(alarm); err != nil {
		fmt.Printf("OPC UA push failed: %v\n", err)
		return
	}

	ctx := context.Background()
	if err := f.db.MarkAlarmPushed(ctx, alarm.AlarmID); err != nil {
		fmt.Printf("Mark alarm pushed failed: %v\n", err)
	} else {
		fmt.Printf("告警已通过OPC UA推送至DCS - 告警ID: %d\n", alarm.AlarmID)
	}
}

func (f *AlarmForwarder) flushBufferedAlarmsPeriodically(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if f.opcuaClient.IsConnected() {
				fmt.Printf("OPC UA buffer size: %d\n", f.opcuaClient.GetBufferSize())
			}
		}
	}
}

func (f *AlarmForwarder) triggerBOGCompressorAdjustment(ctx context.Context, tankID int) {
	modbusCfg := f.modelParams.ModbusRegister
	baseAddr := (tankID - 1) * modbusCfg.TankRegisterBlockSize
	pumpControlAddr := baseAddr + modbusCfg.RegisterOffsetPumpControl

	if f.modbusPoller != nil {
		if err := f.modbusPoller.WriteSingleRegister(uint16(pumpControlAddr), 1); err != nil {
			fmt.Printf("BOG压缩机调节失败 (Modbus write): %v\n", err)
		} else {
			fmt.Printf("BOG压缩机自动调节已启动 - 储罐ID: %d, 寄存器地址: %d\n", tankID, pumpControlAddr)
		}
	}
}

func (f *AlarmForwarder) adjustBOGCompressor(ctx context.Context, tankID int, targetValue float64) error {
	modbusCfg := f.modelParams.ModbusRegister
	baseAddr := (tankID - 1) * modbusCfg.TankRegisterBlockSize
	bogOffset := modbusCfg.RegisterOffsetBOG

	for compID := 1; compID <= modbusCfg.CompressorsPerTank; compID++ {
		addr := baseAddr + bogOffset + (compID-1)*10
		regValue := uint16(targetValue * 100)

		if f.modbusPoller != nil {
			if err := f.modbusPoller.WriteSingleRegister(uint16(addr), regValue); err != nil {
				fmt.Printf("BOG压缩机 %d 调节失败: %v\n", compID, err)
				return err
			}
		}
	}

	fmt.Printf("BOG压缩机调节完成 - 储罐ID: %d, 目标值: %.2f%%\n", tankID, targetValue)
	return nil
}

func (f *AlarmForwarder) startLowPressurePump(ctx context.Context, tankID int) error {
	modbusCfg := f.modelParams.ModbusRegister
	baseAddr := (tankID - 1) * modbusCfg.TankRegisterBlockSize
	pumpOffset := modbusCfg.RegisterOffsetPumpControl

	for pumpID := 1; pumpID <= modbusCfg.PumpsPerTank; pumpID++ {
		addr := baseAddr + pumpOffset + (pumpID - 1)
		if f.modbusPoller != nil {
			if err := f.modbusPoller.WriteSingleRegister(uint16(addr), 1); err != nil {
				fmt.Printf("低压泵 %d 启动失败: %v\n", pumpID, err)
				return err
			}
		}
	}

	fmt.Printf("低压泵已启动 - 储罐ID: %d\n", tankID)
	return nil
}

func (f *AlarmForwarder) clearAlarm(ctx context.Context, alarmKey string, tankID int) {
	f.alarmsMu.Lock()
	alarm, exists := f.activeAlarms[alarmKey]
	if exists {
		delete(f.activeAlarms, alarmKey)
	}
	f.alarmsMu.Unlock()

	if exists {
		if err := f.db.ClearAlarm(ctx, alarm.AlarmID); err != nil {
			fmt.Printf("Clear alarm error: %v\n", err)
		} else {
			fmt.Printf("告警已消除 - 储罐ID: %d, 告警ID: %d, 类型: %s\n", tankID, alarm.AlarmID, alarm.AlarmType)
		}
	}
}

func (f *AlarmForwarder) GetActiveAlarms() []models.Alarm {
	f.alarmsMu.Lock()
	defer f.alarmsMu.Unlock()

	alarms := make([]models.Alarm, 0, len(f.activeAlarms))
	for _, a := range f.activeAlarms {
		alarms = append(alarms, a)
	}
	return alarms
}
