package models

import "time"

type Tank struct {
	TankID          int       `json:"tank_id"`
	TankName        string    `json:"tank_name"`
	Capacity        float64   `json:"capacity"`
	DesignPressure  float64   `json:"design_pressure"`
	Height          float64   `json:"height"`
	Diameter        float64   `json:"diameter"`
	Layers          int       `json:"layers"`
	ThermoPerLayer  int       `json:"thermo_per_layer"`
	DensityMeters   int       `json:"density_meters"`
	CreatedAt       time.Time `json:"created_at"`
}

type TemperatureData struct {
	Time          time.Time `json:"time"`
	TankID        int       `json:"tank_id"`
	Layer         int       `json:"layer"`
	SensorIndex   int       `json:"sensor_index"`
	Temperature   float64   `json:"temperature"`
	ModbusAddress int       `json:"modbus_address"`
}

type DensityData struct {
	Time           time.Time `json:"time"`
	TankID         int       `json:"tank_id"`
	SensorIndex    int       `json:"sensor_index"`
	Density        float64   `json:"density"`
	HeightPosition float64   `json:"height_position"`
	ModbusAddress  int       `json:"modbus_address"`
}

type PressureData struct {
	Time          time.Time `json:"time"`
	TankID        int       `json:"tank_id"`
	Pressure      float64   `json:"pressure"`
	ModbusAddress int       `json:"modbus_address"`
}

type BOGCompressorData struct {
	Time              time.Time `json:"time"`
	TankID            int       `json:"tank_id"`
	CompressorID      int       `json:"compressor_id"`
	RunningStatus     int       `json:"running_status"`
	VibrationLevel    float64   `json:"vibration_level,omitempty"`
	MotorCurrent      float64   `json:"motor_current,omitempty"`
	DischargePressure float64   `json:"discharge_pressure,omitempty"`
	ModbusAddress     int       `json:"modbus_address"`
}

type LayerSummary struct {
	Time     time.Time `json:"time"`
	TankID   int       `json:"tank_id"`
	Layer    int       `json:"layer"`
	AvgTemp  float64   `json:"avg_temp"`
	MinTemp  float64   `json:"min_temp"`
	MaxTemp  float64   `json:"max_temp"`
	TempStd  float64   `json:"temp_std,omitempty"`
}

type RolloverPrediction struct {
	Time                   time.Time `json:"time"`
	TankID                 int       `json:"tank_id"`
	RiskIndex              float64   `json:"risk_index"`
	MaxTempDiff            float64   `json:"max_temp_diff"`
	MaxDensityDiff         float64   `json:"max_density_diff"`
	CriticalTimeHours      float64   `json:"critical_time_hours,omitempty"`
	StratificationStability float64  `json:"stratification_stability"`
	ConvectionVelocity     float64   `json:"convection_velocity"`
	Recommendation         string    `json:"recommendation,omitempty"`
	ModelVersion           string    `json:"model_version"`
}

type Alarm struct {
	AlarmID        int        `json:"alarm_id"`
	Time           time.Time  `json:"time"`
	TankID         int        `json:"tank_id"`
	AlarmLevel     int        `json:"alarm_level"`
	AlarmType      string     `json:"alarm_type"`
	AlarmMessage   string     `json:"alarm_message"`
	ThresholdValue float64    `json:"threshold_value,omitempty"`
	ActualValue    float64    `json:"actual_value,omitempty"`
	Acknowledged   bool       `json:"acknowledged"`
	AcknowledgedTime *time.Time `json:"acknowledged_time,omitempty"`
	Cleared        bool       `json:"cleared"`
	ClearedTime    *time.Time `json:"cleared_time,omitempty"`
	OPCUAPushed    bool       `json:"opcua_pushed"`
	OPCUAPushTime  *time.Time `json:"opcua_push_time,omitempty"`
}

type AlarmConfig struct {
	ConfigID            int       `json:"config_id"`
	AlarmType           string    `json:"alarm_type"`
	AlarmLevel          int       `json:"alarm_level"`
	TempThreshold       float64   `json:"temp_threshold,omitempty"`
	DensityThreshold    float64   `json:"density_threshold,omitempty"`
	PressureThresholdPct float64  `json:"pressure_threshold_pct,omitempty"`
	Enabled             bool      `json:"enabled"`
	Description         string    `json:"description,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SensorTrendData struct {
	Time        time.Time `json:"time"`
	Temperature float64   `json:"temperature,omitempty"`
	Density     float64   `json:"density,omitempty"`
}

type Tank3DData struct {
	TankID        int                 `json:"tank_id"`
	TankName      string              `json:"tank_name"`
	LayerTemps    []float64           `json:"layer_temps"`
	Densities     []float64           `json:"densities"`
	DensityHeights []float64          `json:"density_heights"`
	Pressure      float64             `json:"pressure"`
	RiskIndex     float64             `json:"risk_index"`
	Alarms        []Alarm             `json:"alarms,omitempty"`
	CompressorStatus []BOGCompressorData `json:"compressor_status,omitempty"`
}

type BOGDiagnostic struct {
	Time              time.Time `json:"time"`
	TankID            int       `json:"tank_id"`
	CompressorID      int       `json:"compressor_id"`
	AnomalyScore      float64   `json:"anomaly_score"`
	IsAnomaly         bool      `json:"is_anomaly"`
	AnomalyType       string    `json:"anomaly_type,omitempty"`
	Confidence        float64   `json:"confidence,omitempty"`
	RemainingHours    float64   `json:"remaining_hours,omitempty"`
	Recommendation    string    `json:"recommendation,omitempty"`
	VibrationTrend    float64   `json:"vibration_trend,omitempty"`
	CurrentTrend      float64   `json:"current_trend,omitempty"`
	ModelVersion      string    `json:"model_version"`
}

type HeatLeakAssessment struct {
	Time                    time.Time `json:"time"`
	TankID                  int       `json:"tank_id"`
	EquivalentConductivity  float64   `json:"equivalent_conductivity"`
	InsulationPerformance   float64   `json:"insulation_performance"`
	HeatLeakRate            float64   `json:"heat_leak_rate"`
	LeakRegions             []int     `json:"leak_regions,omitempty"`
	IsWarning               bool      `json:"is_warning"`
	TotalHeatLoadKW         float64   `json:"total_heat_load_kw"`
	AmbientTemp             float64   `json:"ambient_temp,omitempty"`
	InnerTemp               float64   `json:"inner_temp,omitempty"`
	ModelVersion            string    `json:"model_version"`
}

type UnloadingPredictionModel struct {
	Time                time.Time   `json:"time"`
	TankID              int         `json:"tank_id"`
	UnloadingRate       float64     `json:"unloading_rate"`
	UnloadingDensity    float64     `json:"unloading_density"`
	UnloadingTemp       float64     `json:"unloading_temp"`
	EstimatedDuration   float64     `json:"estimated_duration"`
	MaxTempDiff         float64     `json:"max_temp_diff"`
	MaxDensityDiff      float64     `json:"max_density_diff"`
	OptimalPumpOnTime   float64     `json:"optimal_pump_on_time"`
	RolloverRisk        float64     `json:"rollover_risk"`
	TimeSteps           []float64   `json:"time_steps,omitempty"`
	PredictedTemps      [][]float64 `json:"predicted_temps,omitempty"`
	PredictedDensities  [][]float64 `json:"predicted_densities,omitempty"`
	ModelVersion        string      `json:"model_version"`
}

type MultiTankSchedule struct {
	Time                time.Time       `json:"time"`
	CompressorLoads     map[string]int  `json:"compressor_loads"`
	PumpOperations      []PumpSchedule  `json:"pump_operations,omitempty"`
	EvaporationLossKg   float64         `json:"evaporation_loss_kg"`
	EvaporationLossM3   float64         `json:"evaporation_loss_m3"`
	ObjectiveValue      float64         `json:"objective_value"`
	OptimizationStatus  string          `json:"optimization_status"`
	ModelVersion        string          `json:"model_version"`
}

type PumpSchedule struct {
	TankID    int     `json:"tank_id"`
	PumpID    int     `json:"pump_id"`
	StartTime float64 `json:"start_time_hours"`
	Duration  float64 `json:"duration_hours"`
	Action    string  `json:"action"`
}

type UnloadingManualRequest struct {
	TankID            int     `json:"tank_id"`
	UnloadingRate     float64 `json:"unloading_rate" binding:"required,min=100,max=5000"`
	UnloadingDensity  float64 `json:"unloading_density" binding:"required,min=400,max=460"`
	UnloadingTemp     float64 `json:"unloading_temp" binding:"required,min=-165,max=-150"`
	EstimatedDuration float64 `json:"estimated_duration" binding:"required,min=1,max=48"`
}

type DiagnosticManualRequest struct {
	TankID       int `json:"tank_id" binding:"required,min=1,max=4"`
	CompressorID int `json:"compressor_id" binding:"required,min=1,max=2"`
	HistoryHours int `json:"history_hours" binding:"min=1,max=168"`
}

type HeatLeakManualRequest struct {
	TankID             int     `json:"tank_id" binding:"required,min=1,max=4"`
	AmbientTemperature float64 `json:"ambient_temperature" binding:"required,min=-30,max=50"`
	HistoryHours       int     `json:"history_hours" binding:"min=1,max=168"`
}
