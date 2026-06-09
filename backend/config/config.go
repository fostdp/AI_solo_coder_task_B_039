package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type PhysicalProperties struct {
	Gravity                  float64 `json:"gravity"`
	KinematicViscosity       float64 `json:"kinematic_viscosity"`
	ThermalDiffusivity       float64 `json:"thermal_diffusivity"`
	ThermalExpansionCoeff   float64 `json:"thermal_expansion_coefficient"`
	BaseDensity             float64 `json:"base_density"`
	MinDensity              float64 `json:"min_density"`
	MaxDensity              float64 `json:"max_density"`
}

type NumericalMethod struct {
	GridPoints                 int     `json:"grid_points"`
	InitialTimeStep            float64 `json:"initial_time_step"`
	MinTimeStep                float64 `json:"min_time_step"`
	MaxTimeStep                float64 `json:"max_time_step"`
	InitialUnderRelaxation     float64 `json:"initial_under_relaxation"`
	MinUnderRelaxation         float64 `json:"min_under_relaxation"`
	MaxUnderRelaxation         float64 `json:"max_under_relaxation"`
	CFLLimit                   float64 `json:"cfl_limit"`
	MaxTimeSteps               int     `json:"max_time_steps"`
	DivergenceThreshold        float64 `json:"divergence_threshold"`
	ConvergenceThreshold       float64 `json:"convergence_threshold"`
	BoundaryChangeThreshold    float64 `json:"boundary_change_threshold"`
	MaxConsecutiveDivergence   int     `json:"max_consecutive_divergence"`
}

type StabilityAnalysis struct {
	BuoyancyFrequencyScale   float64 `json:"buoyancy_frequency_scale"`
	InterfaceGradientThreshold float64 `json:"interface_gradient_threshold"`
	ConvectionVelocityThreshold float64 `json:"convection_velocity_threshold"`
	RayleighNumberCritical    float64 `json:"rayleigh_number_critical"`
}

type RiskCalculation struct {
	TempDiffWeight         float64            `json:"temp_diff_weight"`
	DensityDiffWeight      float64            `json:"density_diff_weight"`
	InstabilityWeight      float64            `json:"instability_weight"`
	TimeWeight             float64            `json:"time_weight"`
	MaxTempDiffReference   float64            `json:"max_temp_diff_reference"`
	MaxDensityDiffReference float64           `json:"max_density_diff_reference"`
	CriticalTimeReference  float64            `json:"critical_time_reference"`
	RiskThresholds         map[string]float64 `json:"risk_thresholds"`
}

type AlarmThresholds struct {
	TempDiffAlarm          float64 `json:"temp_diff_alarm"`
	DensityDiffAlarm       float64 `json:"density_diff_alarm"`
	PressureThresholdPct   float64 `json:"pressure_threshold_pct"`
	DesignPressureMPa      float64 `json:"design_pressure_mpa"`
}

type ModbusRegisterConfig struct {
	RegisterOffsetPressure    int `json:"register_offset_pressure"`
	RegisterOffsetBOG         int `json:"register_offset_bog"`
	RegisterOffsetDensity     int `json:"register_offset_density"`
	RegisterOffsetPumpControl int `json:"register_offset_pump_control"`
	TankRegisterBlockSize     int `json:"tank_register_block_size"`
	CompressorsPerTank        int `json:"compressors_per_tank"`
	PumpsPerTank              int `json:"pumps_per_tank"`
}

type TankSpecs struct {
	HeightMeters          float64   `json:"height_meters"`
	DiameterMeters        float64   `json:"diameter_meters"`
	CapacityCubicMeters   float64   `json:"capacity_cubic_meters"`
	Layers                int       `json:"layers"`
	ThermometersPerLayer  int       `json:"thermometers_per_layer"`
	DensityMeters         int       `json:"density_meters"`
	DensitySensorHeights  []float64 `json:"density_sensor_heights"`
	LayerHeights          []float64 `json:"layer_heights"`
}

type ModelParams struct {
	PhysicalProperties  PhysicalProperties  `json:"physical_properties"`
	NumericalMethod     NumericalMethod     `json:"numerical_method"`
	StabilityAnalysis   StabilityAnalysis   `json:"stability_analysis"`
	RiskCalculation     RiskCalculation     `json:"risk_calculation"`
	AlarmThresholds     AlarmThresholds     `json:"alarm_thresholds"`
	ModbusRegister      ModbusRegisterConfig `json:"modbus_config"`
	TankSpecs           TankSpecs           `json:"tank_specs"`
}

type Config struct {
	Database    DatabaseConfig
	Modbus      ModbusConfig
	OPCUA       OPCUAConfig
	Server      ServerConfig
	Alarm       AlarmConfig
	Prediction  PredictionConfig
	ModelParams *ModelParams
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

type ModbusConfig struct {
	Host        string
	Port        int
	SlaveID     byte
	IntervalSec int
	TankCount   int
	Layers      int
	ThermoPerLayer int
	DensityMeters int
}

type OPCUAConfig struct {
	Endpoint string
	NodeID   string
}

type ServerConfig struct {
	Host string
	Port int
}

type AlarmConfig struct {
	TempDiffThreshold    float64
	DensityDiffThreshold float64
	PressureThresholdPct float64
}

type PredictionConfig struct {
	IntervalSec      int
	ModelVersion     string
	GridPoints       int
	TimeSteps        int
	StabilityThreshold float64
}

func Load() *Config {
	return &Config{
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvInt("DB_PORT", 5432),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			DBName:   getEnv("DB_NAME", "lng_monitoring"),
		},
		Modbus: ModbusConfig{
			Host:           getEnv("MODBUS_HOST", "localhost"),
			Port:           getEnvInt("MODBUS_PORT", 502),
			SlaveID:        byte(getEnvInt("MODBUS_SLAVE", 1)),
			IntervalSec:    getEnvInt("MODBUS_INTERVAL", 30),
			TankCount:      getEnvInt("TANK_COUNT", 4),
			Layers:         getEnvInt("LAYERS", 5),
			ThermoPerLayer: getEnvInt("THERMO_PER_LAYER", 8),
			DensityMeters:  getEnvInt("DENSITY_METERS", 3),
		},
		OPCUA: OPCUAConfig{
			Endpoint: getEnv("OPCUA_ENDPOINT", "opc.tcp://localhost:4840"),
			NodeID:   getEnv("OPCUA_NODE", "ns=2;s=Alarm"),
		},
		Server: ServerConfig{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnvInt("SERVER_PORT", 8080),
		},
		Alarm: AlarmConfig{
			TempDiffThreshold:    getEnvFloat("TEMP_DIFF_THRESHOLD", 8.0),
			DensityDiffThreshold: getEnvFloat("DENSITY_DIFF_THRESHOLD", 2.0),
			PressureThresholdPct: getEnvFloat("PRESSURE_THRESHOLD_PCT", 90.0),
		},
		Prediction: PredictionConfig{
			IntervalSec:          getEnvInt("PREDICTION_INTERVAL", 300),
			ModelVersion:         "1.0",
			GridPoints:           getEnvInt("GRID_POINTS", 50),
			TimeSteps:            getEnvInt("TIME_STEPS", 1000),
			StabilityThreshold:   getEnvFloat("STABILITY_THRESHOLD", 0.5),
		},
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value, exists := os.LookupEnv(key); exists {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func LoadModelParams(path string) (*ModelParams, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model params file: %w", err)
	}

	var params ModelParams
	if err := json.Unmarshal(data, &params); err != nil {
		return nil, fmt.Errorf("parse model params: %w", err)
	}

	return &params, nil
}

func LoadWithModelParams(configPath string) *Config {
	cfg := Load()
	params, err := LoadModelParams(configPath)
	if err != nil {
		fmt.Printf("Warning: failed to load model params: %v, using defaults\n", err)
		return cfg
	}
	cfg.ModelParams = params
	return cfg
}
