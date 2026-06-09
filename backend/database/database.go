package database

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/models"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

func New(cfg *config.DatabaseConfig) (*DB, error) {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)

	poolConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	poolConfig.MaxConns = 20
	poolConfig.MinConns = 5

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

func (db *DB) InsertTemperatureData(ctx context.Context, data []models.TemperatureData) error {
	batch := &pgxpool.Batch{}
	for _, d := range data {
		batch.Queue(`INSERT INTO temperature_data (time, tank_id, layer, sensor_index, temperature, modbus_address)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			d.Time, d.TankID, d.Layer, d.SensorIndex, d.Temperature, d.ModbusAddress)
	}
	return db.pool.SendBatch(ctx, batch).Close()
}

func (db *DB) InsertDensityData(ctx context.Context, data []models.DensityData) error {
	batch := &pgxpool.Batch{}
	for _, d := range data {
		batch.Queue(`INSERT INTO density_data (time, tank_id, sensor_index, density, height_position, modbus_address)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			d.Time, d.TankID, d.SensorIndex, d.Density, d.HeightPosition, d.ModbusAddress)
	}
	return db.pool.SendBatch(ctx, batch).Close()
}

func (db *DB) InsertPressureData(ctx context.Context, data []models.PressureData) error {
	batch := &pgxpool.Batch{}
	for _, d := range data {
		batch.Queue(`INSERT INTO pressure_data (time, tank_id, pressure, modbus_address)
			VALUES ($1, $2, $3, $4)`,
			d.Time, d.TankID, d.Pressure, d.ModbusAddress)
	}
	return db.pool.SendBatch(ctx, batch).Close()
}

func (db *DB) InsertBOGCompressorData(ctx context.Context, data []models.BOGCompressorData) error {
	batch := &pgxpool.Batch{}
	for _, d := range data {
		batch.Queue(`INSERT INTO bog_compressor_data (time, tank_id, compressor_id, running_status, vibration_level, motor_current, discharge_pressure, modbus_address)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			d.Time, d.TankID, d.CompressorID, d.RunningStatus, d.VibrationLevel, d.MotorCurrent, d.DischargePressure, d.ModbusAddress)
	}
	return db.pool.SendBatch(ctx, batch).Close()
}

func (db *DB) InsertLayerSummary(ctx context.Context, data []models.LayerSummary) error {
	batch := &pgxpool.Batch{}
	for _, d := range data {
		batch.Queue(`INSERT INTO layer_summary (time, tank_id, layer, avg_temp, min_temp, max_temp, temp_stddev)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			d.Time, d.TankID, d.Layer, d.AvgTemp, d.MinTemp, d.MaxTemp, d.TempStd)
	}
	return db.pool.SendBatch(ctx, batch).Close()
}

func (db *DB) InsertRolloverPrediction(ctx context.Context, prediction models.RolloverPrediction) error {
	_, err := db.pool.Exec(ctx, `INSERT INTO rollover_prediction 
		(time, tank_id, risk_index, max_temp_diff, max_density_diff, critical_time_hours, 
		 stratification_stability, convection_velocity, recommendation, model_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		prediction.Time, prediction.TankID, prediction.RiskIndex,
		prediction.MaxTempDiff, prediction.MaxDensityDiff, prediction.CriticalTimeHours,
		prediction.StratificationStability, prediction.ConvectionVelocity,
		prediction.Recommendation, prediction.ModelVersion)
	return err
}

func (db *DB) InsertAlarm(ctx context.Context, alarm models.Alarm) (int, error) {
	var id int
	err := db.pool.QueryRow(ctx, `INSERT INTO alarms 
		(time, tank_id, alarm_level, alarm_type, alarm_message, threshold_value, actual_value)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING alarm_id`,
		alarm.Time, alarm.TankID, alarm.AlarmLevel, alarm.AlarmType,
		alarm.AlarmMessage, alarm.ThresholdValue, alarm.ActualValue).Scan(&id)
	return id, err
}

func (db *DB) GetTanks(ctx context.Context) ([]models.Tank, error) {
	rows, err := db.pool.Query(ctx, `SELECT tank_id, tank_name, capacity, design_pressure, height, diameter, layers, thermometers_per_layer, density_meters, created_at FROM tanks ORDER BY tank_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tanks []models.Tank
	for rows.Next() {
		var t models.Tank
		err := rows.Scan(&t.TankID, &t.TankName, &t.Capacity, &t.DesignPressure, &t.Height, &t.Diameter, &t.Layers, &t.ThermoPerLayer, &t.DensityMeters, &t.CreatedAt)
		if err != nil {
			return nil, err
		}
		tanks = append(tanks, t)
	}
	return tanks, nil
}

func (db *DB) GetLatestTemperatureData(ctx context.Context, tankID int, layers int) ([]models.TemperatureData, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT DISTINCT ON (layer, sensor_index) time, tank_id, layer, sensor_index, temperature, modbus_address
		FROM temperature_data WHERE tank_id = $1 ORDER BY layer, sensor_index, time DESC LIMIT $2`,
		tankID, layers*8)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data []models.TemperatureData
	for rows.Next() {
		var d models.TemperatureData
		err := rows.Scan(&d.Time, &d.TankID, &d.Layer, &d.SensorIndex, &d.Temperature, &d.ModbusAddress)
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	}
	return data, nil
}

func (db *DB) GetLatestDensityData(ctx context.Context, tankID int) ([]models.DensityData, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT DISTINCT ON (sensor_index) time, tank_id, sensor_index, density, height_position, modbus_address
		FROM density_data WHERE tank_id = $1 ORDER BY sensor_index, time DESC LIMIT 3`,
		tankID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data []models.DensityData
	for rows.Next() {
		var d models.DensityData
		err := rows.Scan(&d.Time, &d.TankID, &d.SensorIndex, &d.Density, &d.HeightPosition, &d.ModbusAddress)
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	}
	return data, nil
}

func (db *DB) GetLatestPressureData(ctx context.Context, tankID int) (*models.PressureData, error) {
	var d models.PressureData
	err := db.pool.QueryRow(ctx, `SELECT time, tank_id, pressure, modbus_address FROM pressure_data WHERE tank_id = $1 ORDER BY time DESC LIMIT 1`, tankID).
		Scan(&d.Time, &d.TankID, &d.Pressure, &d.ModbusAddress)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (db *DB) GetLatestRolloverPrediction(ctx context.Context, tankID int) (*models.RolloverPrediction, error) {
	var p models.RolloverPrediction
	err := db.pool.QueryRow(ctx, `SELECT time, tank_id, risk_index, max_temp_diff, max_density_diff, critical_time_hours, stratification_stability, convection_velocity, recommendation, model_version FROM rollover_prediction WHERE tank_id = $1 ORDER BY time DESC LIMIT 1`, tankID).
		Scan(&p.Time, &p.TankID, &p.RiskIndex, &p.MaxTempDiff, &p.MaxDensityDiff, &p.CriticalTimeHours, &p.StratificationStability, &p.ConvectionVelocity, &p.Recommendation, &p.ModelVersion)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (db *DB) GetSensorTrendData(ctx context.Context, tankID, layer, sensorIndex int, duration time.Duration) ([]models.SensorTrendData, error) {
	startTime := time.Now().Add(-duration)
	rows, err := db.pool.Query(ctx, `SELECT time, temperature FROM temperature_data WHERE tank_id = $1 AND layer = $2 AND sensor_index = $3 AND time >= $4 ORDER BY time`,
		tankID, layer, sensorIndex, startTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data []models.SensorTrendData
	for rows.Next() {
		var d models.SensorTrendData
		err := rows.Scan(&d.Time, &d.Temperature)
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	}
	return data, nil
}

func (db *DB) GetDensityTrendData(ctx context.Context, tankID, sensorIndex int, duration time.Duration) ([]models.SensorTrendData, error) {
	startTime := time.Now().Add(-duration)
	rows, err := db.pool.Query(ctx, `SELECT time, density FROM density_data WHERE tank_id = $1 AND sensor_index = $2 AND time >= $3 ORDER BY time`,
		tankID, sensorIndex, startTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data []models.SensorTrendData
	for rows.Next() {
		var d models.SensorTrendData
		err := rows.Scan(&d.Time, &d.Density)
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	}
	return data, nil
}

func (db *DB) GetActiveAlarms(ctx context.Context) ([]models.Alarm, error) {
	rows, err := db.pool.Query(ctx, `SELECT alarm_id, time, tank_id, alarm_level, alarm_type, alarm_message, threshold_value, actual_value, acknowledged, acknowledged_time, cleared, cleared_time, opcua_pushed, opcua_push_time FROM alarms WHERE cleared = false ORDER BY time DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alarms []models.Alarm
	for rows.Next() {
		var a models.Alarm
		err := rows.Scan(&a.AlarmID, &a.Time, &a.TankID, &a.AlarmLevel, &a.AlarmType, &a.AlarmMessage, &a.ThresholdValue, &a.ActualValue, &a.Acknowledged, &a.AcknowledgedTime, &a.Cleared, &a.ClearedTime, &a.OPCUAPushed, &a.OPCUAPushTime)
		if err != nil {
			return nil, err
		}
		alarms = append(alarms, a)
	}
	return alarms, nil
}

func (db *DB) GetLayerAvgTemps(ctx context.Context, tankID int) ([]float64, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT layer, AVG(temperature) as avg_temp
		FROM temperature_data 
		WHERE tank_id = $1 AND time >= NOW() - INTERVAL '5 minutes'
		GROUP BY layer 
		ORDER BY layer`, tankID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var temps []float64
	for rows.Next() {
		var layer int
		var temp float64
		err := rows.Scan(&layer, &temp)
		if err != nil {
			return nil, err
		}
		temps = append(temps, temp)
	}
	return temps, nil
}

func (db *DB) GetHistoricalLayerData(ctx context.Context, tankID int, duration time.Duration) (map[int][]models.LayerSummary, error) {
	startTime := time.Now().Add(-duration)
	rows, err := db.pool.Query(ctx, `
		SELECT time, tank_id, layer, avg_temp, min_temp, max_temp, temp_stddev
		FROM layer_summary 
		WHERE tank_id = $1 AND time >= $2 
		ORDER BY layer, time`,
		tankID, startTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int][]models.LayerSummary)
	for rows.Next() {
		var d models.LayerSummary
		err := rows.Scan(&d.Time, &d.TankID, &d.Layer, &d.AvgTemp, &d.MinTemp, &d.MaxTemp, &d.TempStd)
		if err != nil {
			return nil, err
		}
		result[d.Layer] = append(result[d.Layer], d)
	}
	return result, nil
}

func (db *DB) MarkAlarmPushed(ctx context.Context, alarmID int) error {
	_, err := db.pool.Exec(ctx, `UPDATE alarms SET opcua_pushed = true, opcua_push_time = NOW() WHERE alarm_id = $1`, alarmID)
	return err
}

func (db *DB) AcknowledgeAlarm(ctx context.Context, alarmID int) error {
	_, err := db.pool.Exec(ctx, `UPDATE alarms SET acknowledged = true, acknowledged_time = NOW() WHERE alarm_id = $1`, alarmID)
	return err
}

func (db *DB) ClearAlarm(ctx context.Context, alarmID int) error {
	_, err := db.pool.Exec(ctx, `UPDATE alarms SET cleared = true, cleared_time = NOW() WHERE alarm_id = $1`, alarmID)
	return err
}

func (db *DB) GetAlarmConfig(ctx context.Context) ([]models.AlarmConfig, error) {
	rows, err := db.pool.Query(ctx, `SELECT config_id, alarm_type, alarm_level, temp_threshold, density_threshold, pressure_threshold_pct, enabled, description, updated_at FROM alarm_config ORDER BY config_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []models.AlarmConfig
	for rows.Next() {
		var c models.AlarmConfig
		err := rows.Scan(&c.ConfigID, &c.AlarmType, &c.AlarmLevel, &c.TempThreshold, &c.DensityThreshold, &c.PressureThresholdPct, &c.Enabled, &c.Description, &c.UpdatedAt)
		if err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, nil
}
